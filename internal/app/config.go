package app

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

type Env struct {
	BaseURL, Database, Secret, AdminPassword, APIKey, AliyunMailSender, AliyunMailPassword string
	DeployDir, DeployBranch, DeployStateFile                                               string
	Secure, TrustProxy, Debug                                                              bool
}

const (
	aliyunMailHost        = "smtpdm.aliyun.com"
	aliyunMailPort        = "465"
	aliyunMailPasswordKey = "aliyun_mail_password"
)

func LoadEnv(path string) (Env, error) {
	values := map[string]string{}
	f, err := os.Open(path)
	if err == nil {
		defer f.Close()
		s := bufio.NewScanner(f)
		for s.Scan() {
			line := strings.TrimSpace(s.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			k, v, ok := strings.Cut(line, "=")
			if ok {
				values[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), "\"'")
			}
		}
		if s.Err() != nil {
			return Env{}, s.Err()
		}
	} else if !os.IsNotExist(err) {
		return Env{}, err
	}
	get := func(k, def string) string {
		if v, ok := os.LookupEnv(k); ok {
			return v
		}
		if v, ok := values[k]; ok {
			return v
		}
		return def
	}
	e := Env{BaseURL: strings.TrimRight(get("GIF_BASE_URL", "http://127.0.0.1:8096"), "/"), Database: get("GIF_DATABASE_PATH", "gif.db"), Secret: get("GIF_SECRET", ""), AdminPassword: get("GIF_ADMIN_PASSWORD", ""), APIKey: get("GEEKAI_API_KEY", ""), Secure: get("GIF_COOKIE_SECURE", "false") == "true", AliyunMailSender: get("ALIYUN_DM_SENDER", ""), AliyunMailPassword: get("ALIYUN_DM_SMTP_PASSWORD", ""), DeployDir: get("APP_DIR", "."), DeployBranch: get("BRANCH", "main"), DeployStateFile: get("DEPLOY_STATE_FILE", ".last_deployed_commit")}
	// 服务仅监听回环地址，默认读取本机反向代理传来的真实 IP；仍可显式关闭。
	e.TrustProxy = get("GIF_TRUST_PROXY", "true") == "true"
	e.Debug = strings.EqualFold(get("GIF_DEBUG", "false"), "true")
	if len(e.Secret) < 32 {
		return e, errors.New("GIF_SECRET must contain at least 32 characters")
	}
	if len(e.AdminPassword) < 16 {
		return e, errors.New("GIF_ADMIN_PASSWORD must contain at least 16 characters")
	}
	if e.Secure && !strings.HasPrefix(e.BaseURL, "https://") {
		return e, errors.New("secure cookie requires HTTPS base URL")
	}
	return e, nil
}

type Action struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Icon   string `json:"icon"`
	Prompt string `json:"prompt"`
}
type Category struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Subtitle string   `json:"subtitle"`
	Icon     string   `json:"icon"`
	Clothes  []string `json:"clothes"`
	Colors   []string `json:"colors"`
	Weapons  []string `json:"weapons"`
	Prompt   string   `json:"prompt"`
	Actions  []Action `json:"actions"`
}

// Style 是上传自拍后选择的画风。默认画风保持站点原有的轻度Q版效果，
// Q版与水墨风格在此之上改变整体画风，三者在定稿和动作两个阶段都生效。
type Style struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Subtitle string `json:"subtitle"`
	Icon     string `json:"icon"`
	Prompt   string `json:"prompt"`
}
type Settings struct {
	DefaultCredits  int      `json:"defaultCredits"`
	UserConcurrency int      `json:"userConcurrency"`
	ChargeOnFailure bool     `json:"chargeOnFailure"`
	RedeemHelp      string   `json:"redeemHelp"`
	APIBase         string   `json:"apiBase"`
	Model           string   `json:"model"`
	Quality         string   `json:"quality"`
	AssetHosts      []string `json:"assetHosts"`
	IdentityPrompt  string   `json:"identityPrompt"`
	DraftPrompt     string   `json:"draftPrompt"`
	MotionPrompt    string   `json:"motionPrompt"`
	// MotionGrid 控制动作序列图的网格规格：4x4 为16格（每帧256×256），
	// 5x5 为25格、10x10 为100格（每帧128×128）。生成提示词与 GIF 合成共用该规格。
	MotionGrid string     `json:"motionGrid"`
	Styles     []Style    `json:"styles"`
	Categories []Category `json:"categories"`
	MailHost   string     `json:"mailHost"`
	MailPort   string     `json:"mailPort"`
	MailUser   string     `json:"mailUser"`
	MailFrom   string     `json:"mailFrom"`
	// FeedbackEmail 是用户反馈的接收邮箱，留空时发送到 MailFrom。
	FeedbackEmail string `json:"feedbackEmail"`
}

// defaultStyleID 是未指定画风时使用的编号；界面上对应“默认”选项，效果与原有轻度Q版一致。
const defaultStyleID = "default"

// styleIDs 固定画风的顺序与编号：默认、Q版、水墨风格。
var styleIDs = []string{defaultStyleID, "chibi", "ink"}

func defaultStyles() []Style {
	return []Style{
		{ID: defaultStyleID, Name: "默认", Subtitle: "轻度Q版 · 保留本人特点", Icon: "✧", Prompt: "画风固定为精致二维插画：清晰轮廓、简洁阴影、轻度Q版身体比例（头身比约1:5），面部明显对应本人。前文若出现其他画风或比例描述，以此处为准，本人辨识度始终最高优先。"},
		{ID: "chibi", Name: "Q版", Subtitle: "大头小身 · 更可爱", Icon: "☻", Prompt: "画风固定为明显Q版：头身比约1:3，头部放大、四肢短圆、五官可爱，眼睛稍大而明亮，保留本人脸型宽长比例、发型、发色、肤色与眼镜等辨识特征，不幼化成通用婴儿脸。前文若出现其他画风或比例描述，以此处为准。"},
		{ID: "ink", Name: "水墨风格", Subtitle: "宣纸墨色 · 写意国风", Icon: "墨", Prompt: "画风固定为中国水墨写意：宣纸质感、墨色浓淡与飞白笔触，毛笔线条勾形，矿物淡彩点缀，背景大面积留白。保留本人五官结构、脸型比例、发型、发色与眼镜等辨识特征，不做厚重写实油画。前文若出现其他画风描述，以此处为准。"},
	}
}

// normalizeStyles 保证画风始终是默认、Q版、水墨风格三项且顺序固定：
// 旧配置没有该项时补入默认值，未知编号忽略，名称、说明或提示词留空时回落到默认值，
// 避免前台出现无法选择或缺少说明的画风。
func normalizeStyles(list []Style) []Style {
	out := defaultStyles()
	for i := range out {
		for _, s := range list {
			if s.ID != out[i].ID {
				continue
			}
			if strings.TrimSpace(s.Name) != "" {
				out[i].Name = s.Name
			}
			if strings.TrimSpace(s.Subtitle) != "" {
				out[i].Subtitle = s.Subtitle
			}
			if strings.TrimSpace(s.Icon) != "" {
				out[i].Icon = s.Icon
			}
			if strings.TrimSpace(s.Prompt) != "" {
				out[i].Prompt = s.Prompt
			}
		}
	}
	return out
}

// validateStyles 允许完全不带画风配置的旧后台提交（保存前会补入默认值），
// 但一旦提供就必须是完整、可用的三种画风。
func validateStyles(list []Style) error {
	if len(list) == 0 {
		return nil
	}
	if len(list) != len(styleIDs) {
		return errors.New("画风必须为默认、Q版、水墨风格三种")
	}
	seen := map[string]bool{}
	for _, s := range list {
		if !contains(styleIDs, s.ID) || seen[s.ID] {
			return errors.New("画风配置无效")
		}
		seen[s.ID] = true
		if strings.TrimSpace(s.Name) == "" || len([]rune(s.Name)) > 20 || len([]rune(s.Subtitle)) > 60 || len([]rune(s.Icon)) > 4 {
			return errors.New("画风的名称、说明或图标无效")
		}
		if len(strings.TrimSpace(s.Prompt)) < 20 || len([]rune(s.Prompt)) > 4000 {
			return errors.New("各画风提示词不可为空")
		}
	}
	return nil
}

func defaults(e Env) Settings {
	return Settings{
		DefaultCredits: 5, UserConcurrency: 5, ChargeOnFailure: true,
		APIBase: "https://geekai.co/api/v1", Model: "gpt-image-2.5-sunburst", Quality: "high", AssetHosts: []string{"static.geekai.co", "geekai.co"}, MailHost: aliyunMailHost, MailPort: aliyunMailPort, MailUser: e.AliyunMailSender, MailFrom: e.AliyunMailSender,
		IdentityPrompt: "以自拍中的本人为身份参考，人物辨识度最高优先。保留脸型宽长比例、下颌轮廓、眉形、眼型、眼距、鼻形、嘴形、五官相对位置、发际线、发型、发色、肤色，以及清晰可见的眼镜、痣、雀斑。只做必要的裁切、曝光和白平衡调整，不瘦脸、不尖下巴、不放大眼睛、不美白、不改变年龄。不要变成通用动漫脸，不添加原图没有的身份特征。采用精致二维插画、清晰轮廓、简洁阴影、轻度Q版身体比例，面部明显对应本人。无法判断的衣服和身体依据下方设定设计。",
		DraftPrompt:    "本轮只输出一张静态角色定稿图，同一张图内包含正面脸部近景和完整全身造型，供本人核对。纯净浅色背景，面部无遮挡，完整发型、手脚和装备入镜。无文字、无水印、无动作序列。分类：{{category}}。服装：{{clothes}}。配色：{{color}}。武器：{{weapon}}。",
		MotionPrompt:   "图1是本人自拍，图2是已确认角色定稿。图1核对身份，图2固定画风、服装、比例、装备及配色，只改变动作表情。动作：{{action}}。输出一张1024×1024透明PNG，按后台动作序列图规格排列连续帧。仅全身角色连续动作，不包含定稿图的脸部近景。镜头固定，大小稳定，地面基准线一致。允许合理位移，跳跃允许离地，结尾回起始位置，自然衔接第一帧。每格无边框无间隙无编号无文字，留安全边距，角色武器特效不跨格、不裁切。真实透明背景，不画棋盘格。脸部可见，避免转背、过度模糊、遮脸、五官变形、肢体错误和重复静止帧。",
		MotionGrid:     "4x4",
		Styles:         defaultStyles(),
		Categories: []Category{
			{ID: "male", Name: "男生", Subtitle: "披上铠甲，做自己的英雄", Icon: "⚔", Clothes: []string{"古代札甲", "古代鳞甲", "轻甲与短披风"}, Colors: []string{"玄黑与暗金", "银灰与藏蓝", "深红与铁灰"}, Weapons: []string{"长剑", "长枪", "关刀", "战斧"}, Prompt: "中国古代武将，英气精神有亲和力，保留年龄感，不添加胡须或夸张肌肉。护肩护腕腰带战裙战靴结构明确，细节简化。露出完整面部与发型，不用遮面头盔，短披风不挡动作。武器造型长度配色惯用手始终一致。", Actions: []Action{{"idle", "护卫待机", "🛡", "轻微呼吸，握持武器，短披风小幅摆动。"}, {"greet", "武者致意", "👋", "持械点头致意，武器远离脸部，再恢复原姿势。"}, {"attack", "蓄力攻击", "⚔", "压低重心、向前小幅踏步出招、收势回起点。长剑挥斩，长枪直刺，关刀横扫，战斧下劈；根据所选武器只做对应动作。"}, {"guard", "格挡防御", "🛡", "举起武器格挡，短促火花，恢复站姿。"}, {"win", "得胜庆祝", "✨", "将武器安全地举向侧上方，露出自信笑容，再回位。"}, {"rest", "收兵休息", "☕", "放松肩膀轻轻呼气，再恢复精神。"}}},
			{ID: "female", Name: "女生", Subtitle: "把小小心情，变成可爱日常", Icon: "✿", Clothes: []string{"针织开衫与百褶裙", "宽松卫衣与长裤", "背带裙"}, Colors: []string{"奶油黄", "雾粉", "浅紫", "薄荷绿"}, Weapons: []string{}, Prompt: "可爱温暖的日常角色，通过动作服装配色体现可爱，保留本人年龄，不幼化面容。圆润简洁的休闲鞋，原有发型眼镜，小型发饰不挡发际线。微笑保留本人眼型嘴形，爱心星星不遮脸。", Actions: []Action{{"wave", "开心打招呼", "👋", "微笑，单手左右挥动两次再放下。"}, {"heart", "给你比心", "♡", "双手在胸前组成爱心，小爱心浮起消失，手放回原位。"}, {"clap", "开心鼓掌", "👏", "轻轻拍手两次，肩膀随动作起伏。"}, {"cheer", "加油打气", "✊", "双拳举到胸前上下轻动，眼神坚定，再放下。"}, {"shy", "害羞开心", "🌸", "轻微歪头，双手靠近脸颊，不遮脸，含蓄微笑再回正。"}, {"sleep", "晚安困困", "☾", "轻揉一只眼睛，捂嘴打小哈欠，恢复姿势。"}}},
			{ID: "child", Name: "小朋友", Subtitle: "收藏每一个天真可爱的瞬间", Icon: "★", Clothes: []string{"动物图案卫衣与长裤", "彩色背带裤", "休闲上衣与短裤"}, Colors: []string{"天空蓝与奶油黄", "桃粉与米白", "薄荷绿与浅黄"}, Weapons: []string{}, Prompt: "童趣活泼温暖，保留本人实际年龄阶段的脸颊、额头比例、眼型鼻形及清晰可见的乳牙特点。不把不同年龄都画成婴儿脸，无成人妆容成熟五官。简洁衣服图案圆头运动鞋，不戴遮脸动物头套。搭配固定造型的小熊毛绒玩具。", Actions: []Action{{"wave", "你好呀", "👋", "一只手举起挥动，自然笑容，再放下。"}, {"clap", "好棒好棒", "👏", "开心拍手两次，身体轻轻起伏。"}, {"jump", "耶！成功啦", "🎉", "双手举起，原地轻跳一次，落回起点。"}, {"hug", "抱抱玩偶", "🧸", "抱紧小熊毛绒玩具，轻微左右摇摆，不遮脸。"}, {"curious", "好奇看看", "🔍", "头轻偏一侧，眨眼，再回正。"}, {"sleep", "困了晚安", "☾", "抱玩偶打小哈欠，眼睛慢慢闭合再睁开。"}}},
		}}
}

func token(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func hash(s string) string { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }
func (a *App) mac(s string) string {
	h := hmac.New(sha256.New, []byte(a.env.Secret))
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}
func (a *App) encrypt(s string) string {
	key := sha256.Sum256([]byte(a.env.Secret))
	block, _ := aes.NewCipher(key[:])
	g, _ := cipher.NewGCM(block)
	nonce := make([]byte, g.NonceSize())
	rand.Read(nonce)
	return base64.StdEncoding.EncodeToString(g.Seal(nonce, nonce, []byte(s), nil))
}
func (a *App) decrypt(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	key := sha256.Sum256([]byte(a.env.Secret))
	block, _ := aes.NewCipher(key[:])
	g, _ := cipher.NewGCM(block)
	if len(b) < g.NonceSize() {
		return "", errors.New("invalid encrypted secret")
	}
	p, err := g.Open(nil, b[:g.NonceSize()], b[g.NonceSize():], nil)
	return string(p), err
}
func (a *App) settings() (Settings, error) {
	var raw string
	err := a.db.QueryRow("SELECT value FROM settings WHERE key='config'").Scan(&raw)
	var s Settings
	if err == nil {
		err = json.Unmarshal([]byte(raw), &s)
	}
	// 邮件通道固定为阿里云邮件推送华东1（杭州），不接受数据库中的旧服务端点。
	s.MailHost = aliyunMailHost
	s.MailPort = aliyunMailPort
	// 兼容较早的配置：未设置单用户并发任务数时使用默认值 5。
	if s.UserConcurrency < 1 {
		s.UserConcurrency = 5
	}
	// 兼容较早的配置：未设置动作序列图规格时使用默认 4×4（共16格、每帧256×256）。
	if s.MotionGrid == "" {
		s.MotionGrid = "4x4"
	}
	// 兼容较早的配置：缺少画风定义时补入默认的默认、Q版与水墨风格。
	s.Styles = normalizeStyles(s.Styles)
	return s, err
}

func (a *App) mailConfigured(s Settings) bool {
	_, senderValid := validEmail(s.MailUser)
	return s.MailHost == aliyunMailHost && s.MailPort == aliyunMailPort && senderValid && s.MailUser == s.MailFrom && a.secret(aliyunMailPasswordKey) != ""
}
func (a *App) secret(name string) string {
	var value string
	if a.db.QueryRow("SELECT value FROM settings WHERE key=?", name).Scan(&value) != nil {
		return ""
	}
	v, _ := a.decrypt(value)
	return v
}
func (a *App) setSecret(name, value string) error {
	_, err := a.db.Exec("INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", name, a.encrypt(value))
	return err
}
func validateSettings(s Settings) error {
	if len([]rune(s.RedeemHelp)) > 2000 {
		return errors.New("获取兑换码内容不能超过2000字")
	}
	if s.DefaultCredits < 0 || s.DefaultCredits > 1000 || len(s.Categories) != 3 {
		return errors.New("次数或分类配置超出范围")
	}
	if s.UserConcurrency < 1 || s.UserConcurrency > 20 {
		return errors.New("单用户并发任务数需在1到20之间")
	}
	if s.APIBase != "https://geekai.co/api/v1" {
		return errors.New("接口地址必须为 https://geekai.co/api/v1")
	}
	if s.Model != "gpt-image-2.5-sunburst" && s.Model != "gpt-image-2.5-flare" {
		return errors.New("请选择 GPT Image 2.5 Sunburst 或 Flare")
	}
	if !contains([]string{"low", "medium", "high", "xhigh", "max"}, s.Quality) {
		return errors.New("图片质量无效")
	}
	// 旧后台页面不带动作序列图规格（空串），保存前会补默认值，这里允许留空。
	if s.MotionGrid != "" && !contains(motionGridIDs(), s.MotionGrid) {
		return errors.New("动作序列图规格仅支持 4×4、5×5 或 10×10")
	}
	if len(s.IdentityPrompt) < 20 || len(s.DraftPrompt) < 20 || len(s.MotionPrompt) < 20 {
		return errors.New("各步骤提示词不可为空")
	}
	if err := validateStyles(s.Styles); err != nil {
		return err
	}
	if s.FeedbackEmail != "" {
		if _, ok := validEmail(s.FeedbackEmail); !ok {
			return errors.New("反馈接收邮箱无效")
		}
	}
	seen := map[string]bool{}
	for _, c := range s.Categories {
		if !contains([]string{"male", "female", "child"}, c.ID) || seen[c.ID] || len(c.Clothes) == 0 || len(c.Colors) == 0 || len(c.Actions) == 0 || len(c.Actions) > 12 {
			return errors.New("分类配置无效")
		}
		seen[c.ID] = true
		ids := map[string]bool{}
		for _, ac := range c.Actions {
			if !idPattern.MatchString(ac.ID) || ids[ac.ID] || strings.TrimSpace(ac.Name) == "" || len([]rune(ac.Name)) > 50 || strings.TrimSpace(ac.Prompt) == "" || len(ac.Prompt) > 12000 {
				return errors.New("动作配置无效")
			}
			ids[ac.ID] = true
		}
		if c.ID == "male" && len(c.Weapons) == 0 {
			return errors.New("男生分类需要武器选项")
		}
	}
	if len(s.AssetHosts) == 0 || len(s.AssetHosts) > 10 {
		return fmt.Errorf("图片下载域名数量无效")
	}
	for _, h := range s.AssetHosts {
		if !hostPattern.MatchString(h) || !strings.Contains(h, ".") {
			return errors.New("图片域名必须为完整主机名")
		}
	}
	raw, _ := json.Marshal(s)
	if len(raw) > 100000 {
		return errors.New("配置过大")
	}
	return nil
}
func contains(values []string, v string) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}

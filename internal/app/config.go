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
	e := Env{BaseURL: strings.TrimRight(get("GIF_BASE_URL", "http://127.0.0.1:8096"), "/"), Database: get("GIF_DATABASE_PATH", "gif.db"), Secret: get("GIF_SECRET", ""), AdminPassword: get("GIF_ADMIN_PASSWORD", ""), APIKey: firstNonEmpty(get("OPENROUTER_API_KEY", ""), get("GEEKAI_API_KEY", "")), Secure: get("GIF_COOKIE_SECURE", "false") == "true", AliyunMailSender: get("ALIYUN_DM_SENDER", ""), AliyunMailPassword: get("ALIYUN_DM_SMTP_PASSWORD", ""), DeployDir: get("APP_DIR", "."), DeployBranch: get("BRANCH", "main"), DeployStateFile: get("DEPLOY_STATE_FILE", ".last_deployed_commit")}
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

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
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
	// 5x5 为25格（每帧128×128），10x10 为100格（每帧256×256）。生成提示词与 GIF 合成共用该规格。
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
		{ID: defaultStyleID, Name: "默认", Subtitle: "极致大头 · 5:1比例", Icon: "✧", Prompt: "画风固定为精致二维插画：清晰轮廓、简洁阴影。全身造型采用超大头、极小身体的极致Q版比例——从头顶到下巴的头部高度与从下巴到脚底的身体高度之比为5:1（头约占角色总高83%，下巴以下颈躯干腿鞋合计约17%）；肩宽约为头宽的1/4至1/3，脖子几乎不可见，四肢短小圆润。禁止普通2头身或3头身。面部内部结构不夸张，明显对应本人。前文若出现其他画风或比例描述，以此处为准，本人辨识度始终最高优先。"},
		{ID: "chibi", Name: "Q版", Subtitle: "大头小身 · 更可爱", Icon: "☻", Prompt: "画风固定为明显Q版：在默认极致5:1头身比基础上五官更可爱圆润，眼睛稍明亮，四肢短圆，仍禁止把身体画大成2/3头身。保留本人脸型宽长比例、发型、发色、肤色与眼镜等辨识特征，不幼化成通用婴儿脸。前文若出现其他画风或比例描述，以此处为准。"},
		{ID: "ink", Name: "水墨风格", Subtitle: "宣纸墨色 · 写意国风", Icon: "墨", Prompt: "画风固定为中国水墨写意：宣纸质感、墨色浓淡与飞白笔触，毛笔线条勾形，矿物淡彩点缀，背景大面积留白。身体比例仍保持极致Q版5:1（头大身小）。保留本人五官结构、脸型比例、发型、发色与眼镜等辨识特征，不做厚重写实油画。前文若出现其他画风描述，以此处为准。"},
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
		APIBase: openRouterAPIBase, Model: "openai/gpt-image-2.5-sunburst", Quality: "high", AssetHosts: []string{"openrouter.ai"}, MailHost: aliyunMailHost, MailPort: aliyunMailPort, MailUser: e.AliyunMailSender, MailFrom: e.AliyunMailSender,
		IdentityPrompt: "以自拍中的本人为身份参考，人物面部辨识度最高优先。将参考照片头部转绘为精致二维插画，准确保留脸型宽长比例、下颌轮廓、额头比例、眉形、眼型、眼距、鼻形、嘴形、耳朵形状与位置、五官相对位置、发际线、发型、发色、肤色与年龄感，以及清晰可见的眼镜、痣、雀斑。头部作为整体等比例放大，面部内部结构不随Q版夸张。不瘦脸、不尖下巴、不放大眼睛或瞳孔、不缩小鼻子、不美白、不改变年龄，不添加原图没有的身份特征。脸部与身体使用统一二维绘画语言，避免照片脸贴卡通身。",
		DraftPrompt:    compareDraftPrompt,
		MotionPrompt:   "图1是已确认角色定稿，图2是本人自拍。图1固定画风、服装、极致Q版比例（头:身体=5:1），只使用定稿右侧全身造型，不输出定稿的脸部近景；图2只核对人物身份，不恢复真人身体比例。动作全程保持同一5:1头身比，禁止把身体画大或拉长四肢。动作：{{action}}。输出一张透明PNG，按后台动作序列图规格排列连续帧。仅全身角色连续动作。镜头固定，大小稳定，地面基准线一致。允许合理位移，跳跃允许离地，结尾回起始位置，自然衔接第一帧。每格无边框无间隙无编号无文字；角色须完整缩在单格内，头顶、举手、脚底四周留足透明安全边距（约格高8%以上），禁止邻格内容渗入本格——尤其禁止下一格头顶出现在本格脚底下方。角色武器特效不跨格、不裁切。真实透明背景，不画棋盘格。脸部可见，避免转背、过度模糊、遮脸、五官变形、肢体错误和重复静止帧。",
		MotionGrid:     "5x5",
		Styles:         defaultStyles(),
		Categories:     defaultCategories(),
	}
}

// categoryIDs 固定可选人物分类编号：男生/女生/小朋友专属 + 通用日常。
var categoryIDs = []string{"male", "female", "child", "daily"}

func defaultCategories() []Category {
	return []Category{
		{ID: "male", Name: "男生", Subtitle: "披上铠甲，做自己的英雄", Icon: "⚔", Clothes: []string{"古代札甲", "古代鳞甲", "轻甲与短披风"}, Colors: []string{"玄黑与暗金", "银灰与藏蓝", "深红与铁灰"}, Weapons: []string{"长剑", "长枪", "关刀", "战斧"}, Prompt: "中国古代武将，英气精神有亲和力，保留年龄感，不添加胡须或夸张肌肉。护肩护腕腰带战裙战靴结构明确，细节简化。露出完整面部与发型，不用遮面头盔，短披风不挡动作。武器造型长度配色惯用手始终一致。全身保持极致Q版5:1头身比。", Actions: []Action{
			{"idle", "护卫待机", "🛡", "轻微呼吸，握持武器，短披风小幅摆动。"},
			{"greet", "武者致意", "👋", "持械点头致意，武器远离脸部，再恢复原姿势。"},
			{"attack", "蓄力攻击", "⚔", "压低重心、向前小幅踏步出招、收势回起点。长剑挥斩，长枪直刺，关刀横扫，战斧下劈；根据所选武器只做对应动作。"},
			{"guard", "格挡防御", "🛡", "举起武器格挡，短促火花，恢复站姿。"},
			{"win", "得胜庆祝", "✨", "将武器安全地举向侧上方，露出自信笑容，再回位。"},
			{"rest", "收兵休息", "☕", "放松肩膀轻轻呼气，再恢复精神。"},
			{"shoot", "开枪射击", "🔫", "双手持小型卡通玩具枪（非写实）向前方短促射击两次，轻微后坐，收枪回站姿；枪口不朝镜头、不朝脸，火花极小不遮脸，手臂保持短小不拉长。"},
		}},
		{ID: "female", Name: "女生", Subtitle: "把小小心情，变成可爱日常", Icon: "✿", Clothes: []string{"针织开衫与百褶裙", "宽松卫衣与长裤", "背带裙"}, Colors: []string{"奶油黄", "雾粉", "浅紫", "薄荷绿"}, Weapons: []string{}, Prompt: "可爱温暖的日常角色，通过动作服装配色体现可爱，保留本人年龄，不幼化面容。圆润简洁的休闲鞋，原有发型眼镜，小型发饰不挡发际线。微笑保留本人眼型嘴形，爱心星星不遮脸。全身保持极致Q版5:1头身比。", Actions: []Action{{"wave", "开心打招呼", "👋", "微笑，单手在肩旁小幅左右挥动两次再放下；手臂保持短小，举手最高点距格顶至少约格高12%边距。全程同一取景尺度与脚底基准线：头顶完整入格、鞋底完整入格且略靠格底（距格底约6%–10%边距），收势数格脚更靠下但仍留边；禁止角色放大到贴边，禁止邻格头顶渗进本格脚下。"}, {"heart", "给你比心", "♡", "双手在胸前组成爱心，小爱心浮起消失，手放回原位。"}, {"clap", "开心鼓掌", "👏", "轻轻拍手两次，肩膀随动作起伏。"}, {"cheer", "加油打气", "✊", "双拳举到胸前上下轻动，眼神坚定，再放下。"}, {"shy", "害羞开心", "🌸", "轻微歪头，双手靠近脸颊，不遮脸，含蓄微笑再回正。"}, {"sleep", "晚安困困", "☾", "轻揉一只眼睛，捂嘴打小哈欠，恢复姿势。"}}},
		{ID: "child", Name: "小朋友", Subtitle: "收藏每一个天真可爱的瞬间", Icon: "★", Clothes: []string{"动物图案卫衣与长裤", "彩色背带裤", "休闲上衣与短裤"}, Colors: []string{"天空蓝与奶油黄", "桃粉与米白", "薄荷绿与浅黄"}, Weapons: []string{}, Prompt: "童趣活泼温暖，保留本人实际年龄阶段的脸颊、额头比例、眼型鼻形及清晰可见的乳牙特点。不把不同年龄都画成婴儿脸，无成人妆容成熟五官。简洁衣服图案圆头运动鞋，不戴遮脸动物头套。搭配固定造型的小熊毛绒玩具。全身保持极致Q版5:1头身比。", Actions: []Action{{"wave", "你好呀", "👋", "自然笑容，一只手在肩旁小幅举起挥动两下再放下；手臂短小，举手最高点距格顶至少约格高12%边距。全程同一取景尺度与脚底基准线：头顶完整入格、鞋底完整入格且略靠格底（距格底约6%–10%边距），最后收势数格脚更靠下但仍留安全边；禁止角色放大到贴顶或贴底，禁止邻格头顶渗进本格脚下。"}, {"clap", "好棒好棒", "👏", "开心拍手两次，身体轻轻起伏。"}, {"jump", "耶！成功啦", "🎉", "双手举起，原地轻跳一次，落回起点。"}, {"hug", "抱抱玩偶", "🧸", "抱紧小熊毛绒玩具，轻微左右摇摆，不遮脸。"}, {"curious", "好奇看看", "🔍", "头轻偏一侧，眨眼，再回正。"}, {"sleep", "困了晚安", "☾", "抱玩偶打小哈欠，眼睛慢慢闭合再睁开。"}}},
		{ID: "daily", Name: "日常", Subtitle: "情侣与年轻人的聊天贴纸", Icon: "☀", Clothes: []string{"白色翻领短袖与短裤", "宽松卫衣与长裤", "休闲T恤与牛仔裤"}, Colors: []string{"白与深蓝", "奶白与灰", "浅彩休闲"}, Weapons: []string{}, Prompt: "年轻人日常单人贴纸角色，服装简洁贴合微型身体，不增大体积。保留本人年龄与面部特征，表情自然可发聊天。全身必须保持极致Q版5:1头身比，单人出镜，不出现第二人。", Actions: []Action{
			{"morning", "早呀", "☀", "微带困意揉眼一下（手不遮五官），再小幅挥手问好两次，浅笑，放下回站姿。举手幅度小，手臂保持短小，头顶与举手最高点距格顶至少约格高10%透明边距，脚底距格底同样留边；禁止邻格头顶渗进本格脚下。"},
			{"eat_ask", "吃饭了么", "🍚", "关切表情，胸前端极小白碗向前递一点并点头，再收回回站姿。"},
			{"eat", "干饭", "🍜", "【起始状态】角色正面站定，双手捧住一个超大超宽的碗（不用筷子），碗宽约为头宽的2倍；碗沿水平，并与肩部保持稳定水平关系。碗内白米饭堆成极高饭山，向上延伸并从单格顶部自然超出画面（允许饭山顶部被格边界裁切）；巨大的碗与饭山完全挡住脸部。角色身体、四肢和鞋必须完整入格；5:1头身比全程锁定，不得把身体画大。\n【触发原因】饿意已到顶点，顾不上仪态，立刻埋头开吃。\n【动作过程】主动作只有「连续大口吃饭」这一条：一口接一口，25格为流畅、等间距的小步递进。饭山总体高度逐渐下降；每吃一口，饭山靠近嘴部出现一个与该口对应的新鲜弧形缺口，缺口与饭量变化逐格累积，不得跳变。用轻微连续的头部前倾、嘴部张合、脸颊变化表现进食，双手始终稳稳托碗；碗、双手、身体位置连续稳定，不得忽大忽小或漂移。前期脸被碗与饭山遮住；中期随饭山降低逐渐露出发际线、眼睛和鼻子；后期露出眼睛和正在咀嚼的嘴巴。不得改变5:1头身比例，不得拉长四肢或躯干。\n【结束状态】最后数格饭接近吃完，脸部辨识特征清晰可见；双手仍托住碗，碗沿仍水平；人物停在满足的咀嚼收势，目光落在碗内残留米饭。\n【情绪目的】夸张又可爱的干饭满足感——从埋头猛吃到吃到见底的畅快，不是礼仪示范，而是「终于吃上了」的痛快。\n画面为精致二维人物插画：轮廓清晰稳定，色块干净，阴影柔和克制；25格线稿、色彩、光源、阴影、笔触与造型完全统一。脸部保留个体细节，身体高度简化；可爱感仅来自巨头与微型身体的比例反差。全年龄友好，服装完整不透明。相邻格须为同一轨迹细微递进，无跳帧、无复制帧、镜头固定。"},
			{"play", "出去玩", "🏃", "右手侧上招手两次，身体轻跳一次落回，开心浅笑；幅度小，不拉长四肢。"},
			{"miss", "想你了", "♡", "双手胸前比小小爱心，轻贴胸口浅笑，小爱心浮起消失，手放回两侧。"},
			{"tease", "调侃你", "😜", "眨眼一下，再轻微吐舌或歪头坏笑点头，回平静站姿；吐舌幅度很小。"},
		}},
	}
}

// normalizeCategories 保证男生/女生/小朋友/日常齐全且顺序固定。
// 已有分类保留后台自定义字段；缺类时补默认；男生缺「开枪射击」时补入。
func normalizeCategories(list []Category) []Category {
	defs := defaultCategories()
	byID := map[string]Category{}
	for _, c := range list {
		if contains(categoryIDs, c.ID) {
			byID[c.ID] = c
		}
	}
	out := make([]Category, 0, len(defs))
	for _, d := range defs {
		c, ok := byID[d.ID]
		if !ok {
			out = append(out, d)
			continue
		}
		if c.ID == "male" {
			c.Actions = ensureActions(c.Actions, d.Actions, "shoot")
		}
		if c.ID == "daily" {
			c.Actions = upgradeActionPrompt(c.Actions, d.Actions, "eat",
				"捧小碗低头大口吃两口，腮帮微鼓，抬头满足呼气，回站姿；五官不糊。")
			c.Actions = upgradeActionPrompt(c.Actions, d.Actions, "morning",
				"微带困意揉眼一下（不遮五官），再挥手问好两次，浅笑，放下回站姿。")
		}
		if c.ID == "child" {
			c.Actions = upgradeActionPrompt(c.Actions, d.Actions, "wave",
				"一只手举起挥动，自然笑容，再放下。")
		}
		if c.ID == "female" {
			c.Actions = upgradeActionPrompt(c.Actions, d.Actions, "wave",
				"微笑，单手左右挥动两次再放下。")
		}
		out = append(out, c)
	}
	return out
}

// ensureActions 若 missingIDs 中的动作不在现有列表里，则从 defs 中追加。
func ensureActions(have, defs []Action, missingIDs ...string) []Action {
	seen := map[string]bool{}
	for _, a := range have {
		seen[a.ID] = true
	}
	defByID := map[string]Action{}
	for _, a := range defs {
		defByID[a.ID] = a
	}
	for _, id := range missingIDs {
		if seen[id] {
			continue
		}
		if a, ok := defByID[id]; ok && len(have) < 12 {
			have = append(have, a)
		}
	}
	return have
}

// upgradeActionPrompt 仅当现有动作过程仍是旧默认短文案时，替换为 defs 中的新文案。
func upgradeActionPrompt(have, defs []Action, id, oldPrompt string) []Action {
	var neu string
	for _, a := range defs {
		if a.ID == id {
			neu = a.Prompt
			break
		}
	}
	if neu == "" {
		return have
	}
	for i := range have {
		if have[i].ID == id && strings.TrimSpace(have[i].Prompt) == oldPrompt {
			have[i].Prompt = neu
		}
	}
	return have
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
	// 兼容较早的配置：未设置动作序列图规格时使用默认 5×5（共25格、每帧128×128）。
	if s.MotionGrid == "" {
		s.MotionGrid = "5x5"
	}
	// 兼容较早的配置：缺少画风定义时补入默认的默认、Q版与水墨风格。
	s.Styles = normalizeStyles(s.Styles)
	// 兼容较早的配置：补入「日常」分类，并按固定编号顺序排列。
	s.Categories = normalizeCategories(s.Categories)
	// 一次性迁移：旧动作图序（自拍为图1）改为定稿为图1；旧轻度Q提示词升级为5:1。
	s = migratePromptsToV162(s)
	// 中转从 GeekAI 迁到 OpenRouter。
	s = migrateToOpenRouter(s)
	return s, err
}

// migratePromptsToV162 仅在仍是旧默认文案时替换，不覆盖管理员自定义提示词。
func migratePromptsToV162(s Settings) Settings {
	def := defaults(Env{})
	oldIdentity := "以自拍中的本人为身份参考，人物辨识度最高优先。保留脸型宽长比例、下颌轮廓、眉形、眼型、眼距、鼻形、嘴形、五官相对位置、发际线、发型、发色、肤色，以及清晰可见的眼镜、痣、雀斑。只做必要的裁切、曝光和白平衡调整，不瘦脸、不尖下巴、不放大眼睛、不美白、不改变年龄。不要变成通用动漫脸，不添加原图没有的身份特征。采用精致二维插画、清晰轮廓、简洁阴影、轻度Q版身体比例，面部明显对应本人。无法判断的衣服和身体依据下方设定设计。"
	oldDraft := "本轮只输出一张静态角色定稿图，同一张图内包含正面脸部近景和完整全身造型，供本人核对。纯净浅色背景，面部无遮挡，完整发型、手脚和装备入镜。无文字、无水印、无动作序列。分类：{{category}}。服装：{{clothes}}。配色：{{color}}。武器：{{weapon}}。"
	oldMotion := "图1是本人自拍，图2是已确认角色定稿。图1核对身份，图2固定画风、服装、比例、装备及配色，只改变动作表情。动作：{{action}}。输出一张透明PNG，按后台动作序列图规格排列连续帧。仅全身角色连续动作，不包含定稿图的脸部近景。镜头固定，大小稳定，地面基准线一致。允许合理位移，跳跃允许离地，结尾回起始位置，自然衔接第一帧。每格无边框无间隙无编号无文字，留安全边距，角色武器特效不跨格、不裁切。真实透明背景，不画棋盘格。脸部可见，避免转背、过度模糊、遮脸、五官变形、肢体错误和重复静止帧。"
	oldMotionV162 := "图1是已确认角色定稿，图2是本人自拍。图1固定画风、服装、极致Q版比例（头:身体=5:1），只使用定稿右侧全身造型，不输出定稿的脸部近景；图2只核对人物身份，不恢复真人身体比例。动作全程保持同一5:1头身比，禁止把身体画大或拉长四肢。动作：{{action}}。输出一张透明PNG，按后台动作序列图规格排列连续帧。仅全身角色连续动作。镜头固定，大小稳定，地面基准线一致。允许合理位移，跳跃允许离地，结尾回起始位置，自然衔接第一帧。每格无边框无间隙无编号无文字，留安全边距，角色武器特效不跨格、不裁切。真实透明背景，不画棋盘格。脸部可见，避免转背、过度模糊、遮脸、五官变形、肢体错误和重复静止帧。"
	if s.IdentityPrompt == oldIdentity {
		s.IdentityPrompt = def.IdentityPrompt
	}
	if s.DraftPrompt == "" || s.DraftPrompt == oldDraft {
		s.DraftPrompt = def.DraftPrompt
	}
	if s.MotionPrompt == oldMotion || s.MotionPrompt == oldMotionV162 {
		s.MotionPrompt = def.MotionPrompt
	}
	return s
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
	if s.DefaultCredits < 0 || s.DefaultCredits > 1000 || len(s.Categories) < 3 || len(s.Categories) > 4 {
		return errors.New("次数或分类配置超出范围")
	}
	if s.UserConcurrency < 1 || s.UserConcurrency > 20 {
		return errors.New("单用户并发任务数需在1到20之间")
	}
	if !contains(allowedAPIBases(), strings.TrimRight(s.APIBase, "/")) {
		return errors.New("请选择已支持的画图供应商地址")
	}
	if !modelAllowedForAPIBase(s.APIBase, s.Model) {
		return errors.New("请选择当前供应商对应的画图模型")
	}
	if !contains([]string{"low", "medium", "high", "xhigh", "max"}, s.Quality) {
		return errors.New("图片质量无效")
	}
	// 旧后台页面不带动作序列图规格（空串），保存前会补默认值，这里允许留空。
	if s.MotionGrid != "" && !contains(motionGridIDs(), s.MotionGrid) {
		return errors.New("动作序列图规格仅支持 4×4、5×5 或 10×10")
	}
	if len(s.DraftPrompt) < 20 {
		return errors.New("角色定稿提示词不可为空")
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
		if !contains(categoryIDs, c.ID) || seen[c.ID] || len(c.Clothes) == 0 || len(c.Colors) == 0 || len(c.Actions) == 0 || len(c.Actions) > 12 {
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
	// 男生、女生、小朋友必须存在；日常为可选第四类。
	for _, id := range []string{"male", "female", "child"} {
		if !seen[id] {
			return errors.New("分类配置无效")
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

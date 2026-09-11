package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"strings"
	"time"
)

type Selection struct {
	Category string `json:"category"`
	Clothes  string `json:"clothes"`
	Color    string `json:"color"`
	Weapon   string `json:"weapon"`
}
type Receipt struct {
	User      string    `json:"user"`
	Selfie    string    `json:"selfie"`
	Draft     string    `json:"draft"`
	Selection Selection `json:"selection"`
	Accepted  bool      `json:"accepted"`
	Expires   int64     `json:"expires"`
}
type GenerateInput struct {
	RequestID string    `json:"requestId"`
	Kind      string    `json:"kind"`
	Selection Selection `json:"selection"`
	Action    string    `json:"action"`
	Selfie    string    `json:"selfie"`
	Draft     string    `json:"draft"`
	Receipt   string    `json:"receipt"`
}
type Job struct {
	ID      string `json:"id"`
	User    string `json:"-"`
	Status  string `json:"status"`
	Image   string `json:"image,omitempty"`
	Receipt string `json:"receipt,omitempty"`
	Error   string `json:"error,omitempty"`
	Charged bool   `json:"charged"`
	Expires int64  `json:"-"`
}

func (a *App) signReceipt(v Receipt) string {
	b, _ := json.Marshal(v)
	s := base64.RawURLEncoding.EncodeToString(b)
	return s + "." + a.mac("receipt:"+s)
}
func (a *App) readReceipt(raw, uid string) (Receipt, error) {
	var v Receipt
	data, signature, ok := strings.Cut(raw, ".")
	if !ok || !hmacEqual(signature, a.mac("receipt:"+data)) {
		return v, errors.New("定稿凭证无效，请重新生成")
	}
	b, err := base64.RawURLEncoding.DecodeString(data)
	if err != nil || json.Unmarshal(b, &v) != nil || v.Expires < time.Now().Unix() {
		return v, errors.New("定稿凭证已过期，请重新生成")
	}
	if v.User != uid {
		var target string
		if a.db.QueryRow("SELECT user_id FROM aliases WHERE old_id=?", v.User).Scan(&target) != nil || target != uid {
			return v, errors.New("定稿不属于当前账号")
		}
	}
	return v, nil
}
func (a *App) accept(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Receipt string `json:"receipt"`
	}
	if decode(w, r, &in, 4096) != nil {
		fail(w, 400, "定稿信息无效")
		return
	}
	v, err := a.readReceipt(in.Receipt, current(r).User.ID)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	v.Accepted = true
	v.User = current(r).User.ID
	respond(w, 200, map[string]string{"receipt": a.signReceipt(v)})
}
func imageData(raw string, max int) ([]byte, error) {
	header, data, ok := strings.Cut(raw, ",")
	if !ok || (header != "data:image/png;base64" && header != "data:image/jpeg;base64") {
		return nil, errors.New("仅支持PNG或JPEG图片")
	}
	if len(data) > base64.StdEncoding.EncodedLen(max) {
		return nil, errors.New("图片超过5MB，请压缩后重试")
	}
	b, err := base64.StdEncoding.DecodeString(data)
	if err != nil || len(b) > max {
		return nil, errors.New("图片编码无效或超过限制")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 4096 || cfg.Height > 4096 || cfg.Width*cfg.Height > 16777216 {
		return nil, errors.New("图片损坏或尺寸过大，最长边请控制在4096像素内")
	}
	if (format == "png" && header != "data:image/png;base64") || (format == "jpeg" && header != "data:image/jpeg;base64") {
		return nil, errors.New("图片格式与内容不一致")
	}
	return b, nil
}
func selectionCategory(cfg Settings, s Selection) (Category, error) {
	for _, c := range cfg.Categories {
		if c.ID == s.Category {
			if !contains(c.Clothes, s.Clothes) || !contains(c.Colors, s.Color) {
				return c, errors.New("请选择有效的服装和配色")
			}
			if c.ID == "male" && !contains(c.Weapons, s.Weapon) {
				return c, errors.New("请选择武器")
			}
			if c.ID != "male" && s.Weapon != "" {
				return c, errors.New("当前分类不支持武器")
			}
			return c, nil
		}
	}
	return Category{}, errors.New("请选择人物分类")
}
func renderPrompt(t string, s Selection, c Category, action string) string {
	return strings.NewReplacer("{{category}}", c.Name, "{{clothes}}", s.Clothes, "{{color}}", s.Color, "{{weapon}}", s.Weapon, "{{action}}", action).Replace(t)
}
func (a *App) generate(w http.ResponseWriter, r *http.Request) {
	// 在读取大请求体之前限制并发，避免大量上传同时占用内存。
	select {
	case a.uploads <- struct{}{}:
		defer func() { <-a.uploads }()
	default:
		fail(w, 429, "照片处理繁忙，请稍后重试；本次未扣次")
		return
	}
	uid := current(r).User.ID
	var in GenerateInput
	if err := decode(w, r, &in, 15*1024*1024); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if !idPattern.MatchString(in.RequestID) || (in.Kind != "draft" && in.Kind != "motion") {
		fail(w, 400, "生成请求无效")
		return
	}
	cfg, err := a.settings()
	if err != nil {
		fail(w, 500, "配置读取失败")
		return
	}
	if a.secret("api_key") == "" {
		fail(w, 503, "创作服务尚未配置，请联系管理员；本次不会扣次")
		return
	}
	cat, err := selectionCategory(cfg, in.Selection)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	photo, err := imageData(in.Selfie, 5*1024*1024)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	photoHash := hash(string(photo))
	var rec Receipt
	prompt := cfg.IdentityPrompt + "\n" + cat.Prompt + "\n" + renderPrompt(cfg.DraftPrompt, in.Selection, cat, "")
	images := []string{in.Selfie}
	if in.Kind == "motion" {
		rec, err = a.readReceipt(in.Receipt, uid)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		draft, e := imageData(in.Draft, 5*1024*1024)
		if e != nil {
			fail(w, 400, e.Error())
			return
		}
		if !rec.Accepted || rec.Selfie != photoHash || rec.Draft != hash(string(draft)) || rec.Selection != in.Selection {
			fail(w, 400, "请先确认当前自拍与造型的定稿")
			return
		}
		action := ""
		for _, v := range cat.Actions {
			if v.ID == in.Action {
				action = v.Prompt
			}
		}
		if action == "" {
			fail(w, 400, "请选择有效动作")
			return
		}
		// 动作阶段只包含造型参数，避免将静态定稿的双视图要求带入16帧图。
		appearance := "分类：{{category}}。服装：{{clothes}}。配色：{{color}}。武器：{{weapon}}。"
		prompt = cfg.IdentityPrompt + "\n" + cat.Prompt + "\n" + renderPrompt(appearance, in.Selection, cat, "") + "\n" + renderPrompt(cfg.MotionPrompt, in.Selection, cat, action)
		images = append(images, in.Draft)
	}
	raw, _ := json.Marshal(in)
	digest := hash(string(raw))
	var existing, oldDigest string
	err = a.db.QueryRow("SELECT id,digest FROM jobs WHERE user_id=? AND request_id=?", uid, in.RequestID).Scan(&existing, &oldDigest)
	if err == nil {
		if digest != oldDigest {
			fail(w, 409, "同一请求编号不能用于不同内容")
			return
		}
		respond(w, 202, map[string]string{"id": existing})
		return
	}
	if !a.limit("generate:"+uid, 12, time.Hour) {
		fail(w, 429, "生成请求过于频繁，请稍后再试")
		return
	}
	select {
	case a.slots <- struct{}{}:
	default:
		fail(w, 429, "工作台繁忙，请稍后再试；本次未扣次")
		return
	}
	release := true
	defer func() {
		if release {
			<-a.slots
		}
	}()
	tx, err := a.db.Begin()
	if err != nil {
		fail(w, 500, "创建失败")
		return
	}
	defer tx.Rollback()
	var gift, paid int
	if tx.QueryRow("SELECT gift,paid FROM users WHERE id=? AND disabled=0", uid).Scan(&gift, &paid) != nil {
		fail(w, 403, "账号不可用")
		return
	}
	if gift+paid < 1 {
		fail(w, 402, "创作次数不足，请先兑换次数")
		return
	}
	giftCost, paidCost := 0, 0
	if gift > 0 {
		giftCost = 1
	} else {
		paidCost = 1
	}
	id := token(16)
	if _, err = tx.Exec("INSERT INTO jobs(id,user_id,request_id,digest,kind,status,gift_cost,paid_cost,refund_failure,created) VALUES(?,?,?,?,?,'running',?,?,?,?)", id, uid, in.RequestID, digest, in.Kind, giftCost, paidCost, !cfg.ChargeOnFailure, time.Now().Unix()); err != nil {
		fail(w, 409, "当前账号已有任务，或请求已提交；请等待任务完成")
		return
	}
	if _, err = tx.Exec("UPDATE users SET gift=gift-?,paid=paid-? WHERE id=?", giftCost, paidCost, uid); err != nil {
		fail(w, 500, "扣次失败")
		return
	}
	if tx.Commit() != nil {
		fail(w, 500, "创建失败")
		return
	}
	a.jobsMu.Lock()
	a.jobs[id] = &Job{ID: id, User: uid, Status: "running", Charged: true}
	a.jobsMu.Unlock()
	release = false
	rec = Receipt{User: uid, Selfie: photoHash, Selection: in.Selection, Expires: time.Now().Add(30 * 24 * time.Hour).Unix()}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer func() { <-a.slots }()
		ctx, cancel := context.WithTimeout(a.ctx, 8*time.Minute)
		defer cancel()
		output, e := a.provider(ctx, cfg, prompt, images)
		state := "succeeded"
		receipt := ""
		message := ""
		charged := true
		if e == nil {
			var b []byte
			b, e = imageData(output, 20*1024*1024)
			if e == nil && in.Kind == "draft" {
				if len(b) > 5*1024*1024 {
					e = errors.New("定稿图片超过5MB，请降低后台生成质量后重试")
				} else {
					rec.Draft = hash(string(b))
					receipt = a.signReceipt(rec)
				}
			}
		}
		if e != nil {
			state = "failed"
			message = "图片服务未能完成本次创作，请稍后重试或联系管理员。"
			output = ""
			if !cfg.ChargeOnFailure {
				refund, er := a.db.Begin()
				if er == nil {
					_, er = refund.Exec("UPDATE users SET gift=gift+?,paid=paid+? WHERE id=?", giftCost, paidCost, uid)
					if er == nil {
						_, er = refund.Exec("UPDATE jobs SET gift_cost=0,paid_cost=0 WHERE id=?", id)
					}
					if er == nil {
						er = refund.Commit()
					} else {
						refund.Rollback()
					}
					if er == nil {
						charged = false
					}
				}
			}
		}
		a.db.Exec("UPDATE jobs SET status=? WHERE id=?", state, id)
		a.jobsMu.Lock()
		// Keep temporary results bounded even when many users generate at once.
		var cached int
		for _, job := range a.jobs {
			cached += len(job.Image)
		}
		for cached+len(output) > 64*1024*1024 {
			oldestID := ""
			var oldest int64
			for key, job := range a.jobs {
				if job.Status != "running" && (oldestID == "" || job.Expires < oldest) {
					oldestID, oldest = key, job.Expires
				}
			}
			if oldestID == "" {
				break
			}
			cached -= len(a.jobs[oldestID].Image)
			delete(a.jobs, oldestID)
		}
		a.jobs[id] = &Job{ID: id, User: uid, Status: state, Image: output, Receipt: receipt, Error: message, Charged: charged, Expires: time.Now().Add(10 * time.Minute).Unix()}
		a.jobsMu.Unlock()
	}()
	respond(w, 202, map[string]string{"id": id})
}
func (a *App) getJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	uid := current(r).User.ID
	var owner, status string
	if a.db.QueryRow("SELECT user_id,status FROM jobs WHERE id=?", id).Scan(&owner, &status) != nil || owner != uid {
		fail(w, 404, "任务不存在")
		return
	}
	a.jobsMu.Lock()
	j := a.jobs[id]
	if j != nil {
		copy := *j
		a.jobsMu.Unlock()
		respond(w, 200, copy)
		return
	}
	a.jobsMu.Unlock()
	respond(w, 200, map[string]any{"id": id, "status": "expired", "error": "临时图片已清理或服务已重启。请查看本机作品库；重试不会再次执行旧请求。", "previousStatus": status})
}

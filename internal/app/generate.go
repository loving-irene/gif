package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	_ "golang.org/x/image/webp"
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
	StartedAt      int64        `json:"startedAt"`
	ElapsedSeconds int          `json:"elapsedSeconds"`
	Estimate       TimeEstimate `json:"estimate"`
	ID             string       `json:"id"`
	User           string       `json:"-"`
	Status         string       `json:"status"`
	Image          string       `json:"image,omitempty"`
	Gif            string       `json:"gif,omitempty"`
	Receipt        string       `json:"receipt,omitempty"`
	Error          string       `json:"error,omitempty"`
	Charged        bool         `json:"charged"`
	Expires        int64        `json:"-"`
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
	if !ok || (header != "data:image/png;base64" && header != "data:image/jpeg;base64" && header != "data:image/webp;base64") {
		return nil, errors.New("仅支持PNG、JPEG或WebP图片")
	}
	if len(data) > base64.StdEncoding.EncodedLen(max) {
		return nil, errors.New("图片超过5MB，请压缩后重试")
	}
	b, err := base64.StdEncoding.DecodeString(data)
	if err != nil || len(b) > max {
		return nil, errors.New("图片编码无效或超过限制")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 16384 || cfg.Height > 16384 || cfg.Width*cfg.Height > 50000000 {
		return nil, errors.New("图片损坏或像素过大，请选择不超过5000万像素、最长边不超过16384像素的照片")
	}
	if (format == "png" && header != "data:image/png;base64") || (format == "jpeg" && header != "data:image/jpeg;base64") || (format == "webp" && header != "data:image/webp;base64") {
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
		a.debug(context.WithValue(r.Context(), debugTraceKey{}, existing), "job_reused", map[string]any{"request_id": in.RequestID, "charged_again": false})
		respond(w, 202, map[string]string{"id": existing})
		return
	}
	if !a.limit("generate:"+uid, 12, time.Hour) {
		fail(w, 429, "生成请求过于频繁，请稍后再试")
		return
	}
	// 尝试直接占用生成槽位；占不到时任务进入服务器队列，由调度器稍后启动，
	// 用户关闭页面不影响任务执行。
	haveSlot := false
	select {
	case a.slots <- struct{}{}:
		haveSlot = true
	default:
	}
	input := &jobInput{Prompt: prompt, Images: images, Selection: in.Selection, PhotoHash: photoHash}
	id := token(16)
	created := time.Now().Unix()
	if !haveSlot {
		// 排队任务的输入先落盘再提交事务，保证调度器可见时输入一定存在。
		if b, err := json.Marshal(input); err != nil || a.saveFile(id, "input.json", b) != nil {
			fail(w, 500, "创建失败，请稍后重试；本次未扣次")
			return
		}
	}
	// 占到槽位时，若后续启动失败需要由本函数释放；成功启动后交给 runJob 释放。
	release := haveSlot
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
	// 单用户并发任务数由后台配置（默认5）：达到上限时拒绝新任务。
	var active int
	if tx.QueryRow("SELECT COUNT(*) FROM jobs WHERE user_id=? AND status IN ('queued','running')", uid).Scan(&active) != nil {
		fail(w, 500, "创建失败")
		return
	}
	if active >= cfg.UserConcurrency {
		fail(w, 409, fmt.Sprintf("当前账号已有 %d 个任务在执行，请等待部分任务完成后再提交", cfg.UserConcurrency))
		return
	}
	giftCost, paidCost := 0, 0
	if gift > 0 {
		giftCost = 1
	} else {
		paidCost = 1
	}
	status, started := "queued", int64(0)
	if haveSlot {
		status, started = "running", created
	}
	if _, err = tx.Exec("INSERT INTO jobs(id,user_id,request_id,digest,kind,status,gift_cost,paid_cost,refund_failure,created,started,action,receipt) VALUES(?,?,?,?,?,?,?,?,?,?,?,?, '')", id, uid, in.RequestID, digest, in.Kind, status, giftCost, paidCost, !cfg.ChargeOnFailure, created, started, in.Action); err != nil {
		fail(w, 409, "创建失败，请稍后重试")
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
	estimate := a.estimate(in.Kind, cfg)
	// 个人调用记录（jobs 表）按账号只保留最近的 100 条，超出部分按创建时间淘汰。
	a.pruneCalls(uid)
	a.jobsMu.Lock()
	// 调度器可能在响应返回前就完成极短任务，避免用旧的排队状态覆盖结果。
	if a.jobs[id] == nil {
		a.jobs[id] = &Job{ID: id, User: uid, Status: status, Charged: true, StartedAt: created * 1000, Estimate: estimate}
	}
	a.jobsMu.Unlock()
	traceCtx := context.WithValue(a.ctx, debugTraceKey{}, id)
	a.debug(traceCtx, "job_queued", map[string]any{"request_id": in.RequestID, "kind": in.Kind, "action": in.Action, "model": cfg.Model, "charge_on_failure": cfg.ChargeOnFailure, "queued": !haveSlot})
	if haveSlot {
		release = false
		a.debug(context.WithValue(traceCtx, debugSensitiveKey{}, []string{a.secret("api_key"), prompt, a.env.Secret}), "job_start", nil)
		go a.runJob(id, uid, in.Kind, input)
	} else {
		a.signalDispatch()
	}
	respond(w, 202, map[string]any{"id": id, "estimate": estimate, "elapsedSeconds": 0})
}
func (a *App) getJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	uid := current(r).User.ID
	var owner, status, kind, receipt string
	var created int64
	if a.db.QueryRow("SELECT user_id,status,kind,created,COALESCE(receipt,'') FROM jobs WHERE id=?", id).Scan(&owner, &status, &kind, &created, &receipt) != nil || owner != uid {
		fail(w, 404, "任务不存在")
		return
	}
	a.jobsMu.Lock()
	j := a.jobs[id]
	if j != nil {
		copy := *j
		a.jobsMu.Unlock()
		if copy.Status == "running" || copy.Status == "queued" {
			copy.ElapsedSeconds = int((time.Now().UnixMilli() - copy.StartedAt) / 1000)
		}
		respond(w, 200, copy)
		return
	}
	a.jobsMu.Unlock()
	switch status {
	case "failed":
		respond(w, 200, Job{ID: id, Status: "failed", Error: networkErrorMessage, Charged: true, StartedAt: created * 1000})
	case "succeeded":
		// 内存缓存失效（如服务器重启或缓存淘汰）后，从服务器文件恢复3天内的结果。
		if time.Now().Unix()-created < int64(resultRetention.Seconds()) {
			if stored, err := a.loadStoredJob(id, kind, created, receipt); err == nil {
				respond(w, 200, stored)
				return
			}
		}
		respond(w, 200, map[string]any{"id": id, "status": "expired", "error": networkErrorMessage, "previousStatus": status})
	default:
		respond(w, 200, map[string]any{"id": id, "status": "expired", "error": networkErrorMessage, "previousStatus": status})
	}
}
// pruneCalls 只保留当前账号最近 100 条创作调用记录（jobs 表即调用日志）。
func (a *App) pruneCalls(uid string) {
	a.db.Exec("DELETE FROM jobs WHERE user_id=? AND rowid NOT IN (SELECT rowid FROM jobs WHERE user_id=? ORDER BY created DESC, rowid DESC LIMIT 100)", uid, uid)
}

// calls 返回当前账号最近的创作调用记录（角色定稿 / 动作 GIF，最多 100 条）。
func (a *App) calls(w http.ResponseWriter, r *http.Request) {
	uid := current(r).User.ID
	rows, err := a.db.Query("SELECT kind,COALESCE(action,''),status,gift_cost+paid_cost,created FROM jobs WHERE user_id=? ORDER BY created DESC,rowid DESC LIMIT 100", uid)
	if err != nil {
		fail(w, 500, "读取失败")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var kind, action, status string
		var cost, created int64
		if rows.Scan(&kind, &action, &status, &cost, &created) == nil {
			out = append(out, map[string]any{"kind": kind, "action": action, "status": status, "cost": cost, "created": created})
		}
	}
	respond(w, 200, map[string]any{"items": out})
}
func (a *App) loadStoredJob(id, kind string, created int64, receipt string) (Job, error) {
	b, err := a.readFile(id, "image")
	if err != nil {
		return Job{}, err
	}
	j := Job{ID: id, Status: "succeeded", Image: dataURL(http.DetectContentType(b), b), Receipt: receipt, Charged: true, StartedAt: created * 1000}
	if kind == "motion" {
		if g, err := a.readFile(id, "gif"); err == nil {
			j.Gif = dataURL("image/gif", g)
		}
	}
	return j, nil
}
func dataURL(mime string, b []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b)
}

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
	// Style 是上传自拍后选择的画风编号，参与定稿凭证校验，必须与定稿保持一致。
	Style string `json:"style"`
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
	// AllowDuplicate 只在用户看到重复提示并确认后由页面带上，跳过重复检测。
	// 服务端仍会校验并发上限与剩余次数，重复创建同样计次。
	AllowDuplicate bool `json:"allowDuplicate,omitempty"`
}
type Job struct {
	StartedAt      int64        `json:"startedAt"`
	ElapsedSeconds int          `json:"elapsedSeconds"`
	Estimate       TimeEstimate `json:"estimate"`
	ID             string       `json:"id"`
	User           string       `json:"-"`
	Status         string       `json:"status"`
	// Upstream 表示任务已提交给图像服务、正在等上游产出结果（客户端据此调整提示文案）。
	Upstream bool   `json:"upstream,omitempty"`
	Image    string `json:"image,omitempty"`
	Gif      string `json:"gif,omitempty"`
	Receipt  string `json:"receipt,omitempty"`
	Error    string `json:"error,omitempty"`
	Charged  bool   `json:"charged"`
	Expires  int64  `json:"-"`
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

// duplicateInputDigest 计算“同一份配置”的摘要，用于重复任务检测。
// 与幂等用的 digest 不同：这里刻意排除 requestId、allowDuplicate 等服务端或每次提交
// 都会变化的字段，只保留决定生成结果的输入（任务类型、自拍、定稿、凭证、画风、造型与动作），
// 因此同一份配置换个请求编号再次提交仍会命中同一条记录。
func duplicateInputDigest(in GenerateInput) string {
	raw, _ := json.Marshal(duplicateCanonical{
		Kind:      in.Kind,
		Selection: in.Selection,
		Action:    in.Action,
		Selfie:    in.Selfie,
		Draft:     in.Draft,
		Receipt:   in.Receipt,
	})
	return hash(string(raw))
}

// duplicateCanonical 是配置摘要的固定字段顺序，写入 jobs.dup_digest 后供重复检测比对。
type duplicateCanonical struct {
	Kind      string    `json:"kind"`
	Selection Selection `json:"selection"`
	Action    string    `json:"action"`
	Selfie    string    `json:"selfie"`
	Draft     string    `json:"draft"`
	Receipt   string    `json:"receipt"`
}

// duplicateActiveJob 查询同账号下是否已有配置完全相同的排队/进行中任务。
// 连点两次会生成两个请求编号，因此这里不做“创建时间多久算新任务”的宽限：
// 只要还有同款任务在排队或执行就返回提醒，避免重复提交直接多建一个任务并多扣一次。
// 迁移补写前的旧任务 dup_digest 为空，用当时的 digest 兜底，避免提醒漏掉。
func (a *App) duplicateActiveJob(uid, kind, digest, requestID string) (string, int64, bool) {
	var id string
	var created int64
	err := a.db.QueryRow("SELECT id,created FROM jobs WHERE user_id=? AND kind=? AND status IN ('queued','running','pending_upstream') AND request_id<>? AND (dup_digest=? OR (dup_digest='' AND digest=?)) ORDER BY created,rowid LIMIT 1", uid, kind, requestID, digest, digest).Scan(&id, &created)
	if err != nil {
		return "", 0, false
	}
	return id, created, true
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

// styleOrDefault 解析画风编号：留空时使用默认画风（保持原有轻度Q版效果），
// 其他未知编号一律拒绝，避免客户端自行指定画风。
func styleOrDefault(cfg Settings, id string) (Style, error) {
	if strings.TrimSpace(id) == "" {
		id = defaultStyleID
	}
	for _, s := range cfg.Styles {
		if s.ID == id {
			return s, nil
		}
	}
	return Style{}, errors.New("请选择有效的画风")
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
	// 画风在读取凭证与计算去重摘要之前归一化：留空按默认画风处理，
	// 保证同一请求的摘要、定稿凭证与动作校验使用同一编号。
	style, err := styleOrDefault(cfg, in.Selection.Style)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	in.Selection.Style = style.ID
	photo, err := imageData(in.Selfie, 5*1024*1024)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	photoHash := hash(string(photo))
	var rec Receipt
	prompt := cfg.IdentityPrompt + "\n" + style.Prompt + "\n" + cat.Prompt + "\n" + renderPrompt(cfg.DraftPrompt, in.Selection, cat, "")
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
		// 动作阶段只包含造型参数，避免将静态定稿的双视图要求带入帧图；画风在两个阶段保持一致。
		appearance := "分类：{{category}}。服装：{{clothes}}。配色：{{color}}。武器：{{weapon}}。"
		// 末尾附加以后台配置为准的网格规格说明：模板里写死的格数与所选规格不一致时以此覆盖，
		// 生成侧（格数）与合成侧（切格方式）始终使用同一配置。
		motionPrompt := normalizeMotionPrompt(renderPrompt(cfg.MotionPrompt, in.Selection, cat, action))
		prompt = cfg.IdentityPrompt + "\n" + style.Prompt + "\n" + cat.Prompt + "\n" + renderPrompt(appearance, in.Selection, cat, "") + "\n" + motionPrompt + "\n" + motionSpecPrompt(cfg.MotionGrid)
		images = append(images, in.Draft)
	}
	raw, _ := json.Marshal(in)
	digest := hash(string(raw))
	// 配置摘要（不含请求编号）随任务一起保存，供下一次提交判断“同款配置”是否已在制作。
	dupDigest := duplicateInputDigest(in)
	// SQLite只使用一个连接，但多个独立查询仍可交错；把去重检查和入队视为一个临界区。
	// 图片校验在锁外完成，上游生成异步执行，不占用提交锁。
	a.submitMu.Lock()
	defer a.submitMu.Unlock()
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
	// 同账号已有完全相同的任务在排队或执行时先提醒用户，确认后再创建：
	// 避免重复点击、改完又改回原样或换页面重复提交造成同款任务和次数浪费。
	// 置于限流之前，被拦下的重复请求不计入每小时的生成次数。
	// 已经确认过（in.AllowDuplicate）时不再拦截，由下方的并发与次数校验兜底。
	if !in.AllowDuplicate {
		// 用排除请求编号的配置摘要判断“另一条相同配置的提交”。
		dupID, dupCreated, found := a.duplicateActiveJob(uid, in.Kind, dupDigest, in.RequestID)
		if found {
			a.debug(context.WithValue(r.Context(), debugTraceKey{}, dupID), "job_duplicate_blocked", map[string]any{"request_id": in.RequestID, "kind": in.Kind, "action": in.Action})
			tip := "已经有一个配置相同的任务在制作中"
			if in.Kind == "motion" {
				tip = "这个动作的相同任务已经在制作中"
			}
			respond(w, 409, map[string]any{
				"error":          tip + "，确认要再创建一个吗？重复创建会再消耗 1 次创作次数。",
				"duplicate":      true,
				"existingKind":   in.Kind,
				"existingAction": in.Action,
				"existingAt":     dupCreated,
			})
			return
		}
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
	// 从占用槽位起就登记释放，覆盖输入写盘失败等提前返回路径。
	release := haveSlot
	defer func() {
		if release {
			<-a.slots
			a.signalDispatch()
		}
	}()
	input := &jobInput{Prompt: prompt, Images: images, ImageSize: providerImageSize(in.Kind, cfg.MotionGrid), MotionGrid: cfg.MotionGrid, Selection: in.Selection, PhotoHash: photoHash}
	id := token(16)
	created := time.Now().Unix()
	committed := false
	defer func() {
		if !committed {
			a.removeFile(id, "input.json")
		}
	}()
	// 任务输入统一先落盘再提交事务：排队任务由调度器读回；直接执行的任务在服务重启后
	// 也能凭这份输入重新排队执行，而不是被中断退款。
	if b, err := json.Marshal(input); err != nil || a.saveFile(id, "input.json", b) != nil {
		fail(w, 500, "创建失败，请稍后重试；本次未扣次")
		return
	}
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
	// 文案与前台预检一致（已达任务上限X），走 409 user-facing 提示，不落入通用网络错误文案。
	var active int
	if tx.QueryRow("SELECT COUNT(*) FROM jobs WHERE user_id=? AND status IN ('queued','running','pending_upstream')", uid).Scan(&active) != nil {
		fail(w, 500, "创建失败")
		return
	}
	if active >= cfg.UserConcurrency {
		fail(w, 409, fmt.Sprintf("已达任务上限%d，请等待部分任务完成后再提交", cfg.UserConcurrency))
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
	if _, err = tx.Exec("INSERT INTO jobs(id,user_id,request_id,digest,dup_digest,kind,status,gift_cost,paid_cost,refund_failure,created,started,action,receipt) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,'')", id, uid, in.RequestID, digest, dupDigest, in.Kind, status, giftCost, paidCost, !cfg.ChargeOnFailure, created, started, in.Action); err != nil {
		fail(w, 409, "创建失败，请稍后重试")
		return
	}
	// 消耗随扣次同事务记入统计日志（携带任务类型与人物分类），失败退款时删除，
	// 供每日统计邮件按“定稿图/GIF 动图”“男生/女生/小朋友”等维度汇总实际消耗。
	if _, err = tx.Exec("INSERT INTO usage_stats(job_id,user_id,kind,category,action,created) VALUES(?,?,?,?,?,?)", id, uid, in.Kind, in.Selection.Category, in.Action, created); err != nil {
		fail(w, 500, "创建失败")
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
	committed = true
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
		a.startJob(id, input)
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
	var cost int
	if a.db.QueryRow("SELECT user_id,status,kind,created,COALESCE(receipt,''),gift_cost+paid_cost FROM jobs WHERE id=?", id).Scan(&owner, &status, &kind, &created, &receipt, &cost) != nil || owner != uid {
		fail(w, 404, "任务不存在")
		return
	}
	a.jobsMu.Lock()
	j := a.jobs[id]
	if j != nil && j.Status == status {
		copy := *j
		a.jobsMu.Unlock()
		if jobIsActive(copy.Status) {
			copy.ElapsedSeconds = int((time.Now().UnixMilli() - copy.StartedAt) / 1000)
		}
		respond(w, 200, copy)
		return
	}
	a.jobsMu.Unlock()
	// 内存缓存失效（如服务器重启后仍在等待上游结果）时，按数据库状态继续汇报等待。
	if jobIsActive(status) {
		respond(w, 200, Job{ID: id, Status: status, Upstream: status == statusPendingUpstream, Charged: true, StartedAt: created * 1000, ElapsedSeconds: int(time.Now().Unix() - created)})
		return
	}
	switch status {
	case "failed":
		respond(w, 200, Job{ID: id, Status: "failed", Error: networkErrorMessage, Charged: cost > 0, StartedAt: created * 1000})
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

// pruneCalls 裁剪最近100条以外的终态记录；在途任务必须保留供执行、退款与恢复。
func (a *App) pruneCalls(uid string) {
	a.db.Exec("DELETE FROM jobs WHERE user_id=? AND status NOT IN ('queued','running','pending_upstream') AND rowid NOT IN (SELECT rowid FROM jobs WHERE user_id=? ORDER BY created DESC, rowid DESC LIMIT 100)", uid, uid)
}

// calls 返回当前账号最近的创作调用记录（角色定稿 / 动作 GIF，最多 100 条）。
// 失败记录附带失败原因（error_message），供前端展示更友好的提示。
func (a *App) calls(w http.ResponseWriter, r *http.Request) {
	uid := current(r).User.ID
	rows, err := a.db.Query("SELECT kind,COALESCE(action,''),status,gift_cost+paid_cost,COALESCE(error_message,''),created FROM jobs WHERE user_id=? ORDER BY created DESC,rowid DESC LIMIT 100", uid)
	if err != nil {
		fail(w, 500, "读取失败")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var kind, action, status, message string
		var cost, created int64
		if rows.Scan(&kind, &action, &status, &cost, &message, &created) == nil {
			out = append(out, map[string]any{"kind": kind, "action": action, "status": status, "cost": cost, "error": message, "created": created})
		}
	}
	respond(w, 200, map[string]any{"items": out})
}

// drafts 返回当前账号云端保存的定稿列表（不含图片本体），供页面与本机候选合并实现跨设备同步。
func (a *App) drafts(w http.ResponseWriter, r *http.Request) {
	uid := current(r).User.ID
	rows, err := a.db.Query("SELECT receipt,created,selection FROM drafts WHERE user_id=? ORDER BY created DESC,rowid DESC LIMIT ?", uid, draftsPerUser)
	if err != nil {
		fail(w, 500, "读取失败")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var receipt, selectionJSON string
		var created int64
		if rows.Scan(&receipt, &created, &selectionJSON) == nil {
			selection := Selection{}
			json.Unmarshal([]byte(selectionJSON), &selection)
			out = append(out, map[string]any{"receipt": receipt, "created": created, "selection": selection})
		}
	}
	respond(w, 200, map[string]any{"items": out})
}

// draftImage 返回云端定稿的图片本体；只能读取本人账号的定稿。
func (a *App) draftImage(w http.ResponseWriter, r *http.Request) {
	uid := current(r).User.ID
	receipt := r.PathValue("receipt")
	var image []byte
	if a.db.QueryRow("SELECT image FROM drafts WHERE receipt=? AND user_id=?", receipt, uid).Scan(&image) != nil {
		fail(w, 404, "定稿不存在")
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(image))
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Write(image)
}

// works 返回当前账号云端保存的作品集列表（不含 GIF 本体），供页面与本机作品合并实现跨设备同步。
// 云端副本最多保留 worksRetention（3天），到期由周期清理删除；retentionSeconds 一并下发，
// 供前端区分“保留期内消失=被其他设备删除”与“超过保留期消失=服务器到期清理”。
func (a *App) works(w http.ResponseWriter, r *http.Request) {
	uid := current(r).User.ID
	rows, err := a.db.Query("SELECT id,created,name,category,COALESCE(action,''),gif IS NOT NULL FROM works WHERE user_id=? ORDER BY created DESC,rowid DESC LIMIT ?", uid, worksPerUser)
	if err != nil {
		fail(w, 500, "读取失败")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, category, action string
		var created int64
		var hasGif bool
		if rows.Scan(&id, &created, &name, &category, &action, &hasGif) == nil {
			out = append(out, map[string]any{"id": id, "created": created, "name": name, "category": category, "action": action, "hasGif": hasGif})
		}
	}
	respond(w, 200, map[string]any{"items": out, "retentionSeconds": int64(worksRetention.Seconds())})
}

// workGif 返回云端作品的 GIF 本体；workSheet 返回动作原图（仅 GIF 合成失败时保留）。
// 两者都只能读取本人账号的作品。
func (a *App) workGif(w http.ResponseWriter, r *http.Request)   { a.serveWorkBlob(w, r, "gif") }
func (a *App) workSheet(w http.ResponseWriter, r *http.Request) { a.serveWorkBlob(w, r, "sheet") }

func (a *App) serveWorkBlob(w http.ResponseWriter, r *http.Request, column string) {
	uid := current(r).User.ID
	id := r.PathValue("id")
	var blob []byte
	if !idPattern.MatchString(id) || a.db.QueryRow("SELECT "+column+" FROM works WHERE id=? AND user_id=?", id, uid).Scan(&blob) != nil || blob == nil {
		fail(w, 404, "作品不存在")
		return
	}
	if column == "gif" {
		w.Header().Set("Content-Type", "image/gif")
	} else {
		w.Header().Set("Content-Type", http.DetectContentType(blob))
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Write(blob)
}

// workRemove 删除当前账号的一件云端作品（本机副本由页面自行删除）。
// 对不存在的作品同样返回成功，避免暴露他人作品编号的存在性。
func (a *App) workRemove(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
	}
	if decode(w, r, &in, 4096) != nil || !idPattern.MatchString(in.ID) {
		fail(w, 400, "作品信息无效")
		return
	}
	a.db.Exec("DELETE FROM works WHERE id=? AND user_id=?", in.ID, current(r).User.ID)
	respond(w, 200, map[string]bool{"ok": true})
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

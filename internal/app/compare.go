package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type compareBatch struct {
	ID           string            `json:"id"`
	DraftPrompt  string            `json:"draftPrompt"`
	MotionPrompt string            `json:"motionPrompt"`
	MotionID     string            `json:"motionId"`
	Created      int64             `json:"created"`
	Jobs         []*compareJobItem `json:"jobs"`
}

type compareJobItem struct {
	ID     string `json:"id"`
	Model  string `json:"model"`
	Name   string `json:"name"`
	Vendor string `json:"vendor"`

	DraftStatus string `json:"draftStatus"` // queued|running|succeeded|failed
	DraftError  string `json:"draftError,omitempty"`
	DraftImage  string `json:"-"` // 内存存 data URL，接口用 draftUrl
	DraftURL    string `json:"draftUrl,omitempty"`
	DraftMs     int64  `json:"draftMs,omitempty"`

	AnimStatus string `json:"animStatus"` // queued|running|succeeded|failed|skipped
	AnimError  string `json:"animError,omitempty"`
	AnimGif    string `json:"-"`
	AnimURL    string `json:"animUrl,omitempty"`
	SheetImage string `json:"-"`
	SheetURL   string `json:"sheetUrl,omitempty"`
	AnimMs     int64  `json:"animMs,omitempty"`
}

var (
	compareMu      sync.Mutex
	compareBatches = map[string]*compareBatch{}
)

func (a *App) comparePage(w http.ResponseWriter, r *http.Request) {
	b, err := web.ReadFile("web/compare.html")
	if err != nil {
		fail(w, 500, "页面缺失")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

func (a *App) compareModels(w http.ResponseWriter, r *http.Request) {
	a.debug(r.Context(), "compare_models", map[string]any{"count": len(latestImageModels())})
	respond(w, 200, map[string]any{
		"models":        latestImageModels(),
		"draftPrompt":   compareDraftPrompt,
		"motionPresets": compareMotionPresets(),
		"apiKeySet":     a.secret("api_key") != "",
	})
}

func (a *App) compareSaveAPIKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		APIKey      string `json:"apiKey"`
		ClearAPIKey bool   `json:"clearApiKey"`
	}
	if err := decode(w, r, &in, 4096); err != nil {
		fail(w, 400, err.Error())
		return
	}
	key := strings.TrimSpace(in.APIKey)
	if len(key) > 1024 || strings.ContainsAny(key, "\r\n") {
		fail(w, 400, "密钥格式无效")
		return
	}
	if !in.ClearAPIKey && key == "" {
		fail(w, 400, "请填写 OpenRouter API Key")
		return
	}
	if in.ClearAPIKey {
		if _, err := a.db.Exec("DELETE FROM settings WHERE key='api_key'"); err != nil {
			fail(w, 500, "清除失败")
			return
		}
		a.audit(current(r).User.ID, "compare_api_key_cleared", "")
		a.debug(r.Context(), "compare_api_key", map[string]any{"action": "cleared"})
		respond(w, 200, map[string]any{"ok": true, "apiKeySet": false})
		return
	}
	if err := a.setSecret("api_key", key); err != nil {
		fail(w, 500, "保存失败")
		return
	}
	a.audit(current(r).User.ID, "compare_api_key_saved", "")
	a.debug(r.Context(), "compare_api_key", map[string]any{"action": "saved", "chars": len(key)})
	respond(w, 200, map[string]any{"ok": true, "apiKeySet": true})
}

type compareRunInput struct {
	Selfie       string   `json:"selfie"`
	DraftPrompt  string   `json:"draftPrompt"`
	MotionPrompt string   `json:"motionPrompt"`
	MotionID     string   `json:"motionId"`
	Models       []string `json:"models"`
	Quality      string   `json:"quality"`
}

func (a *App) compareRun(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	body, err := io.ReadAll(io.LimitReader(r.Body, 25<<20+1))
	defer r.Body.Close()
	if err != nil {
		a.debug(r.Context(), "compare_run_reject", map[string]any{"reason": "read_body", "error": errorText(err)})
		fail(w, 400, "请求体读取失败")
		return
	}
	if len(body) > 25<<20 {
		a.debug(r.Context(), "compare_run_reject", map[string]any{"reason": "body_too_large", "bytes": len(body)})
		fail(w, 400, "请求过大（自拍+提示词合计勿超约 25MB）")
		return
	}
	var in compareRunInput
	if err := json.Unmarshal(body, &in); err != nil {
		a.debug(r.Context(), "compare_run_reject", map[string]any{"reason": "json", "bytes": len(body), "error": errorText(err)})
		fail(w, 400, "请求 JSON 无效")
		return
	}
	selfieBytes := 0
	if summaries := imageSummaries([]string{in.Selfie}); len(summaries) > 0 {
		if n, ok := summaries[0]["bytes"].(int); ok {
			selfieBytes = n
		}
	}
	a.debug(r.Context(), "compare_run_recv", map[string]any{
		"body_bytes": len(body), "selfie_bytes": selfieBytes, "models": in.Models,
		"motion_id": in.MotionID, "quality": in.Quality,
		"draft_prompt_chars": utf8.RuneCountInString(in.DraftPrompt),
		"motion_prompt_chars": utf8.RuneCountInString(in.MotionPrompt),
		"origin": r.Header.Get("Origin"), "sec_fetch_site": r.Header.Get("Sec-Fetch-Site"),
	})
	if _, err := imageData(in.Selfie, 12<<20); err != nil {
		a.debug(r.Context(), "compare_run_reject", map[string]any{"reason": "selfie", "error": errorText(err)})
		fail(w, 400, "请上传有效自拍："+err.Error())
		return
	}
	draftPrompt := strings.TrimSpace(in.DraftPrompt)
	if draftPrompt == "" {
		draftPrompt = compareDraftPrompt
	}
	if utf8.RuneCountInString(draftPrompt) < 20 || utf8.RuneCountInString(draftPrompt) > 8000 {
		fail(w, 400, "定稿提示词长度无效")
		return
	}
	motionPrompt := strings.TrimSpace(in.MotionPrompt)
	if motionPrompt == "" {
		for _, p := range compareMotionPresets() {
			if p.ID == in.MotionID || (in.MotionID == "" && p.ID == "eat") {
				motionPrompt = p.Prompt
				in.MotionID = p.ID
				break
			}
		}
	}
	if utf8.RuneCountInString(motionPrompt) < 20 || utf8.RuneCountInString(motionPrompt) > 8000 {
		fail(w, 400, "动作提示词长度无效")
		return
	}
	maxModels := len(latestImageModels())
	if maxModels < 1 {
		maxModels = 20
	}
	if len(in.Models) == 0 || len(in.Models) > maxModels {
		fail(w, 400, fmt.Sprintf("请选择 1–%d 个模型", maxModels))
		return
	}
	if _, err := a.settings(); err != nil || a.secret("api_key") == "" {
		a.debug(r.Context(), "compare_run_reject", map[string]any{"reason": "no_api_key"})
		fail(w, 503, "请先在后台配置 OpenRouter API Key")
		return
	}
	quality := in.Quality
	if quality == "" {
		quality = "high"
	}
	if !contains([]string{"low", "medium", "high", "xhigh", "max"}, quality) {
		fail(w, 400, "图片质量无效")
		return
	}

	batch := &compareBatch{
		ID: token(12), DraftPrompt: draftPrompt, MotionPrompt: motionPrompt,
		MotionID: in.MotionID, Created: time.Now().Unix(),
	}
	seen := map[string]bool{}
	for _, id := range in.Models {
		m, ok := imageModelByID(id)
		if !ok || seen[id] {
			fail(w, 400, "模型无效或重复："+id)
			return
		}
		if !m.Supports {
			fail(w, 400, m.Name+" 不支持自拍参考图，定稿对比请换支持图生图的模型")
			return
		}
		seen[id] = true
		batch.Jobs = append(batch.Jobs, &compareJobItem{
			ID: token(10), Model: m.ID, Name: m.Name, Vendor: m.Vendor,
			DraftStatus: "queued", AnimStatus: "queued",
		})
	}
	compareMu.Lock()
	compareBatches[batch.ID] = batch
	if len(compareBatches) > 20 {
		var oldest string
		var oldestAt int64 = 1 << 62
		for id, b := range compareBatches {
			if b.Created < oldestAt {
				oldestAt, oldest = b.Created, id
			}
		}
		delete(compareBatches, oldest)
	}
	out := publicCompareBatch(batch)
	compareMu.Unlock()

	a.debug(r.Context(), "compare_run_accepted", map[string]any{
		"batch_id": batch.ID, "jobs": len(batch.Jobs), "elapsed_ms": time.Since(started).Milliseconds(),
	})
	respond(w, 202, out)
	for _, job := range batch.Jobs {
		job := job
		go a.runComparePipeline(batch.ID, job, in.Selfie, draftPrompt, motionPrompt, quality)
	}
}

func publicCompareBatch(src *compareBatch) *compareBatch {
	out := &compareBatch{
		ID: src.ID, DraftPrompt: src.DraftPrompt, MotionPrompt: src.MotionPrompt,
		MotionID: src.MotionID, Created: src.Created,
	}
	for _, j := range src.Jobs {
		cp := *j
		if j.DraftImage != "" {
			cp.DraftURL = fmt.Sprintf("/api/admin/compare/batches/%s/jobs/%s/draft", src.ID, j.ID)
		}
		if j.AnimGif != "" {
			cp.AnimURL = fmt.Sprintf("/api/admin/compare/batches/%s/jobs/%s/anim", src.ID, j.ID)
		}
		if j.SheetImage != "" {
			cp.SheetURL = fmt.Sprintf("/api/admin/compare/batches/%s/jobs/%s/sheet", src.ID, j.ID)
		}
		cp.DraftImage, cp.AnimGif, cp.SheetImage = "", "", ""
		out.Jobs = append(out.Jobs, &cp)
	}
	return out
}

func (a *App) runComparePipeline(batchID string, job *compareJobItem, selfie, draftPrompt, motionPrompt, quality string) {
	set := func(fn func()) {
		compareMu.Lock()
		fn()
		compareMu.Unlock()
	}
	trace := context.WithValue(a.ctx, debugTraceKey{}, "compare:"+batchID+":"+job.ID)
	a.debug(trace, "compare_job_start", map[string]any{"batch_id": batchID, "model": job.Model, "name": job.Name})

	set(func() { job.DraftStatus = "running" })
	cfg, err := a.settings()
	if err != nil {
		set(func() { job.DraftStatus, job.DraftError, job.AnimStatus = "failed", "配置读取失败", "skipped" })
		a.debug(trace, "compare_job_failed", map[string]any{"phase": "settings", "error": errorText(err)})
		return
	}
	cfg.Model, cfg.Quality = job.Model, quality
	ctx, cancel := context.WithTimeout(trace, 20*time.Minute)
	defer cancel()

	t0 := time.Now()
	draft, err := a.providerCall(ctx, cfg, draftPrompt, []string{selfie}, "1024x1024", nil)
	set(func() {
		job.DraftMs = time.Since(t0).Milliseconds()
		if err != nil {
			job.DraftStatus, job.DraftError, job.AnimStatus = "failed", trimErr(err), "skipped"
			return
		}
		job.DraftStatus, job.DraftImage = "succeeded", draft
	})
	if err != nil {
		a.debug(trace, "compare_job_failed", map[string]any{"phase": "draft", "error": trimErr(err), "elapsed_ms": time.Since(t0).Milliseconds()})
		return
	}
	a.debug(trace, "compare_draft_ok", map[string]any{"elapsed_ms": time.Since(t0).Milliseconds(), "draft_chars": len(draft)})

	set(func() { job.AnimStatus = "running" })
	t1 := time.Now()
	sheet, err := a.providerCall(ctx, cfg, motionPrompt, []string{selfie, draft}, "1024x1024", nil)
	if err != nil {
		set(func() {
			job.AnimMs = time.Since(t1).Milliseconds()
			job.AnimStatus, job.AnimError = "failed", trimErr(err)
		})
		a.debug(trace, "compare_job_failed", map[string]any{"phase": "motion", "error": trimErr(err), "elapsed_ms": time.Since(t1).Milliseconds()})
		return
	}
	raw, err := imageData(sheet, 20<<20)
	if err != nil {
		set(func() {
			job.AnimMs = time.Since(t1).Milliseconds()
			job.AnimStatus, job.AnimError = "failed", "动作序列图无效"
		})
		a.debug(trace, "compare_job_failed", map[string]any{"phase": "sheet_decode", "error": errorText(err)})
		return
	}
	gifBytes, err := synthesizeGIF(raw, motionSpecOf("5x5"))
	set(func() {
		job.AnimMs = time.Since(t1).Milliseconds()
		job.SheetImage = sheet
		if err != nil {
			job.AnimStatus, job.AnimError = "failed", "切格合成失败："+err.Error()
			return
		}
		job.AnimStatus = "succeeded"
		job.AnimGif = "data:image/gif;base64," + base64.StdEncoding.EncodeToString(gifBytes)
	})
	if err != nil {
		a.debug(trace, "compare_job_failed", map[string]any{"phase": "gif", "error": errorText(err)})
		return
	}
	a.debug(trace, "compare_job_done", map[string]any{
		"draft_ms": job.DraftMs, "anim_ms": job.AnimMs, "gif_bytes": len(gifBytes),
	})
}

func trimErr(err error) string {
	s := err.Error()
	if rs := []rune(s); len(rs) > 240 {
		return string(rs[:240]) + "…"
	}
	return s
}

func (a *App) compareBatchGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	compareMu.Lock()
	src := compareBatches[id]
	if src == nil {
		compareMu.Unlock()
		a.debug(r.Context(), "compare_batch_miss", map[string]any{"batch_id": id})
		fail(w, 404, "对比批次不存在或已过期")
		return
	}
	out := publicCompareBatch(src)
	compareMu.Unlock()
	pending := 0
	for _, j := range out.Jobs {
		if j.DraftStatus == "queued" || j.DraftStatus == "running" || j.AnimStatus == "queued" || j.AnimStatus == "running" {
			pending++
		}
	}
	a.debug(r.Context(), "compare_batch_poll", map[string]any{"batch_id": id, "jobs": len(out.Jobs), "pending": pending})
	respond(w, 200, out)
}

func (a *App) compareJobBlob(w http.ResponseWriter, r *http.Request) {
	batchID, jobID, kind := r.PathValue("id"), r.PathValue("job"), r.PathValue("kind")
	compareMu.Lock()
	src := compareBatches[batchID]
	var raw string
	if src != nil {
		for _, j := range src.Jobs {
			if j.ID != jobID {
				continue
			}
			switch kind {
			case "draft":
				raw = j.DraftImage
			case "anim":
				raw = j.AnimGif
			case "sheet":
				raw = j.SheetImage
			}
			break
		}
	}
	compareMu.Unlock()
	if raw == "" {
		a.debug(r.Context(), "compare_blob_miss", map[string]any{"batch_id": batchID, "job_id": jobID, "kind": kind})
		http.NotFound(w, r)
		return
	}
	header, payload, ok := strings.Cut(raw, ",")
	if !ok || !strings.Contains(header, ";base64") {
		fail(w, 500, "图片数据损坏")
		return
	}
	b, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		fail(w, 500, "图片解码失败")
		return
	}
	ctype := "application/octet-stream"
	switch {
	case strings.Contains(header, "image/gif"):
		ctype = "image/gif"
	case strings.Contains(header, "image/png"):
		ctype = "image/png"
	case strings.Contains(header, "image/jpeg"):
		ctype = "image/jpeg"
	case strings.Contains(header, "image/webp"):
		ctype = "image/webp"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "no-store")
	a.debug(r.Context(), "compare_blob", map[string]any{"batch_id": batchID, "job_id": jobID, "kind": kind, "bytes": len(b)})
	_, _ = w.Write(b)
}

package app

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// jobInput 是排队任务在服务器上暂存的生成输入，调度器启动任务时读回。
type jobInput struct {
	Prompt    string    `json:"prompt"`
	Images    []string  `json:"images"`
	Selection Selection `json:"selection"`
	PhotoHash string    `json:"photoHash"`
}

// signalDispatch 非阻塞通知调度器尝试启动排队任务。
func (a *App) signalDispatch() {
	select {
	case a.dispatch <- struct{}{}:
	default:
	}
}

func (a *App) dispatcher() {
	defer a.wg.Done()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-a.dispatch:
			a.drainQueue()
		}
	}
}

// drainQueue 在槽位空闲时按创建顺序启动排队任务；runJob 负责释放槽位并再次触发调度。
func (a *App) drainQueue() {
	for {
		select {
		case a.slots <- struct{}{}:
		default:
			return
		}
		var id, uid, kind string
		if a.db.QueryRow("SELECT id,user_id,kind FROM jobs WHERE status='queued' ORDER BY created,rowid LIMIT 1").Scan(&id, &uid, &kind) != nil {
			<-a.slots
			return
		}
		if !a.markRunning(id) {
			<-a.slots
			continue
		}
		go a.runJob(id, uid, kind, nil)
	}
}

func (a *App) markRunning(id string) bool {
	res, err := a.db.Exec("UPDATE jobs SET status='running',started=? WHERE id=? AND status='queued'", time.Now().Unix(), id)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return false
	}
	a.jobsMu.Lock()
	if job := a.jobs[id]; job != nil {
		job.Status = "running"
	}
	a.jobsMu.Unlock()
	return true
}

// runJob 执行一次生成任务：调用图像服务、合成 GIF、把结果写入服务器文件并更新状态。
// 调用方必须已经占用生成槽位；任务结束时在这里释放并触发下一次调度。
func (a *App) runJob(id, uid, kind string, inline *jobInput) {
	defer func() { <-a.slots; a.signalDispatch() }()
	var giftCost, paidCost int
	if a.db.QueryRow("SELECT gift_cost,paid_cost FROM jobs WHERE id=?", id).Scan(&giftCost, &paidCost) != nil {
		return
	}
	input := inline
	if input == nil {
		if b, err := a.readFile(id, "input.json"); err == nil {
			var v jobInput
			if json.Unmarshal(b, &v) == nil && v.Prompt != "" && len(v.Images) != 0 {
				input = &v
			}
		}
	}
	a.removeFile(id, "input.json")
	cfg, cfgErr := a.settings()
	traceCtx := context.WithValue(a.ctx, debugTraceKey{}, id)
	started := time.Now()
	output, receipt, gifImage := "", "", ""
	state, message := "succeeded", ""
	charged := true
	var callDuration time.Duration
	var jobErr error
	if cfgErr != nil || input == nil {
		jobErr = errors.New("job input unavailable")
		traceCtx = context.WithValue(traceCtx, debugSensitiveKey{}, []string{a.env.Secret})
	} else {
		traceCtx = context.WithValue(traceCtx, debugSensitiveKey{}, []string{a.secret("api_key"), input.Prompt, a.env.Secret})
	}
	if jobErr == nil {
		ctx, cancel := context.WithTimeout(traceCtx, generationTimeout)
		output, jobErr = a.provider(ctx, cfg, input.Prompt, input.Images)
		callDuration = time.Since(started)
		cancel()
	}
	if jobErr == nil {
		var b []byte
		b, jobErr = imageData(output, 20*1024*1024)
		if jobErr == nil && kind == "draft" && len(b) > 5*1024*1024 {
			jobErr = errors.New("定稿图片超过5MB，请降低后台生成质量后重试")
		}
		if jobErr == nil {
			if err := a.saveFile(id, "image", b); err != nil {
				// 结果写盘失败不影响本次返回，但服务器重启后将无法恢复该结果。
				a.debug(traceCtx, "result_save_error", map[string]any{"error": errorText(err)})
			}
			if kind == "motion" {
				if g, err := synthesizeGIF(b); err != nil {
					a.debug(traceCtx, "gif_synth_error", map[string]any{"error": errorText(err)})
				} else if err := a.saveFile(id, "gif", g); err != nil {
					a.debug(traceCtx, "gif_save_error", map[string]any{"error": errorText(err)})
				} else {
					gifImage = dataURL("image/gif", g)
				}
			}
		}
		if jobErr == nil && kind == "draft" {
			rec := Receipt{User: uid, Selfie: input.PhotoHash, Selection: input.Selection, Expires: time.Now().Add(30 * 24 * time.Hour).Unix(), Draft: hash(string(b))}
			receipt = a.signReceipt(rec)
		}
	}
	if jobErr != nil {
		a.debug(traceCtx, "job_error", map[string]any{"error": jobErr.Error(), "elapsed_ms": time.Since(started).Milliseconds()})
		state = "failed"
		message = networkErrorMessage
		output = ""
		gifImage = ""
		receipt = ""
		if cfgErr == nil && !cfg.ChargeOnFailure {
			charged = false
			if refund, er := a.db.Begin(); er == nil {
				_, er = refund.Exec("UPDATE users SET gift=gift+?,paid=paid+? WHERE id=?", giftCost, paidCost, uid)
				if er == nil {
					_, er = refund.Exec("UPDATE jobs SET gift_cost=0,paid_cost=0 WHERE id=?", id)
				}
				if er == nil {
					er = refund.Commit()
				} else {
					refund.Rollback()
				}
				if er != nil {
					charged = true
				}
			}
		}
	}
	if receipt != "" {
		if _, err := a.db.Exec("UPDATE jobs SET status=?,receipt=? WHERE id=?", state, receipt, id); err != nil {
			a.debug(traceCtx, "status_write_error", map[string]any{"error": errorText(err)})
		}
	} else if _, err := a.db.Exec("UPDATE jobs SET status=? WHERE id=?", state, id); err != nil {
		a.debug(traceCtx, "status_write_error", map[string]any{"error": errorText(err)})
	}
	timingErr := a.recordTiming(id, kind, cfg, callDuration, state)
	nextEstimate := a.estimate(kind, cfg)
	a.debug(traceCtx, "timing_recorded", map[string]any{"duration_ms": callDuration.Milliseconds(), "status": state, "estimate_seconds": nextEstimate.Seconds, "samples": nextEstimate.Samples, "error": errorText(timingErr)})
	a.debug(traceCtx, "job_finished", map[string]any{"status": state, "charged": charged, "elapsed_ms": time.Since(started).Milliseconds(), "result_chars": len(output), "gif_chars": len(gifImage)})
	a.jobsMu.Lock()
	// Keep temporary results bounded even when many users generate at once.
	var cached int
	for _, job := range a.jobs {
		cached += len(job.Image) + len(job.Gif)
	}
	for cached+len(output)+len(gifImage) > 64*1024*1024 {
		oldestID := ""
		var oldest int64
		for key, job := range a.jobs {
			if job.Status != "running" && job.Status != "queued" && (oldestID == "" || job.Expires < oldest) {
				oldestID, oldest = key, job.Expires
			}
		}
		if oldestID == "" {
			break
		}
		cached -= len(a.jobs[oldestID].Image) + len(a.jobs[oldestID].Gif)
		delete(a.jobs, oldestID)
	}
	a.jobs[id] = &Job{ID: id, User: uid, Status: state, Image: output, Gif: gifImage, Receipt: receipt, Error: message, Charged: charged, Expires: time.Now().Add(10 * time.Minute).Unix(), StartedAt: started.UnixMilli(), ElapsedSeconds: int(callDuration.Seconds()), Estimate: nextEstimate}
	a.jobsMu.Unlock()
}

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

// 任务状态：queued（等待服务器槽位）、running（正在执行）、pending_upstream（已提交上游、
// 上游仍在生成，等待继续认领结果）。前两者属于本地排队，第三者是“上游动作时长超过单次等待”。
const (
	statusQueued          = "queued"
	statusRunning         = "running"
	statusPendingUpstream = "pending_upstream"
)

// jobIsActive 判断任务是否仍在处理中（占用并发额度、参与重复提交检测）。
func jobIsActive(status string) bool {
	return status == statusQueued || status == statusRunning || status == statusPendingUpstream
}

// jobState 是 runJob 进入时的任务现场：续查任务沿用同一个上游任务号，不再提交新请求。
type jobState struct {
	ID       string
	Kind     string
	Resumed  bool
	Upstream string
	Budget   time.Duration
}

// timeBudget 是任务从创建起可用的总时长：首次提交给一次提交等待（a.waitBudget），
// 之后按上游时效继续认领；超出后收口，保证客户端轮询窗口内一定给出结论。
func (a *App) timeBudget(jobID string) time.Duration {
	var created int64
	if a.db.QueryRow("SELECT created FROM jobs WHERE id=?", jobID).Scan(&created) != nil {
		return a.submitWait()
	}
	if remaining := a.waitBudget - time.Since(time.Unix(created, 0)); remaining > 0 {
		return remaining
	}
	return 0
}

// submitWait 是单次等待上游的时间上限；waitBudget 是任务从创建起的总认领时长。
func (a *App) submitWait() time.Duration {
	if a.waitBudget > 0 && a.waitBudget < generationTimeout {
		return a.waitBudget
	}
	return generationTimeout
}

// readJobState 读取任务现场并判断是否为续查：已有上游任务号即视为续查。
func (a *App) readJobState(id string) jobState {
	state := jobState{ID: id, Budget: generationTimeout}
	var upstream string
	if a.db.QueryRow("SELECT kind,COALESCE(upstream_task_id,'') FROM jobs WHERE id=?", id).Scan(&state.Kind, &upstream) != nil {
		return state
	}
	state.Upstream = upstream
	state.Resumed = upstream != ""
	state.Budget = a.timeBudget(id)
	return state
}

// recordUpstream 记录上游任务号：此时提交已经成功，超时后据此继续认领同一任务，不会重复提交。
func (a *App) recordUpstream(id, taskID string) {
	if taskID == "" {
		return
	}
	a.db.Exec("UPDATE jobs SET upstream_task_id=? WHERE id=?", taskID, id)
}

// pendingUpstream 把任务转为“等待上游结果”：不退款、不判失败，等上游产出后继续认领。
func (a *App) pendingUpstream(id, taskID string, wait time.Duration) {
	ms := wait.Milliseconds()
	if _, err := a.db.Exec("UPDATE jobs SET status=?,upstream_task_id=?,upstream_wait_ms=upstream_wait_ms+? WHERE id=?", statusPendingUpstream, taskID, ms, id); err != nil {
		a.debug(context.WithValue(a.ctx, debugTraceKey{}, id), "status_write_error", map[string]any{"error": errorText(err)})
	}
	a.jobsMu.Lock()
	if job := a.jobs[id]; job != nil {
		job.Status = statusPendingUpstream
		job.Upstream = true
	}
	a.jobsMu.Unlock()
	a.signalDispatch()
}

// backfillDuplicateDigests 为升级前的排队任务补写 dup_digest（配置摘要）。
// 生成中的任务输入只在内存里，无法补写；jobs.dup_digest 为空时由 digest 兜底比对，
// 不会因此漏掉重复提醒，只是提交新任务时才写入精确的配置摘要。
func (a *App) backfillDuplicateDigests() error {
	rows, err := a.db.Query("SELECT id,kind FROM jobs WHERE status IN ('queued','running') AND dup_digest=''")
	if err != nil {
		return err
	}
	items := [][2]string{}
	for rows.Next() {
		var id, kind string
		if rows.Scan(&id, &kind) == nil {
			items = append(items, [2]string{id, kind})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range items {
		b, err := a.readFile(item[0], "input.json")
		if err != nil {
			continue
		}
		var input jobInput
		if json.Unmarshal(b, &input) != nil || len(input.Images) == 0 {
			continue
		}
		// 动作任务的输入是“自拍 + 定稿”两张图；定稿任务只有自拍。
		in := GenerateInput{Kind: item[1], Selection: input.Selection, Selfie: input.Images[0]}
		if len(input.Images) > 1 {
			in.Draft = input.Images[1]
		}
		if _, err := a.db.Exec("UPDATE jobs SET dup_digest=? WHERE id=?", duplicateInputDigest(in), item[0]); err != nil {
			return err
		}
	}
	return nil
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

// drainQueue 在槽位空闲时按创建顺序启动任务：先补做“等待上游结果”的续查任务，
// 再按创建顺序启动排队任务；runJob 负责释放槽位并再次触发调度。
func (a *App) drainQueue() {
	for {
		select {
		case a.slots <- struct{}{}:
		default:
			return
		}
		var id, uid, kind string
		err := a.db.QueryRow("SELECT id,user_id,kind FROM jobs WHERE status=? ORDER BY created,rowid LIMIT 1", statusPendingUpstream).Scan(&id, &uid, &kind)
		if err != nil {
			// 没有待认领任务时按创建顺序启动排队任务。
			err = a.db.QueryRow("SELECT id,user_id,kind FROM jobs WHERE status=? ORDER BY created,rowid LIMIT 1", statusQueued).Scan(&id, &uid, &kind)
		}
		if err != nil {
			<-a.slots
			return
		}
		if !a.markRunning(id) {
			<-a.slots
			continue
		}
		go a.runJob(id, nil)
	}
}

func (a *App) markRunning(id string) bool {
	res, err := a.db.Exec("UPDATE jobs SET status=?,started=? WHERE id=? AND status<>?", statusRunning, time.Now().Unix(), id, statusRunning)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return false
	}
	a.jobsMu.Lock()
	if job := a.jobs[id]; job != nil {
		job.Status = statusRunning
	}
	a.jobsMu.Unlock()
	return true
}

// applyResult 把上游返回的图片落盘、合成 GIF、签发定稿凭证。
func (a *App) applyResult(ctx context.Context, id, uid, kind, photoHash string, selection Selection, output string) (receipt, gifImage string, err error) {
	b, err := imageData(output, 20*1024*1024)
	if err != nil {
		return "", "", err
	}
	if kind == "draft" && len(b) > 5*1024*1024 {
		return "", "", errors.New("定稿图片超过5MB，请降低后台生成质量后重试")
	}
	if err := a.saveFile(id, "image", b); err != nil {
		// 结果写盘失败不影响本次返回，但服务器重启后将无法恢复该结果。
		a.debug(ctx, "result_save_error", map[string]any{"error": errorText(err)})
	}
	if kind == "motion" {
		if g, err := synthesizeGIF(b); err != nil {
			a.debug(ctx, "gif_synth_error", map[string]any{"error": errorText(err)})
		} else if err := a.saveFile(id, "gif", g); err != nil {
			a.debug(ctx, "gif_save_error", map[string]any{"error": errorText(err)})
		} else {
			gifImage = dataURL("image/gif", g)
		}
	}
	if kind == "draft" {
		rec := Receipt{User: uid, Selfie: photoHash, Selection: selection, Expires: time.Now().Add(30 * 24 * time.Hour).Unix(), Draft: hash(string(b))}
		receipt = a.signReceipt(rec)
	}
	return receipt, gifImage, nil
}

// upstreamTimedOut 判断本次失败是否只是“等上游太久”：只有超时值得留到下一轮继续认领；
// 服务停止（上下文取消）等原因仍按失败收口，避免把中断当成上游还在生成。
func upstreamTimedOut(err error) bool {
	return errors.Is(err, context.DeadlineExceeded)
}

// upstreamRetention 是上游任务可继续认领的时长：超过后按失败收口，不再无限期占用生成槽位。
const upstreamRetention = 30 * time.Minute

// runJob 执行一次生成任务：调用图像服务、合成 GIF、把结果写入服务器文件并更新状态。
// 调用方必须已经占用生成槽位；任务结束时在这里释放并触发下一次调度。
// 上游超过单次等待时间时，任务转为“等待上游结果”，不再重发请求，稍后按同一个上游任务号继续认领。
func (a *App) runJob(id string, inline *jobInput) {
	defer func() { <-a.slots; a.signalDispatch() }()
	state := a.readJobState(id)
	kind := state.Kind
	var giftCost, paidCost int
	if a.db.QueryRow("SELECT gift_cost,paid_cost FROM jobs WHERE id=?", id).Scan(&giftCost, &paidCost) != nil {
		return
	}
	var uid string
	if a.db.QueryRow("SELECT user_id FROM jobs WHERE id=?", id).Scan(&uid) != nil {
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
	if !state.Resumed {
		// 续查任务不再发出新的生成请求，因此保留 input.json 以便后续再次续查。
		a.removeFile(id, "input.json")
	}
	cfg, cfgErr := a.settings()
	traceCtx := context.WithValue(a.ctx, debugTraceKey{}, id)
	started := time.Now()
	output, receipt, gifImage := "", "", ""
	jobState, message := "succeeded", ""
	// upstream 记录本次实际使用的上游任务号：首次提交由回调写入，续查沿用数据库里的值。
	upstream := state.Upstream
	charged := true
	var callDuration time.Duration
	var jobErr error
	prompt := ""
	photoHash := ""
	selection := Selection{}
	if input != nil {
		prompt, photoHash, selection = input.Prompt, input.PhotoHash, input.Selection
	}
	if cfgErr != nil || (input == nil && !state.Resumed) {
		jobErr = errors.New("job input unavailable")
		traceCtx = context.WithValue(traceCtx, debugSensitiveKey{}, []string{a.env.Secret})
	} else {
		traceCtx = context.WithValue(traceCtx, debugSensitiveKey{}, []string{a.secret("api_key"), prompt, a.env.Secret})
	}
	if jobErr == nil {
		// 每一轮只等 submitWait()；续查轮次沿用原上游任务号。
		budget := state.Budget
		if budget <= 0 {
			// 总时长已经耗尽：按失败收口，避免无限占用生成槽位。
			jobErr = errors.New("upstream result window exhausted")
		} else {
			if budget > a.submitWait() {
				budget = a.submitWait()
			}
			ctx, cancel := context.WithTimeout(traceCtx, budget)
			if state.Resumed {
				upstream = state.Upstream
				output, jobErr = a.providerContinue(ctx, cfg, upstream)
			} else {
				// 上游任务号一拿到就记下来，超时后据此继续认领，不会重复提交生成请求。
				output, jobErr = a.providerCall(ctx, cfg, prompt, input.Images, func(taskID string) {
					upstream = taskID
					a.recordUpstream(id, taskID)
				})
			}
			callDuration = time.Since(started)
			cancel()
		}
	}
	if jobErr == nil {
		receipt, gifImage, jobErr = a.applyResult(traceCtx, id, uid, kind, photoHash, selection, output)
	}
	if jobErr != nil && upstreamTimedOut(jobErr) && upstream != "" {
		// 上游仍在生成：保留任务与已扣次数，稍后继续认领同一个上游任务。
		a.debug(traceCtx, "job_waiting_upstream", map[string]any{"error": jobErr.Error(), "elapsed_ms": time.Since(started).Milliseconds()})
		a.pendingUpstream(id, upstream, callDuration)
		return
	}
	if jobErr != nil {
		a.debug(traceCtx, "job_error", map[string]any{"error": jobErr.Error(), "elapsed_ms": time.Since(started).Milliseconds()})
		jobState = "failed"
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
		if _, err := a.db.Exec("UPDATE jobs SET status=?,receipt=? WHERE id=?", jobState, receipt, id); err != nil {
			a.debug(traceCtx, "status_write_error", map[string]any{"error": errorText(err)})
		}
	} else if _, err := a.db.Exec("UPDATE jobs SET status=? WHERE id=?", jobState, id); err != nil {
		a.debug(traceCtx, "status_write_error", map[string]any{"error": errorText(err)})
	}
	timingErr := a.recordTiming(id, kind, cfg, callDuration, jobState)
	nextEstimate := a.estimate(kind, cfg)
	a.debug(traceCtx, "timing_recorded", map[string]any{"duration_ms": callDuration.Milliseconds(), "status": jobState, "estimate_seconds": nextEstimate.Seconds, "min_seconds": nextEstimate.MinSeconds, "max_seconds": nextEstimate.MaxSeconds, "samples": nextEstimate.Samples, "error": errorText(timingErr)})
	a.debug(traceCtx, "job_finished", map[string]any{"status": jobState, "charged": charged, "elapsed_ms": time.Since(started).Milliseconds(), "result_chars": len(output), "gif_chars": len(gifImage)})
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
			if !jobIsActive(job.Status) && (oldestID == "" || job.Expires < oldest) {
				oldestID, oldest = key, job.Expires
			}
		}
		if oldestID == "" {
			break
		}
		cached -= len(a.jobs[oldestID].Image) + len(a.jobs[oldestID].Gif)
		delete(a.jobs, oldestID)
	}
	a.jobs[id] = &Job{ID: id, User: uid, Status: jobState, Image: output, Gif: gifImage, Receipt: receipt, Error: message, Charged: charged, Expires: time.Now().Add(10 * time.Minute).Unix(), StartedAt: started.UnixMilli(), ElapsedSeconds: int(callDuration.Seconds()), Estimate: nextEstimate}
	a.jobsMu.Unlock()
}

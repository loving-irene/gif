package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/png"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// asProviderCall 把只关心“提示词 + 参考图”的测试桩适配为带上游任务号回调的接口；
// 需要验证超时续查的测试直接设置 a.providerCall / a.providerContinue。
func asProviderCall(fn func(context.Context, Settings, string, []string) (string, error)) func(context.Context, Settings, string, []string, func(string)) (string, error) {
	return func(ctx context.Context, cfg Settings, prompt string, images []string, _ func(string)) (string, error) {
		return fn(ctx, cfg, prompt, images)
	}
}

// sampleSheetGrid 生成 cols×cols 网格的 1024×1024 透明 PNG 序列图：
// 每格中央画一个带偏移的不透明色块，其余保持透明，用于验证切格、缩放与透明处置。
func sampleSheetGrid(cols int) string {
	img := image.NewNRGBA(image.Rect(0, 0, 1024, 1024))
	body := color.NRGBA{168, 186, 147, 255}
	head := color.NRGBA{244, 199, 164, 255}
	for n := 0; n < cols*cols; n++ {
		x0, y0 := (n%cols)*1024/cols, (n/cols)*1024/cols
		x1, y1 := ((n%cols)+1)*1024/cols, ((n/cols)+1)*1024/cols
		offset := n % cols * 3
		draw.Draw(img, image.Rect(x0+30+offset, y0+60, x1-30, y1-30), &image.Uniform{body}, image.Point{}, draw.Src)
		draw.Draw(img, image.Rect(x0+30+offset, y0+20, x1-30+offset, y0+70), &image.Uniform{head}, image.Point{}, draw.Src)
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
}

func TestServerGIFSynthesisMatchesBrowserEncoder(t *testing.T) {
	sheet, err := imageData(sampleImage(true), 20*1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	data, err := synthesizeGIF(sheet, motionSpecOf("4x4"))
	if err != nil {
		t.Fatal(err)
	}
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Image) != 16 || g.LoopCount != 0 || g.Config.Width != 256 || g.Config.Height != 256 {
		t.Fatal("GIF animation metadata invalid")
	}
	for i, frame := range g.Image {
		if g.Disposal[i] != gif.DisposalBackground {
			t.Fatal("transparent frame disposal invalid")
		}
		delay := 10
		if i >= 4 && i <= 7 {
			delay = 6
		}
		if g.Delay[i] != delay {
			t.Fatal("frame timing invalid")
		}
		if _, _, _, alpha := frame.At(128, 130).RGBA(); alpha == 0 {
			t.Fatal("moving subject was lost")
		}
		if _, _, _, alpha := frame.At(0, 0).RGBA(); alpha != 0 {
			t.Fatal("transparent background lost")
		}
	}
}

// 5×5 规格与上游约定仍产出 1024×1024 序列图：1024 不能被 5 整除，
// 验证按比例取整切格后得到 25 帧、每帧 128×128，展开阶段（第 7—12 帧）稍快。
func TestServerGIFSynthesis5x5Grid(t *testing.T) {
	sheet, err := imageData(sampleSheetGrid(5), 20*1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	data, err := synthesizeGIF(sheet, motionSpecOf("5x5"))
	if err != nil {
		t.Fatal(err)
	}
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Image) != 25 || g.LoopCount != 0 || g.Config.Width != 128 || g.Config.Height != 128 {
		t.Fatal("GIF animation metadata invalid", len(g.Image), g.Config.Width, g.Config.Height)
	}
	for i, frame := range g.Image {
		if g.Disposal[i] != gif.DisposalBackground {
			t.Fatal("transparent frame disposal invalid")
		}
		delay := 10
		if i >= 6 && i <= 11 {
			delay = 6
		}
		if g.Delay[i] != delay {
			t.Fatal("frame timing invalid")
		}
		if _, _, _, alpha := frame.At(64, 90).RGBA(); alpha == 0 {
			t.Fatal("moving subject was lost")
		}
		if _, _, _, alpha := frame.At(0, 0).RGBA(); alpha != 0 {
			t.Fatal("transparent background lost")
		}
	}
}

func TestJobsQueueWhenSlotsFullAndAdminListsActive(t *testing.T) {
	a := testApp(t)
	admin := codeAdmin(t, a, loginDevice(t, a, "admin"))
	one := loginDevice(t, a, "queue-one")
	two := loginDevice(t, a, "queue-two")
	three := loginDevice(t, a, "queue-three")
	release := make(chan struct{})
	a.providerCall = asProviderCall(func(context.Context, Settings, string, []string) (string, error) {
		<-release
		return sampleImage(false), nil
	})
	first := jobID(t, request(t, a, one, "POST", "/api/generate", draftInput()))
	second := jobID(t, request(t, a, two, "POST", "/api/generate", draftInput()))
	third := jobID(t, request(t, a, three, "POST", "/api/generate", draftInput()))
	// 两个生成槽位都被占用时，第三个任务应排队等待而不是被拒绝。
	var queued Job
	for i := 0; i < 300; i++ {
		w := request(t, a, three, "GET", "/api/jobs/"+third, nil)
		json.Unmarshal(w.Body.Bytes(), &queued)
		if queued.Status == "queued" {
			break
		}
	}
	if queued.Status != "queued" {
		t.Fatal("third job was not queued", queued.Status)
	}
	w := request(t, a, admin, "GET", "/api/admin/jobs", nil)
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list.Items) != 3 {
		t.Fatal("admin job list wrong", w.Code, w.Body.String())
	}
	states := map[string]int{}
	for _, j := range list.Items {
		states[j["status"].(string)]++
	}
	if states["running"] != 2 || states["queued"] != 1 {
		t.Fatal("admin job states wrong", states)
	}
	close(release)
	for _, id := range []string{first, second, third} {
		s := one
		if id == second {
			s = two
		}
		if id == third {
			s = three
		}
		if j := waitJob(t, a, s, id); j.Status != "succeeded" {
			t.Fatal("queued job did not finish", id, j.Status)
		}
	}
	w = request(t, a, admin, "GET", "/api/admin/jobs", nil)
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Items) != 0 {
		t.Fatal("completed jobs should disappear from admin list")
	}
}

// 同账号可并行提交任务：只有达到后台配置的单账号并发上限后才拒绝新任务。
// 前端据此在等待期间保持生成按钮可用，允许边等待边改造型再提交一个定稿。
func TestParallelJobsUpToUserConcurrency(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "parallel")
	cfg, _ := a.settings()
	cfg.UserConcurrency = 2
	raw, _ := json.Marshal(cfg)
	a.db.Exec("UPDATE settings SET value=? WHERE key='config'", string(raw))
	a.db.Exec("UPDATE users SET gift=10 WHERE id=?", s.User.ID)
	release := make(chan struct{})
	a.providerCall = asProviderCall(func(context.Context, Settings, string, []string) (string, error) {
		<-release
		return sampleImage(false), nil
	})
	first := jobID(t, request(t, a, s, "POST", "/api/generate", draftInput()))
	// 第二个任务换一套配置：这里验证并发上限，同款配置的提醒由 TestDuplicateSelection* 覆盖。
	second := jobID(t, request(t, a, s, "POST", "/api/generate", draftInputWith("古代鳞甲", "银灰与藏蓝", "")))
	if first == second {
		t.Fatal("parallel submissions reused one job")
	}
	if w := request(t, a, s, "POST", "/api/generate", draftInputWith("轻甲与短披风", "深红与铁灰", "")); w.Code != 409 {
		t.Fatal("concurrency limit not enforced", w.Code, w.Body.String())
	} else if !strings.Contains(w.Body.String(), "已达任务上限2") {
		t.Fatal("concurrency limit message should state the limit", w.Body.String())
	}
	close(release)
	for _, id := range []string{first, second} {
		if j := waitJob(t, a, s, id); j.Status != "succeeded" {
			t.Fatal("parallel job did not finish", id, j.Status)
		}
	}
	if u, _ := a.readUser(s.User.ID); u.Credits != 8 {
		t.Fatal("unexpected credits", u.Credits)
	}
}

// 同账号已有配置完全相同的任务在排队或执行时，服务端先返回 409 让页面二次确认；
// 用户确认后（allowDuplicate）才创建第二个任务，并照常计次。
// 同一请求编号重传仍走幂等分支，不会误报重复。
func TestDuplicateSelectionRequiresConfirmation(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "duplicate")
	a.db.Exec("UPDATE users SET gift=10 WHERE id=?", s.User.ID)
	release := blockingProvider(a)
	first := draftInput()
	firstID := jobID(t, request(t, a, s, "POST", "/api/generate", first))
	if u, _ := a.readUser(s.User.ID); u.Credits != 9 {
		t.Fatalf("first submission should charge once: %d", u.Credits)
	}
	// 同一请求编号重传：沿用原任务，不算重复、不重复扣次。
	again := request(t, a, s, "POST", "/api/generate", first)
	if again.Code != 202 || jobID(t, again) != firstID {
		t.Fatalf("same request id should reuse the job: %d %s", again.Code, again.Body.String())
	}
	// 同款配置换新请求编号：先提醒，未确认前不能创建。
	same := draftInput()
	w := request(t, a, s, "POST", "/api/generate", same)
	var body map[string]any
	if json.Unmarshal(w.Body.Bytes(), &body) != nil || w.Code != 409 || body["duplicate"] != true {
		t.Fatalf("duplicate selection not blocked: %d %s", w.Code, w.Body.String())
	}
	if body["existingKind"] != "draft" || body["existingAt"] == nil {
		t.Fatalf("duplicate details missing: %s", w.Body.String())
	}
	if u, _ := a.readUser(s.User.ID); u.Credits != 9 {
		t.Fatalf("blocked duplicate must not charge: %d", u.Credits)
	}
	if n := countJobs(t, a, s.User.ID, "queued", "running"); n != 1 {
		t.Fatalf("blocked duplicate created a job: %d", n)
	}
	// 用户确认后允许创建：新任务照常扣次，两个任务各自完成。
	forced := draftInput()
	forced.AllowDuplicate = true
	forcedID := jobID(t, request(t, a, s, "POST", "/api/generate", forced))
	if forcedID == firstID {
		t.Fatal("confirmed duplicate reused the same job")
	}
	// 确认后的第二个任务同样会被并发上限与次数校验约束，先放开阻塞让两个任务结束。
	close(release)
	for _, id := range []string{firstID, forcedID} {
		if j := waitJob(t, a, s, id); j.Status != "succeeded" {
			t.Fatalf("job %s did not finish: %s", id, j.Status)
		}
	}
	if u, _ := a.readUser(s.User.ID); u.Credits != 8 {
		t.Fatalf("confirmed duplicate should charge once: %d", u.Credits)
	}
	// 两个任务都结束后，同款配置不再命中重复提醒，可以直接提交。
	free := draftInput()
	if w := request(t, a, s, "POST", "/api/generate", free); w.Code != 202 {
		t.Fatalf("finished jobs should not block new submissions: %d %s", w.Code, w.Body.String())
	}
}

// 不同账号、不同画风或不同动作都不算重复：只有同账号同款配置才会提醒。
func TestDuplicateSelectionScopedByAccountAndSelection(t *testing.T) {
	a := testApp(t)
	one := loginDevice(t, a, "dup-one")
	two := loginDevice(t, a, "dup-two")
	release := blockingProvider(a)
	defer close(release)
	base := draftInput()
	jobID(t, request(t, a, one, "POST", "/api/generate", base))
	// 另一账号提交同款配置不受影响。
	if w := request(t, a, two, "POST", "/api/generate", base); w.Code != 202 {
		t.Fatalf("other account should not be blocked: %d %s", w.Code, w.Body.String())
	}
	// 同账号换画风属于不同配置，不提醒。
	other := draftInput()
	other.Selection.Style = "ink"
	if w := request(t, a, one, "POST", "/api/generate", other); w.Code != 202 {
		t.Fatalf("different style should not be blocked: %d %s", w.Code, w.Body.String())
	}
	// 同账号同画风再次提交才提醒。
	repeat := base
	repeat.RequestID = token(16)
	if w := request(t, a, one, "POST", "/api/generate", repeat); w.Code != 409 {
		t.Fatalf("same account and selection should be blocked: %d %s", w.Code, w.Body.String())
	}
}

func countJobs(t *testing.T, a *App, uid string, statuses ...string) int {
	t.Helper()
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(statuses)), ",")
	args := make([]any, 0, len(statuses)+1)
	args = append(args, uid)
	for _, s := range statuses {
		args = append(args, s)
	}
	var n int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM jobs WHERE user_id=? AND status IN ("+placeholders+")", args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// 上游生成时间超过单次等待时间（generationTimeout）时，任务不再直接失败：
// 记下上游任务号转为等待上游结果，稍后按同一个任务号继续认领，拿到图片后照常合成 GIF 并计次。
func TestUpstreamResultClaimedAfterTimeout(t *testing.T) {
	a := testApp(t)
	// 轮询间隔缩短，让“提交后多次轮询才拿到结果”在秒级内确定地走完；
	// 首轮只留 2 秒认领窗口，使“等上游超时”快速发生。
	a.pollInterval = 20 * time.Millisecond
	a.waitBudget = 2 * time.Second

	s := loginDevice(t, a, "upstream-wait")
	a.db.Exec("UPDATE users SET gift=3 WHERE id=?", s.User.ID)
	var submits, polls atomic.Int32
	// release 控制上游何时产出图片：超时阶段一直返回“仍在生成”，续查阶段才返回图片。
	var release atomic.Bool
	a.providerClient = func() *http.Client {
		return &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
			body := `{"task_id":"upstream-task-1","task_status":"running"}`
			if r.Method == "POST" {
				submits.Add(1)
			} else {
				polls.Add(1)
				if release.Load() {
					b, _ := json.Marshal(map[string]any{"task_status": "succeed", "task_id": "upstream-task-1", "data": []map[string]string{{"b64_json": strings.SplitN(sampleImage(false), ",", 2)[1]}}})
					body = string(b)
				}
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
	}
	id := jobID(t, request(t, a, s, "POST", "/api/generate", draftInput()))
	// 等首轮真正发出提交（此时 2 秒的等待上下文已经建好）再放宽总认领窗口：
	// 续查轮次才有足够时间轮询到结果，而不是在窗口刚耗尽时立刻按失败收口。
	for i := 0; submits.Load() == 0 && i < 200; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if submits.Load() == 0 {
		t.Fatal("first round never submitted the generation request")
	}
	a.waitBudget = time.Minute
	// 第一次等待耗尽后任务转为“等待上游结果”：不判失败、不掉次数。
	// 该状态会被下一轮续查立刻改回 running，因此用累计等待时长确认它确实出现过。
	for i := 0; i < 2000 && upstreamWaitMS(t, a, id) == 0; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if upstreamWaitMS(t, a, id) == 0 {
		t.Fatal("job never waited for the upstream result")
	}
	if polls.Load() == 0 {
		t.Fatal("timed out before polling the upstream task")
	}
	if u, _ := a.readUser(s.User.ID); u.Credits != 2 {
		t.Fatalf("waiting job must stay charged: %d", u.Credits)
	}
	// 上游产出图片后，调度器按同一个上游任务号继续认领并签发定稿凭证。
	release.Store(true)
	done := waitForStatus(t, a, s, id, "succeeded")
	if done.Receipt == "" {
		t.Fatalf("resumed job should deliver the draft: %+v", done)
	}
	if submits.Load() != 1 {
		t.Fatalf("resume must not submit a new generation request: %d", submits.Load())
	}
	if u, _ := a.readUser(s.User.ID); u.Credits != 2 {
		t.Fatalf("resumed job charged twice: %d", u.Credits)
	}
}

// 服务重启后，已记录上游任务号的在途任务转回“等待上游结果”，由调度器按原任务号续查：
// 不再重复提交生成请求、不重复扣次，续查成功后凭落盘输入签发完整定稿凭证。
func TestRestartResumesUpstreamClaim(t *testing.T) {
	e := Env{Database: filepath.Join(t.TempDir(), "restart-upstream.db"), Secret: strings.Repeat("a", 64), AdminPassword: "test-admin-password-123", APIKey: "fake-only-test-key"}
	a, err := New(e)
	if err != nil {
		t.Fatal(err)
	}
	s := loginDevice(t, a, "restart-upstream")
	// 提交成功拿到上游任务号后阻塞等待：进程被杀时任务停留在 running、上游任务号已落盘。
	release := make(chan struct{})
	a.providerCall = func(ctx context.Context, cfg Settings, prompt string, images []string, onTaskID func(string)) (string, error) {
		onTaskID("upstream-restart-1")
		select {
		case <-release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		return sampleImage(false), nil
	}
	id := jobID(t, request(t, a, s, "POST", "/api/generate", draftInput()))
	for i := 0; i < 200; i++ {
		var up string
		a.db.QueryRow("SELECT upstream_task_id FROM jobs WHERE id=?", id).Scan(&up)
		if up != "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	a.Close()
	a2, err := New(e)
	if err != nil {
		t.Fatal(err)
	}
	defer a2.Close()
	var status string
	if a2.db.QueryRow("SELECT status FROM jobs WHERE id=?", id).Scan(&status); status != statusPendingUpstream {
		t.Fatalf("job with upstream ID should resume waiting: %s", status)
	}
	if u, _ := a2.readUser(s.User.ID); u.Credits != 4 {
		t.Fatalf("resumed job must stay charged: %d", u.Credits)
	}
	// 调度启动后按原任务号续查；期间不允许再提交新的生成请求。
	var submits atomic.Int32
	resumed := make(chan string, 4)
	a2.providerCall = func(ctx context.Context, cfg Settings, prompt string, images []string, onTaskID func(string)) (string, error) {
		submits.Add(1)
		return "", context.DeadlineExceeded
	}
	a2.providerContinue = func(ctx context.Context, cfg Settings, taskID string) (string, error) {
		select {
		case resumed <- taskID:
		default:
		}
		return sampleImage(false), nil
	}
	a2.signalDispatch()
	if j := waitForStatus(t, a2, s, id, "succeeded"); j.Receipt == "" {
		t.Fatalf("resumed job should deliver the draft: %+v", j)
	}
	if taskID := <-resumed; taskID != "upstream-restart-1" {
		t.Fatalf("resume must claim the original upstream task: %s", taskID)
	}
	if submits.Load() != 0 {
		t.Fatal("restart resume must not submit a new generation request")
	}
	if u, _ := a2.readUser(s.User.ID); u.Credits != 4 {
		t.Fatalf("resumed job charged twice: %d", u.Credits)
	}
	close(release)
}

// 等待上游结果的任务超过总认领时效后按失败收口，避免一直占着并发额度与生成槽位。
func TestUpstreamWaitWindowCollapsesToFailure(t *testing.T) {
	a := testApp(t)
	setChargeOnFailure(t, a, false)
	// 轮询间隔与首轮窗口都缩短，让任务在秒级内确定地进入“等待上游结果”。
	a.pollInterval = 20 * time.Millisecond
	a.waitBudget = 2 * time.Second

	s := loginDevice(t, a, "upstream-expire")
	a.db.Exec("UPDATE users SET gift=3 WHERE id=?", s.User.ID)
	a.providerClient = func() *http.Client {
		return &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"task_id":"stuck-task","task_status":"running"}`))}, nil
		})}
	}
	id := jobID(t, request(t, a, s, "POST", "/api/generate", draftInput()))
	// 上游一直不产出：任务先进入“等待上游结果”，总认领窗口耗尽后按失败收口。
	if j := waitForStatus(t, a, s, id, "failed"); j.Error != networkErrorMessage {
		t.Fatalf("stale waiting job should fail with the network message: %+v", j)
	}
	if upstreamWaitMS(t, a, id) == 0 {
		t.Fatal("job failed without ever waiting for the upstream result")
	}
	if u, _ := a.readUser(s.User.ID); u.Credits != 3 {
		t.Fatalf("failed waiting job was not refunded: %d", u.Credits)
	}
	// 内存缓存失效（服务重启）后，查询接口按数据库状态继续汇报等待，并带上 upstream 标记。
	// 先停掉调度器：任务失败会触发一次兜底调度，避免它把手工置回的等待状态又改成 running。
	a.cancel()
	a.db.Exec("UPDATE jobs SET status=? WHERE id=?", statusPendingUpstream, id)
	a.jobsMu.Lock()
	a.jobs = map[string]*Job{}
	a.jobsMu.Unlock()
	if j := fetchJSONJob(t, a, s, id); j.Status != statusPendingUpstream || !j.Upstream {
		t.Fatalf("waiting job should be reported as upstream: %+v", j)
	}
}

// waitForStatus 直查数据库直到任务进入指定状态；不走 HTTP 查询，避免长时间等待时撞上接口限流。
// 只用于等待 succeeded / failed 这类终态：中间的 pending_upstream 会被下一轮续查立刻改写。
func waitForStatus(t *testing.T, a *App, s *testSession, id, want string) Job {
	t.Helper()
	for i := 0; i < 2000; i++ {
		var status string
		if a.db.QueryRow("SELECT status FROM jobs WHERE id=?", id).Scan(&status) != nil {
			t.Fatal("job row missing")
		}
		if status == want {
			return fetchJSONJob(t, a, s, id)
		}
		if status == "failed" || status == "succeeded" {
			t.Fatalf("job reached %s while waiting for %s", status, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job never reached %s", want)
	return Job{}
}

// upstreamWaitMS 读取任务累计等待上游结果的时长：pending_upstream 只是瞬时状态
// （调度器会立刻用同一个上游任务号续查并改回 running），累计时长才是持久证据。
func upstreamWaitMS(t *testing.T, a *App, id string) int64 {
	t.Helper()
	var ms int64
	if a.db.QueryRow("SELECT upstream_wait_ms FROM jobs WHERE id=?", id).Scan(&ms) != nil {
		t.Fatal("job row missing")
	}
	return ms
}

func TestJobResultsSurviveMemoryCacheLoss(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "keep")
	a.providerCall = asProviderCall(func(context.Context, Settings, string, []string) (string, error) {
		return sampleImage(false), nil
	})
	id := jobID(t, request(t, a, s, "POST", "/api/generate", draftInput()))
	j := waitJob(t, a, s, id)
	if j.Status != "succeeded" || j.Receipt == "" {
		t.Fatal(j)
	}
	// 模拟内存缓存被淘汰或服务器重启后：结果应从服务器文件恢复，直到保留期结束。
	a.jobsMu.Lock()
	a.jobs = map[string]*Job{}
	a.jobsMu.Unlock()
	w := request(t, a, s, "GET", "/api/jobs/"+id, nil)
	var restored Job
	json.Unmarshal(w.Body.Bytes(), &restored)
	if restored.Status != "succeeded" || restored.Receipt != j.Receipt || len(restored.Image) == 0 {
		t.Fatal("stored result not restored", restored.Status, restored.Receipt)
	}
	if restored.Image[:22] != "data:image/png;base64," {
		t.Fatal("restored image is not a PNG data URL")
	}
}

func TestMotionJobDeliversServerGIF(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "motion")
	a.providerCall = asProviderCall(func(ctx context.Context, cfg Settings, prompt string, images []string) (string, error) {
		return sampleImage(len(images) == 2), nil
	})
	input := draftInput()
	id := jobID(t, request(t, a, s, "POST", "/api/generate", input))
	j := waitJob(t, a, s, id)
	w := request(t, a, s, "POST", "/api/accept", map[string]string{"receipt": j.Receipt})
	var accepted map[string]string
	json.Unmarshal(w.Body.Bytes(), &accepted)
	motion := GenerateInput{RequestID: token(16), Kind: "motion", Selection: input.Selection, Action: "attack", Selfie: input.Selfie, Draft: j.Image, Receipt: accepted["receipt"]}
	id = jobID(t, request(t, a, s, "POST", "/api/generate", motion))
	j = waitJob(t, a, s, id)
	if j.Status != "succeeded" || len(j.Gif) == 0 {
		t.Fatal("motion job missing server GIF", j.Status)
	}
	raw, err := base64.StdEncoding.DecodeString(j.Gif[len("data:image/gif;base64,"):])
	if err != nil {
		t.Fatal(err)
	}
	g, err := gif.DecodeAll(bytes.NewReader(raw))
	if err != nil || len(g.Image) != 16 {
		t.Fatal("server GIF invalid", err)
	}
	a.jobsMu.Lock()
	a.jobs = map[string]*Job{}
	a.jobsMu.Unlock()
	w = request(t, a, s, "GET", "/api/jobs/"+id, nil)
	var restored Job
	json.Unmarshal(w.Body.Bytes(), &restored)
	if restored.Status != "succeeded" || len(restored.Gif) == 0 || len(restored.Image) == 0 {
		t.Fatal("stored motion result not restored")
	}
}

// 后台切换动作序列图规格为 5×5 后：动作提示词末尾附加规格说明（以此为准），
// 服务器按 5×5 切格合成 25 帧、每帧 128×128 的 GIF。
func TestMotionJobDeliversServerGIF5x5(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "motion-5x5")
	cfg, _ := a.settings()
	cfg.MotionGrid = "5x5"
	raw, _ := json.Marshal(cfg)
	a.db.Exec("UPDATE settings SET value=? WHERE key='config'", string(raw))
	var prompts []string
	a.providerCall = asProviderCall(func(ctx context.Context, cfg Settings, prompt string, images []string) (string, error) {
		prompts = append(prompts, prompt)
		if len(images) == 2 {
			return sampleSheetGrid(5), nil
		}
		return sampleImage(false), nil
	})
	input := draftInput()
	id := jobID(t, request(t, a, s, "POST", "/api/generate", input))
	j := waitJob(t, a, s, id)
	w := request(t, a, s, "POST", "/api/accept", map[string]string{"receipt": j.Receipt})
	var accepted map[string]string
	json.Unmarshal(w.Body.Bytes(), &accepted)
	motion := GenerateInput{RequestID: token(16), Kind: "motion", Selection: input.Selection, Action: "attack", Selfie: input.Selfie, Draft: j.Image, Receipt: accepted["receipt"]}
	id = jobID(t, request(t, a, s, "POST", "/api/generate", motion))
	j = waitJob(t, a, s, id)
	if j.Status != "succeeded" || len(j.Gif) == 0 {
		t.Fatal("motion job missing server GIF", j.Status)
	}
	if len(prompts) != 2 || !strings.Contains(prompts[1], "严格5列×5行共25格") || !strings.Contains(prompts[1], "以此为准") {
		t.Fatal("motion prompt missing 5x5 grid spec override")
	}
	gifBytes, err := base64.StdEncoding.DecodeString(j.Gif[len("data:image/gif;base64,"):])
	if err != nil {
		t.Fatal(err)
	}
	g, err := gif.DecodeAll(bytes.NewReader(gifBytes))
	if err != nil || len(g.Image) != 25 || g.Config.Width != 128 || g.Config.Height != 128 {
		t.Fatal("server GIF not 5x5 spec", err, len(g.Image), g.Config.Width)
	}
}

// 同一账号的多个动作可以同时制作（“让我的角色动起来”整批并行提交）：
// 动作配置摘要包含动作编号，因此不同动作并行提交不会被“同款配置重复提交”拦截，
// 服务端只按单账号并发上限与全局生成槽位排队，不会被串行化成一个一个做。
func TestParallelMotionActionsRunTogether(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "motion-parallel")
	a.db.Exec("UPDATE users SET gift=10 WHERE id=?", s.User.ID)
	a.providerCall = asProviderCall(func(context.Context, Settings, string, []string) (string, error) {
		return sampleImage(false), nil
	})
	input := draftInput()
	draft := waitJob(t, a, s, jobID(t, request(t, a, s, "POST", "/api/generate", input)))
	w := request(t, a, s, "POST", "/api/accept", map[string]string{"receipt": draft.Receipt})
	var accepted map[string]string
	json.Unmarshal(w.Body.Bytes(), &accepted)
	// 两个动作都先卡在上游：能同时处于“排队/生成中”，说明没有被串行化。
	release := make(chan struct{})
	a.providerCall = asProviderCall(func(context.Context, Settings, string, []string) (string, error) {
		<-release
		return sampleImage(true), nil
	})
	ids := make([]string, 0, 2)
	for _, action := range []string{"attack", "guard"} {
		ids = append(ids, jobID(t, request(t, a, s, "POST", "/api/generate", GenerateInput{
			RequestID: token(16), Kind: "motion", Selection: input.Selection, Action: action,
			Selfie: input.Selfie, Draft: draft.Image, Receipt: accepted["receipt"],
		})))
	}
	if ids[0] == ids[1] {
		t.Fatal("parallel motions reused one job")
	}
	if n := countJobs(t, a, s.User.ID, "queued", "running"); n != 2 {
		t.Fatalf("parallel motions were serialized: %d active", n)
	}
	close(release)
	for _, id := range ids {
		j := waitJob(t, a, s, id)
		if j.Status != "succeeded" || len(j.Gif) == 0 {
			t.Fatal("parallel motion did not finish", id, j.Status)
		}
	}
	if u, _ := a.readUser(s.User.ID); u.Credits != 7 {
		t.Fatalf("unexpected credits: %d", u.Credits)
	}
}

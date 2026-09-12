package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image/gif"
	"strings"
	"testing"
)

func TestServerGIFSynthesisMatchesBrowserEncoder(t *testing.T) {
	sheet, err := imageData(sampleImage(true), 20*1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	data, err := synthesizeGIF(sheet)
	if err != nil {
		t.Fatal(err)
	}
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Image) != gifOutputFrames || g.LoopCount != 0 || g.Config.Width != 256 || g.Config.Height != 256 {
		t.Fatal("GIF animation metadata invalid")
	}
	for i, frame := range g.Image {
		if g.Disposal[i] != gif.DisposalBackground {
			t.Fatal("transparent frame disposal invalid")
		}
		if g.Delay[i] != gifFrameDelay {
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

func TestJobsQueueWhenSlotsFullAndAdminListsActive(t *testing.T) {
	a := testApp(t)
	// 本测试需要4个设备账号（管理员+3位用户），放宽同网络注册上限。
	cfg, _ := a.settings()
	cfg.RegistrationDailyLimit = 100
	raw, _ := json.Marshal(cfg)
	a.db.Exec("UPDATE settings SET value=? WHERE key='config'", string(raw))
	admin := codeAdmin(t, a, loginDevice(t, a, "admin"))
	one := loginDevice(t, a, "queue-one")
	two := loginDevice(t, a, "queue-two")
	three := loginDevice(t, a, "queue-three")
	release := make(chan struct{})
	a.provider = func(context.Context, Settings, string, []string) (string, error) {
		<-release
		return sampleImage(false), nil
	}
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
	a.provider = func(context.Context, Settings, string, []string) (string, error) {
		<-release
		return sampleImage(false), nil
	}
	first := jobID(t, request(t, a, s, "POST", "/api/generate", draftInput()))
	// 第二个任务换一套配置：这里验证并发上限，同款配置的提醒由 TestDuplicateSelection* 覆盖。
	second := jobID(t, request(t, a, s, "POST", "/api/generate", draftInputWith("古代鳞甲", "银灰与藏蓝", "")))
	if first == second {
		t.Fatal("parallel submissions reused one job")
	}
	if w := request(t, a, s, "POST", "/api/generate", draftInputWith("轻甲与短披风", "深红与铁灰", "")); w.Code != 409 {
		t.Fatal("concurrency limit not enforced", w.Code, w.Body.String())
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

func TestJobResultsSurviveMemoryCacheLoss(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "keep")
	a.provider = func(context.Context, Settings, string, []string) (string, error) {
		return sampleImage(false), nil
	}
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
	a.provider = func(ctx context.Context, cfg Settings, prompt string, images []string) (string, error) {
		return sampleImage(len(images) == 2), nil
	}
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
	if err != nil || len(g.Image) != gifOutputFrames {
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

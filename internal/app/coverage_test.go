package app

// 补齐各功能模块的测试覆盖：结果文件过期、任务越权、排队任务重启/输入丢失、
// 限流窗口、请求解码、配置校验、管理用户与邮箱配置、旧库索引迁移、单用户并发配置。
import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func instantProvider(a *App) {
	a.providerCall = asProviderCall(func(context.Context, Settings, string, []string) (string, error) {
		return sampleImage(false), nil
	})
}
func blockingProvider(a *App) chan struct{} {
	release := make(chan struct{})
	a.providerCall = asProviderCall(func(context.Context, Settings, string, []string) (string, error) {
		<-release
		return sampleImage(false), nil
	})
	return release
}
func setChargeOnFailure(t *testing.T, a *App, value bool) {
	t.Helper()
	cfg, _ := a.settings()
	cfg.ChargeOnFailure = value
	raw, _ := json.Marshal(cfg)
	if _, err := a.db.Exec("UPDATE settings SET value=? WHERE key='config'", string(raw)); err != nil {
		t.Fatal(err)
	}
}

func TestResultFilesExpireAfterRetention(t *testing.T) {
	a := testApp(t)
	instantProvider(a)
	one := loginDevice(t, a, "expire-one")
	two := loginDevice(t, a, "expire-two")
	oldID := jobID(t, request(t, a, one, "POST", "/api/generate", draftInput()))
	if j := waitJob(t, a, one, oldID); j.Status != "succeeded" {
		t.Fatal(j)
	}
	freshID := jobID(t, request(t, a, two, "POST", "/api/generate", draftInput()))
	if j := waitJob(t, a, two, freshID); j.Status != "succeeded" {
		t.Fatal(j)
	}
	// 把第一个任务标记为4天前，模拟超过保留期。
	stale := time.Now().Add(-4 * 24 * time.Hour)
	a.db.Exec("UPDATE jobs SET created=? WHERE id=?", stale.Unix(), oldID)
	if err := os.Chtimes(a.filePath(oldID, "image"), stale, stale); err != nil {
		t.Fatal(err)
	}
	a.cleanupFiles()
	if _, err := a.readFile(oldID, "image"); !os.IsNotExist(err) {
		t.Fatal("expired result file was not cleaned")
	}
	if _, err := a.readFile(freshID, "image"); err != nil {
		t.Fatal("fresh result file was removed")
	}
	a.jobsMu.Lock()
	a.jobs = map[string]*Job{}
	a.jobsMu.Unlock()
	w := request(t, a, one, "GET", "/api/jobs/"+oldID, nil)
	var expired map[string]any
	json.Unmarshal(w.Body.Bytes(), &expired)
	if w.Code != 200 || expired["status"] != "expired" || expired["previousStatus"] != "succeeded" {
		t.Fatal("stale job should report expired", w.Code, w.Body.String())
	}
	if j := fetchJSONJob(t, a, two, freshID); j.Status != "succeeded" || len(j.Image) == 0 {
		t.Fatal("fresh job lost after cleanup")
	}
}

func fetchJSONJob(t *testing.T, a *App, s *testSession, id string) Job {
	t.Helper()
	w := request(t, a, s, "GET", "/api/jobs/"+id, nil)
	var j Job
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &j) != nil {
		t.Fatalf("getJob %d %s", w.Code, w.Body.String())
	}
	return j
}

func TestJobOwnershipAndInterruptedStatus(t *testing.T) {
	a := testApp(t)
	instantProvider(a)
	one := loginDevice(t, a, "owner-one")
	two := loginDevice(t, a, "owner-two")
	id := jobID(t, request(t, a, one, "POST", "/api/generate", draftInput()))
	waitJob(t, a, one, id)
	if w := request(t, a, two, "GET", "/api/jobs/"+id, nil); w.Code != 404 {
		t.Fatal("other user could read the job", w.Code)
	}
	// 中断任务在内存缓存失效后按 expired 汇报，保留原状态供前端提示。
	a.db.Exec("UPDATE jobs SET status='interrupted' WHERE id=?", id)
	a.jobsMu.Lock()
	a.jobs = map[string]*Job{}
	a.jobsMu.Unlock()
	w := request(t, a, one, "GET", "/api/jobs/"+id, nil)
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "expired" || body["previousStatus"] != "interrupted" {
		t.Fatal("interrupted job status wrong", w.Body.String())
	}
}

// 服务重启不再一律中断退款：输入仍在服务器的任务重新排队并继续执行（不重复扣次）；
// 输入缺失的任务（旧版本运行中任务的输入只在内存）才按中断收口并退款。
func TestRestartRecoversJobsInsteadOfInterrupting(t *testing.T) {
	e := Env{Database: filepath.Join(t.TempDir(), "restart-recover.db"), Secret: strings.Repeat("a", 64), AdminPassword: "test-admin-password-123", APIKey: "fake-only-test-key"}
	a, err := New(e)
	if err != nil {
		t.Fatal(err)
	}
	setChargeOnFailure(t, a, false)
	sessions := []*testSession{loginDevice(t, a, "recover-a"), loginDevice(t, a, "recover-b"), loginDevice(t, a, "recover-c")}
	release := blockingProvider(a)
	ids := make([]string, 3)
	for i, s := range sessions {
		ids[i] = jobID(t, request(t, a, s, "POST", "/api/generate", draftInput()))
	}
	// 三人都被扣1次；第三个任务在队列中等待。
	for _, s := range sessions {
		if u, _ := a.readUser(s.User.ID); u.Credits != 4 {
			t.Fatalf("pre-restart credits %d", u.Credits)
		}
	}
	// 第一个任务模拟旧版本行为：运行中任务的输入只在内存，重启后无从恢复。
	a.removeFile(ids[0], "input.json")
	a.Close()
	a2, err := New(e)
	if err != nil {
		t.Fatal(err)
	}
	defer a2.Close()
	// 输入缺失的任务被中断并退款。
	var status string
	if a2.db.QueryRow("SELECT status FROM jobs WHERE id=?", ids[0]).Scan(&status); status != "interrupted" {
		t.Fatalf("job without input should be interrupted: %s", status)
	}
	if u, _ := a2.readUser(sessions[0].User.ID); u.Credits != 5 {
		t.Fatalf("interrupted job not refunded: %d", u.Credits)
	}
	// 输入仍在的任务重新排队，保持已扣次，不退款。
	for _, id := range ids[1:] {
		if a2.db.QueryRow("SELECT status FROM jobs WHERE id=?", id).Scan(&status); status != "queued" {
			t.Fatalf("recoverable job should be requeued: %s", status)
		}
	}
	for _, s := range sessions[1:] {
		if u, _ := a2.readUser(s.User.ID); u.Credits != 4 {
			t.Fatalf("requeued job should stay charged: %d", u.Credits)
		}
	}
	// 恢复排队的任务在调度启动后重新执行并成功。
	instantProvider(a2)
	a2.signalDispatch()
	for i := 1; i < len(ids); i++ {
		if j := waitJob(t, a2, sessions[i], ids[i]); j.Status != "succeeded" {
			t.Fatalf("recovered job %s: %+v", ids[i], j)
		}
	}
	close(release)
}

func TestQueuedJobWithoutInputFailsAndRefunds(t *testing.T) {
	a := testApp(t)
	setChargeOnFailure(t, a, false)
	users := []*testSession{loginDevice(t, a, "lost-a"), loginDevice(t, a, "lost-b"), loginDevice(t, a, "lost-c")}
	release := blockingProvider(a)
	ids := make([]string, 3)
	for i, u := range users {
		ids[i] = jobID(t, request(t, a, u, "POST", "/api/generate", draftInput()))
	}
	// 删除排队任务的输入文件：调度器启动后应按失败处理并退回次数。
	a.removeFile(ids[2], "input.json")
	close(release)
	j := waitJob(t, a, users[2], ids[2])
	if j.Status != "failed" || j.Error != networkErrorMessage {
		t.Fatal("missing input should fail with unified message", j.Status, j.Error)
	}
	if u, _ := a.readUser(users[2].User.ID); u.Credits != 5 {
		t.Fatalf("lost-input job not refunded: %d", u.Credits)
	}
	for i := 0; i < 2; i++ {
		if jj := waitJob(t, a, users[i], ids[i]); jj.Status != "succeeded" {
			t.Fatal("other jobs affected", jj.Status)
		}
	}
	// 失败任务的输入残留与结果文件不应存在。
	if _, err := a.readFile(ids[2], "image"); !os.IsNotExist(err) {
		t.Fatal("failed job should not store result")
	}
}

func TestRateLimitWindowResets(t *testing.T) {
	a := testApp(t)
	key := "suite:limit"
	for i := 0; i < 2; i++ {
		if !a.limit(key, 2, time.Minute) {
			t.Fatal("limit rejected inside window")
		}
	}
	if a.limit(key, 2, time.Minute) {
		t.Fatal("limit allowed over quota")
	}
	a.db.Exec("UPDATE rate_limits SET expires=0 WHERE bucket=?", key)
	if !a.limit(key, 2, time.Minute) {
		t.Fatal("limit did not reset after window")
	}
}

func TestDecodeRejectsInvalidRequests(t *testing.T) {
	cases := []string{
		`{"receipt":"r","extra":1}`, // 未知字段
		`{"receipt":"r"} {"a":1}`,   // 多个JSON值
		`not-json`,                  // 非JSON
		`{"receipt":"` + strings.Repeat("x", 9000) + `"}`, // 超过大小限制
	}
	for _, body := range cases {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		w := httptest.NewRecorder()
		var v struct {
			Receipt string `json:"receipt"`
		}
		if decode(w, r, &v, 4096) == nil {
			t.Fatal("invalid request accepted", body[:20])
		}
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"receipt":"ok"}`))
	w := httptest.NewRecorder()
	var v struct {
		Receipt string `json:"receipt"`
	}
	if decode(w, r, &v, 4096) != nil || v.Receipt != "ok" {
		t.Fatal("valid request rejected")
	}
}

func TestValidateSettingsRejectsBadConfigs(t *testing.T) {
	a := testApp(t)
	base, err := a.settings()
	if err != nil {
		t.Fatal(err)
	}
	if validateSettings(base) != nil {
		t.Fatal("default settings rejected")
	}
	cases := []struct {
		name   string
		mutate func(*Settings)
	}{
		{"兑换帮助超长", func(s *Settings) { s.RedeemHelp = strings.Repeat("字", 2001) }},
		{"默认次数越界", func(s *Settings) { s.DefaultCredits = 1001 }},
		{"分类数量错误", func(s *Settings) { s.Categories = s.Categories[:2] }},
		{"接口地址非法", func(s *Settings) { s.APIBase = "https://evil.example/api/v1" }},
		{"模型非法", func(s *Settings) { s.Model = "other-model" }},
		{"画质非法", func(s *Settings) { s.Quality = "ultra" }},
		{"定稿提示词过短", func(s *Settings) { s.DraftPrompt = "太短" }},
		{"动作名称为空", func(s *Settings) { s.Categories[0].Actions[0].Name = " " }},
		{"动作编号非法", func(s *Settings) { s.Categories[0].Actions[0].ID = "bad id" }},
		{"男生分类缺武器", func(s *Settings) { s.Categories[0].Weapons = nil }},
		{"下载域名为空", func(s *Settings) { s.AssetHosts = nil }},
		{"下载域名不完整", func(s *Settings) { s.AssetHosts = []string{"geekai"} }},
		{"画风数量错误", func(s *Settings) { s.Styles = s.Styles[:2] }},
		{"画风编号非法", func(s *Settings) { s.Styles[1].ID = "other" }},
		{"画风名称为空", func(s *Settings) { s.Styles[0].Name = " " }},
		{"画风提示词过短", func(s *Settings) { s.Styles[2].Prompt = "太短" }},
		{"动作序列图规格非法", func(s *Settings) { s.MotionGrid = "6x6" }},
	}
	for _, tc := range cases {
		s := base
		tc.mutate(&s)
		if validateSettings(s) == nil {
			t.Fatal("invalid settings accepted:", tc.name)
		}
	}
}

func TestAdminUserSearchUpdateAndDisable(t *testing.T) {
	a := testApp(t)
	admin := codeAdmin(t, a, loginDevice(t, a, "admin"))
	one := loginDevice(t, a, "user-one")
	two := loginDevice(t, a, "user-two")
	a.db.Exec("UPDATE users SET email='person@example.com' WHERE id=?", one.User.ID)
	w := request(t, a, admin, "GET", "/api/admin/users?q=person@example.com", nil)
	var found struct {
		Items []User `json:"items"`
	}
	json.Unmarshal(w.Body.Bytes(), &found)
	if len(found.Items) != 1 || found.Items[0].ID != one.User.ID {
		t.Fatal("email search wrong", w.Body.String())
	}
	if w = request(t, a, admin, "POST", "/api/admin/users", map[string]any{"id": one.User.ID, "add": 3, "disabled": false}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if u, _ := a.readUser(one.User.ID); u.Credits != 8 {
		t.Fatal("credits not added", u.Credits)
	}
	if w = request(t, a, admin, "POST", "/api/admin/users", map[string]any{"id": "missing", "add": 0, "disabled": false}); w.Code != 404 {
		t.Fatal("unknown account accepted")
	}
	if w = request(t, a, admin, "POST", "/api/admin/users", map[string]any{"id": one.User.ID, "add": -1, "disabled": false}); w.Code != 400 {
		t.Fatal("negative add accepted")
	}
	if w = request(t, a, admin, "POST", "/api/admin/users", map[string]any{"id": one.User.ID, "add": 0, "disabled": true}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request(t, a, one, "GET", "/api/me", nil); w.Code != 401 {
		t.Fatal("disabled session remained valid")
	}
	var sessions int
	a.db.QueryRow("SELECT COUNT(*) FROM sessions WHERE user_id=?", one.User.ID).Scan(&sessions)
	if sessions != 0 {
		t.Fatal("disabled sessions not cleared")
	}
	if w = request(t, a, two, "GET", "/api/me", nil); w.Code != 200 {
		t.Fatal("other user affected by disable")
	}
}

func TestAdminSettingsRejectBadMailConfig(t *testing.T) {
	a := testApp(t)
	admin := codeAdmin(t, a, loginDevice(t, a, "admin"))
	cfg, _ := a.settings()
	cases := []struct {
		name   string
		mutate func(*Settings)
	}{
		{"主机含非法字符", func(s *Settings) { s.MailHost = "bad host" }},
		{"端口不支持", func(s *Settings) { s.MailHost = "smtp.example.com"; s.MailPort = "25" }},
		{"发件邮箱无效", func(s *Settings) { s.MailHost = "smtp.example.com"; s.MailFrom = "not-an-email" }},
	}
	for _, tc := range cases {
		s := cfg
		tc.mutate(&s)
		w := request(t, a, admin, "POST", "/api/admin/settings", map[string]any{"settings": s})
		if w.Code != 400 {
			t.Fatal("bad mail config accepted:", tc.name, w.Code, w.Body.String())
		}
	}
}

func TestLegacyJobsSchemaDropsActiveIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-jobs.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// 重建旧版任务表：缺少 started/action/receipt 列，唯一索引只约束 running。
	_, err = db.Exec(`CREATE TABLE jobs(id TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id),request_id TEXT NOT NULL,digest TEXT NOT NULL,kind TEXT NOT NULL,status TEXT NOT NULL,gift_cost INTEGER NOT NULL,paid_cost INTEGER NOT NULL,refund_failure INTEGER NOT NULL DEFAULT 0,created INTEGER NOT NULL,UNIQUE(user_id,request_id));
 CREATE UNIQUE INDEX one_active_job ON jobs(user_id) WHERE status='running';`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	e := Env{Database: path, Secret: strings.Repeat("a", 64), AdminPassword: "test-admin-password-123"}
	a, err := New(e)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	// 重开一次验证迁移幂等。
	a.Close()
	if a, err = New(e); err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.db.Exec("INSERT INTO users(id,gift,paid,created) VALUES('legacy-user',5,0,0)")
	if _, err = a.db.Exec("INSERT INTO jobs(id,user_id,request_id,digest,kind,status,gift_cost,paid_cost,created,started,action,receipt) VALUES('q1','legacy-user','r1','d','draft','queued',0,0,0,0,'','')"); err != nil {
		t.Fatal("new columns missing after migration:", err)
	}
	// 单用户并发数改为后台按数量校验，旧库的唯一索引应被删除。
	var indexes int
	if err = a.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='one_active_job'").Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if indexes != 0 {
		t.Fatal("legacy one_active_job index should be dropped after migration")
	}
	if _, err = a.db.Exec("INSERT INTO jobs(id,user_id,request_id,digest,kind,status,gift_cost,paid_cost,created,started,action,receipt) VALUES('q2','legacy-user','r2','d','draft','queued',0,0,0,0,'','')"); err != nil {
		t.Fatal("multiple active jobs should be allowed after migration:", err)
	}
	// 重复提交检测需要新增的配置摘要列。
	if _, err = a.db.Exec("UPDATE jobs SET dup_digest='x' WHERE id='q1'"); err != nil {
		t.Fatal("dup_digest column missing after migration:", err)
	}
}

func TestUserConcurrencyConfigAndLimit(t *testing.T) {
	a := testApp(t)
	cfg, _ := a.settings()
	if cfg.UserConcurrency != 5 {
		t.Fatal("default user concurrency should be 5")
	}
	// 前台配置接口应返回并发数与服务器生成槽位（动作批次据此估算并行宽度）。
	s := loginDevice(t, a, "concurrency-one")
	w := request(t, a, s, "GET", "/api/catalog", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"userConcurrency":5`) {
		t.Fatal("catalog missing userConcurrency", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), fmt.Sprintf(`"generationSlots":%d`, generationSlots)) {
		t.Fatal("catalog missing generationSlots", w.Code, w.Body.String())
	}
	a.db.Exec("UPDATE users SET gift=20 WHERE id=?", s.User.ID)
	release := blockingProvider(a)
	// 每次提交都换一个配置：这里验证的是单账号并发上限，不是同款配置的重复提醒。
	dress := [][2]string{{"古代札甲", "玄黑与暗金"}, {"古代鳞甲", "银灰与藏蓝"}, {"轻甲与短披风", "深红与铁灰"}, {"古代札甲", "银灰与藏蓝"}, {"古代鳞甲", "深红与铁灰"}}
	ids := make([]string, len(dress))
	for i, c := range dress {
		ids[i] = jobID(t, request(t, a, s, "POST", "/api/generate", draftInputWith(c[0], c[1], "")))
	}
	// 默认并发5：第6个任务被拒绝且不扣次。
	if w := request(t, a, s, "POST", "/api/generate", draftInput()); w.Code != 409 {
		t.Fatal("over-limit job accepted:", w.Code, w.Body.String())
	}
	if u, _ := a.readUser(s.User.ID); u.Credits != 15 {
		t.Fatalf("over-limit job charged credits: %d", u.Credits)
	}
	close(release)
	for _, id := range ids {
		if j := waitJob(t, a, s, id); j.Status != "succeeded" {
			t.Fatal(j)
		}
	}
	// 后台可调整并发数，范围外拒绝；调整后按新上限校验。
	admin := codeAdmin(t, a, loginDevice(t, a, "admin-concurrency"))
	for _, bad := range []int{0, 21} {
		badCfg := cfg
		badCfg.UserConcurrency = bad
		if w := request(t, a, admin, "POST", "/api/admin/settings", map[string]any{"settings": badCfg}); w.Code != 400 {
			t.Fatal("invalid concurrency accepted:", bad, w.Code, w.Body.String())
		}
	}
	newCfg := cfg
	newCfg.UserConcurrency = 2
	if w := request(t, a, admin, "POST", "/api/admin/settings", map[string]any{"settings": newCfg}); w.Code != 200 {
		t.Fatal("valid concurrency rejected:", w.Code, w.Body.String())
	}
	release2 := blockingProvider(a)
	for i := 0; i < 2; i++ {
		jobID(t, request(t, a, s, "POST", "/api/generate", draftInputWith("古代札甲", "玄黑与暗金", []string{"", "chibi"}[i])))
	}
	if w := request(t, a, s, "POST", "/api/generate", draftInputWith("古代鳞甲", "玄黑与暗金", "ink")); w.Code != 409 {
		t.Fatal("configured limit not enforced:", w.Code, w.Body.String())
	}
	close(release2)
}

func TestAccountNameSaveAndValidation(t *testing.T) {
	a := testApp(t)
	one := loginDevice(t, a, "name-one")
	// 有安全隐患或不合规的账户名一律拒绝：HTML/注入字符、空格、表情、超长、纯空白。
	for _, bad := range []string{
		"<script>",
		"a&b",
		"who/am I",
		`"quote"`,
		"emoji😀",
		strings.Repeat("长", 21),
		"   ",
	} {
		w := request(t, a, one, "POST", "/api/account/name", map[string]string{"name": bad})
		if w.Code != 400 {
			t.Fatal("bad name accepted:", bad, w.Code, w.Body.String())
		}
	}
	// 合法账户名（中文/字母/数字/_/-/·）保存成功并回读。
	name := "小可爱_01·好"
	w := request(t, a, one, "POST", "/api/account/name", map[string]string{"name": name})
	if w.Code != 200 {
		t.Fatal("valid name rejected:", w.Code, w.Body.String())
	}
	var saved struct {
		User User `json:"user"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil || saved.User.Name != name {
		t.Fatal("name not saved:", w.Body.String())
	}
	w = request(t, a, one, "GET", "/api/me", nil)
	var me struct {
		User User `json:"user"`
	}
	json.Unmarshal(w.Body.Bytes(), &me)
	if me.User.Name != name {
		t.Fatal("name missing in /api/me:", w.Body.String())
	}
	// 管理端可按账户名搜索到用户。
	admin := codeAdmin(t, a, one)
	w = request(t, a, admin, "GET", "/api/admin/users?q=%E5%B0%8F%E5%8F%AF%E7%88%B1", nil)
	var list struct {
		Items []User `json:"items"`
		Total int    `json:"total"`
	}
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Items) != 1 || list.Items[0].Name != name || list.Total != 1 {
		t.Fatal("admin search by name failed:", w.Body.String())
	}
}

func TestDefaultUserNameInitialized(t *testing.T) {
	a := testApp(t)
	// 新账号注册即初始化默认用户名：前缀“用户”+ 当天年月日 + 4 位随机数。
	day := time.Now().Format("20060102")
	one := loginDevice(t, a, "default-name-one")
	if !regexp.MustCompile(`^用户` + day + `\d{4}$`).MatchString(one.User.Name) {
		t.Fatal("unexpected default name:", one.User.Name)
	}
	// 不同新账号的默认用户名互不重复。
	two := loginDevice(t, a, "default-name-two")
	if two.User.Name == one.User.Name {
		t.Fatal("duplicated default name:", one.User.Name)
	}
	// 功能上线前的旧账号未设置用户名，保持为空，由管理端列表展示“-”。
	if _, err := a.db.Exec("INSERT INTO users(id,gift,created) VALUES('legacy-user',5,0)"); err != nil {
		t.Fatal(err)
	}
	admin := codeAdmin(t, a, one)
	w := request(t, a, admin, "GET", "/api/admin/users?q="+url.QueryEscape(one.User.Name), nil)
	var list struct {
		Items []User `json:"items"`
		Total int    `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || list.Total != 1 || len(list.Items) != 1 || list.Items[0].Name != one.User.Name {
		t.Fatal("admin search by default name failed:", w.Body.String())
	}
	w = request(t, a, admin, "GET", "/api/admin/users?q=legacy-user", nil)
	json.Unmarshal(w.Body.Bytes(), &list)
	if list.Total != 1 || len(list.Items) != 1 || list.Items[0].Name != "" {
		t.Fatal("legacy user should keep empty name:", w.Body.String())
	}
}

// TestCallsListAndRetention 验证个人调用记录接口与按账号仅保留最近 100 条的策略。
func TestCallsListAndRetention(t *testing.T) {
	a := testApp(t)
	one := loginDevice(t, a, "calls-one")
	uid := one.User.ID
	// 105 条历史调用：前 5 条角色定稿，其余为动作 GIF，created 递增。
	for i := 0; i < 105; i++ {
		kind, action := "motion", "wave"
		if i < 5 {
			kind, action = "draft", ""
		}
		if _, err := a.db.Exec("INSERT INTO jobs(id,user_id,request_id,digest,kind,status,gift_cost,paid_cost,refund_failure,created,started,action,receipt) VALUES(?,?,?,?,?,?,?,?,0,?,0,?,'')",
			fmt.Sprintf("job-%03d", i), uid, fmt.Sprintf("req-%03d", i), "", kind, "succeeded", 1, 0, i, action); err != nil {
			t.Fatal(err)
		}
	}
	w := request(t, a, one, "GET", "/api/calls", nil)
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list.Items) != 100 {
		t.Fatal("calls list wrong:", w.Body.String())
	}
	// 最新记录在前：created=104 的动作 GIF 排第一，仅保留窗口内的记录可见。
	first := list.Items[0]
	if first["created"].(float64) != 104 || first["kind"] != "motion" || first["action"] != "wave" || first["status"] != "succeeded" || first["cost"].(float64) != 1 {
		t.Fatal("newest call wrong:", first)
	}
	// 淘汰后每个账号仅保留最近 100 条调用记录。
	a.pruneCalls(uid)
	var n int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM jobs WHERE user_id=?", uid).Scan(&n); err != nil || n != 100 {
		t.Fatal("retention wrong:", n, err)
	}
	// 未登录不可访问。
	if w = request(t, a, nil, "GET", "/api/calls", nil); w.Code != 401 {
		t.Fatal("calls should require auth")
	}
}

func TestAdminListPagination(t *testing.T) {
	a := testApp(t)
	admin := codeAdmin(t, a, loginDevice(t, a, "admin"))
	// 创建 101 个兑换码，验证列表分页：第 1 页 100 条，第 2 页 1 条。
	for _, count := range []int{100, 1} {
		w := request(t, a, admin, "POST", "/api/admin/codes", map[string]any{"count": count, "credits": 1, "label": "分页"})
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	type page struct {
		Items    []map[string]any `json:"items"`
		Page     int              `json:"page"`
		PageSize int              `json:"pageSize"`
		Total    int              `json:"total"`
	}
	w := request(t, a, admin, "GET", "/api/admin/codes?page=1", nil)
	var p1 page
	json.Unmarshal(w.Body.Bytes(), &p1)
	if len(p1.Items) != 100 || p1.Page != 1 || p1.PageSize != 100 || p1.Total != 101 {
		t.Fatal("codes page 1 wrong:", w.Body.String()[:min(300, len(w.Body.String()))])
	}
	w = request(t, a, admin, "GET", "/api/admin/codes?page=2", nil)
	var p2 page
	json.Unmarshal(w.Body.Bytes(), &p2)
	if len(p2.Items) != 1 || p2.Page != 2 || p2.Total != 101 {
		t.Fatal("codes page 2 wrong:", w.Body.String())
	}
	// 非法页码回落到第 1 页。
	w = request(t, a, admin, "GET", "/api/admin/codes?page=abc", nil)
	var p0 page
	json.Unmarshal(w.Body.Bytes(), &p0)
	if p0.Page != 1 || len(p0.Items) != 100 {
		t.Fatal("bad page fallback wrong:", w.Body.String())
	}
}

// TestAdminJobsPagination 验证任务列表分页与总数：每用户一个活跃任务，排队任务 150 个分两页，完成不计入。
func TestAdminJobsPagination(t *testing.T) {
	a := testApp(t)
	admin := codeAdmin(t, a, loginDevice(t, a, "admin"))
	for i := 0; i < 150; i++ {
		id := fmt.Sprintf("page-user-%03d", i)
		if _, err := a.db.Exec("INSERT INTO users(id,email,name,gift,paid,disabled,created) VALUES(?,?,'','0',0,0,0)", id, id+"@example.invalid"); err != nil {
			t.Fatal(err)
		}
		kind := "draft"
		if i%2 == 1 {
			kind = "motion"
		}
		if _, err := a.db.Exec("INSERT INTO jobs(id,user_id,request_id,digest,kind,status,gift_cost,paid_cost,created,started,action,receipt) VALUES(?,?,?,'d',?,'queued',1,0,?,0,'','')",
			fmt.Sprintf("job-%03d", i), id, fmt.Sprintf("req-%03d", i), kind, int64(1000+i)); err != nil {
			t.Fatal(err)
		}
	}
	type page struct {
		Items    []map[string]any `json:"items"`
		Page     int              `json:"page"`
		PageSize int              `json:"pageSize"`
		Total    int              `json:"total"`
	}
	var p1, p2 page
	w := request(t, a, admin, "GET", "/api/admin/jobs?page=1", nil)
	json.Unmarshal(w.Body.Bytes(), &p1)
	if len(p1.Items) != 100 || p1.Page != 1 || p1.PageSize != 100 || p1.Total != 150 {
		t.Fatal("jobs page 1 wrong:", w.Body.String())
	}
	// 排序按创建时间升序：第 1 页应从最早的 job-000 开始。
	if p1.Items[0]["id"] != "job-000" || p1.Items[99]["id"] != "job-099" {
		t.Fatal("jobs ordering wrong:", w.Body.String())
	}
	w = request(t, a, admin, "GET", "/api/admin/jobs?page=2", nil)
	json.Unmarshal(w.Body.Bytes(), &p2)
	if len(p2.Items) != 50 || p2.Page != 2 || p2.Total != 150 {
		t.Fatal("jobs page 2 wrong:", w.Body.String())
	}
	// 完成后的任务从列表消失且不计入总数。
	if _, err := a.db.Exec("UPDATE jobs SET status='succeeded' WHERE id IN (SELECT id FROM jobs LIMIT 50)"); err != nil {
		t.Fatal(err)
	}
	var p3 page
	w = request(t, a, admin, "GET", "/api/admin/jobs", nil)
	json.Unmarshal(w.Body.Bytes(), &p3)
	if p3.Total != 100 || len(p3.Items) != 100 {
		t.Fatal("completed jobs should not count:", w.Body.String())
	}
}

// TestAdminUsersPagination 验证账号管理分页：总数含既有账号，排序按创建时间倒序，搜索可叠加分页。
func TestAdminUsersPagination(t *testing.T) {
	a := testApp(t)
	admin := codeAdmin(t, a, loginDevice(t, a, "admin"))
	var base int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&base); err != nil {
		t.Fatal(err)
	}
	// created 取远大于当前时间戳的值，保证插入用户占据列表最前且顺序确定。
	for i := 0; i < 120; i++ {
		id := fmt.Sprintf("page-user-%03d", i)
		if _, err := a.db.Exec("INSERT INTO users(id,email,name,gift,paid,disabled,created) VALUES(?,?,?,'2',0,0,?)",
			id, id+"@example.com", fmt.Sprintf("用户%03d", i), int64(2000000000+i)); err != nil {
			t.Fatal(err)
		}
	}
	type page struct {
		Items []User `json:"items"`
		Page  int    `json:"page"`
		Total int    `json:"total"`
	}
	var p1, p2 page
	w := request(t, a, admin, "GET", "/api/admin/users?page=1", nil)
	json.Unmarshal(w.Body.Bytes(), &p1)
	if len(p1.Items) != 100 || p1.Page != 1 || p1.Total != base+120 {
		t.Fatal("users page 1 wrong:", w.Body.String())
	}
	if p1.Items[0].ID != "page-user-119" || p1.Items[99].ID != "page-user-020" {
		t.Fatal("users ordering wrong:", p1.Items[0].ID, p1.Items[99].ID)
	}
	// 账号列表必须返回创建时间，供后台展示。
	if p1.Items[0].Created != int64(2000000119) || p1.Items[99].Created != int64(2000000020) {
		t.Fatal("users created missing:", p1.Items[0].Created, p1.Items[99].Created)
	}
	w = request(t, a, admin, "GET", "/api/admin/users?page=2", nil)
	json.Unmarshal(w.Body.Bytes(), &p2)
	if len(p2.Items) != 20+base || p2.Total != base+120 {
		t.Fatal("users page 2 wrong:", w.Body.String())
	}
	// 搜索命中子集：page-user-11 前缀匹配 110~119 共 10 个账号。
	w = request(t, a, admin, "GET", "/api/admin/users?q=page-user-11", nil)
	var p3 page
	json.Unmarshal(w.Body.Bytes(), &p3)
	if len(p3.Items) != 10 || p3.Total != 10 {
		t.Fatal("users search wrong:", w.Body.String())
	}
}

// TestAdminAuditPagination 验证操作记录分页：第 1 页最新在前，第 3 页为剩余 50 条。
func TestAdminAuditPagination(t *testing.T) {
	a := testApp(t)
	admin := codeAdmin(t, a, loginDevice(t, a, "admin"))
	var base int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM audit").Scan(&base); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 250; i++ {
		if _, err := a.db.Exec("INSERT INTO audit(actor,event,target,created) VALUES('admin','pagination',?,?)", fmt.Sprintf("t-%03d", i), int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	type page struct {
		Items []map[string]any `json:"items"`
		Page  int              `json:"page"`
		Total int              `json:"total"`
	}
	var p1, p3 page
	w := request(t, a, admin, "GET", "/api/admin/audit?page=1", nil)
	json.Unmarshal(w.Body.Bytes(), &p1)
	if len(p1.Items) != 100 || p1.Total != base+250 {
		t.Fatal("audit page 1 wrong:", w.Body.String())
	}
	// ORDER BY id DESC：最新插入的记录在最前。
	if p1.Items[0]["target"] != "t-249" {
		t.Fatal("audit ordering wrong:", w.Body.String())
	}
	w = request(t, a, admin, "GET", "/api/admin/audit?page=3", nil)
	json.Unmarshal(w.Body.Bytes(), &p3)
	if len(p3.Items) != 50+base || p3.Page != 3 || p3.Total != base+250 {
		t.Fatal("audit page 3 wrong:", w.Body.String())
	}
}

// TestAdminListsIncludeUserName 验证任务、兑换码、操作记录列表均返回账号对应的用户名。
func TestAdminListsIncludeUserName(t *testing.T) {
	a := testApp(t)
	admin := codeAdmin(t, a, loginDevice(t, a, "admin"))
	if _, err := a.db.Exec("INSERT INTO users(id,email,name,gift,paid,disabled,created) VALUES('named-user','named@example.com','有名字','5',0,0,0)"); err != nil {
		t.Fatal(err)
	}
	// 任务列表：排队任务展示所属账号的用户名。
	if _, err := a.db.Exec("INSERT INTO jobs(id,user_id,request_id,digest,kind,status,gift_cost,paid_cost,created,started,action,receipt) VALUES('job-named','named-user','req-named','d','draft','queued',1,0,1,0,'','')"); err != nil {
		t.Fatal(err)
	}
	// 兑换码列表：已兑换的码展示使用者用户名。
	if _, err := a.db.Exec("INSERT INTO codes(hash,label,credits,used_by,used_at,created,marked,encrypted_code) VALUES('hash-named','名单',5,'named-user',10,10,0,'')"); err != nil {
		t.Fatal(err)
	}
	// 操作记录：展示操作人用户名。
	if _, err := a.db.Exec("INSERT INTO audit(actor,event,target,created) VALUES('named-user','redeem','5',1)"); err != nil {
		t.Fatal(err)
	}
	type page struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	check := func(path, key string) {
		t.Helper()
		w := request(t, a, admin, "GET", path, nil)
		var p page
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || len(p.Items) == 0 {
			t.Fatal(path, "empty:", w.Body.String())
		}
		if p.Items[0][key] != "有名字" {
			t.Fatal(path, "missing", key, ":", w.Body.String())
		}
	}
	check("/api/admin/jobs", "userName")
	check("/api/admin/codes", "usedByName")
	check("/api/admin/audit", "actorName")
}

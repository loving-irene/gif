package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testSession struct {
	User   User   `json:"user"`
	CSRF   string `json:"csrf"`
	cookie *http.Cookie
}

func testApp(t *testing.T) *App {
	t.Helper()
	a, err := New(Env{Database: filepath.Join(t.TempDir(), "test.db"), BaseURL: "http://127.0.0.1:8096", Secret: strings.Repeat("a", 64), AdminPassword: "test-admin-password-123", APIKey: "fake-only-test-key"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a
}
func request(t *testing.T, a *App, s *testSession, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set("Origin", a.env.BaseURL)
	r.Header.Set("Content-Type", "application/json")
	if s != nil {
		r.AddCookie(s.cookie)
		r.Header.Set("X-CSRF-Token", s.CSRF)
	}
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w
}
func loginDevice(t *testing.T, a *App, device string) *testSession {
	t.Helper()
	w := request(t, a, nil, "POST", "/api/session", map[string]string{"device": hash(device), "fingerprint": hash("same-fingerprint")})
	if w.Code != 200 {
		t.Fatalf("bootstrap: %d %s", w.Code, w.Body.String())
	}
	var s testSession
	json.Unmarshal(w.Body.Bytes(), &s)
	s.cookie = w.Result().Cookies()[0]
	return &s
}
func sampleImage(sheet bool) string {
	size := 256
	if sheet {
		size = 1024
	}
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for n := 0; n < 16; n++ {
		if !sheet && n > 0 {
			break
		}
		x, y := 0, 0
		if sheet {
			x = n % 4 * 256
			y = n / 4 * 256
		}
		body := color.NRGBA{168, 186, 147, 255}
		head := color.NRGBA{244, 199, 164, 255}
		offset := n % 4 * 3
		draw.Draw(img, image.Rect(x+85+offset, y+130, x+170+offset, y+220), &image.Uniform{body}, image.Point{}, draw.Src)
		draw.Draw(img, image.Rect(x+80+offset, y+35, x+175+offset, y+135), &image.Uniform{head}, image.Point{}, draw.Src)
		draw.Draw(img, image.Rect(x+79+offset, y+29, x+176+offset, y+58), &image.Uniform{color.NRGBA{93, 69, 54, 255}}, image.Point{}, draw.Src)
		for _, eyeX := range []int{105, 148} {
			draw.Draw(img, image.Rect(x+eyeX+offset, y+81, x+eyeX+6+offset, y+88), &image.Uniform{color.NRGBA{59, 42, 34, 255}}, image.Point{}, draw.Src)
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
}
func draftInput() GenerateInput {
	return GenerateInput{RequestID: token(16), Kind: "draft", Selection: Selection{Category: "male", Clothes: "古代札甲", Color: "玄黑与暗金", Weapon: "长剑"}, Selfie: sampleImage(false)}
}

// draftInputWith 返回指定服装/配色/画风的定稿请求，用于在并发等测试中构造不同配置，
// 避免命中“同款配置重复提交”的二次确认（该行为由 TestDuplicateSelection* 覆盖）。
func draftInputWith(clothes, color, style string) GenerateInput {
	in := draftInput()
	in.Selection.Clothes = clothes
	in.Selection.Color = color
	in.Selection.Style = style
	return in
}
func waitJob(t *testing.T, a *App, s *testSession, id string) Job {
	t.Helper()
	for i := 0; i < 300; i++ {
		w := request(t, a, s, "GET", "/api/jobs/"+id, nil)
		var j Job
		json.Unmarshal(w.Body.Bytes(), &j)
		if j.Status != "running" && j.Status != "queued" {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("job did not finish")
	return Job{}
}
func jobID(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	if w.Code != 202 {
		t.Fatalf("generate: %d %s", w.Code, w.Body.String())
	}
	var out map[string]string
	json.Unmarshal(w.Body.Bytes(), &out)
	return out["id"]
}
func TestDeviceCredentialAndCSRF(t *testing.T) {
	a := testApp(t)
	one := loginDevice(t, a, "one")
	two := loginDevice(t, a, "two")
	if one.User.ID == two.User.ID {
		t.Fatal("fingerprint alone allowed account takeover")
	}
	again := loginDevice(t, a, "one")
	if again.User.ID != one.User.ID || again.User.Credits != 5 {
		t.Fatal("device did not retain its account")
	}
	one.CSRF = "bad"
	w := request(t, a, one, "POST", "/api/redeem", map[string]string{"code": "bad"})
	if w.Code != 403 {
		t.Fatal("CSRF not rejected")
	}
	r := httptest.NewRequest("POST", "/api/session", strings.NewReader("{}"))
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin write accepted")
	}
}

func TestNewUsersAlwaysGetDefaultCredits(t *testing.T) {
	a := testApp(t)
	cfg, _ := a.settings()
	cfg.DefaultCredits = 7
	raw, _ := json.Marshal(cfg)
	a.db.Exec("UPDATE settings SET value=? WHERE key='config'", string(raw))
	one := loginDevice(t, a, "one")
	two := loginDevice(t, a, "two")
	if one.User.Credits != 7 || two.User.Credits != 7 {
		t.Fatal("every new user should get the default gift credits")
	}
}

func TestRestartRefundsInterruptedJobsOnce(t *testing.T) {
	e := Env{Database: filepath.Join(t.TempDir(), "restart.db"), Secret: strings.Repeat("a", 64), AdminPassword: "test-admin-password-123"}
	a, err := New(e)
	if err != nil {
		t.Fatal(err)
	}
	a.db.Exec("INSERT INTO users(id,gift,paid,created) VALUES('user',0,0,0)")
	a.db.Exec("INSERT INTO jobs(id,user_id,request_id,digest,kind,status,gift_cost,paid_cost,refund_failure,created) VALUES('job','user','request','digest','draft','running',1,0,1,0)")
	a.Close()
	for i := 0; i < 2; i++ {
		a, err = New(e)
		if err != nil {
			t.Fatal(err)
		}
		u, _ := a.readUser("user")
		if u.Credits != 1 {
			t.Fatalf("restart refunded %d instead of 1", u.Credits)
		}
		a.Close()
	}
}
func TestQuotaIdempotencyAndReceipt(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "one")
	var calls int32
	a.providerCall = asProviderCall(func(context.Context, Settings, string, []string) (string, error) {
		atomic.AddInt32(&calls, 1)
		return sampleImage(false), nil
	})
	input := draftInput()
	id := jobID(t, request(t, a, s, "POST", "/api/generate", input))
	j := waitJob(t, a, s, id)
	if j.Status != "succeeded" {
		t.Fatal(j)
	}
	if id2 := jobID(t, request(t, a, s, "POST", "/api/generate", input)); id2 != id {
		t.Fatal("request retry created another job")
	}
	u, _ := a.readUser(s.User.ID)
	if u.Credits != 4 || calls != 1 {
		t.Fatalf("credits=%d calls=%d", u.Credits, calls)
	}
	motion := input
	motion.RequestID = token(16)
	motion.Kind = "motion"
	motion.Action = "attack"
	motion.Draft = j.Image
	motion.Receipt = j.Receipt
	if w := request(t, a, s, "POST", "/api/generate", motion); w.Code != 400 {
		t.Fatal("unaccepted draft allowed")
	}
	w := request(t, a, s, "POST", "/api/accept", map[string]string{"receipt": j.Receipt})
	var accepted map[string]string
	json.Unmarshal(w.Body.Bytes(), &accepted)
	motion.Receipt = accepted["receipt"]
	id = jobID(t, request(t, a, s, "POST", "/api/generate", motion))
	waitJob(t, a, s, id)
	motion.RequestID = token(16)
	motion.Selection.Weapon = "战斧"
	if w = request(t, a, s, "POST", "/api/generate", motion); w.Code != 400 {
		t.Fatal("changed appearance bypassed approval")
	}
	u, _ = a.readUser(s.User.ID)
	if u.Credits != 3 {
		t.Fatal("invalid requests charged")
	}
	input.RequestID = token(16)
	input.Selfie = "https://127.0.0.1/private"
	if w = request(t, a, s, "POST", "/api/generate", input); w.Code != 400 {
		t.Fatal("remote selfie accepted")
	}
}
func TestConcurrentQuotaAndFailureRefund(t *testing.T) {
	for _, charge := range []bool{true, false} {
		t.Run(fmt.Sprint(charge), func(t *testing.T) {
			a := testApp(t)
			s := loginDevice(t, a, "one")
			cfg, _ := a.settings()
			cfg.ChargeOnFailure = charge
			raw, _ := json.Marshal(cfg)
			a.db.Exec("UPDATE settings SET value=? WHERE key='config'", string(raw))
			a.db.Exec("UPDATE users SET gift=1 WHERE id=?", s.User.ID)
			started := make(chan struct{})
			finish := make(chan struct{})
			a.providerCall = asProviderCall(func(context.Context, Settings, string, []string) (string, error) {
				close(started)
				<-finish
				return "", errors.New("simulated failure")
			})
			id := jobID(t, request(t, a, s, "POST", "/api/generate", draftInput()))
			<-started
			w := request(t, a, s, "POST", "/api/generate", draftInput())
			if w.Code != 402 && w.Code != 409 {
				t.Fatalf("unexpected concurrent request status %d", w.Code)
			}
			close(finish)
			j := waitJob(t, a, s, id)
			if j.Status != "failed" {
				t.Fatal(j)
			}
			u, _ := a.readUser(s.User.ID)
			want := 0
			if !charge {
				want = 1
			}
			if u.Credits != want {
				t.Fatalf("want %d credits got %d", want, u.Credits)
			}
		})
	}
}
func TestRedeemIsAtomicAndPermanent(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "one")
	code := strings.ToUpper(token(16))
	a.db.Exec("INSERT INTO codes(hash,label,credits,created) VALUES(?,'old',7,0)", a.mac("code:"+code))
	var wg sync.WaitGroup
	var success int32
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := request(t, a, s, "POST", "/api/redeem", map[string]string{"code": code})
			if w.Code == 200 {
				atomic.AddInt32(&success, 1)
			}
		}()
	}
	wg.Wait()
	u, _ := a.readUser(s.User.ID)
	if success != 1 || u.Credits != 12 {
		t.Fatalf("redemptions=%d credits=%d", success, u.Credits)
	}
}

type creditHistoryItem struct {
	User     string
	UserName string `json:"userName"`
	Event    string
	Credits  int
	Note     string
	Created  int64
}

// loginAdmin 用管理密码把设备会话升级为管理会话。
func loginAdmin(t *testing.T, a *App, s *testSession) {
	t.Helper()
	s.CSRF = a.mac("csrf:" + s.cookie.Value)
	w := request(t, a, s, "POST", "/api/admin/login", map[string]string{"password": "test-admin-password-123"})
	if w.Code != 200 {
		t.Fatalf("admin login: %d %s", w.Code, w.Body.String())
	}
	s.cookie = w.Result().Cookies()[0]
	var response map[string]string
	json.Unmarshal(w.Body.Bytes(), &response)
	s.CSRF = response["csrf"]
}

// TestCreditHistory 验证三类次数来源（注册赠送、兑换码、后台增加）实时写入历史，
// 以及后台兑换记录列表与按账号搜索。
func TestCreditHistory(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "one")
	code := strings.ToUpper(token(16))
	codeHash := a.mac("code:" + code)
	a.db.Exec("INSERT INTO codes(hash,label,credits,created) VALUES(?,'历史测试',7,0)", codeHash)
	if w := request(t, a, s, "POST", "/api/redeem", map[string]string{"code": code}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	loginAdmin(t, a, s)
	if w := request(t, a, s, "POST", "/api/admin/users", map[string]any{"id": s.User.ID, "add": 3, "disabled": false}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var page struct {
		Items []creditHistoryItem `json:"items"`
		Total int                 `json:"total"`
	}
	w := request(t, a, s, "GET", "/api/admin/credits?page=1", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &page)
	if page.Total != 3 || len(page.Items) != 3 {
		t.Fatalf("total=%d items=%d %s", page.Total, len(page.Items), w.Body.String())
	}
	byEvent := map[string]creditHistoryItem{}
	for _, item := range page.Items {
		byEvent[item.Event] = item
	}
	if byEvent["register"].Credits != 5 || byEvent["redeem"].Credits != 7 || byEvent["admin"].Credits != 3 {
		t.Fatal("credit amounts mismatch", byEvent)
	}
	if got := byEvent["redeem"].Note; got != "兑换码 "+codeHash[:12] {
		t.Fatal("redeem note mismatch:", got)
	}
	// 按账号编号搜索全部命中，不存在的关键词无结果。
	json.Unmarshal(request(t, a, s, "GET", "/api/admin/credits?q="+s.User.ID, nil).Body.Bytes(), &page)
	if page.Total != 3 {
		t.Fatalf("search by id total=%d", page.Total)
	}
	json.Unmarshal(request(t, a, s, "GET", "/api/admin/credits?q=no-such-user", nil).Body.Bytes(), &page)
	if page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("unknown search total=%d", page.Total)
	}
	// 不增加次数、仅保存停用状态时不产生历史记录。
	if w := request(t, a, s, "POST", "/api/admin/users", map[string]any{"id": s.User.ID, "add": 0, "disabled": false}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	json.Unmarshal(request(t, a, s, "GET", "/api/admin/credits?page=1", nil).Body.Bytes(), &page)
	if page.Total != 3 {
		t.Fatalf("zero add should not record history, total=%d", page.Total)
	}
}

// TestCreditHistoryBackfill 模拟旧库首次升级：兑换码兑换与后台增加从原表精确回溯，
// 注册赠送按“剩余赠送+已消耗赠送”估算，无法解析或不存在的审计记录跳过；重复重启不重复回溯。
func TestCreditHistoryBackfill(t *testing.T) {
	e := Env{Database: filepath.Join(t.TempDir(), "backfill.db"), BaseURL: "http://127.0.0.1:8096", Secret: strings.Repeat("a", 64), AdminPassword: "test-admin-password-123"}
	a, err := New(e)
	if err != nil {
		t.Fatal(err)
	}
	// 构造升级前的旧库数据：消耗过 1 次赠送、剩 2 次赠送的账号、已兑换的兑换码与审计记录。
	a.db.Exec("INSERT INTO users(id,name,gift,paid,created) VALUES('u1','用户一',2,0,100)")
	a.db.Exec("INSERT INTO jobs(id,user_id,request_id,digest,kind,status,gift_cost,paid_cost,created) VALUES('j1','u1','r1','d','draft','succeeded',1,0,50)")
	a.db.Exec("INSERT INTO codes(hash,label,credits,used_by,used_at,created) VALUES(?,'旧批次',7,'u1',200,0)", strings.Repeat("b", 64))
	a.db.Exec("INSERT INTO audit(actor,event,target,created) VALUES('admin','user_update','u1 add=3 disabled=false',300)")
	a.db.Exec("INSERT INTO audit(actor,event,target,created) VALUES('admin','user_update','u1 add=0 disabled=true',301)")
	a.db.Exec("INSERT INTO audit(actor,event,target,created) VALUES('admin','user_update','missing-user add=9 disabled=false',302)")
	// 删除历史表与回溯标记，还原为“未升级”状态后重启触发回溯。
	a.db.Exec("DROP TABLE credit_history")
	a.db.Exec("DELETE FROM settings WHERE key='credit_history_backfill'")
	a.Close()
	a, err = New(e)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := a.db.Query("SELECT user_id,event,credits,note,created FROM credit_history ORDER BY created")
	if err != nil {
		a.Close()
		t.Fatal(err)
	}
	got := []creditHistoryItem{}
	for rows.Next() {
		var item creditHistoryItem
		if err := rows.Scan(&item.User, &item.Event, &item.Credits, &item.Note, &item.Created); err != nil {
			t.Fatal(err)
		}
		got = append(got, item)
	}
	rows.Close()
	want := []creditHistoryItem{
		{User: "u1", Event: "register", Credits: 3, Note: "历史估算", Created: 100},
		{User: "u1", Event: "redeem", Credits: 7, Note: "兑换码 " + strings.Repeat("b", 12), Created: 200},
		{User: "u1", Event: "admin", Credits: 3, Created: 300},
	}
	if len(got) != len(want) {
		a.Close()
		t.Fatalf("backfill rows = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			a.Close()
			t.Fatalf("row %d = %+v want %+v", i, got[i], want[i])
		}
	}
	a.Close()
	// 再次重启不会重复回溯。
	a, err = New(e)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	var count int
	a.db.QueryRow("SELECT COUNT(*) FROM credit_history").Scan(&count)
	if count != len(want) {
		t.Fatalf("repeated backfill count=%d", count)
	}
}

func TestEmailMergePreservesPaidNotGift(t *testing.T) {
	a := testApp(t)
	one := loginDevice(t, a, "one")
	two := loginDevice(t, a, "two")
	a.db.Exec("UPDATE users SET email='person@example.com',gift=2 WHERE id=?", one.User.ID)
	a.db.Exec("UPDATE users SET paid=7 WHERE id=?", two.User.ID)
	otp := "123456"
	a.db.Exec("INSERT INTO email_codes(user_id,email,code,expires) VALUES(?,'person@example.com',?,?)", two.User.ID, a.mac("otp:"+two.User.ID+":person@example.com:"+otp), time.Now().Add(time.Minute).Unix())
	w := request(t, a, two, "POST", "/api/email/verify", map[string]string{"email": "person@example.com", "code": otp})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var merged testSession
	json.Unmarshal(w.Body.Bytes(), &merged)
	if merged.User.ID != one.User.ID || merged.User.Credits != 9 {
		t.Fatal("merge duplicated gifted credits", w.Body.String())
	}
	reconnect := loginDevice(t, a, "two")
	if reconnect.User.ID != one.User.ID {
		t.Fatal("device not associated")
	}
	if w = request(t, a, two, "GET", "/api/me", nil); w.Code != 401 {
		t.Fatal("old session remained valid")
	}
}

func TestEmailSendAndVerifyWithoutExternalDelivery(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "one")
	cfg, _ := a.settings()
	cfg.MailHost, cfg.MailFrom = "smtp.example.com", "studio@example.com"
	raw, _ := json.Marshal(cfg)
	a.db.Exec("UPDATE settings SET value=? WHERE key='config'", string(raw))
	a.setSecret("mail_password", "test-only-mail-password")
	var received string
	a.mailSend = func(_ Settings, to, code string) error {
		if to != "reader@example.com" {
			t.Fatal("recipient was changed")
		}
		received = code
		return nil
	}
	w := request(t, a, s, "POST", "/api/email/send", map[string]string{"email": "reader@example.com"})
	if w.Code != 200 || len(received) != 6 || strings.Contains(w.Body.String(), received) {
		t.Fatal("email code request failed or exposed OTP")
	}
	w = request(t, a, s, "POST", "/api/email/verify", map[string]string{"email": "reader@example.com", "code": received})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	u, _ := a.readUser(s.User.ID)
	if u.Email != "reader@example.com" {
		t.Fatal("email not linked")
	}
}
func TestSecurityAndValidation(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "one")
	for _, path := range []string{"/api/admin/settings", "/api/admin/users", "/api/admin/codes", "/api/admin/jobs"} {
		if w := request(t, a, s, "GET", path, nil); w.Code != 403 {
			t.Fatal("admin permission bypass", path)
		}
	}
	for _, addr := range []string{"127.0.0.1", "10.0.0.2", "169.254.169.254", "100.64.0.1", "::1", "fd00::1", "192.168.0.1"} {
		if publicIP(net.ParseIP(addr)) {
			t.Fatal("private IP accepted", addr)
		}
	}
	if !publicIP(net.ParseIP("1.1.1.1")) {
		t.Fatal("public IP blocked")
	}
	if _, err := imageData("data:image/svg+xml;base64,"+base64.StdEncoding.EncodeToString([]byte("<svg/>")), 1024); err == nil {
		t.Fatal("SVG upload accepted")
	}
	var secret string
	a.db.QueryRow("SELECT value FROM settings WHERE key='api_key'").Scan(&secret)
	if strings.Contains(secret, "fake-only-test-key") {
		t.Fatal("API key stored in plaintext")
	}
	s.CSRF = a.mac("csrf:" + s.cookie.Value)
	w := request(t, a, s, "POST", "/api/admin/login", map[string]string{"password": "test-admin-password-123"})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	s.cookie = w.Result().Cookies()[0]
	var response map[string]string
	json.Unmarshal(w.Body.Bytes(), &response)
	s.CSRF = response["csrf"]
	w = request(t, a, s, "GET", "/api/admin/settings", nil)
	if strings.Contains(w.Body.String(), "fake-only-test-key") {
		t.Fatal("secret exposed to browser")
	}
}

// This harness only exists in the test binary; production builds cannot enable it.
func TestBrowserHarness(t *testing.T) {
	if os.Getenv("GIF_BROWSER_TEST") != "1" {
		t.Skip("manual browser integration harness")
	}
	a := testApp(t)
	a.env.BaseURL = "http://localhost:8097"
	cfg, _ := a.settings()
	cfg.DefaultCredits = 30
	raw, _ := json.Marshal(cfg)
	a.db.Exec("UPDATE settings SET value=? WHERE key='config'", string(raw))
	a.providerCall = asProviderCall(func(ctx context.Context, c Settings, prompt string, images []string) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Second):
		}
		return sampleImage(len(images) == 2), nil
	})
	if dir := os.Getenv("GIF_TEST_OUTPUT"); dir != "" {
		os.MkdirAll(dir, 0755)
		b, _ := imageData(sampleImage(false), 5*1024*1024)
		os.WriteFile(filepath.Join(dir, "selfie-fixture.png"), b, 0600)
	}
	// 提供只有摘要的旧码，复现内置浏览器不支持prompt的补录路径；原码为32个A。
	if _, err := a.db.Exec("INSERT INTO codes(hash,label,credits,created) VALUES(?,?,?,?)", a.mac("code:"+strings.Repeat("A", 32)), "旧码补录测试", 10, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	t.Log("browser harness at http://localhost:8097 ; image provider is simulated")
	server := http.Server{Addr: "127.0.0.1:8097", Handler: a.Handler(), ReadHeaderTimeout: 5 * time.Second}
	if err := server.ListenAndServe(); err != nil {
		t.Fatal(err)
	}
}

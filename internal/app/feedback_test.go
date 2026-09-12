package app

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type sentMail struct {
	To      string
	Subject string
	Body    string
}

// mailReady 打开邮件通道并捕获通知邮件；sendErr 非空时模拟SMTP发送失败。
func mailReady(t *testing.T, a *App, feedbackEmail string, sendErr error) *sentMail {
	t.Helper()
	cfg, err := a.settings()
	if err != nil {
		t.Fatal(err)
	}
	cfg.MailHost, cfg.MailPort, cfg.MailUser, cfg.MailFrom = "smtp.example.com", "465", "studio", "studio@example.com"
	cfg.FeedbackEmail = feedbackEmail
	raw, _ := json.Marshal(cfg)
	if _, err = a.db.Exec("UPDATE settings SET value=? WHERE key='config'", string(raw)); err != nil {
		t.Fatal(err)
	}
	if err = a.setSecret("mail_password", "mail-secret"); err != nil {
		t.Fatal(err)
	}
	sent := &sentMail{}
	a.mailNotify = func(_ Settings, to, subject, body string) error {
		sent.To, sent.Subject, sent.Body = to, subject, body
		return sendErr
	}
	return sent
}

func feedbackAuditCount(t *testing.T, a *App) int {
	t.Helper()
	var n int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM audit WHERE event='feedback_sent'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// 首页反馈提交后按配置发送通知邮件：默认发到发件邮箱，配置了反馈邮箱则优先使用。
func TestFeedbackSendsNotificationMail(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "feedback-user")
	sent := mailReady(t, a, "", nil)
	if w := request(t, a, s, "POST", "/api/feedback", map[string]string{"content": "  希望增加更多动作，谢谢！  "}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if sent.To != "studio@example.com" {
		t.Fatal("unexpected recipient", sent.To)
	}
	if sent.Subject == "" || !strings.Contains(sent.Body, "希望增加更多动作，谢谢！") || strings.Contains(sent.Body, "  希望") {
		t.Fatal("feedback content not trimmed into notification", sent.Body)
	}
	if !strings.Contains(sent.Body, s.User.ID) || !strings.Contains(sent.Body, "邮箱：未绑定邮箱") {
		t.Fatal("notification missing account context", sent.Body)
	}
	var target string
	if err := a.db.QueryRow("SELECT target FROM audit WHERE event='feedback_sent'").Scan(&target); err != nil || target != "希望增加更多动作，谢谢！" {
		t.Fatal("feedback not audited", err, target)
	}
	// 配置专用反馈邮箱后优先发送到该地址。
	second := mailReady(t, a, "owner@example.com", nil)
	if w := request(t, a, s, "POST", "/api/feedback", map[string]string{"content": "第二条反馈"}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if second.To != "owner@example.com" {
		t.Fatal("feedback email setting ignored", second.To)
	}
}

// 空内容、纯空白与超过200字都被拒绝，正好200字可以提交。
func TestFeedbackRejectsInvalidContent(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "feedback-invalid")
	sent := mailReady(t, a, "", nil)
	for _, tc := range []struct{ name, content string }{
		{"空内容", ""},
		{"仅空白", "   \r\n\t "},
		{"超过200字", strings.Repeat("反", feedbackMaxRunes+1)},
	} {
		if w := request(t, a, s, "POST", "/api/feedback", map[string]string{"content": tc.content}); w.Code != 400 {
			t.Fatal("invalid feedback accepted:", tc.name, w.Code, w.Body.String())
		}
	}
	if sent.To != "" || feedbackAuditCount(t, a) != 0 {
		t.Fatal("invalid feedback produced a notification")
	}
	if w := request(t, a, s, "POST", "/api/feedback", map[string]string{"content": strings.Repeat("反", feedbackMaxRunes)}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}

// 未配置邮件服务时明确提示，并如实告知前台反馈入口不可用。
func TestFeedbackRequiresMailConfiguration(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "feedback-unconfigured")
	if w := request(t, a, s, "POST", "/api/feedback", map[string]string{"content": "还没有配置邮箱"}); w.Code != 503 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out struct {
		FeedbackConfigured bool `json:"feedbackConfigured"`
	}
	w := request(t, a, s, "GET", "/api/catalog", nil)
	if json.Unmarshal(w.Body.Bytes(), &out) != nil || out.FeedbackConfigured {
		t.Fatal("catalog reported feedback as configured", w.Body.String())
	}
	mailReady(t, a, "", nil)
	w = request(t, a, s, "GET", "/api/catalog", nil)
	if json.Unmarshal(w.Body.Bytes(), &out) != nil || !out.FeedbackConfigured {
		t.Fatal("catalog did not report feedback as configured", w.Body.String())
	}
}

// 发送失败返回502且不写审计；单账号每小时最多3条，第4条被限流。
func TestFeedbackFailureAndRateLimit(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "feedback-limit")
	mailReady(t, a, "", errors.New("smtp unavailable"))
	if w := request(t, a, s, "POST", "/api/feedback", map[string]string{"content": "发送失败的一条"}); w.Code != 502 {
		t.Fatal(w.Code, w.Body.String())
	}
	if feedbackAuditCount(t, a) != 0 {
		t.Fatal("failed feedback was audited")
	}
	mailReady(t, a, "", nil)
	for i := 0; i < 2; i++ {
		if w := request(t, a, s, "POST", "/api/feedback", map[string]string{"content": "正常反馈" + string(rune('A'+i))}); w.Code != 200 {
			t.Fatal(i, w.Code, w.Body.String())
		}
	}
	if w := request(t, a, s, "POST", "/api/feedback", map[string]string{"content": "第四条"}); w.Code != 429 {
		t.Fatal("rate limit not applied", w.Code, w.Body.String())
	}
	if feedbackAuditCount(t, a) != 2 {
		t.Fatal("unexpected audit count", feedbackAuditCount(t, a))
	}
}

func TestFeedbackBodyIncludesAccountContext(t *testing.T) {
	now := time.Date(2026, 9, 12, 15, 30, 0, 0, time.UTC)
	body := feedbackBody(User{ID: "abc123"}, "内容", now)
	for _, want := range []string{"内容", "abc123", "未命名账号", "未绑定邮箱", "2026-09-12 15:30:00"} {
		if !strings.Contains(body, want) {
			t.Fatal("feedback body missing", want, body)
		}
	}
	named := feedbackBody(User{ID: "abc123", Name: "小明", Email: "me@example.com"}, "内容", now)
	if !strings.Contains(named, "小明") || !strings.Contains(named, "me@example.com") {
		t.Fatal("feedback body missing account", named)
	}
}

// 邮件头必须单行、非ASCII主题按RFC 2047编码、正文换行统一为CRLF。
func TestMailMessageFormat(t *testing.T) {
	msg := mailMessage("studio@example.com", "owner@example.com", "拾光 GIF 用户反馈", "第一行\n第二行")
	if !strings.HasPrefix(msg, "From: studio@example.com\r\nTo: owner@example.com\r\n") {
		t.Fatal("unexpected headers", msg)
	}
	if !strings.Contains(msg, "Subject: =?UTF-8?") {
		t.Fatal("non-ascii subject not encoded", msg)
	}
	if !strings.Contains(msg, "Content-Type: text/plain; charset=UTF-8") || !strings.Contains(msg, "Content-Transfer-Encoding: 8bit") {
		t.Fatal("missing content headers", msg)
	}
	if !strings.Contains(msg, "\r\n\r\n第一行\r\n第二行") {
		t.Fatal("body newlines not normalised", msg)
	}
	if injected := mailMessage("studio@example.com", "owner@example.com", "ok\r\nBcc: evil@example.com", "正文"); strings.Contains(injected, "\r\nBcc:") {
		t.Fatal("header injection not blocked", injected)
	}
	otp := mailMessage("studio@example.com", "user@example.com", "GIF Studio verification code", "你的拾光 GIF 验证码：123456\r\n10 分钟内有效。请勿向他人透露。\r\n")
	if !strings.Contains(otp, "Subject: GIF Studio verification code\r\n") || !strings.Contains(otp, "验证码：123456") {
		t.Fatal("verification mail changed", otp)
	}
}

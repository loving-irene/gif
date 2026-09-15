package app

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// feedbackMaxRunes 是单条反馈允许的最大字数（按字符计，含中文与emoji）。
const feedbackMaxRunes = 200

// feedbackRecipient 返回反馈通知的收件地址：优先使用后台配置的反馈接收邮箱，留空时回落到发件邮箱。
func feedbackRecipient(s Settings) string {
	if to := strings.TrimSpace(s.FeedbackEmail); to != "" {
		return to
	}
	return strings.TrimSpace(s.MailFrom)
}

// feedbackConfigured 判断反馈通知所需的邮箱配置是否完整。
func (a *App) feedbackConfigured(s Settings) bool {
	return a.mailConfigured(s) && feedbackRecipient(s) != ""
}

// feedbackBody 生成通知正文：包含反馈原文与提交账号信息，便于必要时回复用户。
func feedbackBody(u User, content string, now time.Time) string {
	name := strings.TrimSpace(u.Name)
	if name == "" {
		name = "未命名账号"
	}
	email := u.Email
	if email == "" {
		email = "未绑定邮箱"
	}
	return fmt.Sprintf("收到一条新的用户反馈：\r\n\r\n%s\r\n\r\n——\r\n时间：%s\r\n账号：%s（%s）\r\n邮箱：%s\r\n", content, now.Format("2006-01-02 15:04:05"), name, u.ID, email)
}

// feedback 接收首页反馈入口提交的内容，按配置发送邮件通知并写入审计记录。
func (a *App) feedback(w http.ResponseWriter, r *http.Request) {
	s := current(r)
	var in struct {
		Content string `json:"content"`
	}
	if decode(w, r, &in, 8192) != nil {
		fail(w, 400, "反馈内容无效")
		return
	}
	content := strings.TrimSpace(in.Content)
	if content == "" || len([]rune(content)) > feedbackMaxRunes {
		fail(w, 400, fmt.Sprintf("反馈内容需为1—%d字", feedbackMaxRunes))
		return
	}
	cfg, err := a.settings()
	if err != nil {
		fail(w, 500, "配置读取失败")
		return
	}
	if !a.feedbackConfigured(cfg) {
		fail(w, 503, "反馈邮箱尚未配置，请联系管理员")
		return
	}
	// 反馈不是高频操作：单账号每小时最多3条，单IP每小时最多10条，避免被刷邮件。
	if !a.limit("feedback-user:"+s.User.ID, 3, time.Hour) || !a.limit("feedback-ip:"+a.ip(r), 10, time.Hour) {
		fail(w, 429, "反馈提交过于频繁，请稍后再试")
		return
	}
	to := feedbackRecipient(cfg)
	if err = a.mailNotify(cfg, to, "拾光 GIF 用户反馈", feedbackBody(s.User, content, time.Now())); err != nil {
		a.debug(r.Context(), "feedback_mail_failed", map[string]any{"characters": len([]rune(content)), "recipient": to, "error": err.Error()})
		fail(w, 502, "反馈发送失败，请稍后重试")
		return
	}
	// 除邮件外再写一条审计记录：邮件漏看或发送失败重试后，管理员仍能在后台读到原文。
	a.audit(s.User.ID, "feedback_sent", content)
	a.debug(r.Context(), "feedback_sent", map[string]any{"characters": len([]rune(content)), "recipient": to})
	respond(w, 200, map[string]bool{"ok": true})
}

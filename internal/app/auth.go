package app

import (
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"database/sql"
	"fmt"
	"math/big"
	"mime"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

func constantEqual(x, y string) bool { return subtle.ConstantTimeCompare([]byte(x), []byte(y)) == 1 }
func (a *App) bootstrap(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Fingerprint string `json:"fingerprint"`
		Device      string `json:"device"`
	}
	if err := decode(w, r, &in, 2048); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if !hexPattern.MatchString(in.Fingerprint) || !hexPattern.MatchString(in.Device) {
		fail(w, 400, "设备信息格式无效")
		return
	}
	if c, err := r.Cookie("gif_session"); err == nil {
		var uid string
		var admin bool
		if a.db.QueryRow("SELECT user_id,admin FROM sessions WHERE token=? AND expires>?", hash(c.Value), time.Now().Unix()).Scan(&uid, &admin) == nil {
			u, e := a.readUser(uid)
			if e == nil && !u.Disabled {
				respond(w, 200, map[string]any{"user": u, "csrf": a.mac("csrf:" + c.Value), "admin": admin})
				return
			}
		}
	}
	cred := a.mac("device:" + in.Device)
	var uid string
	err := a.db.QueryRow("SELECT user_id FROM devices WHERE credential=?", cred).Scan(&uid)
	if err == sql.ErrNoRows {
		cfg, e := a.settings()
		if e != nil {
			fail(w, 500, "配置读取失败")
			return
		}
		if !a.limit("register-hard:"+a.ip(r), 30, time.Hour) {
			fail(w, 429, "当前网络创建账号过于频繁，请稍后再试")
			return
		}
		gift := cfg.DefaultCredits
		if !a.limit("register-gift:"+a.ip(r), cfg.RegistrationDailyLimit, 24*time.Hour) {
			// 免费额度限流不阻止用户登录已有邮箱账号。
			gift = 0
		}
		// 默认用户名须在开启事务前生成：数据库仅单连接，事务内再查询会死锁。
		name := a.defaultName()
		tx, e := a.db.Begin()
		if e != nil {
			fail(w, 500, "账号创建失败")
			return
		}
		defer tx.Rollback()
		uid = token(16)
		if _, e = tx.Exec("INSERT INTO users(id,name,gift,created) VALUES(?,?,?,?)", uid, name, gift, time.Now().Unix()); e == nil {
			_, e = tx.Exec("INSERT INTO devices(credential,fingerprint,user_id,created) VALUES(?,?,?,?)", cred, a.mac("fp:"+in.Fingerprint), uid, time.Now().Unix())
		}
		if e != nil || tx.Commit() != nil {
			fail(w, 409, "设备正在连接，请重试")
			return
		}
	} else if err != nil {
		fail(w, 500, "账号读取失败")
		return
	}
	u, err := a.readUser(uid)
	if err != nil || u.Disabled {
		fail(w, 403, "账号不可用")
		return
	}
	t, err := a.newSession(w, uid, false)
	if err != nil {
		fail(w, 500, "会话创建失败")
		return
	}
	respond(w, 200, map[string]any{"user": u, "csrf": a.mac("csrf:" + t), "admin": false})
}
func validEmail(raw string) (string, bool) {
	s := strings.ToLower(strings.TrimSpace(raw))
	at := strings.LastIndexByte(s, '@')
	// 严格校验：仅接受真实合法的邮箱地址（标准 local@domain.tld 形式，域名必须带点且顶级域为字母），
	// 拒绝 mail.ParseAddress 会放行的引号本地部分、IP 字面量、无点域名、注释等变体。
	if at < 1 || at > 64 || len(s) > 254 || !emailPattern.MatchString(s) {
		return s, false
	}
	return s, true
}

// validName 校验自定义账户名：白名单机制，仅允许中文/各国字母/数字及“_”“-”“·”，
// 最长 20 个字符；<>\"'&/\\、空格、控制字符、表情等有安全隐患或易引起混淆的字符一律拒绝。
func validName(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" || utf8.RuneCountInString(s) > 20 {
		return "", false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' && r != '·' {
			return "", false
		}
	}
	return s, true
}

// defaultName 为新账号生成默认用户名：前缀“用户”+ 当天年月日 + 4 位随机数（如 用户202609123847），
// 与已有用户名重复时自动重试，尽量避免新账号默认名冲突。
func (a *App) defaultName() string {
	day := time.Now().Format("20060102")
	for i := 0; i < 5; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(10000))
		if err != nil {
			break
		}
		name := fmt.Sprintf("用户%s%04d", day, n.Int64())
		var exists int
		if a.db.QueryRow("SELECT 1 FROM users WHERE name=? LIMIT 1", name).Scan(&exists) == sql.ErrNoRows {
			return name
		}
	}
	n, _ := rand.Int(rand.Reader, big.NewInt(100000000))
	return fmt.Sprintf("用户%s%08d", day, n.Int64())
}

func (a *App) accountNameSave(w http.ResponseWriter, r *http.Request) {
	s := current(r)
	var in struct {
		Name string `json:"name"`
	}
	if decode(w, r, &in, 256) != nil {
		fail(w, 400, "账户名格式无效")
		return
	}
	name, ok := validName(in.Name)
	if !ok {
		fail(w, 400, "账户名最长 20 个字符，仅支持中文、字母、数字及 _ - ·，不含空格或其他特殊符号")
		return
	}
	if !a.limit("account-name:"+s.User.ID, 5, time.Hour) {
		fail(w, 429, "修改过于频繁，请稍后再试")
		return
	}
	if _, err := a.db.Exec("UPDATE users SET name=? WHERE id=?", name, s.User.ID); err != nil {
		fail(w, 500, "账户名保存失败")
		return
	}
	a.audit(s.User.ID, "name_changed", name)
	u, err := a.readUser(s.User.ID)
	if err != nil {
		fail(w, 500, "账号读取失败")
		return
	}
	respond(w, 200, map[string]any{"user": u})
}
func (a *App) emailSend(w http.ResponseWriter, r *http.Request) {
	s := current(r)
	var in struct {
		Email string `json:"email"`
	}
	if decode(w, r, &in, 1024) != nil {
		fail(w, 400, "邮箱格式无效")
		return
	}
	email, ok := validEmail(in.Email)
	if !ok {
		fail(w, 400, "请输入有效邮箱")
		return
	}
	if s.User.Email != "" && s.User.Email != email {
		fail(w, 409, "当前账号已绑定邮箱，请使用新浏览器切换其他账号")
		return
	}
	if !a.limit("mail-user:"+s.User.ID, 1, time.Minute) || !a.limit("mail-ip:"+a.ip(r), 8, time.Hour) || !a.limit("mail-target:"+a.mac(email), 5, time.Hour) {
		fail(w, 429, "验证码发送过于频繁，请稍后重试")
		return
	}
	cfg, err := a.settings()
	if err != nil || cfg.MailHost == "" || cfg.MailFrom == "" || a.secret("mail_password") == "" {
		fail(w, 503, "邮箱服务尚未配置，请联系管理员")
		return
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		fail(w, 500, "验证码生成失败")
		return
	}
	code := fmt.Sprintf("%06d", n.Int64())
	if err = a.mailSend(cfg, email, code); err != nil {
		fail(w, 502, "邮件发送失败，请稍后重试")
		return
	}
	_, err = a.db.Exec("INSERT INTO email_codes(user_id,email,code,expires) VALUES(?,?,?,?) ON CONFLICT(user_id,email) DO UPDATE SET code=excluded.code,expires=excluded.expires,attempts=0", s.User.ID, email, a.mac("otp:"+s.User.ID+":"+email+":"+code), time.Now().Add(10*time.Minute).Unix())
	if err != nil {
		fail(w, 500, "验证码保存失败")
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}
func (a *App) emailVerify(w http.ResponseWriter, r *http.Request) {
	s := current(r)
	var in struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if decode(w, r, &in, 1024) != nil {
		fail(w, 400, "验证信息无效")
		return
	}
	email, ok := validEmail(in.Email)
	if !ok || len(in.Code) != 6 {
		fail(w, 400, "请输入邮箱与六位验证码")
		return
	}
	if !a.limit("verify:"+s.User.ID, 10, 10*time.Minute) {
		fail(w, 429, "验证过于频繁")
		return
	}
	if s.User.Email != "" && s.User.Email != email {
		fail(w, 409, "当前账号已绑定其他邮箱")
		return
	}
	var expected string
	var tries int
	var expiry int64
	err := a.db.QueryRow("UPDATE email_codes SET attempts=attempts+1 WHERE user_id=? AND email=? AND expires>? AND attempts<5 RETURNING code,attempts,expires", s.User.ID, email, time.Now().Unix()).Scan(&expected, &tries, &expiry)
	if err != nil || !hmacEqual(expected, a.mac("otp:"+s.User.ID+":"+email+":"+in.Code)) {
		fail(w, 400, "验证码错误、已过期或尝试次数已达上限")
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		fail(w, 500, "账号关联失败")
		return
	}
	defer tx.Rollback()
	// 消费验证码与合并账号处于同一事务，阻止同一验证码并发重放。
	result, err := tx.Exec("DELETE FROM email_codes WHERE user_id=? AND email=? AND code=? AND expires>?", s.User.ID, email, expected, time.Now().Unix())
	if err != nil {
		fail(w, 500, "验证失败")
		return
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		fail(w, 409, "验证码已使用")
		return
	}
	var target string
	err = tx.QueryRow("SELECT id FROM users WHERE email=?", email).Scan(&target)
	if err == sql.ErrNoRows {
		target = s.User.ID
		_, err = tx.Exec("UPDATE users SET email=? WHERE id=?", email, target)
	} else if err == nil && target != s.User.ID {
		var active, disabled int
		tx.QueryRow("SELECT count(*) FROM jobs WHERE user_id IN (?,?) AND status='running'", target, s.User.ID).Scan(&active)
		tx.QueryRow("SELECT disabled FROM users WHERE id=?", target).Scan(&disabled)
		if active > 0 {
			fail(w, 409, "请等待图片生成结束后再关联邮箱")
			return
		}
		if disabled != 0 {
			fail(w, 403, "目标账号不可用")
			return
		}
		// 已有邮箱账号只接收兑换/管理赠送的付费余额，不重复叠加新设备免费额度。
		_, err = tx.Exec("UPDATE users SET paid=paid+(SELECT paid FROM users WHERE id=?) WHERE id=?", s.User.ID, target)
		if err == nil {
			_, err = tx.Exec("UPDATE devices SET user_id=? WHERE user_id=?", target, s.User.ID)
		}
		if err == nil {
			_, err = tx.Exec("UPDATE jobs SET user_id=? WHERE user_id=?", target, s.User.ID)
		}
		// 定稿云端记录同样跟随账号合并，保证换绑邮箱后“我的定稿”不丢。
		if err == nil {
			_, err = tx.Exec("UPDATE drafts SET user_id=? WHERE user_id=?", target, s.User.ID)
		}
		// 作品集云端记录同样跟随账号合并，保证换绑邮箱后“我的作品集”不丢。
		if err == nil {
			_, err = tx.Exec("UPDATE works SET user_id=? WHERE user_id=?", target, s.User.ID)
		}
		if err == nil {
			_, err = tx.Exec("INSERT INTO aliases(old_id,user_id) VALUES(?,?) ON CONFLICT(old_id) DO UPDATE SET user_id=excluded.user_id", s.User.ID, target)
		}
		if err == nil {
			_, err = tx.Exec("UPDATE aliases SET user_id=? WHERE user_id=?", target, s.User.ID)
		}
		if err == nil {
			_, err = tx.Exec("UPDATE users SET paid=0,gift=0,disabled=1 WHERE id=?", s.User.ID)
		}
	}
	if err == nil {
		_, err = tx.Exec("DELETE FROM sessions WHERE user_id=?", s.User.ID)
	}
	if err != nil || tx.Commit() != nil {
		fail(w, 500, "账号关联失败")
		return
	}
	t, err := a.newSession(w, target, false)
	if err != nil {
		fail(w, 500, "会话更新失败，请刷新")
		return
	}
	u, _ := a.readUser(target)
	a.audit(target, "email_verified", s.User.ID)
	respond(w, 200, map[string]any{"user": u, "csrf": a.mac("csrf:" + t), "mergedFrom": s.User.ID})
}
func (a *App) sendMail(s Settings, to, code string) error {
	return a.sendMailMessage(s, to, "GIF Studio verification code", fmt.Sprintf("你的拾光 GIF 验证码：%s\r\n10 分钟内有效。请勿向他人透露。\r\n", code))
}

// headerLine 把可能来自配置或用户的文本压成单行，避免邮件头注入。
func headerLine(value string) string {
	return strings.NewReplacer("\r", " ", "\n", " ", "\x00", " ").Replace(value)
}

// encodeHeader 对含非ASCII字符的邮件头使用RFC 2047编码，避免客户端显示乱码。
func encodeHeader(value string) string {
	value = headerLine(value)
	for _, r := range value {
		if r > 127 {
			return mime.QEncoding.Encode("UTF-8", value)
		}
	}
	return value
}

// crlf 把正文换行统一为CRLF：SMTP要求CRLF，单个换行会被部分服务器截断。
func crlf(value string) string {
	return strings.NewReplacer("\r\n", "\n", "\r", "\n", "\n", "\r\n").Replace(value)
}

// mailMessage 组装一封纯文本UTF-8邮件（不含DATA结束点）。
func mailMessage(from, to, subject, body string) string {
	return fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n%s", headerLine(from), headerLine(to), encodeHeader(subject), crlf(body))
}

// sendMailMessage 通过已配置的SMTP账号投递一封纯文本邮件，主题与正文由调用方给出。
func (a *App) sendMailMessage(s Settings, to, subject, body string) error {
	address := net.JoinHostPort(s.MailHost, s.MailPort)
	conf := &tls.Config{ServerName: s.MailHost, MinVersion: tls.VersionTLS12}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	var err error
	if s.MailPort == "465" {
		conn, err = tls.DialWithDialer(dialer, "tcp", address, conf)
	} else {
		conn, err = dialer.Dial("tcp", address)
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(20 * time.Second))
	client, err := smtp.NewClient(conn, s.MailHost)
	if err != nil {
		return err
	}
	defer client.Close()
	if s.MailPort != "465" {
		if err = client.StartTLS(conf); err != nil {
			return err
		}
	}
	if err = client.Auth(smtp.PlainAuth("", s.MailUser, a.secret("mail_password"), s.MailHost)); err != nil {
		return err
	}
	from, err := mail.ParseAddress(s.MailFrom)
	if err != nil {
		return err
	}
	recipient, err := mail.ParseAddress(to)
	if err != nil {
		return err
	}
	if err = client.Mail(from.Address); err != nil {
		return err
	}
	if err = client.Rcpt(recipient.Address); err != nil {
		return err
	}
	out, err := client.Data()
	if err != nil {
		return err
	}
	if _, err = out.Write([]byte(mailMessage(from.Address, recipient.Address, subject, body))); err != nil {
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	return client.Quit()
}
func (a *App) redeem(w http.ResponseWriter, r *http.Request) {
	s := current(r)
	var in struct {
		Code string `json:"code"`
	}
	if decode(w, r, &in, 256) != nil {
		fail(w, 400, "兑换码无效")
		return
	}
	if !a.limit("redeem:"+s.User.ID, 10, time.Hour) {
		fail(w, 429, "兑换尝试过于频繁")
		return
	}
	code := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(in.Code), "-", ""))
	if len(code) != 32 {
		fail(w, 400, "兑换码无效")
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		fail(w, 500, "兑换失败")
		return
	}
	defer tx.Rollback()
	var credits int
	err = tx.QueryRow("UPDATE codes SET used_by=?,used_at=? WHERE hash=? AND used_by IS NULL RETURNING credits", s.User.ID, time.Now().Unix(), a.mac("code:"+code)).Scan(&credits)
	if err != nil {
		fail(w, 400, "兑换码无效或已经使用")
		return
	}
	_, err = tx.Exec("UPDATE users SET paid=paid+? WHERE id=?", credits, s.User.ID)
	if err != nil || tx.Commit() != nil {
		fail(w, 500, "兑换失败")
		return
	}
	a.audit(s.User.ID, "redeem", fmt.Sprint(credits))
	u, _ := a.readUser(s.User.ID)
	respond(w, 200, map[string]any{"user": u, "added": credits})
}

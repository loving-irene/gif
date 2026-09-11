package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (a *App) adminLogin(w http.ResponseWriter, r *http.Request) {
	s := current(r)
	var in struct {
		Password string `json:"password"`
	}
	if decode(w, r, &in, 1024) != nil {
		fail(w, 400, "登录信息无效")
		return
	}
	if !a.limit("admin:"+a.ip(r), 5, 15*time.Minute) {
		fail(w, 429, "登录尝试过多，请15分钟后重试")
		return
	}
	if !constantEqual(hash(in.Password), hash(a.env.AdminPassword)) {
		fail(w, 403, "管理密码错误")
		return
	}
	t, err := a.newSession(w, s.User.ID, true)
	if err != nil {
		fail(w, 500, "登录失败")
		return
	}
	a.db.Exec("DELETE FROM sessions WHERE token=?", hash(s.Token))
	a.audit(s.User.ID, "admin_login", "")
	respond(w, 200, map[string]string{"csrf": a.mac("csrf:" + t)})
}
func (a *App) adminSettingsGet(w http.ResponseWriter, r *http.Request) {
	s, err := a.settings()
	if err != nil {
		fail(w, 500, "配置读取失败")
		return
	}
	respond(w, 200, map[string]any{"settings": s, "apiKeySet": a.secret("api_key") != "", "mailPasswordSet": a.secret("mail_password") != ""})
}
func (a *App) adminSettingsSave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Settings     Settings `json:"settings"`
		APIKey       string   `json:"apiKey"`
		MailPassword string   `json:"mailPassword"`
		ClearAPIKey  bool     `json:"clearApiKey"`
	}
	if err := decode(w, r, &in, 128000); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if err := validateSettings(in.Settings); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if len(in.APIKey) > 1024 || len(in.MailPassword) > 1024 || strings.ContainsAny(in.APIKey, "\r\n") {
		fail(w, 400, "密钥格式无效")
		return
	}
	if in.Settings.MailHost != "" {
		if !hostPattern.MatchString(in.Settings.MailHost) || strings.ContainsAny(in.Settings.MailFrom+in.Settings.MailUser, "\r\n") {
			fail(w, 400, "邮件配置无效")
			return
		}
		port, err := strconv.Atoi(in.Settings.MailPort)
		if err != nil || (port != 465 && port != 587) {
			fail(w, 400, "邮件端口仅支持465或587，强制TLS")
			return
		}
		if _, ok := validEmail(in.Settings.MailFrom); !ok {
			fail(w, 400, "发件邮箱无效")
			return
		}
	}
	tx, err := a.db.Begin()
	if err != nil {
		fail(w, 500, "保存失败")
		return
	}
	defer tx.Rollback()
	raw, _ := json.Marshal(in.Settings)
	_, err = tx.Exec("UPDATE settings SET value=? WHERE key='config'", string(raw))
	for k, v := range map[string]string{"api_key": in.APIKey, "mail_password": in.MailPassword} {
		if v != "" && err == nil {
			_, err = tx.Exec("INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", k, a.encrypt(v))
		}
	}
	if in.ClearAPIKey && err == nil {
		_, err = tx.Exec("DELETE FROM settings WHERE key='api_key'")
	}
	if err != nil || tx.Commit() != nil {
		fail(w, 500, "保存失败")
		return
	}
	a.audit(current(r).User.ID, "settings_updated", "")
	respond(w, 200, map[string]bool{"ok": true})
}
func (a *App) adminUsers(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > 254 {
		fail(w, 400, "搜索条件过长")
		return
	}
	rows, err := a.db.Query("SELECT id,COALESCE(email,''),gift+paid,disabled FROM users WHERE id LIKE ? OR email LIKE ? ORDER BY created DESC LIMIT 100", "%"+q+"%", "%"+q+"%")
	if err != nil {
		fail(w, 500, "读取失败")
		return
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		var u User
		if rows.Scan(&u.ID, &u.Email, &u.Credits, &u.Disabled) == nil {
			out = append(out, u)
		}
	}
	respond(w, 200, out)
}
func (a *App) adminUserUpdate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID       string `json:"id"`
		Add      int    `json:"add"`
		Disabled bool   `json:"disabled"`
	}
	if decode(w, r, &in, 1024) != nil || in.Add < 0 || in.Add > 10000 {
		fail(w, 400, "增加次数范围为0—10000")
		return
	}
	res, err := a.db.Exec("UPDATE users SET paid=paid+?,disabled=? WHERE id=?", in.Add, in.Disabled, in.ID)
	if err != nil {
		fail(w, 500, "更新失败")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		fail(w, 404, "账号不存在")
		return
	}
	if in.Disabled {
		a.db.Exec("DELETE FROM sessions WHERE user_id=?", in.ID)
	}
	a.audit(current(r).User.ID, "user_update", fmt.Sprintf("%s add=%d disabled=%t", in.ID, in.Add, in.Disabled))
	respond(w, 200, map[string]bool{"ok": true})
}
func (a *App) adminCodesCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Count   int    `json:"count"`
		Credits int    `json:"credits"`
		Label   string `json:"label"`
	}
	if decode(w, r, &in, 2048) != nil || in.Count < 1 || in.Count > 100 || in.Credits < 1 || in.Credits > 10000 || len(in.Label) > 100 {
		fail(w, 400, "每批1—100个兑换码，每码1—10000次")
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		fail(w, 500, "创建失败")
		return
	}
	defer tx.Rollback()
	codes := []string{}
	items := []map[string]string{}
	for i := 0; i < in.Count; i++ {
		c := strings.ToUpper(token(16))
		_, err = tx.Exec("INSERT INTO codes(hash,label,credits,created,encrypted_code) VALUES(?,?,?,?,?)", a.mac("code:"+c), in.Label, in.Credits, time.Now().Unix(), a.encrypt(c))
		if err != nil {
			fail(w, 500, "创建失败")
			return
		}
		codes = append(codes, c[:8]+"-"+c[8:16]+"-"+c[16:24]+"-"+c[24:])
		items = append(items, map[string]string{"id": a.mac("code:" + c), "code": codes[len(codes)-1]})
	}
	if tx.Commit() != nil {
		fail(w, 500, "创建失败")
		return
	}
	a.audit(current(r).User.ID, "codes_created", fmt.Sprintf("count=%d credits=%d", in.Count, in.Credits))
	respond(w, 200, map[string]any{"codes": codes, "items": items})
}
func (a *App) adminCodes(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query("SELECT hash,label,credits,COALESCE(used_by,''),COALESCE(used_at,0),created,marked,encrypted_code<>'' FROM codes ORDER BY created DESC,rowid DESC LIMIT 200")
	if err != nil {
		fail(w, 500, "读取失败")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, label, used string
		var marked bool
		var copyAvailable bool
		var credits int
		var usedAt, created int64
		if rows.Scan(&id, &label, &credits, &used, &usedAt, &created, &marked, &copyAvailable) == nil {
			status := "unredeemed"
			if used != "" {
				status = "redeemed"
			} else if marked {
				status = "marked"
			}
			out = append(out, map[string]any{"id": id, "label": label, "credits": credits, "usedBy": used, "usedAt": usedAt, "created": created, "status": status, "copyAvailable": copyAvailable})
		}
	}
	respond(w, 200, out)
}

func (a *App) adminCodeMark(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID     string `json:"id"`
		Marked *bool  `json:"marked"`
	}
	if decode(w, r, &in, 1024) != nil || !hexPattern.MatchString(in.ID) || in.Marked == nil {
		fail(w, 400, "兑换码编号或标记状态无效")
		return
	}
	var id string
	err := a.db.QueryRow("UPDATE codes SET marked=? WHERE hash=? AND used_by IS NULL RETURNING hash", *in.Marked, in.ID).Scan(&id)
	if err == sql.ErrNoRows {
		fail(w, 409, "兑换码不存在或已经兑换，请刷新列表")
		return
	}
	if err != nil {
		fail(w, 500, "标记更新失败")
		return
	}
	event := "code_marked"
	status := "marked"
	if !*in.Marked {
		event, status = "code_unmarked", "unredeemed"
	}
	a.audit(current(r).User.ID, event, id)
	respond(w, 200, map[string]string{"status": status})
}
func (a *App) adminAudit(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query("SELECT actor,event,target,created FROM audit ORDER BY id DESC LIMIT 100")
	if err != nil {
		fail(w, 500, "读取失败")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var actor, event, target string
		var created int64
		if rows.Scan(&actor, &event, &target, &created) == nil {
			out = append(out, map[string]any{"actor": actor, "event": event, "target": target, "created": created})
		}
	}
	respond(w, 200, out)
}

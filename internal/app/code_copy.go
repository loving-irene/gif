package app

import (
	"database/sql"
	"net/http"
	"strings"
)

func (a *App) adminCodeCopy(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
	}
	if decode(w, r, &in, 1024) != nil || !hexPattern.MatchString(in.ID) {
		fail(w, 400, "兑换码编号无效")
		return
	}
	var encrypted string
	err := a.db.QueryRow("SELECT encrypted_code FROM codes WHERE hash=?", in.ID).Scan(&encrypted)
	if err == sql.ErrNoRows {
		fail(w, 404, "兑换码不存在")
		return
	}
	if err != nil {
		fail(w, 500, "读取兑换码失败")
		return
	}
	if encrypted == "" {
		fail(w, 409, "旧兑换码只保存了摘要，请先补录原码")
		return
	}
	code, err := a.decrypt(encrypted)
	if err != nil || len(code) != 32 || !hmacEqual(a.mac("code:"+code), in.ID) {
		fail(w, 500, "兑换码解密校验失败，请联系管理员")
		return
	}
	a.audit(current(r).User.ID, "code_copy_requested", in.ID)
	respond(w, 200, map[string]string{"code": code[:8] + "-" + code[8:16] + "-" + code[16:24] + "-" + code[24:]})
}

func (a *App) adminCodeRestore(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID   string `json:"id"`
		Code string `json:"code"`
	}
	if decode(w, r, &in, 1024) != nil || !hexPattern.MatchString(in.ID) {
		fail(w, 400, "兑换码信息无效")
		return
	}
	code := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(in.Code), "-", ""))
	if len(code) != 32 || !hmacEqual(a.mac("code:"+code), in.ID) {
		fail(w, 400, "原码与所选编号不匹配，请核对导出清单")
		return
	}
	result, err := a.db.Exec("UPDATE codes SET encrypted_code=? WHERE hash=?", a.encrypt(code), in.ID)
	if err != nil {
		fail(w, 500, "补录失败")
		return
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		fail(w, 404, "兑换码不存在")
		return
	}
	a.audit(current(r).User.ID, "code_restored", in.ID)
	respond(w, 200, map[string]bool{"ok": true})
}

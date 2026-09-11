package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedeemHelpConfiguredByAdminAndExposedToUsers(t *testing.T) {
	a := testApp(t)
	user := loginDevice(t, a, "help-user")
	admin := codeAdmin(t, a, loginDevice(t, a, "help-admin"))
	cfg, _ := a.settings()
	cfg.RedeemHelp = "🎁 获取兑换码\n请通过页脚联系我。\n<script>alert(1)</script>"
	if w := request(t, a, user, "POST", "/api/admin/settings", map[string]any{"settings": cfg}); w.Code != 403 {
		t.Fatal("non-admin changed help")
	}
	w := request(t, a, admin, "POST", "/api/admin/settings", map[string]any{"settings": cfg})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	stored, _ := a.settings()
	if stored.RedeemHelp != cfg.RedeemHelp {
		t.Fatal("content did not persist")
	}
	w = request(t, a, user, "GET", "/api/catalog", nil)
	var content struct {
		Help string `json:"redeemHelp"`
	}
	if json.Unmarshal(w.Body.Bytes(), &content) != nil || content.Help != cfg.RedeemHelp {
		t.Fatal("user did not receive help")
	}
	cfg.RedeemHelp = strings.Repeat("字", 2001)
	if w = request(t, a, admin, "POST", "/api/admin/settings", map[string]any{"settings": cfg}); w.Code != 400 {
		t.Fatal("oversized help accepted")
	}
	cfg.RedeemHelp = ""
	if w = request(t, a, admin, "POST", "/api/admin/settings", map[string]any{"settings": cfg}); w.Code != 200 {
		t.Fatal("cannot clear help")
	}
	stored, _ = a.settings()
	if stored.RedeemHelp != "" {
		t.Fatal("clear did not persist")
	}
}

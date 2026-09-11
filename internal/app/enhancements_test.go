package app

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCodeCopyEncryptionAndLegacyRestore(t *testing.T) {
	a := testApp(t)
	admin := codeAdmin(t, a, loginDevice(t, a, "admin"))
	user := loginDevice(t, a, "user")
	w := request(t, a, admin, "POST", "/api/admin/codes", map[string]any{"count": 1, "credits": 9, "label": "copy test"})
	var batch struct {
		Items []struct {
			ID   string `json:"id"`
			Code string `json:"code"`
		} `json:"items"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &batch) != nil || len(batch.Items) != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	id, code := batch.Items[0].ID, batch.Items[0].Code
	var encrypted string
	a.db.QueryRow("SELECT encrypted_code FROM codes WHERE hash=?", id).Scan(&encrypted)
	if encrypted == "" || strings.Contains(encrypted, strings.ReplaceAll(code, "-", "")) {
		t.Fatal("code was not encrypted")
	}
	w = request(t, a, admin, "GET", "/api/admin/codes", nil)
	if strings.Contains(w.Body.String(), code) || strings.Contains(w.Body.String(), encrypted) {
		t.Fatal("list leaked secret code")
	}
	for _, endpoint := range []string{"copy", "restore"} {
		body := map[string]any{"id": id}
		if endpoint == "restore" {
			body["code"] = code
		}
		if w = request(t, a, user, "POST", "/api/admin/codes/"+endpoint, body); w.Code != 403 {
			t.Fatal("non-admin could access code", endpoint)
		}
		bad := *admin
		bad.CSRF = "bad"
		if w = request(t, a, &bad, "POST", "/api/admin/codes/"+endpoint, body); w.Code != 403 {
			t.Fatal("CSRF bypass", endpoint)
		}
	}
	w = request(t, a, admin, "POST", "/api/admin/codes/copy", map[string]string{"id": id})
	var copied map[string]string
	json.Unmarshal(w.Body.Bytes(), &copied)
	if w.Code != 200 || copied["code"] != code || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("copy mismatch", w.Body.String())
	}
	a.db.Exec("UPDATE codes SET encrypted_code='',marked=1 WHERE hash=?", id)
	if w = request(t, a, admin, "POST", "/api/admin/codes/copy", map[string]string{"id": id}); w.Code != 409 {
		t.Fatal("legacy code invented plaintext")
	}
	if w = request(t, a, admin, "POST", "/api/admin/codes/restore", map[string]string{"id": id, "code": strings.Repeat("F", 32)}); w.Code != 400 {
		t.Fatal("wrong code accepted")
	}
	if w = request(t, a, admin, "POST", "/api/admin/codes/restore", map[string]string{"id": id, "code": code}); w.Code != 200 {
		t.Fatal("legacy code restore failed", w.Body.String())
	}
	if codeStatus(t, a, admin, id) != "marked" {
		t.Fatal("restore changed marked state")
	}
	if w = request(t, a, user, "POST", "/api/redeem", map[string]string{"code": code}); w.Code != 200 {
		t.Fatal("restored code unusable")
	}
	u, _ := a.readUser(user.User.ID)
	if u.Credits != 14 {
		t.Fatal("restore modified quota")
	}
}

func TestActionPairsCanBeAddedAndDeleted(t *testing.T) {
	a := testApp(t)
	admin := codeAdmin(t, a, loginDevice(t, a, "admin"))
	cfg, _ := a.settings()
	oldID := cfg.Categories[0].Actions[0].ID
	added := Action{ID: "action-new", Name: "新的挥手", Icon: "✦", Prompt: "举起右手挥动两次，然后放回原位。"}
	cfg.Categories[0].Actions = append(cfg.Categories[0].Actions[1:], added)
	w := request(t, a, admin, "POST", "/api/admin/settings", map[string]any{"settings": cfg})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	saved, _ := a.settings()
	found := false
	for _, v := range saved.Categories[0].Actions {
		if v.ID == oldID {
			t.Fatal("deleted action remained")
		}
		if v.ID == added.ID {
			found = true
			if v != added {
				t.Fatal("action name and process mismatched")
			}
		}
	}
	if !found {
		t.Fatal("added action missing")
	}
	for _, bad := range []Action{{ID: "bad", Name: " ", Prompt: "动作过程"}, {ID: "bad", Name: "名称", Prompt: " "}} {
		cfg.Categories[0].Actions = []Action{bad}
		if w = request(t, a, admin, "POST", "/api/admin/settings", map[string]any{"settings": cfg}); w.Code != 400 {
			t.Fatal("empty pair accepted")
		}
	}
}

func TestSEOUsesVisibleContentAndConfiguredOrigin(t *testing.T) {
	a := testApp(t)
	a.env.BaseURL = "https://gif.jcc666.top"
	r := httptest.NewRequest("GET", "/", nil)
	r.Host = "untrusted.example"
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	body := w.Body.String()
	if w.Code != 200 || strings.Contains(body, "ZgotmplZ") || !strings.Contains(body, `rel="canonical" href="https://gif.jcc666.top/"`) || strings.Contains(body, "untrusted.example") {
		t.Fatal("canonical or template failed", w.Code)
	}
	if strings.Contains(body, `href="/who"`) || strings.Contains(body, `href="/admin"`) {
		t.Fatal("homepage exposed admin entrance")
	}
	start := strings.Index(body, `type="application/ld+json"`)
	if start < 0 {
		t.Fatal("missing JSON-LD")
	}
	script := body[start:]
	script = script[strings.Index(script, ">")+1:]
	script = script[:strings.Index(script, "</script>")]
	var graph map[string]any
	if json.Unmarshal([]byte(script), &graph) != nil {
		t.Fatal("invalid JSON-LD", script)
	}
	for _, f := range siteFAQs {
		if strings.Count(body, f.Question) < 2 || !strings.Contains(body, "<h3>"+f.Question+"</h3>") {
			t.Fatal("FAQ markup does not match visible content")
		}
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "'nonce-") {
		t.Fatal("structured data lacks CSP nonce")
	}
	for _, path := range []string{"/robots.txt", "/sitemap.xml", "/llms.txt"} {
		w = request(t, a, nil, "GET", path, nil)
		if w.Code != 200 || strings.Contains(w.Body.String(), "/who") || strings.Contains(w.Body.String(), "/admin") {
			t.Fatal("public machine-readable entry leaked admin", path)
		}
	}
	for _, path := range []string{"/who", "/api/admin/codes"} {
		w = request(t, a, nil, "GET", path, nil)
		if w.Header().Get("X-Robots-Tag") != "noindex, nofollow" {
			t.Fatal("private page can be indexed", path)
		}
	}
}

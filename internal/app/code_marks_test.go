package app

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func codeAdmin(t *testing.T, a *App, s *testSession) *testSession {
	t.Helper()
	w := request(t, a, s, "POST", "/api/admin/login", map[string]string{"password": a.env.AdminPassword})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result map[string]string
	json.Unmarshal(w.Body.Bytes(), &result)
	return &testSession{User: s.User, CSRF: result["csrf"], cookie: w.Result().Cookies()[0]}
}

func codeStatus(t *testing.T, a *App, admin *testSession, id string) string {
	t.Helper()
	w := request(t, a, admin, "GET", "/api/admin/codes", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var codes []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &codes); err != nil {
		t.Fatal(err)
	}
	for _, c := range codes {
		if c.ID == id {
			return c.Status
		}
	}
	t.Fatal("code missing from management list")
	return ""
}

func TestCodeMarkStatusAndRedemption(t *testing.T) {
	a := testApp(t)
	admin := codeAdmin(t, a, loginDevice(t, a, "admin"))
	user := loginDevice(t, a, "user")
	w := request(t, a, admin, "POST", "/api/admin/codes", map[string]any{"count": 1, "credits": 7, "label": "标记测试"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var created struct {
		Codes []string `json:"codes"`
		Items []struct {
			ID   string `json:"id"`
			Code string `json:"code"`
		} `json:"items"`
	}
	json.Unmarshal(w.Body.Bytes(), &created)
	if len(created.Items) != 1 || created.Items[0].Code != created.Codes[0] {
		t.Fatal("export code mapping invalid")
	}
	id := created.Items[0].ID
	if codeStatus(t, a, admin, id) != "unredeemed" {
		t.Fatal("new code should be unredeemed")
	}
	if w = request(t, a, user, "POST", "/api/admin/codes/mark", map[string]any{"id": id, "marked": true}); w.Code != 403 {
		t.Fatal("non-admin could mark a code")
	}
	invalid := *admin
	invalid.CSRF = "invalid"
	if w = request(t, a, &invalid, "POST", "/api/admin/codes/mark", map[string]any{"id": id, "marked": true}); w.Code != 403 {
		t.Fatal("mark endpoint skipped CSRF")
	}
	for _, marked := range []bool{true, false, true} {
		w = request(t, a, admin, "POST", "/api/admin/codes/mark", map[string]any{"id": id, "marked": marked})
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		want := "unredeemed"
		if marked {
			want = "marked"
		}
		if codeStatus(t, a, admin, id) != want {
			t.Fatal("incorrect marked state")
		}
	}
	u, _ := a.readUser(user.User.ID)
	if u.Credits != 5 {
		t.Fatal("mark changed user credits")
	}
	w = request(t, a, user, "POST", "/api/redeem", map[string]string{"code": created.Codes[0]})
	if w.Code != 200 {
		t.Fatal("marked code cannot redeem", w.Body.String())
	}
	if codeStatus(t, a, admin, id) != "redeemed" {
		t.Fatal("redemption must take precedence over mark")
	}
	for _, marked := range []bool{true, false} {
		w = request(t, a, admin, "POST", "/api/admin/codes/mark", map[string]any{"id": id, "marked": marked})
		if w.Code != 409 {
			t.Fatal("redeemed code should reject marking")
		}
	}
	u, _ = a.readUser(user.User.ID)
	if u.Credits != 12 {
		t.Fatal("redemption quota changed")
	}
	var events int
	a.db.QueryRow("SELECT COUNT(*) FROM audit WHERE target=? AND event IN ('code_marked','code_unmarked')", id).Scan(&events)
	if events != 3 {
		t.Fatal("mark audit missing")
	}
}

func TestLegacyCodeMarkMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE codes(hash TEXT PRIMARY KEY,label TEXT NOT NULL,credits INTEGER NOT NULL,used_by TEXT,used_at INTEGER,created INTEGER NOT NULL);
 INSERT INTO codes(hash,label,credits,created) VALUES('legacy','原有兑换码',8,1);`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	e := Env{Database: path, Secret: strings.Repeat("a", 64), AdminPassword: "test-admin-password-123"}
	for i := 0; i < 2; i++ {
		a, err := New(e)
		if err != nil {
			t.Fatal(err)
		}
		var marked, credits int
		err = a.db.QueryRow("SELECT marked,credits FROM codes WHERE hash='legacy'").Scan(&marked, &credits)
		if err != nil || marked != i || credits != 8 {
			a.Close()
			t.Fatalf("legacy migration failed: marked=%d credits=%d err=%v", marked, credits, err)
		}
		a.db.Exec("UPDATE codes SET marked=1 WHERE hash='legacy'")
		a.Close()
	}
}

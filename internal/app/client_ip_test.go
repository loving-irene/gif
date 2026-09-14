package app

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadEnvTrustProxy(t *testing.T) {
	t.Setenv("GIF_SECRET", strings.Repeat("a", 64))
	t.Setenv("GIF_ADMIN_PASSWORD", "test-admin-password-123")
	t.Setenv("GIF_COOKIE_SECURE", "false")
	for _, tc := range []struct {
		name, config string
		want         bool
	}{
		{"default", "", true},
		{"enabled", "GIF_TRUST_PROXY=true\n", true},
		{"disabled", "GIF_TRUST_PROXY=false\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// 隔离宿主进程配置；Setenv 注册的清理会恢复原值。
			t.Setenv("GIF_TRUST_PROXY", "")
			if err := os.Unsetenv("GIF_TRUST_PROXY"); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), ".env")
			if err := os.WriteFile(path, []byte(tc.config), 0600); err != nil {
				t.Fatal(err)
			}
			env, err := LoadEnv(path)
			if err != nil || env.TrustProxy != tc.want {
				t.Fatalf("TrustProxy=%v, want %v; err=%v", env.TrustProxy, tc.want, err)
			}
			t.Setenv("GIF_TRUST_PROXY", "false")
			env, err = LoadEnv(path)
			if err != nil || env.TrustProxy {
				t.Fatalf("environment override ignored: TrustProxy=%v; err=%v", env.TrustProxy, err)
			}
		})
	}
}

func TestRawIPProxyTrustBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, remote, realIP, forwarded, want string
		trust                                 bool
	}{
		{"nginx_ipv4", "127.0.0.1:8096", "203.0.113.10", "198.51.100.99", "203.0.113.10", true},
		{"nginx_ipv6", "[::1]:8096", "2001:db8::10", "", "2001:db8::10", true},
		{"external_spoof", "203.0.113.20:1234", "198.51.100.99", "198.51.100.99", "203.0.113.20", true},
		{"private_proxy_not_trusted", "10.0.0.2:1234", "198.51.100.99", "", "10.0.0.2", true},
		{"disabled", "127.0.0.1:8096", "203.0.113.10", "", "127.0.0.1", false},
		{"missing_header", "127.0.0.1:8096", "", "203.0.113.10", "127.0.0.1", true},
		{"invalid_header", "127.0.0.1:8096", "invalid", "203.0.113.10", "127.0.0.1", true},
		{"multiple_addresses", "127.0.0.1:8096", "203.0.113.10, 198.51.100.99", "", "127.0.0.1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &App{env: Env{TrustProxy: tc.trust}}
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tc.remote
			r.Header.Set("X-Real-IP", tc.realIP)
			r.Header.Set("X-Forwarded-For", tc.forwarded)
			if got := a.rawIP(r); got != tc.want {
				t.Fatalf("rawIP=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestRegistrationRealIPAndRateLimit(t *testing.T) {
	a := testApp(t)
	a.env.TrustProxy = true
	register := func(device, ip string) string {
		t.Helper()
		body := fmt.Sprintf(`{"device":%q,"fingerprint":%q}`, hash(device), hash("fingerprint"))
		r := httptest.NewRequest("POST", "/api/session", strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:45678"
		r.Header.Set("Origin", a.env.BaseURL)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Real-IP", ip)
		r.Header.Set("X-Forwarded-For", "198.51.100.99")
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("registration: %d %s", w.Code, w.Body.String())
		}
		var session testSession
		if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil {
			t.Fatal(err)
		}
		return session.User.ID
	}
	first := register("first", "203.0.113.10")
	// 耗尽第一个客户端的真实注册限流桶，第二个客户端仍能创建账号。
	for i := 0; i < 29; i++ {
		if !a.limit("register-hard:"+a.mac("ip:203.0.113.10"), 30, time.Hour) {
			t.Fatal("unexpected registration limit")
		}
	}
	if a.limit("register-hard:"+a.mac("ip:203.0.113.10"), 30, time.Hour) {
		t.Fatal("first client's registration limit was not applied")
	}
	second := register("second", "2001:db8::20")
	if got := register("first", "203.0.113.30"); got != first {
		t.Fatal("existing device should retain its account")
	}
	want := map[string]string{first: "203.0.113.10", second: "2001:db8::20"}
	for id, ip := range want {
		var stored string
		if err := a.db.QueryRow("SELECT ip FROM users WHERE id=?", id).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if stored != ip {
			t.Fatalf("stored IP=%q, want original registration IP %q", stored, ip)
		}
	}
	w := httptest.NewRecorder()
	a.adminUsers(w, httptest.NewRequest("GET", "/api/admin/users", nil))
	var out struct {
		Items []User `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != 200 {
		t.Fatalf("admin users: status=%d err=%v", w.Code, err)
	}
	if len(out.Items) != len(want) {
		t.Fatalf("admin users count=%d, want %d", len(out.Items), len(want))
	}
	for _, user := range out.Items {
		if user.IP != want[user.ID] {
			t.Fatalf("admin IP=%q, want %q", user.IP, want[user.ID])
		}
	}
}

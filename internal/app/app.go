package app

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	_ "modernc.org/sqlite"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

//go:embed web/*
var web embed.FS
var idPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
var hexPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var hostPattern = regexp.MustCompile(`^[a-zA-Z0-9.-]+$`)
// 严格邮箱格式：本地部分不含首尾点与连续点，域名必须带点且顶级域为字母，拒绝引号、IP、无点域名等不真实地址。
var emailPattern = regexp.MustCompile(`^[a-z0-9_%+-]+(?:\.[a-z0-9_%+-]+)*@(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

const networkErrorMessage = "网络异常，请稍后重试~"

type App struct {
	db             *sql.DB
	env            Env
	files          string
	jobsMu         sync.Mutex
	jobs           map[string]*Job
	slots          chan struct{}
	uploads        chan struct{}
	dispatch       chan struct{}
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
	mailSend       func(Settings, string, string) error
	provider       func(context.Context, Settings, string, []string) (string, error)
	providerClient func() *http.Client
}
type User struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Credits  int    `json:"credits"`
	Disabled bool   `json:"disabled"`
	// Created 只在管理后台账号列表填充；会话等场景保持零值并由 omitempty 隐藏。
	Created int64 `json:"created,omitempty"`
}
type session struct {
	User  User
	Token string
	Admin bool
}
type sessionKey struct{}

func New(e Env) (*App, error) {
	db, err := sql.Open("sqlite", e.Database)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	files := filepath.Join(filepath.Dir(e.Database), "files")
	if err = os.MkdirAll(files, 0700); err != nil {
		db.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &App{db: db, env: e, files: files, jobs: map[string]*Job{}, slots: make(chan struct{}, 2), uploads: make(chan struct{}, 2), dispatch: make(chan struct{}, 1), ctx: ctx, cancel: cancel}
	if _, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;
 CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY,value TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS users(id TEXT PRIMARY KEY,email TEXT UNIQUE,name TEXT,gift INTEGER NOT NULL CHECK(gift>=0),paid INTEGER NOT NULL DEFAULT 0 CHECK(paid>=0),disabled INTEGER NOT NULL DEFAULT 0,created INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS devices(credential TEXT PRIMARY KEY,fingerprint TEXT NOT NULL,user_id TEXT NOT NULL REFERENCES users(id),created INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS aliases(old_id TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id));
 CREATE TABLE IF NOT EXISTS sessions(token TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id),admin INTEGER NOT NULL DEFAULT 0,expires INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS rate_limits(bucket TEXT PRIMARY KEY,count INTEGER NOT NULL,expires INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS email_codes(user_id TEXT NOT NULL,email TEXT NOT NULL,code TEXT NOT NULL,attempts INTEGER NOT NULL DEFAULT 0,expires INTEGER NOT NULL,PRIMARY KEY(user_id,email));
 CREATE TABLE IF NOT EXISTS codes(hash TEXT PRIMARY KEY,label TEXT NOT NULL,credits INTEGER NOT NULL CHECK(credits>0),used_by TEXT REFERENCES users(id),used_at INTEGER,created INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS jobs(id TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id),request_id TEXT NOT NULL,digest TEXT NOT NULL,kind TEXT NOT NULL,status TEXT NOT NULL,gift_cost INTEGER NOT NULL,paid_cost INTEGER NOT NULL,refund_failure INTEGER NOT NULL DEFAULT 0,created INTEGER NOT NULL,started INTEGER NOT NULL DEFAULT 0,action TEXT NOT NULL DEFAULT '',receipt TEXT NOT NULL DEFAULT '',UNIQUE(user_id,request_id));
 CREATE TABLE IF NOT EXISTS audit(id INTEGER PRIMARY KEY AUTOINCREMENT,actor TEXT NOT NULL,event TEXT NOT NULL,target TEXT NOT NULL,created INTEGER NOT NULL);
 BEGIN;
 UPDATE users SET gift=gift+COALESCE((SELECT SUM(gift_cost) FROM jobs WHERE jobs.user_id=users.id AND status IN ('queued','running') AND refund_failure=1),0),paid=paid+COALESCE((SELECT SUM(paid_cost) FROM jobs WHERE jobs.user_id=users.id AND status IN ('queued','running') AND refund_failure=1),0);
 UPDATE jobs SET gift_cost=0,paid_cost=0 WHERE status IN ('queued','running') AND refund_failure=1;
 UPDATE jobs SET status='interrupted' WHERE status IN ('queued','running');
 COMMIT;`); err != nil {
		db.Close()
		cancel()
		return nil, err
	}
	if err = a.migrateJobs(); err != nil {
		a.Close()
		return nil, err
	}
	if err = a.migrateUsers(); err != nil {
		a.Close()
		return nil, err
	}
	if err = a.migrateCodeMarks(); err != nil {
		a.Close()
		return nil, err
	}
	if err = a.migrateTimings(); err != nil {
		a.Close()
		return nil, err
	}
	raw, _ := json.Marshal(defaults(e))
	if _, err = db.Exec("INSERT OR IGNORE INTO settings(key,value) VALUES('config',?)", string(raw)); err != nil {
		a.Close()
		return nil, err
	}
	for name, value := range map[string]string{"api_key": e.APIKey, "mail_password": e.MailPassword} {
		if value != "" && a.secret(name) == "" {
			if err = a.setSecret(name, value); err != nil {
				a.Close()
				return nil, err
			}
		}
	}
	a.mailSend = a.sendMail
	a.provider = a.callProvider
	a.providerClient = safeClient
	a.wg.Add(1)
	go a.dispatcher()
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.cleanup()
			}
		}
	}()
	return a, nil
}

// migrateUsers 为旧数据库补齐自定义账户名列。
func (a *App) migrateUsers() error {
	rows, err := a.db.Query("PRAGMA table_info(users)")
	if err != nil {
		return err
	}
	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dflt sql.NullString
		var pk int
		if rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk) == nil {
			cols[name] = true
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	if !cols["name"] {
		if _, err = a.db.Exec("ALTER TABLE users ADD COLUMN name TEXT"); err != nil {
			return err
		}
	}
	return nil
}

// migrateJobs 为旧数据库补齐任务表的新列，并把“仅running唯一”索引升级为“排队+进行中唯一”。
func (a *App) migrateJobs() error {
	rows, err := a.db.Query("PRAGMA table_info(jobs)")
	if err != nil {
		return err
	}
	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dflt sql.NullString
		var pk int
		if rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk) == nil {
			cols[name] = true
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, column := range []struct{ name, ddl string }{
		{"started", "ALTER TABLE jobs ADD COLUMN started INTEGER NOT NULL DEFAULT 0"},
		{"action", "ALTER TABLE jobs ADD COLUMN action TEXT NOT NULL DEFAULT ''"},
		{"receipt", "ALTER TABLE jobs ADD COLUMN receipt TEXT NOT NULL DEFAULT ''"},
	} {
		if !cols[column.name] {
			if _, err = a.db.Exec(column.ddl); err != nil {
				return err
			}
		}
	}
	// 单用户并发任务数改为后台可配置（settings.userConcurrency，默认5），
	// 由创建任务时按数量校验，不再使用唯一索引限制单任务。
	if _, err = a.db.Exec("DROP INDEX IF EXISTS one_active_job"); err != nil {
		return err
	}
	return nil
}
func (a *App) Close() { a.cancel(); a.wg.Wait(); a.db.Close() }
func (a *App) cleanup() {
	now := time.Now().Unix()
	a.db.Exec("DELETE FROM sessions WHERE expires<?", now)
	a.db.Exec("DELETE FROM email_codes WHERE expires<?", now)
	a.db.Exec("DELETE FROM rate_limits WHERE expires<?", now)
	a.cleanupFiles()
	a.jobsMu.Lock()
	defer a.jobsMu.Unlock()
	for id, j := range a.jobs {
		if j.Status != "running" && j.Status != "queued" && j.Expires < now {
			delete(a.jobs, id)
		}
	}
}
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("POST /api/session", a.bootstrap)
	mux.HandleFunc("GET /api/me", a.auth(a.me, false))
	mux.HandleFunc("GET /api/catalog", a.auth(a.catalog, false))
	mux.HandleFunc("POST /api/logout", a.auth(a.logout, false))
	mux.HandleFunc("POST /api/account/name", a.auth(a.accountNameSave, false))
	mux.HandleFunc("POST /api/email/send", a.auth(a.emailSend, false))
	mux.HandleFunc("POST /api/email/verify", a.auth(a.emailVerify, false))
	mux.HandleFunc("POST /api/redeem", a.auth(a.redeem, false))
	mux.HandleFunc("POST /api/generate", a.auth(a.generate, false))
	mux.HandleFunc("GET /api/jobs/{id}", a.auth(a.getJob, false))
	mux.HandleFunc("GET /api/calls", a.auth(a.calls, false))
	mux.HandleFunc("POST /api/accept", a.auth(a.accept, false))
	mux.HandleFunc("POST /api/admin/login", a.auth(a.adminLogin, false))
	mux.HandleFunc("GET /api/admin/settings", a.auth(a.adminSettingsGet, true))
	mux.HandleFunc("POST /api/admin/settings", a.auth(a.adminSettingsSave, true))
	mux.HandleFunc("GET /api/admin/users", a.auth(a.adminUsers, true))
	mux.HandleFunc("POST /api/admin/users", a.auth(a.adminUserUpdate, true))
	mux.HandleFunc("GET /api/admin/codes", a.auth(a.adminCodes, true))
	mux.HandleFunc("POST /api/admin/codes", a.auth(a.adminCodesCreate, true))
	mux.HandleFunc("POST /api/admin/codes/mark", a.auth(a.adminCodeMark, true))
	mux.HandleFunc("POST /api/admin/codes/copy", a.auth(a.adminCodeCopy, true))
	mux.HandleFunc("POST /api/admin/codes/restore", a.auth(a.adminCodeRestore, true))
	mux.HandleFunc("GET /api/admin/audit", a.auth(a.adminAudit, true))
	mux.HandleFunc("GET /api/admin/jobs", a.auth(a.adminJobs, true))
	mux.HandleFunc("GET /robots.txt", a.robots)
	mux.HandleFunc("GET /sitemap.xml", a.sitemap)
	mux.HandleFunc("GET /llms.txt", a.llms)
	assets, _ := fs.Sub(web, "web")
	files := http.FileServer(http.FS(assets))
	mux.Handle("GET /assets/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// HTML页面只通过页面路由访问，不作为静态资源提供其他入口。
		if strings.HasSuffix(r.URL.Path, ".html") || strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		http.StripPrefix("/assets/", files).ServeHTTP(w, r)
	}))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			a.home(w, r)
			return
		}
		if r.URL.Path != "/who" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		b, _ := web.ReadFile("web/admin.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.env.Debug && (r.URL.Path == "/api/generate" || strings.HasPrefix(r.URL.Path, "/api/jobs/")) {
			started := time.Now()
			status := &debugStatusWriter{ResponseWriter: w, status: 200}
			w = status
			route := "/api/generate"
			if strings.HasPrefix(r.URL.Path, "/api/jobs/") {
				route = "/api/jobs/{id}"
			}
			defer func() {
				a.debug(r.Context(), "local_http", map[string]any{"route": route, "method": r.Method, "http_status": status.status, "elapsed_ms": time.Since(started).Milliseconds()})
			}()
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; connect-src 'self'; worker-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("Cache-Control", "no-store")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		if a.env.Secure {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			if r.Header.Get("Origin") != a.env.BaseURL || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				fail(w, 403, "请求来源无效，请刷新页面")
				return
			}
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				fail(w, 415, "仅支持 JSON 请求")
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && !a.limit("api:"+a.ip(r), 240, time.Minute) {
			fail(w, 429, "请求过于频繁，请稍后再试")
			return
		}
		defer func() {
			if recover() != nil {
				log.Print("request panic (details withheld)")
				fail(w, 500, "服务暂时不可用")
			}
		}()
		mux.ServeHTTP(w, r)
	})
}
func (a *App) ip(r *http.Request) string {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if a.env.TrustProxy && net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback() {
		if ip := net.ParseIP(r.Header.Get("X-Real-IP")); ip != nil {
			host = ip.String()
		}
	}
	return a.mac("ip:" + host)
}
func (a *App) limit(key string, n int, window time.Duration) bool {
	now := time.Now().Unix()
	var count int
	err := a.db.QueryRow(`INSERT INTO rate_limits(bucket,count,expires) VALUES(?,1,?) ON CONFLICT(bucket) DO UPDATE SET count=CASE WHEN expires<=? THEN 1 ELSE count+1 END,expires=CASE WHEN expires<=? THEN excluded.expires ELSE expires END RETURNING count`, key, now+int64(window.Seconds()), now, now).Scan(&count)
	return err == nil && count <= n
}
func decode(w http.ResponseWriter, r *http.Request, v any, max int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, max)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return errors.New("请求内容无效或文件超过大小限制")
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("请求内容无效")
	}
	return nil
}
func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, message string) {
	if status >= 500 {
		message = networkErrorMessage
	}
	respond(w, status, map[string]string{"error": message})
}
func current(r *http.Request) session { return r.Context().Value(sessionKey{}).(session) }
func (a *App) readUser(id string) (User, error) {
	u := User{}
	err := a.db.QueryRow("SELECT id,COALESCE(name,''),COALESCE(email,''),gift+paid,disabled FROM users WHERE id=?", id).Scan(&u.ID, &u.Name, &u.Email, &u.Credits, &u.Disabled)
	return u, err
}
func (a *App) auth(next http.HandlerFunc, admin bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("gif_session")
		if err != nil {
			fail(w, 401, "请刷新页面重新连接账号")
			return
		}
		s := session{Token: c.Value}
		err = a.db.QueryRow(`SELECT u.id,COALESCE(u.name,''),COALESCE(u.email,''),u.gift+u.paid,u.disabled,s.admin FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token=? AND s.expires>?`, hash(c.Value), time.Now().Unix()).Scan(&s.User.ID, &s.User.Name, &s.User.Email, &s.User.Credits, &s.User.Disabled, &s.Admin)
		if err != nil || s.User.Disabled {
			fail(w, 401, "账号不可用，请重新登录")
			return
		}
		if admin && !s.Admin {
			fail(w, 403, "请先登录管理后台")
			return
		}
		if r.Method != "GET" && !hmacEqual(r.Header.Get("X-CSRF-Token"), a.mac("csrf:"+c.Value)) {
			fail(w, 403, "会话校验失败，请刷新页面")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, s)))
	}
}
func hmacEqual(x, y string) bool { return len(x) == len(y) && constantEqual(x, y) }
func (a *App) newSession(w http.ResponseWriter, uid string, admin bool) (string, error) {
	t := token(32)
	expires := time.Now().Add(30 * 24 * time.Hour)
	if admin {
		expires = time.Now().Add(2 * time.Hour)
	}
	_, err := a.db.Exec("INSERT INTO sessions(token,user_id,admin,expires) VALUES(?,?,?,?)", hash(t), uid, admin, expires.Unix())
	if err == nil {
		http.SetCookie(w, &http.Cookie{Name: "gif_session", Value: t, Path: "/", Secure: a.env.Secure, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: int(time.Until(expires).Seconds())})
	}
	return t, err
}
func (a *App) me(w http.ResponseWriter, r *http.Request) {
	s := current(r)
	respond(w, 200, map[string]any{"user": s.User, "csrf": a.mac("csrf:" + s.Token), "admin": s.Admin})
}
func (a *App) catalog(w http.ResponseWriter, r *http.Request) {
	s, err := a.settings()
	if err != nil {
		fail(w, 500, "配置读取失败")
		return
	}
	for i := range s.Categories {
		s.Categories[i].Prompt = ""
		for j := range s.Categories[i].Actions {
			s.Categories[i].Actions[j].Prompt = ""
		}
	}
	respond(w, 200, map[string]any{"categories": s.Categories, "chargeOnFailure": s.ChargeOnFailure, "configured": a.secret("api_key") != "", "emailConfigured": s.MailHost != "" && s.MailFrom != "" && a.secret("mail_password") != "", "estimates": a.estimates(s), "redeemHelp": s.RedeemHelp, "userConcurrency": s.UserConcurrency})
}
func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	s := current(r)
	a.db.Exec("DELETE FROM sessions WHERE token=?", hash(s.Token))
	http.SetCookie(w, &http.Cookie{Name: "gif_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.env.Secure, SameSite: http.SameSiteStrictMode})
	respond(w, 200, map[string]bool{"ok": true})
}
func (a *App) audit(actor, event, target string) {
	a.db.Exec("INSERT INTO audit(actor,event,target,created) VALUES(?,?,?,?)", actor, event, target, time.Now().Unix())
}

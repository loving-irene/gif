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

// generationSlots 是服务器同时执行的生成任务数上限，多出的任务按创建顺序排队；
// /api/catalog 会把它下发给前台，用于估算并行动作的整批用时。
const generationSlots = 2

type App struct {
	db         *sql.DB
	env        Env
	files      string
	jobsMu     sync.Mutex
	submitMu   sync.Mutex
	jobs       map[string]*Job
	slots      chan struct{}
	uploads    chan struct{}
	dispatch   chan struct{}
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	workersMu  sync.Mutex
	workers    sync.WaitGroup
	mailSend   func(Settings, string, string) error
	mailNotify func(Settings, string, string, string) error
	// waitBudget 是任务从创建起可用来完成生成的总时长（提交等待 + 上游结果认领），
	// 即 jobTimeLimit（20 分钟），超过直接按失败收口；测试可缩短它来跳过等待。
	waitBudget time.Duration
	// pollInterval 是轮询上游结果的间隔，由 New 设为 providerPollInterval；
	// 测试可缩短它，让“多次轮询后拿到结果”在秒级内确定地发生。
	pollInterval time.Duration
	// providerCall 提交一次新的生成请求；providerContinue 只按已记录的上游任务号继续认领结果。
	// 超时续查走后者，不会产生第二次上游请求。
	providerCall     func(context.Context, Settings, string, []string, func(string)) (string, error)
	providerContinue func(context.Context, Settings, string) (string, error)
	providerClient   func() *http.Client
}
type User struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Credits  int    `json:"credits"`
	Disabled bool   `json:"disabled"`
	// Created 与 IP 只在管理后台账号列表填充；会话等场景保持零值并由 omitempty 隐藏。
	Created int64  `json:"created,omitempty"`
	IP      string `json:"ip,omitempty"`
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
	a := &App{db: db, env: e, files: files, jobs: map[string]*Job{}, slots: make(chan struct{}, generationSlots), uploads: make(chan struct{}, 2), dispatch: make(chan struct{}, 1), ctx: ctx, cancel: cancel}
	if _, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;
 CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY,value TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS users(id TEXT PRIMARY KEY,email TEXT UNIQUE,name TEXT,gift INTEGER NOT NULL CHECK(gift>=0),paid INTEGER NOT NULL DEFAULT 0 CHECK(paid>=0),disabled INTEGER NOT NULL DEFAULT 0,created INTEGER NOT NULL,ip TEXT NOT NULL DEFAULT '');
 CREATE TABLE IF NOT EXISTS devices(credential TEXT PRIMARY KEY,fingerprint TEXT NOT NULL,user_id TEXT NOT NULL REFERENCES users(id),created INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS aliases(old_id TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id));
 CREATE TABLE IF NOT EXISTS sessions(token TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id),admin INTEGER NOT NULL DEFAULT 0,expires INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS rate_limits(bucket TEXT PRIMARY KEY,count INTEGER NOT NULL,expires INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS email_codes(user_id TEXT NOT NULL,email TEXT NOT NULL,code TEXT NOT NULL,attempts INTEGER NOT NULL DEFAULT 0,expires INTEGER NOT NULL,PRIMARY KEY(user_id,email));
 CREATE TABLE IF NOT EXISTS codes(hash TEXT PRIMARY KEY,label TEXT NOT NULL,credits INTEGER NOT NULL CHECK(credits>0),used_by TEXT REFERENCES users(id),used_at INTEGER,created INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS jobs(id TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id),request_id TEXT NOT NULL,digest TEXT NOT NULL,kind TEXT NOT NULL,status TEXT NOT NULL,gift_cost INTEGER NOT NULL,paid_cost INTEGER NOT NULL,refund_failure INTEGER NOT NULL DEFAULT 0,created INTEGER NOT NULL,started INTEGER NOT NULL DEFAULT 0,action TEXT NOT NULL DEFAULT '',receipt TEXT NOT NULL DEFAULT '',dup_digest TEXT NOT NULL DEFAULT '',upstream_task_id TEXT NOT NULL DEFAULT '',upstream_wait_ms INTEGER NOT NULL DEFAULT 0,timing_recorded INTEGER NOT NULL DEFAULT 0,error_message TEXT NOT NULL DEFAULT '',UNIQUE(user_id,request_id));
 CREATE TABLE IF NOT EXISTS drafts(receipt TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id),created INTEGER NOT NULL,selection TEXT NOT NULL DEFAULT '{}',image BLOB NOT NULL);
 CREATE INDEX IF NOT EXISTS drafts_user ON drafts(user_id,created DESC);
 CREATE TABLE IF NOT EXISTS works(id TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id),created INTEGER NOT NULL,name TEXT NOT NULL DEFAULT '',category TEXT NOT NULL DEFAULT '',action TEXT NOT NULL DEFAULT '',gif BLOB,sheet BLOB);
 CREATE INDEX IF NOT EXISTS works_user ON works(user_id,created DESC);
 -- 社区分享池：作品被分享后 GIF 本体复制到这里，与作品集（3天保留）完全独立、永不清理。
 -- (sharer,work_id) 唯一：同一账号同一张作品只保留一条，重复分享只刷新时间与内容、重新分享不产生重复条目。
 CREATE TABLE IF NOT EXISTS community_shares(id TEXT PRIMARY KEY,sharer TEXT NOT NULL REFERENCES users(id),work_id TEXT NOT NULL,name TEXT NOT NULL DEFAULT '',image BLOB NOT NULL,action TEXT NOT NULL DEFAULT '',category TEXT NOT NULL DEFAULT '',created INTEGER NOT NULL,updated INTEGER NOT NULL,UNIQUE(sharer,work_id));
 CREATE INDEX IF NOT EXISTS community_shares_created ON community_shares(created DESC);
 CREATE TABLE IF NOT EXISTS audit(id INTEGER PRIMARY KEY AUTOINCREMENT,actor TEXT NOT NULL,event TEXT NOT NULL,target TEXT NOT NULL,created INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS credit_history(id INTEGER PRIMARY KEY AUTOINCREMENT,user_id TEXT NOT NULL REFERENCES users(id),event TEXT NOT NULL CHECK(event IN ('register','redeem','admin')),credits INTEGER NOT NULL CHECK(credits>0),note TEXT NOT NULL DEFAULT '',created INTEGER NOT NULL);
 CREATE INDEX IF NOT EXISTS credit_history_created ON credit_history(created,id);
 CREATE TABLE IF NOT EXISTS usage_stats(id INTEGER PRIMARY KEY AUTOINCREMENT,job_id TEXT NOT NULL UNIQUE,user_id TEXT NOT NULL,kind TEXT NOT NULL,category TEXT NOT NULL DEFAULT '',action TEXT NOT NULL DEFAULT '',created INTEGER NOT NULL);
 CREATE INDEX IF NOT EXISTS usage_stats_created ON usage_stats(created);`); err != nil {
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
	if err = a.migrateCreditHistory(); err != nil {
		a.Close()
		return nil, err
	}
	if err = a.migrateTimings(); err != nil {
		a.Close()
		return nil, err
	}
	if err = a.recoverJobs(); err != nil {
		a.Close()
		return nil, err
	}
	raw, _ := json.Marshal(defaults(e))
	if _, err = db.Exec("INSERT OR IGNORE INTO settings(key,value) VALUES('config',?)", string(raw)); err != nil {
		a.Close()
		return nil, err
	}
	for name, value := range map[string]string{"api_key": e.APIKey, aliyunMailPasswordKey: e.AliyunMailPassword} {
		if value != "" && a.secret(name) == "" {
			if err = a.setSecret(name, value); err != nil {
				a.Close()
				return nil, err
			}
		}
	}
	a.mailSend = a.sendMail
	a.mailNotify = a.sendMailMessage
	a.waitBudget = jobTimeLimit
	a.pollInterval = providerPollInterval
	a.providerCall = a.callProvider
	a.providerContinue = a.continueProvider
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
	// 每日统计邮件：北京时间每天 00:23 起汇总前一天的新增用户、消耗次数（定稿图/GIF 动图、
	// 男生/女生/小朋友分类）与兑换码兑换等，发送到通知邮箱；每分钟检查，每天最多成功发送一封。
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
				a.dailyStatsTick(time.Now().In(beijingZone))
			}
		}
	}()
	// 等待上游结果的任务由调度器持续认领；这里按周期兜底触发，避免漏掉通知。
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.signalDispatch()
			}
		}
	}()
	return a, nil
}

// migrateUsers 为旧数据库补齐自定义账户名与注册 IP 列。
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
	// ip 记录注册时的来源 IP，仅用于管理后台展示；历史账号没有记录，后台显示为“-”。
	if !cols["ip"] {
		if _, err = a.db.Exec("ALTER TABLE users ADD COLUMN ip TEXT NOT NULL DEFAULT ''"); err != nil {
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
		{"dup_digest", "ALTER TABLE jobs ADD COLUMN dup_digest TEXT NOT NULL DEFAULT ''"},
		{"upstream_task_id", "ALTER TABLE jobs ADD COLUMN upstream_task_id TEXT NOT NULL DEFAULT ''"},
		{"upstream_wait_ms", "ALTER TABLE jobs ADD COLUMN upstream_wait_ms INTEGER NOT NULL DEFAULT 0"},
		// timing_recorded 记录该任务是否已经计入耗时统计：统计只保留聚合值时，
		// 用它保证同一任务不会被重复计入（替代原来按 job_id 去重的一次性记录表）。
		{"timing_recorded", "ALTER TABLE jobs ADD COLUMN timing_recorded INTEGER NOT NULL DEFAULT 0"},
		// error_message 记录任务失败/中断原因，仅供管理后台任务历史展示，用户侧仍统一提示网络异常。
		{"error_message", "ALTER TABLE jobs ADD COLUMN error_message TEXT NOT NULL DEFAULT ''"},
	} {
		if !cols[column.name] {
			if _, err = a.db.Exec(column.ddl); err != nil {
				return err
			}
		}
	}
	// 补写历史遗留中断任务的原因：本列新增之前发生的服务重启中断没有记录可循。
	if _, err = a.db.Exec("UPDATE jobs SET error_message='服务重启时任务被中断' WHERE status='interrupted' AND error_message=''"); err != nil {
		return err
	}
	// 为迁移前就存在的排队/进行中任务补写配置摘要，使重复提交检测覆盖进行中的旧任务。
	// 排队的输入存在 input.json（生成中任务的输入只在内存里，无法补写，保持空值兜底）。
	if err = a.backfillDuplicateDigests(); err != nil {
		return err
	}
	// 单用户并发任务数改为后台可配置（settings.userConcurrency，默认5），
	// 由创建任务时按数量校验，不再使用唯一索引限制单任务。
	if _, err = a.db.Exec("DROP INDEX IF EXISTS one_active_job"); err != nil {
		return err
	}
	return nil
}

// recoverJobs 恢复上次进程退出时遗留的活跃任务，替代原先“一律置为中断并退款”：
//  1. 已记录上游任务号的（上游提交已成功）转回“等待上游结果”，调度器按原任务号继续认领，
//     不重复提交、不产生第二次上游计费；
//  2. 其余任务若输入文件（input.json）仍在，重新排队执行——仅出现在提交请求尚未被上游
//     确认就中断的极小窗口，会重新提交一次上游请求，用户不会被扣第二次；
//  3. 两者都不具备的（旧版本运行中任务的输入只在内存）按中断收口，并按退款配置退回次数。
//
// 恢复任务的执行由既有的 30 秒兜底通知触发，启动阶段不主动发起上游请求。
func (a *App) recoverJobs() error {
	if _, err := a.db.Exec("UPDATE jobs SET status=? WHERE status IN (?,?,?) AND upstream_task_id<>''", statusPendingUpstream, statusQueued, statusRunning, statusPendingUpstream); err != nil {
		return err
	}
	rows, err := a.db.Query("SELECT id FROM jobs WHERE status IN (?,?)", statusQueued, statusRunning)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := a.readFile(id, "input.json"); err != nil {
			// 输入缺失（旧版本运行中任务的输入只在内存）：按中断收口，下面统一退款。
			if _, err := a.db.Exec("UPDATE jobs SET status='interrupted',error_message='服务重启时任务被中断' WHERE id=? AND status IN (?,?)", id, statusQueued, statusRunning); err != nil {
				return err
			}
			continue
		}
		if _, err := a.db.Exec("UPDATE jobs SET status=?,started=0 WHERE id=?", statusQueued, id); err != nil {
			return err
		}
	}
	if _, err := a.db.Exec(`UPDATE users SET gift=gift+COALESCE((SELECT SUM(gift_cost) FROM jobs WHERE jobs.user_id=users.id AND status='interrupted' AND refund_failure=1),0),paid=paid+COALESCE((SELECT SUM(paid_cost) FROM jobs WHERE jobs.user_id=users.id AND status='interrupted' AND refund_failure=1),0) WHERE id IN (SELECT user_id FROM jobs WHERE status='interrupted' AND refund_failure=1)`); err != nil {
		return err
	}
	_, err = a.db.Exec("UPDATE jobs SET gift_cost=0,paid_cost=0 WHERE status='interrupted' AND refund_failure=1")
	if err != nil {
		return err
	}
	// 中断退款的消耗统计记录一并删除，与在线失败退款的统计口径一致。
	_, err = a.db.Exec("DELETE FROM usage_stats WHERE job_id IN (SELECT id FROM jobs WHERE status='interrupted' AND refund_failure=1)")
	return err
}
func (a *App) Close() {
	// 与启动生成任务互斥，确保 Wait 开始后不会再登记新 worker。
	a.workersMu.Lock()
	a.cancel()
	a.workersMu.Unlock()
	a.wg.Wait()
	a.workers.Wait()
	a.db.Close()
}
func (a *App) cleanup() {
	now := time.Now().Unix()
	a.db.Exec("DELETE FROM sessions WHERE expires<?", now)
	a.db.Exec("DELETE FROM email_codes WHERE expires<?", now)
	a.db.Exec("DELETE FROM rate_limits WHERE expires<?", now)
	// 云端作品集按保留期清理：到期删除云端副本，设备需在窗口内同步；本机副本不受影响。
	a.db.Exec("DELETE FROM works WHERE created<?", now-int64(worksRetention.Seconds()))
	// 社区分享池是独立且永久的：不参与保留期清理，只在分享人主动取消时删除。
	// 上游一直没有结果的等待任务超过认领时效后收口为失败，不再占用并发额度与槽位。
	a.expirePendingJobs(now)
	a.cleanupFiles()
	a.jobsMu.Lock()
	defer a.jobsMu.Unlock()
	for id, j := range a.jobs {
		if !jobIsActive(j.Status) && j.Expires < now {
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
	mux.HandleFunc("POST /api/feedback", a.auth(a.feedback, false))
	mux.HandleFunc("POST /api/generate", a.auth(a.generate, false))
	mux.HandleFunc("GET /api/jobs/{id}", a.auth(a.getJob, false))
	mux.HandleFunc("GET /api/calls", a.auth(a.calls, false))
	// 云端“我的定稿”：列表 + 图片，用于同一账号跨设备同步定稿。
	mux.HandleFunc("GET /api/drafts", a.auth(a.drafts, false))
	mux.HandleFunc("GET /api/drafts/{receipt}/image", a.auth(a.draftImage, false))
	// 云端“我的作品集”：列表 + GIF/原图 + 删除，用于同一账号跨设备同步作品。
	mux.HandleFunc("GET /api/works", a.auth(a.works, false))
	mux.HandleFunc("GET /api/works/{id}/gif", a.auth(a.workGif, false))
	mux.HandleFunc("GET /api/works/{id}/sheet", a.auth(a.workSheet, false))
	mux.HandleFunc("POST /api/works/remove", a.auth(a.workRemove, false))
	mux.HandleFunc("POST /api/accept", a.auth(a.accept, false))
	// 社区分享池：公开浏览的分享列表（需登录以标记“我的分享”）、分享/取消分享与动图本体。
	mux.HandleFunc("GET /api/community", a.auth(a.communityList, false))
	mux.HandleFunc("GET /api/community/states", a.auth(a.communityStates, false))
	mux.HandleFunc("GET /api/community/{id}/gif", a.auth(a.communityImage, false))
	mux.HandleFunc("POST /api/community/share", a.auth(a.communityShare, false))
	mux.HandleFunc("POST /api/community/unshare", a.auth(a.communityUnshare, false))
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
	mux.HandleFunc("GET /api/admin/credits", a.auth(a.adminCredits, true))
	mux.HandleFunc("GET /api/admin/audit", a.auth(a.adminAudit, true))
	mux.HandleFunc("GET /api/admin/jobs", a.auth(a.adminJobs, true))
	mux.HandleFunc("GET /api/admin/jobs/history", a.auth(a.adminJobHistory, true))
	mux.HandleFunc("GET /api/admin/gallery", a.auth(a.adminGallery, true))
	mux.HandleFunc("GET /api/admin/gallery/drafts/{receipt}/image", a.auth(a.adminDraftImage, true))
	mux.HandleFunc("GET /api/admin/gallery/works/{id}/{blob}", a.auth(a.adminWorkBlob, true))
	mux.HandleFunc("GET /api/admin/dashboard", a.auth(a.adminDashboard, true))
	mux.HandleFunc("POST /api/admin/dashboard/email", a.auth(a.adminDashboardEmail, true))
	mux.HandleFunc("GET /api/admin/deployment-version", a.auth(a.adminDeploymentVersion, true))
	mux.HandleFunc("GET /robots.txt", a.robots)
	mux.HandleFunc("GET /sitemap.xml", a.sitemap)
	mux.HandleFunc("GET /llms.txt", a.llms)
	// 社区页是用户生成内容的公开浏览页，不进 sitemap，并带 noindex 避免收录。
	mux.HandleFunc("GET /community", a.communityPage)
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
		// 社区页是用户生成内容，不参与搜索收录。
		if r.URL.Path == "/community" {
			w.Header().Set("X-Robots-Tag", "noindex, follow")
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

// rawIP 返回请求来源 IP 的明文：代理可信时优先取 X-Real-IP，供注册 IP 记录与管理后台展示。
func (a *App) rawIP(r *http.Request) string {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if a.env.TrustProxy && net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback() {
		if ip := net.ParseIP(r.Header.Get("X-Real-IP")); ip != nil {
			host = ip.String()
		}
	}
	return host
}

// ip 返回来源 IP 的哈希值，仅用于限流键，避免在日志/限流表中落明文。
func (a *App) ip(r *http.Request) string {
	return a.mac("ip:" + a.rawIP(r))
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
	// 画风提示词只保留在服务端，前台只拿到编号、名称、说明与图标。
	for i := range s.Styles {
		s.Styles[i].Prompt = ""
	}
	respond(w, 200, map[string]any{"categories": s.Categories, "styles": s.Styles, "chargeOnFailure": s.ChargeOnFailure, "configured": a.secret("api_key") != "", "emailConfigured": a.mailConfigured(s), "feedbackConfigured": a.feedbackConfigured(s), "estimates": a.estimates(s), "redeemHelp": s.RedeemHelp, "userConcurrency": s.UserConcurrency, "generationSlots": cap(a.slots), "motionGrid": s.MotionGrid})
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

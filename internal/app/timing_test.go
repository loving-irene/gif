package app

import (
	"fmt"
	"testing"
	"time"
)

func addTiming(t *testing.T, a *App, id, kind string, cfg Settings, duration time.Duration, status string) {
	t.Helper()
	_, err := a.db.Exec("INSERT OR IGNORE INTO users(id,gift,created) VALUES('timing-user',5,0)")
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.db.Exec("INSERT OR IGNORE INTO jobs(id,user_id,request_id,digest,kind,status,gift_cost,paid_cost,created) VALUES(?,'timing-user',?,'test',?,?,0,0,0)", id, id, kind, status)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.recordTiming(id, kind, cfg, duration, status); err != nil {
		t.Fatal(err)
	}
}

func TestTimingAggregatesSuccessesAndSeparatesGroups(t *testing.T) {
	a := testApp(t)
	cfg, _ := a.settings()
	if e := a.estimate("draft", cfg); e.Seconds != 120 || e.Samples != 0 || e.Source != "initial" || e.MinSeconds != 0 || e.MaxSeconds != 0 {
		t.Fatal(e)
	}
	addTiming(t, a, "one", "draft", cfg, 100*time.Second, "succeeded")
	addTiming(t, a, "two", "draft", cfg, 200*time.Second, "succeeded")
	e := a.estimate("draft", cfg)
	// 移动平均：0.7×100 + 0.3×200 = 130；实测区间 100～200。
	// 样本不足 5 次时按估计值上下 30%（至少 15 秒）补足宽度，得到 91～200。
	if e.Seconds != 130 || e.Samples != 2 || e.MinSeconds != 100 || e.MaxSeconds != 200 || e.LowSeconds != 91 || e.HighSeconds != 200 {
		t.Fatal("unexpected aggregate", e)
	}
	addTiming(t, a, "fast-failure", "draft", cfg, time.Second, "failed")
	addTiming(t, a, "two", "draft", cfg, 400*time.Second, "succeeded")
	if e = a.estimate("draft", cfg); e.Seconds != 130 || e.Samples != 2 || e.MaxSeconds != 200 {
		t.Fatal("failure or duplicate changed prediction", e)
	}
	// 只保留聚合统计：每个分组一行，不再保留每次请求的耗时。
	var rows int
	a.db.QueryRow("SELECT count(*) FROM generation_timing_stats").Scan(&rows)
	if rows != 1 {
		t.Fatal("should keep one aggregate row per group", rows)
	}
	var legacy int
	a.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='generation_timings'").Scan(&legacy)
	if legacy != 0 {
		t.Fatal("per-request timing table should be gone", legacy)
	}
	if e = a.estimate("motion", cfg); e.Seconds != 240 || e.Samples != 0 {
		t.Fatal("motion mixed with draft", e)
	}
	other := cfg
	other.Quality = "medium"
	if e = a.estimate("draft", other); e.Samples != 0 {
		t.Fatal("quality groups mixed")
	}
	other = cfg
	other.Model = "gpt-image-2.5-flare"
	if e = a.estimate("draft", other); e.Samples != 0 {
		t.Fatal("model groups mixed")
	}
}

func TestTimingUsesObservedRangeOnceSamplesAreEnough(t *testing.T) {
	a := testApp(t)
	cfg, _ := a.settings()
	for i, duration := range []time.Duration{300 * time.Second, 90 * time.Second, 240 * time.Second, 150 * time.Second, 420 * time.Second, 120 * time.Second} {
		addTiming(t, a, fmt.Sprint("range-", i), "draft", cfg, duration, "succeeded")
	}
	e := a.estimate("draft", cfg)
	if e.Samples != 6 || e.MinSeconds != 90 || e.MaxSeconds != 420 || e.Source != "history" {
		t.Fatal("observed min/max missing", e)
	}
	// 样本达到 5 次后参考区间直接取实测最短/最长，估计值仍落在区间内。
	if e.LowSeconds != 90 || e.HighSeconds != 420 || e.Seconds < e.LowSeconds || e.Seconds > e.HighSeconds {
		t.Fatal("unexpected range", e)
	}
	if e.Seconds != 228 {
		t.Fatal("unexpected moving average", e)
	}
}

func TestTimingPersistsAggregates(t *testing.T) {
	a := testApp(t)
	cfg, _ := a.settings()
	for i := 0; i < 25; i++ {
		duration := 100 * time.Second
		if i < 5 {
			duration = 400 * time.Second
		}
		addTiming(t, a, fmt.Sprint("sample-", i), "motion", cfg, duration, "succeeded")
	}
	e := a.estimate("motion", cfg)
	// 样本不设窗口上限：25 次全部计入；最近样本占 30% 权重后移动平均收敛到 100 秒附近
	// （每步取整会停在上取整后的 101 秒），实测区间始终保留 100～400。
	if e.Samples != 25 || e.Seconds != 101 || e.MinSeconds != 100 || e.MaxSeconds != 400 || e.LowSeconds != 100 || e.HighSeconds != 400 {
		t.Fatal("aggregates lost samples", e)
	}
	other, err := New(a.env)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if saved := other.estimate("motion", cfg); saved != e {
		t.Fatal("timing lost after reopen", saved)
	}
}

func TestTimingMigratesLegacyRecordsAndDropsThem(t *testing.T) {
	a := testApp(t)
	cfg, _ := a.settings()
	if _, err := a.db.Exec(`CREATE TABLE generation_timings(job_id TEXT PRIMARY KEY,kind TEXT NOT NULL,model TEXT NOT NULL,quality TEXT NOT NULL,duration_ms INTEGER NOT NULL CHECK(duration_ms>=0),status TEXT NOT NULL,completed_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for i, item := range []struct {
		ms     int64
		kind   string
		status string
	}{
		{100000, "draft", "succeeded"},
		{200000, "draft", "succeeded"},
		{1000, "draft", "failed"},
		{300000, "motion", "succeeded"},
		{300000, "motion", "succeeded"},
		{120000, "motion", "succeeded"},
	} {
		if _, err := a.db.Exec("INSERT INTO generation_timings(job_id,kind,model,quality,duration_ms,status,completed_at) VALUES(?,?,?,?,?,?,?)", fmt.Sprint("legacy-", i), item.kind, cfg.Model, cfg.Quality, item.ms, item.status, i); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.migrateTimings(); err != nil {
		t.Fatal(err)
	}
	e := a.estimate("draft", cfg)
	if e.Seconds != 130 || e.Samples != 2 || e.MinSeconds != 100 || e.MaxSeconds != 200 {
		t.Fatal("draft legacy records not aggregated", e)
	}
	// 动作组：300、300、120 → 移动平均 0.7×(0.7×300+0.3×300)+0.3×120 = 246。
	motion := a.estimate("motion", cfg)
	if motion.Seconds != 246 || motion.Samples != 3 || motion.MinSeconds != 120 || motion.MaxSeconds != 300 {
		t.Fatal("motion legacy records not aggregated", motion)
	}
	var legacy int
	a.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='generation_timings'").Scan(&legacy)
	if legacy != 0 {
		t.Fatal("legacy table should be dropped after merging", legacy)
	}
	// 再次迁移不会重复计入（表已删除）。
	if err := a.migrateTimings(); err != nil {
		t.Fatal(err)
	}
	if again := a.estimate("draft", cfg); again != e {
		t.Fatal("second migration changed aggregates", again)
	}
}

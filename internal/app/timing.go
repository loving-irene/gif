package app

import (
	"math"
	"time"
)

const generationTimeout = 8 * time.Minute

// 预估只保留聚合统计，不保留每次请求的耗时：按“类型 + 模型 + 画质”累计成功样本数、
// 实测最短/最长耗时与移动平均。

// timingRecentWeight 是最近一次成功调用在移动平均里的权重。
const timingRecentWeight = 0.3

// timingRangeSamples 是直接采用实测最短/最长作为参考区间所需的样本数；
// 样本更少时按估计值上下 30%（至少 15 秒）补足，避免单次样本得到零宽区间。
const timingRangeSamples = 5

type TimeEstimate struct {
	Seconds     int `json:"seconds"`
	LowSeconds  int `json:"lowSeconds"`
	HighSeconds int `json:"highSeconds"`
	// MinSeconds / MaxSeconds 是历史成功调用的实测最短/最长耗时；MinSeconds 为 0 表示还没有成功样本。
	MinSeconds int `json:"minSeconds"`
	MaxSeconds int `json:"maxSeconds"`
	// Samples 是累计成功样本数（只保留聚合值，样本数不再设窗口上限）。
	Samples int    `json:"samples"`
	Source  string `json:"source"`
}

// timingStats 是某个（类型, 模型, 画质）分组的历史统计聚合。
type timingStats struct {
	samples int
	minMs   int64
	maxMs   int64
	avgMs   int64
}

func (a *App) migrateTimings() error {
	if _, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS generation_timing_stats (
 kind TEXT NOT NULL,model TEXT NOT NULL,quality TEXT NOT NULL,
 samples INTEGER NOT NULL DEFAULT 0 CHECK(samples>=0),
 min_ms INTEGER NOT NULL DEFAULT 0 CHECK(min_ms>=0),max_ms INTEGER NOT NULL DEFAULT 0 CHECK(max_ms>=0),
 avg_ms INTEGER NOT NULL DEFAULT 0 CHECK(avg_ms>=0),updated_at INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(kind,model,quality));`); err != nil {
		return err
	}
	var legacy int
	if err := a.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='generation_timings'").Scan(&legacy); err != nil {
		return err
	}
	if legacy == 0 {
		return nil
	}
	return a.mergeLegacyTimings()
}

// mergeLegacyTimings 把旧库里的逐次耗时一次性汇总成聚合统计，随后删除逐次记录。
// 升级只保留样本数、实测最短/最长与移动平均，之后不再保留任何一次请求的耗时。
func (a *App) mergeLegacyTimings() error {
	rows, err := a.db.Query("SELECT kind,model,quality,duration_ms,status FROM generation_timings WHERE duration_ms>0 ORDER BY completed_at,rowid")
	if err != nil {
		return err
	}
	type group struct{ kind, model, quality string }
	stats := map[group]*timingStats{}
	for rows.Next() {
		var g group
		var ms int64
		var status string
		if err := rows.Scan(&g.kind, &g.model, &g.quality, &ms, &status); err != nil {
			rows.Close()
			return err
		}
		if status != "succeeded" {
			// 失败耗时只统计成功样本时无法代表生成用时，直接丢弃，不污染最短/最长。
			continue
		}
		s := stats[g]
		if s == nil {
			s = &timingStats{}
			stats[g] = s
		}
		s.samples++
		if s.samples == 1 || ms < s.minMs {
			s.minMs = ms
		}
		if ms > s.maxMs {
			s.maxMs = ms
		}
		s.avgMs = movingAverage(s.avgMs, ms, s.samples)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UnixMilli()
	for g, s := range stats {
		if _, err = tx.Exec(`INSERT INTO generation_timing_stats(kind,model,quality,samples,min_ms,max_ms,avg_ms,updated_at) VALUES(?,?,?,?,?,?,?,?)
ON CONFLICT(kind,model,quality) DO UPDATE SET
 samples=samples+excluded.samples,
 min_ms=CASE WHEN samples>0 AND min_ms>0 THEN MIN(min_ms,excluded.min_ms) ELSE excluded.min_ms END,
 max_ms=CASE WHEN samples>0 AND max_ms>0 THEN MAX(max_ms,excluded.max_ms) ELSE excluded.max_ms END,
 avg_ms=CASE WHEN samples>0 AND avg_ms>0 THEN CAST(ROUND(avg_ms*(1.0-?)+excluded.avg_ms*?) AS INTEGER) ELSE excluded.avg_ms END,
 updated_at=excluded.updated_at`, g.kind, g.model, g.quality, s.samples, s.minMs, s.maxMs, s.avgMs, now, timingRecentWeight, timingRecentWeight); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("DROP TABLE IF EXISTS generation_timings"); err != nil {
		return err
	}
	return tx.Commit()
}

// movingAverage 把一次新的成功耗时并入移动平均：第一个样本直接取该值，之后最近一次占 30% 权重。
func movingAverage(avg, sample int64, samples int) int64 {
	if samples <= 1 {
		return sample
	}
	return int64(math.Round(float64(avg)*(1-timingRecentWeight) + float64(sample)*timingRecentWeight))
}

// recordTiming 把一次结束的调用并入聚合统计。同一任务只计入一次：
// jobs.timing_recorded 从 0 翻到 1 的那次才写统计，重复调用直接忽略。
// 只有成功调用参与最短/最长与移动平均，失败不写入耗时样本。
func (a *App) recordTiming(id, kind string, cfg Settings, duration time.Duration, status string) error {
	result, err := a.db.Exec("UPDATE jobs SET timing_recorded=1 WHERE id=? AND timing_recorded=0", id)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	ms := duration.Milliseconds()
	if updated == 0 || status != "succeeded" || ms <= 0 {
		return nil
	}
	_, err = a.db.Exec(`INSERT INTO generation_timing_stats(kind,model,quality,samples,min_ms,max_ms,avg_ms,updated_at) VALUES(?,?,?,1,?,?,?,?)
ON CONFLICT(kind,model,quality) DO UPDATE SET
 samples=samples+1,
 min_ms=CASE WHEN samples>0 AND min_ms>0 THEN MIN(min_ms,excluded.min_ms) ELSE excluded.min_ms END,
 max_ms=CASE WHEN samples>0 AND max_ms>0 THEN MAX(max_ms,excluded.max_ms) ELSE excluded.max_ms END,
 avg_ms=CASE WHEN samples>0 AND avg_ms>0 THEN CAST(ROUND(avg_ms*(1.0-?)+excluded.avg_ms*?) AS INTEGER) ELSE excluded.avg_ms END,
 updated_at=excluded.updated_at`, kind, cfg.Model, cfg.Quality, ms, ms, ms, time.Now().UnixMilli(), timingRecentWeight, timingRecentWeight)
	return err
}

func defaultEstimate(kind string) TimeEstimate {
	if kind == "motion" {
		return TimeEstimate{Seconds: 240, LowSeconds: 120, HighSeconds: 360, Source: "initial"}
	}
	return TimeEstimate{Seconds: 120, LowSeconds: 60, HighSeconds: 180, Source: "initial"}
}

// estimate 按“类型 + 模型 + 画质”读取聚合统计：估计值取移动平均，
// 参考区间优先取实测最短/最长，样本不足（< timingRangeSamples）时按估计值补足宽度。
// 没有成功样本时回退到初始估计。
func (a *App) estimate(kind string, cfg Settings) TimeEstimate {
	estimate := defaultEstimate(kind)
	stats := timingStats{}
	if err := a.db.QueryRow("SELECT samples,min_ms,max_ms,avg_ms FROM generation_timing_stats WHERE kind=? AND model=? AND quality=? AND samples>0", kind, cfg.Model, cfg.Quality).Scan(&stats.samples, &stats.minMs, &stats.maxMs, &stats.avgMs); err != nil {
		return estimate
	}
	if stats.samples <= 0 || stats.avgMs <= 0 || stats.maxMs <= 0 {
		return estimate
	}
	seconds := float64(stats.avgMs) / 1000
	observedLow, observedHigh := float64(stats.minMs)/1000, float64(stats.maxMs)/1000
	low, high := observedLow, observedHigh
	if stats.samples < timingRangeSamples {
		spread := math.Max(15, seconds*timingRecentWeight)
		low = math.Min(low, seconds-spread)
		high = math.Max(high, seconds+spread)
	}
	estimate.Seconds = clampSeconds(seconds)
	estimate.LowSeconds = clampSeconds(math.Min(low, seconds))
	estimate.HighSeconds = clampSeconds(math.Max(high, seconds))
	estimate.MinSeconds = clampSeconds(observedLow)
	estimate.MaxSeconds = clampSeconds(observedHigh)
	estimate.Samples = stats.samples
	estimate.Source = "history"
	return estimate
}

// clampSeconds 把秒数收敛到 [1, generationTimeout] 并向上取整。
func clampSeconds(value float64) int {
	return int(math.Ceil(math.Min(generationTimeout.Seconds(), math.Max(1, value))))
}

func (a *App) estimates(cfg Settings) map[string]TimeEstimate {
	return map[string]TimeEstimate{"draft": a.estimate("draft", cfg), "motion": a.estimate("motion", cfg)}
}

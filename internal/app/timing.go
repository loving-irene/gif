package app

import (
	"math"
	"time"
)

const generationTimeout = 8 * time.Minute

type TimeEstimate struct {
	Seconds     int    `json:"seconds"`
	LowSeconds  int    `json:"lowSeconds"`
	HighSeconds int    `json:"highSeconds"`
	Samples     int    `json:"samples"`
	Source      string `json:"source"`
}

func (a *App) migrateTimings() error {
	_, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS generation_timings (
 job_id TEXT PRIMARY KEY REFERENCES jobs(id),kind TEXT NOT NULL,model TEXT NOT NULL,quality TEXT NOT NULL,
 duration_ms INTEGER NOT NULL CHECK(duration_ms>=0),status TEXT NOT NULL,completed_at INTEGER NOT NULL);
 CREATE INDEX IF NOT EXISTS generation_timings_group ON generation_timings(kind,model,quality,status,completed_at DESC);`)
	return err
}

func (a *App) recordTiming(id, kind string, cfg Settings, duration time.Duration, status string) error {
	_, err := a.db.Exec("INSERT OR IGNORE INTO generation_timings(job_id,kind,model,quality,duration_ms,status,completed_at) VALUES(?,?,?,?,?,?,?)", id, kind, cfg.Model, cfg.Quality, duration.Milliseconds(), status, time.Now().UnixMilli())
	return err
}

func defaultEstimate(kind string) TimeEstimate {
	if kind == "motion" {
		return TimeEstimate{Seconds: 240, LowSeconds: 120, HighSeconds: 360, Source: "initial"}
	}
	return TimeEstimate{Seconds: 120, LowSeconds: 60, HighSeconds: 180, Source: "initial"}
}

func (a *App) estimate(kind string, cfg Settings) TimeEstimate {
	estimate := defaultEstimate(kind)
	rows, err := a.db.Query("SELECT duration_ms FROM generation_timings WHERE kind=? AND model=? AND quality=? AND status='succeeded' AND duration_ms>0 ORDER BY completed_at DESC,rowid DESC LIMIT 20", kind, cfg.Model, cfg.Quality)
	if err != nil {
		return estimate
	}
	defer rows.Close()
	values := []float64{}
	for rows.Next() {
		var ms int64
		if rows.Scan(&ms) != nil {
			return estimate
		}
		values = append(values, float64(ms)/1000)
	}
	if rows.Err() != nil || len(values) == 0 {
		return estimate
	}
	// 从旧到新计算指数移动平均，最近一次成功调用占30%的权重。
	mean := values[len(values)-1]
	for i := len(values) - 2; i >= 0; i-- {
		mean = .7*mean + .3*values[i]
	}
	spread := math.Max(15, mean*.3)
	maxSeconds := generationTimeout.Seconds()
	estimate.Seconds = int(math.Ceil(math.Min(maxSeconds, math.Max(1, mean))))
	estimate.LowSeconds = int(math.Ceil(math.Min(maxSeconds, math.Max(1, mean-spread))))
	estimate.HighSeconds = int(math.Ceil(math.Min(maxSeconds, mean+spread)))
	estimate.Samples = len(values)
	estimate.Source = "history"
	return estimate
}

func (a *App) estimates(cfg Settings) map[string]TimeEstimate {
	return map[string]TimeEstimate{"draft": a.estimate("draft", cfg), "motion": a.estimate("motion", cfg)}
}

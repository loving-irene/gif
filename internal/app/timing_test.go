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

func TestTimingUpdatesUsesSuccessesAndSeparatesGroups(t *testing.T) {
	a := testApp(t)
	cfg, _ := a.settings()
	if e := a.estimate("draft", cfg); e.Seconds != 120 || e.Samples != 0 || e.Source != "initial" {
		t.Fatal(e)
	}
	addTiming(t, a, "one", "draft", cfg, 100*time.Second, "succeeded")
	addTiming(t, a, "two", "draft", cfg, 200*time.Second, "succeeded")
	e := a.estimate("draft", cfg)
	if e.Seconds != 130 || e.Samples != 2 || e.LowSeconds != 91 || e.HighSeconds != 169 {
		t.Fatal("unexpected EWMA", e)
	}
	addTiming(t, a, "fast-failure", "draft", cfg, time.Second, "failed")
	addTiming(t, a, "two", "draft", cfg, 400*time.Second, "succeeded")
	if e = a.estimate("draft", cfg); e.Seconds != 130 || e.Samples != 2 {
		t.Fatal("failure or duplicate changed prediction", e)
	}
	var n int
	a.db.QueryRow("SELECT count(*) FROM generation_timings").Scan(&n)
	if n != 3 {
		t.Fatal("should record failed calls exactly once", n)
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

func TestTimingKeepsRecentWindowAndPersists(t *testing.T) {
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
	if e.Samples != 20 || e.Seconds != 100 {
		t.Fatal("old records affected rolling window", e)
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

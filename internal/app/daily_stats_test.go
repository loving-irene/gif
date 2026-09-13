package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestDailyStatsDue 验证发送时刻：00:23 之前不发，00:23 起统计北京时间前一天。
func TestDailyStatsDue(t *testing.T) {
	if _, ok := dailyStatsDue(time.Date(2026, 9, 13, 0, 22, 59, 0, beijingZone)); ok {
		t.Fatal("00:22 should not send")
	}
	day, ok := dailyStatsDue(time.Date(2026, 9, 13, 0, 23, 0, 0, beijingZone))
	if !ok || day != "2026-09-12" {
		t.Fatal("00:23 should summarize 2026-09-12:", day, ok)
	}
	// 发送时刻之后整天都允许补发前一天（服务晚启动仍能发出）。
	day, ok = dailyStatsDue(time.Date(2026, 9, 13, 15, 5, 0, 0, beijingZone))
	if !ok || day != "2026-09-12" {
		t.Fatal("late run should still summarize previous day:", day, ok)
	}
}

// TestDailyStatsMail 汇总前一天的新增用户、消耗次数（类型×人物分类）与次数来源并发送邮件；
// 窗口外数据不计入，同一统计日不重复发送。
func TestDailyStatsMail(t *testing.T) {
	a := testApp(t)
	sent := mailReady(t, a, "owner@example.com", nil)
	start := time.Date(2026, 9, 12, 0, 0, 0, 0, beijingZone)
	from, to := start.Unix(), start.Add(24*time.Hour).Unix()
	// 新增用户：窗口内 2 个；窗口外 1 个不计入。
	a.db.Exec("INSERT INTO users(id,gift,paid,created) VALUES('u1',0,0,?)", from+3600)
	a.db.Exec("INSERT INTO users(id,gift,paid,created) VALUES('u2',0,0,?)", to-1)
	a.db.Exec("INSERT INTO users(id,gift,paid,created) VALUES('old',0,0,?)", from-1)
	// 消耗：窗口内 定稿男生2、动图男生1、动图女生1、定稿儿童1；窗口外 1 条不计入。
	insertUsage := func(job, kind, category string, created int64) {
		a.db.Exec("INSERT INTO usage_stats(job_id,user_id,kind,category,created) VALUES(?,?,?,?,?)", job, "u1", kind, category, created)
	}
	insertUsage("j1", "draft", "male", from+60)
	insertUsage("j2", "draft", "male", from+120)
	insertUsage("j3", "motion", "male", from+180)
	insertUsage("j4", "motion", "female", from+240)
	insertUsage("j5", "draft", "child", to-1)
	insertUsage("j0", "draft", "male", from-10)
	// 次数来源：兑换码 2 个共 30 次、注册赠送 1 个账号 5 次；窗口外不计入。
	a.db.Exec("INSERT INTO credit_history(user_id,event,credits,created) VALUES('u1','redeem',10,?)", from+300)
	a.db.Exec("INSERT INTO credit_history(user_id,event,credits,created) VALUES('u2','redeem',20,?)", from+400)
	a.db.Exec("INSERT INTO credit_history(user_id,event,credits,created) VALUES('u1','register',5,?)", from+500)
	a.db.Exec("INSERT INTO credit_history(user_id,event,credits,created) VALUES('old','redeem',99,?)", from-5)
	// 未到发送时刻不发。
	a.dailyStatsTick(time.Date(2026, 9, 13, 0, 22, 0, 0, beijingZone))
	if sent.To != "" {
		t.Fatal("mail sent before 00:23")
	}
	a.dailyStatsTick(time.Date(2026, 9, 13, 0, 23, 0, 0, beijingZone))
	if sent.To != "owner@example.com" || !strings.Contains(sent.Subject, "2026-09-12") {
		t.Fatal("unexpected recipient or subject:", sent.To, sent.Subject)
	}
	for _, want := range []string{
		"新增用户：2",
		"消耗创作次数：5",
		"定稿图：3",
		"GIF 动图：2",
		"男生：3（定稿图 2 · GIF 动图 1）",
		"女生：1（定稿图 0 · GIF 动图 1）",
		"小朋友：1（定稿图 1 · GIF 动图 0）",
		"兑换码兑换：共 30 次（2 个兑换码）",
		"注册赠送：共 5 次（1 个账号）",
	} {
		if !strings.Contains(sent.Body, want) {
			t.Fatalf("stats body missing %q:\n%s", want, sent.Body)
		}
	}
	// 同一统计日重复触发不重复发送。
	sent.To = ""
	a.dailyStatsTick(time.Date(2026, 9, 13, 9, 0, 0, 0, beijingZone))
	if sent.To != "" {
		t.Fatal("duplicate daily stats mail")
	}
}

// TestDailyStatsMailRetriesAfterFailure 发送失败不记标记，恢复后同一统计日仍可发出。
func TestDailyStatsMailRetriesAfterFailure(t *testing.T) {
	a := testApp(t)
	start := time.Date(2026, 9, 12, 0, 0, 0, 0, beijingZone)
	a.db.Exec("INSERT INTO users(id,gift,paid,created) VALUES('u1',0,0,?)", start.Unix()+60)
	mailReady(t, a, "", errors.New("smtp down"))
	a.dailyStatsTick(time.Date(2026, 9, 13, 0, 23, 0, 0, beijingZone))
	var marker string
	a.db.QueryRow("SELECT value FROM settings WHERE key='daily_stats_sent'").Scan(&marker)
	if marker != "" {
		t.Fatal("failed mail should not mark the day as sent")
	}
	ok := mailReady(t, a, "", nil)
	a.dailyStatsTick(time.Date(2026, 9, 13, 0, 24, 0, 0, beijingZone))
	if ok.To != "studio@example.com" || !strings.Contains(ok.Body, "新增用户：1") {
		t.Fatal("retry did not send stats:", ok.To, ok.Body)
	}
}

// TestUsageStatsFollowsCharges 消耗随扣次写入统计日志（携带类型与人物分类），失败退款时删除。
func TestUsageStatsFollowsCharges(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "stats-user")
	a.providerCall = asProviderCall(func(context.Context, Settings, string, []string) (string, error) {
		return sampleImage(false), nil
	})
	id := jobID(t, request(t, a, s, "POST", "/api/generate", draftInput()))
	if j := waitJob(t, a, s, id); j.Status != "succeeded" {
		t.Fatal(j.Status)
	}
	// 失败退款的任务：扣次后退款，统计记录应随退款删除。
	cfg, _ := a.settings()
	cfg.ChargeOnFailure = false
	raw, _ := json.Marshal(cfg)
	a.db.Exec("UPDATE settings SET value=? WHERE key='config'", string(raw))
	a.providerCall = asProviderCall(func(context.Context, Settings, string, []string) (string, error) {
		return "", errors.New("simulated failure")
	})
	id2 := jobID(t, request(t, a, s, "POST", "/api/generate", draftInputWith("古代鳞甲", "银灰与藏蓝", "")))
	if j := waitJob(t, a, s, id2); j.Status != "failed" {
		t.Fatal(j.Status)
	}
	var kind, category string
	if err := a.db.QueryRow("SELECT kind,category FROM usage_stats").Scan(&kind, &category); err != nil {
		t.Fatal(err)
	}
	if kind != "draft" || category != "male" {
		t.Fatal("unexpected usage row:", kind, category)
	}
}

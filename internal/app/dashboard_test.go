package app

import (
	"encoding/json"
	"testing"
	"time"
)

// TestDashboardPeriods 验证本日/本周/本月的窗口起点：本日取 00:00、本周取周一、
// 本月取 1 日，且三者都不晚于当前时刻，周起点与当天间隔不足 7 天。
func TestDashboardPeriods(t *testing.T) {
	for _, now := range []time.Time{
		time.Date(2026, 9, 13, 15, 0, 0, 0, beijingZone),
		time.Date(2026, 9, 7, 0, 0, 0, 0, beijingZone),
		time.Date(2026, 9, 1, 23, 59, 0, 0, beijingZone),
		time.Date(2026, 1, 1, 0, 0, 0, 0, beijingZone),
	} {
		day, week, month := dashboardPeriods(now)
		if day.Location() != beijingZone || day.Hour() != 0 || day.Minute() != 0 || day.Second() != 0 {
			t.Fatalf("%v: day start wrong: %v", now, day)
		}
		if week.Weekday() != time.Monday {
			t.Fatalf("%v: week does not start on Monday: %v", now, week)
		}
		if day.Before(week) || day.Sub(week) >= 7*24*time.Hour {
			t.Fatalf("%v: week out of range: %v -> %v", now, week, day)
		}
		if month.Day() != 1 || month.After(day) {
			t.Fatalf("%v: month start wrong: %v", now, month)
		}
	}
}

// TestAdminDashboard 验证看板接口的聚合结果：新增用户、消耗次数、本日分布、
// 兑换码统计与 TOP 榜单。窗口外数据不计入本日/本月。
func TestAdminDashboard(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "one") // 该设备账号创建于“今天”，会计入本日/本周/本月新增。
	loginAdmin(t, a, s)
	now := time.Now().In(beijingZone)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, beijingZone)
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, beijingZone)

	// 新增用户：今天 1 个、本月之前 1 个。
	a.db.Exec("INSERT INTO users(id,gift,paid,created) VALUES('today-user',0,0,?)", day.Unix()+1)
	a.db.Exec("INSERT INTO users(id,gift,paid,created) VALUES('old-user',0,0,?)", month.Unix()-1)

	// 消耗：今天 定稿男生2、动图女生1、动图儿童1；本月之前 1 条不计入。
	insertUsage := func(job, kind, category string, created int64) {
		a.db.Exec("INSERT INTO usage_stats(job_id,user_id,kind,category,created) VALUES(?,?,?,?,?)", job, "today-user", kind, category, created)
	}
	insertUsage("j1", "draft", "male", day.Unix()+1)
	insertUsage("j2", "draft", "male", day.Unix()+2)
	insertUsage("j3", "motion", "female", day.Unix()+3)
	insertUsage("j4", "motion", "child", day.Unix()+4)
	insertUsage("j-old", "draft", "male", month.Unix()-1)

	// 兑换码：3 个共 35 次，其中 1 个已兑换（5 次）。
	a.db.Exec("INSERT INTO codes(hash,label,credits,created) VALUES('c1','b',10,0)")
	a.db.Exec("INSERT INTO codes(hash,label,credits,created) VALUES('c2','b',20,0)")
	a.db.Exec("INSERT INTO codes(hash,label,credits,used_by,used_at,created) VALUES('c3','b',5,'today-user',0,0)")
	// 兑换历史：today-user 兑换 5 次（1 个兑换码），另有 1 条注册赠送不计入兑换榜。
	a.db.Exec("INSERT INTO credit_history(user_id,event,credits,created) VALUES('today-user','redeem',5,?)", day.Unix()+10)
	a.db.Exec("INSERT INTO credit_history(user_id,event,credits,created) VALUES('today-user','register',5,?)", day.Unix()+11)

	w := request(t, a, s, "GET", "/api/admin/dashboard", nil)
	if w.Code != 200 {
		t.Fatalf("dashboard: %d %s", w.Code, w.Body.String())
	}
	var d dashboardData
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// 新增用户：设备账号 + today-user 各计入一次；old-user 在本月之前。
	for _, key := range []string{"day", "week", "month"} {
		if got := d.Ranges[key].NewUsers; got != 2 {
			t.Fatalf("ranges[%s].newUsers=%d want 2", key, got)
		}
	}
	// 消耗：today-user 今天 4 次；j-old 在本月之前不计入任何窗口。
	for _, key := range []string{"day", "week", "month"} {
		if got := d.Ranges[key].Consumed; got != 4 {
			t.Fatalf("ranges[%s].consumed=%d want 4", key, got)
		}
	}
	// 累计消耗为全量口径，包含窗口外的 j-old。
	if d.TotalConsumed != 5 {
		t.Fatalf("totalConsumed=%d want 5", d.TotalConsumed)
	}

	// 本日分布：定稿图 2、GIF 动图 2；男生定稿2、女生动图1、小朋友动图1。
	if d.Today.Total != 4 {
		t.Fatalf("today.total=%d want 4", d.Today.Total)
	}
	if len(d.Today.Kinds) != 2 || d.Today.Kinds[0].Count != 2 || d.Today.Kinds[1].Count != 2 {
		t.Fatalf("today.kinds=%+v", d.Today.Kinds)
	}
	byCat := map[string]dashboardCategory{}
	for _, c := range d.Today.Categories {
		byCat[c.ID] = c
	}
	if byCat["male"].Draft != 2 || byCat["male"].Motion != 0 ||
		byCat["female"].Draft != 0 || byCat["female"].Motion != 1 ||
		byCat["child"].Draft != 0 || byCat["child"].Motion != 1 {
		t.Fatalf("today.categories=%+v", d.Today.Categories)
	}

	// 兑换码：3 个、累计 35 次、已兑换 1 个（5 次）、未兑换 2 个。
	if d.Codes.Total != 3 || d.Codes.Credits != 35 || d.Codes.Redeemed != 1 || d.Codes.Unredeemed != 2 || d.Codes.RedeemedCredits != 5 {
		t.Fatalf("codes=%+v", d.Codes)
	}

	// TOP5 消耗：today-user 共 5 条消耗记录（本日 4 条 + 窗口外 1 条）排第一；
	// 设备账号无消耗记录不出现。
	if len(d.TopConsume) != 1 || d.TopConsume[0].User != "today-user" || d.TopConsume[0].Count != 5 || d.TopConsume[0].Credits != 0 {
		t.Fatalf("topConsume=%+v", d.TopConsume)
	}
	// TOP5 兑换：today-user 兑换 5 次、1 个兑换码；register 事件不计入。
	if len(d.TopRedeem) != 1 || d.TopRedeem[0].User != "today-user" || d.TopRedeem[0].Credits != 5 || d.TopRedeem[0].Count != 1 {
		t.Fatalf("topRedeem=%+v", d.TopRedeem)
	}
}

// TestAdminDashboardRequiresAdmin 非管理员访问看板接口返回 403。
func TestAdminDashboardRequiresAdmin(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "one")
	if w := request(t, a, s, "GET", "/api/admin/dashboard", nil); w.Code != 403 {
		t.Fatalf("non-admin dashboard status=%d want 403", w.Code)
	}
}

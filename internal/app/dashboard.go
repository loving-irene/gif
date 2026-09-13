package app

import (
	"net/http"
	"time"
)

// 数据看板：管理后台首页的核心运营指标。所有时间口径统一使用北京时间（Asia/Shanghai）自然日，
// 消耗口径与每日统计邮件一致（usage_stats 记录实际消耗，失败退款会删除对应记录）。

// dashboardRange 是单个统计窗口（本日/本周/本月）内的新增用户与消耗次数。
type dashboardRange struct {
	NewUsers int `json:"newUsers"`
	Consumed int `json:"consumed"`
}

// dashboardKind 是消耗次数按任务类型的聚合（定稿图 / GIF 动图）。
type dashboardKind struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// dashboardCategory 是消耗次数按人物分类再细分到任务类型。
type dashboardCategory struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Draft  int    `json:"draft"`
	Motion int    `json:"motion"`
}

// dashboardCodes 汇总兑换码的数量与次数。
type dashboardCodes struct {
	Total           int `json:"total"`
	Credits         int `json:"credits"`
	Redeemed        int `json:"redeemed"`
	Unredeemed      int `json:"unredeemed"`
	RedeemedCredits int `json:"redeemedCredits"`
}

// dashboardRank 是 TOP 榜单中的一行（消耗次数或兑换次数）。
type dashboardRank struct {
	User    string `json:"user"`
	Name    string `json:"name"`
	Count   int    `json:"count"`
	Credits int    `json:"credits"`
}

// dashboardToday 是本日消耗的分布：按任务类型与人物分类。
type dashboardToday struct {
	Total      int                 `json:"total"`
	Kinds      []dashboardKind     `json:"kinds"`
	Categories []dashboardCategory `json:"categories"`
}

// dashboardData 是 /api/admin/dashboard 的响应。
type dashboardData struct {
	Ranges        map[string]dashboardRange `json:"ranges"`
	TotalConsumed int                       `json:"totalConsumed"`
	Today         dashboardToday            `json:"today"`
	Codes         dashboardCodes            `json:"codes"`
	TopConsume    []dashboardRank           `json:"topConsume"`
	TopRedeem     []dashboardRank           `json:"topRedeem"`
}

// dashboardPeriods 计算看板的三个统计窗口起点（北京时间）：
// 本日 00:00、本周一 00:00、本月 1 日 00:00。
func dashboardPeriods(now time.Time) (day, week, month time.Time) {
	now = now.In(beijingZone)
	day = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, beijingZone)
	week = day.AddDate(0, 0, -int((now.Weekday()+6)%7))
	month = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, beijingZone)
	return day, week, month
}

// categoryDisplayName 返回人物分类编号对应的展示名，优先取后台配置，缺失时回落默认名。
func categoryDisplayName(categories []Category, id string) string {
	for _, c := range categories {
		if c.ID == id {
			return c.Name
		}
	}
	switch id {
	case "male":
		return "男生"
	case "female":
		return "女生"
	case "child":
		return "小朋友"
	}
	return id
}

// adminDashboard 返回管理后台数据看板所需的全部聚合指标。
func (a *App) adminDashboard(w http.ResponseWriter, r *http.Request) {
	day, week, month := dashboardPeriods(time.Now())
	cfg, err := a.settings()
	if err != nil {
		fail(w, 500, "读取失败")
		return
	}

	out := dashboardData{Ranges: map[string]dashboardRange{}}
	for key, from := range map[string]time.Time{"day": day, "week": week, "month": month} {
		var v dashboardRange
		if err := a.db.QueryRow("SELECT COUNT(*) FROM users WHERE created>=?", from.Unix()).Scan(&v.NewUsers); err != nil {
			fail(w, 500, "读取失败")
			return
		}
		if err := a.db.QueryRow("SELECT COUNT(*) FROM usage_stats WHERE created>=?", from.Unix()).Scan(&v.Consumed); err != nil {
			fail(w, 500, "读取失败")
			return
		}
		out.Ranges[key] = v
	}
	if err := a.db.QueryRow("SELECT COUNT(*) FROM usage_stats").Scan(&out.TotalConsumed); err != nil {
		fail(w, 500, "读取失败")
		return
	}

	// 本日消耗分布：按任务类型与人物分类。
	kindCounts := map[string]int{"draft": 0, "motion": 0}
	categoryCounts := map[string]map[string]int{}
	rows, err := a.db.Query("SELECT kind,category,COUNT(*) FROM usage_stats WHERE created>=? GROUP BY kind,category", day.Unix())
	if err != nil {
		fail(w, 500, "读取失败")
		return
	}
	for rows.Next() {
		var kind, category string
		var n int
		if err := rows.Scan(&kind, &category, &n); err != nil {
			rows.Close()
			fail(w, 500, "读取失败")
			return
		}
		out.Today.Total += n
		kindCounts[kind] += n
		if categoryCounts[category] == nil {
			categoryCounts[category] = map[string]int{}
		}
		categoryCounts[category][kind] += n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		fail(w, 500, "读取失败")
		return
	}
	out.Today.Kinds = []dashboardKind{
		{ID: "draft", Name: "定稿图", Count: kindCounts["draft"]},
		{ID: "motion", Name: "GIF 动图", Count: kindCounts["motion"]},
	}
	for _, id := range []string{"male", "female", "child"} {
		counts := categoryCounts[id]
		out.Today.Categories = append(out.Today.Categories, dashboardCategory{
			ID:     id,
			Name:   categoryDisplayName(cfg.Categories, id),
			Draft:  counts["draft"],
			Motion: counts["motion"],
		})
	}

	// 兑换码：总数、累计次数、已兑换/未兑换个数与已兑换累计次数。
	if err := a.db.QueryRow("SELECT COUNT(*),COALESCE(SUM(credits),0),COALESCE(SUM(CASE WHEN used_by IS NOT NULL THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN used_by IS NOT NULL THEN credits ELSE 0 END),0) FROM codes").Scan(&out.Codes.Total, &out.Codes.Credits, &out.Codes.Redeemed, &out.Codes.RedeemedCredits); err != nil {
		fail(w, 500, "读取失败")
		return
	}
	out.Codes.Unredeemed = out.Codes.Total - out.Codes.Redeemed

	// TOP5 消耗次数（按 usage_stats 记录数）。
	if out.TopConsume, err = a.dashboardRanked("SELECT u.id,COALESCE(u.name,''),COUNT(*) AS amount,0 FROM usage_stats s JOIN users u ON u.id=s.user_id GROUP BY s.user_id ORDER BY amount DESC,u.id LIMIT 5", true); err != nil {
		fail(w, 500, "读取失败")
		return
	}
	// TOP5 兑换次数（按 credit_history 中 redeem 事件的累计次数，附带兑换码个数）。
	if out.TopRedeem, err = a.dashboardRanked("SELECT h.user_id,COALESCE(u.name,''),COALESCE(SUM(h.credits),0),COUNT(*) FROM credit_history h JOIN users u ON u.id=h.user_id WHERE h.event='redeem' GROUP BY h.user_id ORDER BY 3 DESC,u.id LIMIT 5", false); err != nil {
		fail(w, 500, "读取失败")
		return
	}

	respond(w, 200, out)
}

// dashboardRanked 执行 TOP 榜单查询。consume 为真时第四列为消耗次数（计入 Count），
// 为假时第三列为累计次数（计入 Credits）、第四列为兑换码个数（计入 Count）。
func (a *App) dashboardRanked(query string, consume bool) ([]dashboardRank, error) {
	rows, err := a.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []dashboardRank{}
	for rows.Next() {
		var item dashboardRank
		if err := rows.Scan(&item.User, &item.Name, &item.Credits, &item.Count); err != nil {
			return nil, err
		}
		if consume {
			item.Count, item.Credits = item.Credits, 0
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

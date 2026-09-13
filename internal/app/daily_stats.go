package app

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// beijingZone 是每日统计使用的固定时区：北京时间（UTC+8，我国不实行夏令时）。
var beijingZone = time.FixedZone("Asia/Shanghai", 8*60*60)

// dailyStatsSendMinute 是每天开始发送统计邮件的北京时间分钟数（凌晨 00:23）。
const dailyStatsSendMinute = 23

// usageKindNames 把任务类型转成邮件里的展示名。
var usageKindNames = map[string]string{"draft": "定稿图", "motion": "GIF 动图"}

// creditEventNames 把次数来源转成邮件里的展示名。
var creditEventNames = map[string]string{
	creditEventRegister: "注册赠送",
	creditEventRedeem:   "兑换码兑换",
	creditEventAdmin:    "后台增加",
}

// creditEventUnits 是次数来源的计量单位，用于“（2 个兑换码）”式补充说明。
var creditEventUnits = map[string]string{
	creditEventRegister: "个账号",
	creditEventRedeem:   "个兑换码",
	creditEventAdmin:    "笔",
}

// creditEventStat 是单个次数来源在统计窗口内的聚合值。
type creditEventStat struct {
	Count   int
	Credits int
}

// dailyStats 是一个统计日（北京时间自然日）的汇总数据。
type dailyStats struct {
	NewUsers int
	// Total 是当天实际消耗的创作次数合计（usage_stats 全部记录）。
	Total int
	// ByKind 按任务类型（draft 定稿图 / motion GIF 动图）汇总消耗次数。
	ByKind map[string]int
	// ByCategory 按人物分类（男生/女生/小朋友）再细分到任务类型。
	ByCategory map[string]map[string]int
	// Events 汇总次数来源（注册赠送、兑换码兑换、后台增加）的笔数与次数。
	Events map[string]creditEventStat
}

// dailyStatsDue 判断当前北京时间是否已到每日发送时刻（凌晨 00:23）：
// 到点后返回应统计的日期（北京时间前一天）；00:23 之前不发送。
func dailyStatsDue(now time.Time) (string, bool) {
	if now.Hour() == 0 && now.Minute() < dailyStatsSendMinute {
		return "", false
	}
	return now.AddDate(0, 0, -1).Format("2006-01-02"), true
}

// dailyStatsTick 由每分钟的定时器调用：到达发送时刻且该统计日尚未发送时，
// 汇总前一天数据并发送邮件。发送成功才写入标记（每天最多成功发送一封）；
// 发送失败（邮件服务未配置、网络异常等）不记标记，下一分钟自动重试；
// 服务在发送时刻后才启动的，当天稍后仍会补发前一天的统计。
func (a *App) dailyStatsTick(now time.Time) {
	day, due := dailyStatsDue(now)
	if !due {
		return
	}
	var sent string
	if a.db.QueryRow("SELECT value FROM settings WHERE key='daily_stats_sent'").Scan(&sent) == nil && sent == day {
		return
	}
	if err := a.sendDailyStats(day); err != nil {
		a.debug(a.ctx, "daily_stats_mail_failed", map[string]any{"day": day, "error": errorText(err)})
		return
	}
	if _, err := a.db.Exec("INSERT INTO settings(key,value) VALUES('daily_stats_sent',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", day); err != nil {
		a.debug(a.ctx, "daily_stats_mark_failed", map[string]any{"day": day, "error": errorText(err)})
	}
}

// sendDailyStats 汇总一个统计日的数据并发送邮件。收件人与用户反馈通知一致：
// 后台“反馈接收邮箱”，留空时回落到发件邮箱。
func (a *App) sendDailyStats(day string) error {
	cfg, err := a.settings()
	if err != nil {
		return err
	}
	if !a.feedbackConfigured(cfg) {
		return errors.New("邮件服务尚未配置，暂无法发送每日统计")
	}
	start, err := time.ParseInLocation("2006-01-02", day, beijingZone)
	if err != nil {
		return err
	}
	stats, err := a.collectDailyStats(start.Unix(), start.Add(24*time.Hour).Unix())
	if err != nil {
		return err
	}
	return a.mailNotify(cfg, feedbackRecipient(cfg), "拾光 GIF 每日统计 "+day, dailyStatsBody(day, stats, cfg))
}

// collectDailyStats 汇总统计窗口 [from,to)（北京时间一个自然日）内的数据：
// 新增用户、按任务类型与人物分类的实际消耗次数、各来源的次数发放。
// 消耗口径与扣次一致：任务创建扣次时记入 usage_stats、失败退款时删除，
// 因此只统计实际消耗；功能上线前的历史任务没有消耗记录。
func (a *App) collectDailyStats(from, to int64) (dailyStats, error) {
	stats := dailyStats{ByKind: map[string]int{}, ByCategory: map[string]map[string]int{}, Events: map[string]creditEventStat{}}
	if err := a.db.QueryRow("SELECT COUNT(*) FROM users WHERE created>=? AND created<?", from, to).Scan(&stats.NewUsers); err != nil {
		return stats, err
	}
	rows, err := a.db.Query("SELECT kind,category,COUNT(*) FROM usage_stats WHERE created>=? AND created<? GROUP BY kind,category", from, to)
	if err != nil {
		return stats, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, category string
		var n int
		if err := rows.Scan(&kind, &category, &n); err != nil {
			return stats, err
		}
		stats.Total += n
		stats.ByKind[kind] += n
		if stats.ByCategory[category] == nil {
			stats.ByCategory[category] = map[string]int{}
		}
		stats.ByCategory[category][kind] += n
	}
	if err := rows.Err(); err != nil {
		return stats, err
	}
	eventRows, err := a.db.Query("SELECT event,COUNT(*),COALESCE(SUM(credits),0) FROM credit_history WHERE created>=? AND created<? GROUP BY event", from, to)
	if err != nil {
		return stats, err
	}
	defer eventRows.Close()
	for eventRows.Next() {
		var event string
		var s creditEventStat
		if err := eventRows.Scan(&event, &s.Count, &s.Credits); err != nil {
			return stats, err
		}
		stats.Events[event] = s
	}
	return stats, eventRows.Err()
}

// dailyStatsBody 生成纯文本统计正文：新增用户、消耗创作次数（先按定稿图/GIF 动图拆分，
// 再按男生/女生/小朋友分类拆出“定稿图 x · GIF 动图 y”），以及兑换码兑换等次数来源。
func dailyStatsBody(day string, stats dailyStats, cfg Settings) string {
	var b strings.Builder
	fmt.Fprintf(&b, "拾光 GIF 每日统计（%s）\r\n\r\n", day)
	fmt.Fprintf(&b, "新增用户：%d\r\n\r\n", stats.NewUsers)
	fmt.Fprintf(&b, "消耗创作次数：%d\r\n", stats.Total)
	for _, kind := range []string{"draft", "motion"} {
		fmt.Fprintf(&b, "- %s：%d\r\n", usageKindNames[kind], stats.ByKind[kind])
	}
	for _, c := range cfg.Categories {
		counts := stats.ByCategory[c.ID]
		fmt.Fprintf(&b, "- %s：%d（定稿图 %d · GIF 动图 %d）\r\n", c.Name, counts["draft"]+counts["motion"], counts["draft"], counts["motion"])
	}
	if len(stats.Events) > 0 {
		b.WriteString("\r\n次数来源\r\n")
		for _, event := range []string{creditEventRedeem, creditEventRegister, creditEventAdmin} {
			s := stats.Events[event]
			if s.Count == 0 {
				continue
			}
			fmt.Fprintf(&b, "- %s：共 %d 次（%d %s）\r\n", creditEventNames[event], s.Credits, s.Count, creditEventUnits[event])
		}
	}
	return b.String()
}

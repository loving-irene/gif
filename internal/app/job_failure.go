package app

import (
	"context"
	"database/sql"
	"time"
)

// failJob 只收口仍处于预期状态的任务，将终态、退款与消耗统计放在同一事务。
// 使用提交时保存的退款策略，重复清理或并发调度不会再次退款。
func (a *App) failJob(id, reason, expectedStatus string) (charged, changed bool, err error) {
	tx, err := a.db.Begin()
	if err != nil {
		return false, false, err
	}
	defer tx.Rollback()
	var uid string
	var gift, paid int
	var refund bool
	var created int64
	err = tx.QueryRow("UPDATE jobs SET status='failed',error_message=? WHERE id=? AND status=? RETURNING user_id,gift_cost,paid_cost,refund_failure,created", reason, id, expectedStatus).Scan(&uid, &gift, &paid, &refund, &created)
	if err == sql.ErrNoRows {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	charged = gift+paid > 0
	if refund {
		if _, err = tx.Exec("UPDATE users SET gift=gift+?,paid=paid+? WHERE id=?", gift, paid, uid); err != nil {
			return false, false, err
		}
		if _, err = tx.Exec("UPDATE jobs SET gift_cost=0,paid_cost=0 WHERE id=?", id); err != nil {
			return false, false, err
		}
		if _, err = tx.Exec("DELETE FROM usage_stats WHERE job_id=?", id); err != nil {
			return false, false, err
		}
		charged = false
	}
	if err = tx.Commit(); err != nil {
		return false, false, err
	}
	a.jobsMu.Lock()
	a.jobs[id] = &Job{ID: id, User: uid, Status: "failed", Error: networkErrorMessage, Charged: charged, StartedAt: created * 1000, Expires: time.Now().Add(10 * time.Minute).Unix()}
	a.jobsMu.Unlock()
	a.removeFile(id, "input.json")
	return charged, true, nil
}

func (a *App) expirePendingJobs(now int64) {
	rows, err := a.db.Query("SELECT id FROM jobs WHERE status=? AND created<?", statusPendingUpstream, now-int64(a.waitBudget.Seconds()))
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err != nil || rows.Err() != nil {
		return
	}
	for _, id := range ids {
		if _, _, err := a.failJob(id, "等待上游结果超时，已按失败收口", statusPendingUpstream); err != nil {
			a.debug(context.WithValue(a.ctx, debugTraceKey{}, id), "job_expire_error", map[string]any{"error": errorText(err)})
		}
	}
}

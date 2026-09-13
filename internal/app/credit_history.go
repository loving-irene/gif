package app

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
)

// 次数历史的三类来源：注册赠送、兑换码兑换、后台手动增加。
const (
	creditEventRegister = "register"
	creditEventRedeem   = "redeem"
	creditEventAdmin    = "admin"
)

type creditExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// addCredit 记录一次次数来源；与余额变动同事务调用，保证历史与余额一致。
func addCredit(db creditExecer, user, event string, credits int, note string, created int64) error {
	_, err := db.Exec("INSERT INTO credit_history(user_id,event,credits,note,created) VALUES(?,?,?,?,?)", user, event, credits, note, created)
	return err
}

// migrateCreditHistory 对旧库做一次性回溯，补齐历史次数来源记录：
// 兑换码兑换与后台手动增加分别从 codes、audit 精确还原；注册赠送无法追溯原始值，
// 按“当前剩余赠送 + 任务已消耗赠送次数”估算，备注标注“历史估算”（合并过邮箱的账号可能略有偏差）。
// 回溯与完成标记在同一事务内提交，重复重启不会产生重复记录。
func (a *App) migrateCreditHistory() error {
	var done int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM settings WHERE key='credit_history_backfill'").Scan(&done); err != nil {
		return err
	}
	if done > 0 {
		return nil
	}
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO credit_history(user_id,event,credits,note,created)
SELECT c.used_by,'redeem',c.credits,'兑换码 '||substr(c.hash,1,12),c.used_at FROM codes c WHERE c.used_by IS NOT NULL AND c.used_at IS NOT NULL`); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO credit_history(user_id,event,credits,note,created)
SELECT u.id,'register',u.gift+COALESCE((SELECT SUM(j.gift_cost) FROM jobs j WHERE j.user_id=u.id),0),'历史估算',u.created FROM users u
WHERE u.gift+COALESCE((SELECT SUM(j.gift_cost) FROM jobs j WHERE j.user_id=u.id),0)>0`); err != nil {
		return err
	}
	// 后台手动增加在审计中的格式固定为 "<账号> add=N disabled=t"；无法解析或次数为0的跳过。
	rows, err := tx.Query("SELECT target,created FROM audit WHERE event='user_update'")
	if err != nil {
		return err
	}
	type manualAdd struct {
		user    string
		credits int
		created int64
	}
	adds := []manualAdd{}
	for rows.Next() {
		var target string
		var created int64
		if err := rows.Scan(&target, &created); err != nil {
			rows.Close()
			return err
		}
		parts := strings.Fields(target)
		if len(parts) != 3 || !strings.HasPrefix(parts[1], "add=") {
			continue
		}
		n, err := strconv.Atoi(parts[1][len("add="):])
		if err != nil || n <= 0 {
			continue
		}
		adds = append(adds, manualAdd{parts[0], n, created})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, add := range adds {
		var exists int
		if err := tx.QueryRow("SELECT 1 FROM users WHERE id=?", add.user).Scan(&exists); err == sql.ErrNoRows {
			continue
		} else if err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO credit_history(user_id,event,credits,note,created) VALUES(?,'admin',?,'',?)", add.user, add.credits, add.created); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("INSERT INTO settings(key,value) VALUES('credit_history_backfill','1')"); err != nil {
		return err
	}
	return tx.Commit()
}

// adminCredits 返回次数兑换/赠送历史：注册赠送、兑换码兑换与后台手动增加，
// 按时间倒序分页，可按账户名、邮箱或账号编号过滤。
func (a *App) adminCredits(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > 254 {
		fail(w, 400, "搜索条件过长")
		return
	}
	page := pageParam(r)
	where, args := "", []any{}
	if q != "" {
		like := "%" + q + "%"
		where = " WHERE h.user_id LIKE ? OR COALESCE(u.name,'') LIKE ? OR COALESCE(u.email,'') LIKE ?"
		args = append(args, like, like, like)
	}
	var total int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM credit_history h LEFT JOIN users u ON u.id=h.user_id"+where, args...).Scan(&total); err != nil {
		fail(w, 500, "读取失败")
		return
	}
	rows, err := a.db.Query("SELECT h.user_id,COALESCE(u.name,''),h.event,h.credits,h.note,h.created FROM credit_history h LEFT JOIN users u ON u.id=h.user_id"+where+" ORDER BY h.created DESC,h.id DESC LIMIT ? OFFSET ?", append(args, adminPageSize, (page-1)*adminPageSize)...)
	if err != nil {
		fail(w, 500, "读取失败")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var user, userName, event, note string
		var credits int
		var created int64
		if rows.Scan(&user, &userName, &event, &credits, &note, &created) == nil {
			out = append(out, map[string]any{"user": user, "userName": userName, "event": event, "credits": credits, "note": note, "created": created})
		}
	}
	pageResult(w, page, total, out)
}

package app

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// 分享到社区成功后赠送创作次数：每人每个北京时间自然日最多 1 次。
const shareRewardCredits = 1

// migrateShareRewardSchema 扩展次数历史事件类型，并建立每日分享奖励去重表。
// 旧库 credit_history 的 CHECK 不含 share，需要重建表才能写入新事件。
func (a *App) migrateShareRewardSchema() error {
	if err := a.migrateCreditHistoryShareEvent(); err != nil {
		return err
	}
	_, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS share_daily_rewards(
user_id TEXT NOT NULL REFERENCES users(id),
day TEXT NOT NULL,
credits INTEGER NOT NULL CHECK(credits>0),
created INTEGER NOT NULL,
PRIMARY KEY(user_id,day))`)
	return err
}

func (a *App) migrateCreditHistoryShareEvent() error {
	var schema string
	err := a.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='credit_history'`).Scan(&schema)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if strings.Contains(schema, "'share'") {
		return nil
	}
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE TABLE credit_history_new(
id INTEGER PRIMARY KEY AUTOINCREMENT,
user_id TEXT NOT NULL REFERENCES users(id),
event TEXT NOT NULL CHECK(event IN ('register','redeem','admin','share')),
credits INTEGER NOT NULL CHECK(credits>0),
note TEXT NOT NULL DEFAULT '',
created INTEGER NOT NULL)`); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO credit_history_new(id,user_id,event,credits,note,created)
SELECT id,user_id,event,credits,note,created FROM credit_history`); err != nil {
		return err
	}
	if _, err = tx.Exec(`DROP TABLE credit_history`); err != nil {
		return err
	}
	if _, err = tx.Exec(`ALTER TABLE credit_history_new RENAME TO credit_history`); err != nil {
		return err
	}
	if _, err = tx.Exec(`CREATE INDEX IF NOT EXISTS credit_history_created ON credit_history(created,id)`); err != nil {
		return err
	}
	return tx.Commit()
}

// tryGrantShareReward 在分享成功后尝试赠送 1 次；同一账号同一北京自然日只成功一次。
// 用 share_daily_rewards 主键去重，避免并发分享重复发放。
func (a *App) tryGrantShareReward(uid string, now time.Time) (int, error) {
	day := now.In(beijingZone)
	dayKey := day.Format("2006-01-02")
	created := now.Unix()
	tx, err := a.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(
		`INSERT OR IGNORE INTO share_daily_rewards(user_id,day,credits,created) VALUES(?,?,?,?)`,
		uid, dayKey, shareRewardCredits, created,
	)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, nil
	}
	if _, err = tx.Exec(`UPDATE users SET gift=gift+? WHERE id=?`, shareRewardCredits, uid); err != nil {
		return 0, err
	}
	if err = addCredit(tx, uid, creditEventShare, shareRewardCredits, fmt.Sprintf("分享奖励 %s", dayKey), created); err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return shareRewardCredits, nil
}

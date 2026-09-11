package app

// migrateCodeMarks 为已有数据库添加管理标记，原有兑换码和兑换记录保持不变。
func (a *App) migrateCodeMarks() error {
	rows, err := a.db.Query("PRAGMA table_info(codes)")
	if err != nil {
		return err
	}
	found := map[string]bool{}
	for rows.Next() {
		var name string
		var cid, kind, notNull, defaultValue, primaryKey any
		if err = rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		found[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if !found["marked"] {
		if _, err = a.db.Exec("ALTER TABLE codes ADD COLUMN marked INTEGER NOT NULL DEFAULT 0 CHECK(marked IN (0,1))"); err != nil {
			return err
		}
	}
	if !found["encrypted_code"] {
		_, err = a.db.Exec("ALTER TABLE codes ADD COLUMN encrypted_code TEXT NOT NULL DEFAULT ''")
	}
	return err
}

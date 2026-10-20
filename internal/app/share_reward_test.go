package app

import (
	"encoding/json"
	"testing"
	"time"
)

// 分享到社区：当天第一次成功分享获赠 1 次，同一天再次分享不再加次。
func TestCommunityShareDailyRewardOnce(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "share-reward")
	before, err := a.readUser(s.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	gif := gifFixture(t)
	a.db.Exec("INSERT INTO works(id,user_id,created,name,category,action,gif) VALUES('reward01',?,1,'挥手问好','male','wave',?)", s.User.ID, gif)
	a.db.Exec("INSERT INTO works(id,user_id,created,name,category,action,gif) VALUES('reward02',?,2,'干饭','male','eat',?)", s.User.ID, gif)

	first := request(t, a, s, "POST", "/api/community/share", map[string]string{"id": "reward01"})
	if first.Code != 200 {
		t.Fatal("first share failed", first.Code, first.Body.String())
	}
	var item shareItem
	if err := json.Unmarshal(first.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	if item.RewardCredits != 1 {
		t.Fatalf("first share should grant 1 credit, got %+v", item)
	}
	afterFirst, _ := a.readUser(s.User.ID)
	if afterFirst.Credits != before.Credits+1 || item.Credits != afterFirst.Credits {
		t.Fatalf("credits not increased: before=%d after=%d response=%d", before.Credits, afterFirst.Credits, item.Credits)
	}

	second := request(t, a, s, "POST", "/api/community/share", map[string]string{"id": "reward02"})
	if second.Code != 200 {
		t.Fatal("second share failed", second.Code, second.Body.String())
	}
	item = shareItem{}
	json.Unmarshal(second.Body.Bytes(), &item)
	if item.RewardCredits != 0 {
		t.Fatalf("same-day second share must not grant again: %+v", item)
	}
	afterSecond, _ := a.readUser(s.User.ID)
	if afterSecond.Credits != afterFirst.Credits {
		t.Fatalf("credits changed on second share: %d -> %d", afterFirst.Credits, afterSecond.Credits)
	}

	var event string
	var credits int
	if err := a.db.QueryRow(
		`SELECT event,credits FROM credit_history WHERE user_id=? AND event=?`,
		s.User.ID, creditEventShare,
	).Scan(&event, &credits); err != nil || credits != 1 {
		t.Fatal("share credit history missing", err, event, credits)
	}
	dayKey := time.Now().In(beijingZone).Format("2006-01-02")
	var day string
	if err := a.db.QueryRow(`SELECT day FROM share_daily_rewards WHERE user_id=?`, s.User.ID).Scan(&day); err != nil || day != dayKey {
		t.Fatal("daily reward marker missing", err, day, dayKey)
	}
}

func TestMigrateCreditHistoryAllowsShareEvent(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "share-migrate-user")
	// 模拟旧表 CHECK 不含 share：重建后 migrate 应允许写入 share 事件。
	if _, err := a.db.Exec(`DROP TABLE credit_history`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`CREATE TABLE credit_history(
id INTEGER PRIMARY KEY AUTOINCREMENT,
user_id TEXT NOT NULL REFERENCES users(id),
event TEXT NOT NULL CHECK(event IN ('register','redeem','admin')),
credits INTEGER NOT NULL CHECK(credits>0),
note TEXT NOT NULL DEFAULT '',
created INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(
		`INSERT INTO credit_history(user_id,event,credits,note,created) VALUES(?,'share',1,'test',?)`,
		s.User.ID, time.Now().Unix(),
	); err == nil {
		t.Fatal("legacy CHECK should reject share event")
	}
	if err := a.migrateShareRewardSchema(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(
		`INSERT INTO credit_history(user_id,event,credits,note,created) VALUES(?,'share',1,'test',?)`,
		s.User.ID, time.Now().Unix(),
	); err != nil {
		t.Fatal("share event still rejected after migration", err)
	}
}

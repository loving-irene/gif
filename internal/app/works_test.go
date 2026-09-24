package app

// 云端“我的作品集”同步测试：动作成功后写入云端、跨设备按账号读取、
// GIF 优先保存（合成失败时保留原图）、删除云端副本、每账号最多保留 30 张、
// 越权与未登录访问被拒绝。
import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"strings"
	"testing"
	"time"
)

// acceptedDraft 生成并确认一份定稿，返回动作请求所需的自拍、定稿图与已确认凭证。
func acceptedDraft(t *testing.T, a *App, s *testSession) (selfie, draft, receipt string) {
	t.Helper()
	instantProvider(a)
	input := draftInput()
	id := jobID(t, request(t, a, s, "POST", "/api/generate", input))
	j := waitJob(t, a, s, id)
	w := request(t, a, s, "POST", "/api/accept", map[string]string{"receipt": j.Receipt})
	var accepted map[string]string
	json.Unmarshal(w.Body.Bytes(), &accepted)
	return input.Selfie, j.Image, accepted["receipt"]
}

func motionInput(selfie, draft, receipt, action string) GenerateInput {
	return GenerateInput{RequestID: token(16), Kind: "motion", Selection: draftInput().Selection, Action: action, Selfie: selfie, Draft: draft, Receipt: receipt}
}

type workItem struct {
	ID       string `json:"id"`
	Created  int64  `json:"created"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Action   string `json:"action"`
	HasGif   bool   `json:"hasGif"`
	HasSheet bool   `json:"hasSheet"`
}
type workList struct {
	Items            []workItem `json:"items"`
	RetentionSeconds int64      `json:"retentionSeconds"`
}

func TestWorksSyncAcrossDevices(t *testing.T) {
	a := testApp(t)
	one := loginDevice(t, a, "works-one")
	two := loginDevice(t, a, "works-two")
	selfie, draft, receipt := acceptedDraft(t, a, one)
	// 设备一生成动作 GIF：成功后云端作品集应出现该作品。
	id := jobID(t, request(t, a, one, "POST", "/api/generate", motionInput(selfie, draft, receipt, "attack")))
	if j := waitJob(t, a, one, id); j.Status != "succeeded" || len(j.Gif) == 0 {
		t.Fatal(j)
	}
	w := request(t, a, one, "GET", "/api/works", nil)
	var list workList
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list.Items) != 1 {
		t.Fatal("works list wrong:", w.Body.String())
	}
	item := list.Items[0]
	if item.ID != id || !item.HasGif || !item.HasSheet || item.Name != "蓄力攻击" || item.Category != "male" || item.Action != "attack" {
		t.Fatal("work item wrong:", item)
	}
	// 列表同时下发保留窗口（3天），供前端区分用户删除与到期清理。
	if list.RetentionSeconds != int64(worksRetention.Seconds()) {
		t.Fatal("retention window wrong:", list.RetentionSeconds)
	}
	// 本人可取回 GIF 与动作序列原图。
	w = request(t, a, one, "GET", "/api/works/"+id+"/gif", nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/gif" || !strings.HasPrefix(w.Body.String(), "GIF") {
		t.Fatal("work gif wrong:", w.Code, w.Header().Get("Content-Type"))
	}
	w = request(t, a, one, "GET", "/api/works/"+id+"/sheet", nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" {
		t.Fatal("work sheet wrong:", w.Code, w.Header().Get("Content-Type"))
	}
	// 其他账号看不到也取不到。
	if w = request(t, a, two, "GET", "/api/works", nil); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var other workList
	json.Unmarshal(w.Body.Bytes(), &other)
	if len(other.Items) != 0 {
		t.Fatal("works leaked to another account")
	}
	if w = request(t, a, two, "GET", "/api/works/"+id+"/gif", nil); w.Code != 404 {
		t.Fatal("work gif should stay private:", w.Code)
	}
	// 删除云端作品后本人列表与 GIF 均不可再取；重复删除与删除他人作品同样返回成功。
	if w = request(t, a, one, "POST", "/api/works/remove", map[string]string{"id": id}); w.Code != 200 {
		t.Fatal("work remove failed:", w.Code, w.Body.String())
	}
	if w = request(t, a, one, "POST", "/api/works/remove", map[string]string{"id": id}); w.Code != 200 {
		t.Fatal("removing again should stay idempotent:", w.Code)
	}
	if w = request(t, a, two, "POST", "/api/works/remove", map[string]string{"id": id}); w.Code != 200 {
		t.Fatal("foreign remove should not leak existence:", w.Code)
	}
	w = request(t, a, one, "GET", "/api/works", nil)
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Items) != 0 {
		t.Fatal("work not removed")
	}
	if w = request(t, a, one, "GET", "/api/works/"+id+"/gif", nil); w.Code != 404 {
		t.Fatal("work gif should be gone:", w.Code)
	}
	// 无效编号与未登录请求被拒绝。
	if w = request(t, a, one, "POST", "/api/works/remove", map[string]string{"id": "../bad"}); w.Code != 400 {
		t.Fatal("invalid work id should be rejected:", w.Code)
	}
	if w = request(t, a, nil, "GET", "/api/works", nil); w.Code != 401 {
		t.Fatal("works should require auth")
	}
}

// GIF 合成失败（上游返回非网格图）时云端保存动作原图，供其他设备拉取后本机重新合成。
func TestWorkStoresSheetWhenGIFMissing(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "works-sheet")
	selfie, draft, receipt := acceptedDraft(t, a, s)
	// 非方形小图：服务器按网格规格切格失败，GIF 不落盘。
	var buf bytes.Buffer
	png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 64, 32)))
	flat := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	a.providerCall = asProviderCall(func(context.Context, Settings, string, []string) (string, error) {
		return flat, nil
	})
	id := jobID(t, request(t, a, s, "POST", "/api/generate", motionInput(selfie, draft, receipt, "attack")))
	if j := waitJob(t, a, s, id); j.Status != "succeeded" || len(j.Gif) != 0 {
		t.Fatal(j)
	}
	w := request(t, a, s, "GET", "/api/works", nil)
	var list workList
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list.Items) != 1 || list.Items[0].HasGif {
		t.Fatal("work should be stored without gif:", w.Body.String())
	}
	if w = request(t, a, s, "GET", "/api/works/"+id+"/gif", nil); w.Code != 404 {
		t.Fatal("missing gif should 404:", w.Code)
	}
	if w = request(t, a, s, "GET", "/api/works/"+id+"/sheet", nil); w.Code != 200 || w.Header().Get("Content-Type") != "image/png" {
		t.Fatal("sheet fallback wrong:", w.Code, w.Header().Get("Content-Type"))
	}
}

// 云端作品集最多保留 3 天：超期副本由周期清理删除，保留期内的作品不受影响。
func TestWorksExpireAfterRetention(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "works-retention")
	uid := s.User.ID
	cfg, _ := a.settings()
	for _, item := range []struct {
		id string
		age time.Duration
	}{
		{"workold01", 4 * 24 * time.Hour},
		{"workfresh1", 0},
	} {
		if err := a.saveFile(item.id, "gif", []byte("GIF89a")); err != nil {
			t.Fatal(err)
		}
		if _, err := a.db.Exec("INSERT INTO jobs(id,user_id,request_id,digest,kind,status,gift_cost,paid_cost,created) VALUES(?,?,?,?,?,?,?,?,?)", item.id, uid, "req-"+item.id, "digest-"+item.id, "motion", "succeeded", 1, 0, time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
		a.saveWork(context.Background(), item.id, uid, cfg, Selection{Category: "male"})
		if item.age > 0 {
			// 模拟超过保留期（3天）的云端副本。
			a.db.Exec("UPDATE works SET created=? WHERE id=?", time.Now().Add(-item.age).Unix(), item.id)
		}
	}
	a.cleanup()
	var n int
	a.db.QueryRow("SELECT COUNT(*) FROM works WHERE user_id=?", uid).Scan(&n)
	if n != 1 {
		t.Fatal("expired works were not cleaned:", n)
	}
	var exists int
	a.db.QueryRow("SELECT COUNT(*) FROM works WHERE user_id=? AND id='workold01'", uid).Scan(&exists)
	if exists != 0 {
		t.Fatal("expired work should be evicted")
	}
	if w := request(t, a, s, "GET", "/api/works/workold01/gif", nil); w.Code != 404 {
		t.Fatal("expired work gif should be gone:", w.Code)
	}
}

func TestWorksKeepLatestPerUser(t *testing.T) {
	a := testApp(t)
	one := loginDevice(t, a, "works-cap")
	uid := one.User.ID
	cfg, _ := a.settings()
	// 连续保存超上限一张：只保留最新 30 张，最早的被淘汰。
	for i := 0; i < worksPerUser+1; i++ {
		id := fmt.Sprintf("work%02d", i)
		if err := a.saveFile(id, "gif", []byte("GIF89a")); err != nil {
			t.Fatal(err)
		}
		if _, err := a.db.Exec("INSERT INTO jobs(id,user_id,request_id,digest,kind,status,gift_cost,paid_cost,created) VALUES(?,?,?,?,?,?,?,?,?)", id, uid, "req-"+id, "digest-"+id, "motion", "succeeded", 1, 0, time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
		a.saveWork(context.Background(), id, uid, cfg, Selection{Category: "male"})
	}
	var n int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM works WHERE user_id=?", uid).Scan(&n); err != nil || n != worksPerUser {
		t.Fatal("works cap wrong:", n, err)
	}
	var newest string
	if err := a.db.QueryRow("SELECT id FROM works WHERE user_id=? ORDER BY created DESC,rowid DESC", uid).Scan(&newest); err != nil || newest != "work30" {
		t.Fatal("newest work wrong:", newest, err)
	}
	var exists int
	a.db.QueryRow("SELECT COUNT(*) FROM works WHERE user_id=? AND id='work00'", uid).Scan(&exists)
	if exists != 0 {
		t.Fatal("oldest work should be evicted")
	}
	// 云端按账号隔离：上限淘汰不会影响其他账号。
	two := loginDevice(t, a, "works-cap-other")
	selfie, draft, receipt := acceptedDraft(t, a, two)
	id := jobID(t, request(t, a, two, "POST", "/api/generate", motionInput(selfie, draft, receipt, "attack")))
	if j := waitJob(t, a, two, id); j.Status != "succeeded" {
		t.Fatal(j)
	}
	var other int
	a.db.QueryRow("SELECT COUNT(*) FROM works WHERE user_id=?", two.User.ID).Scan(&other)
	if other != 1 {
		t.Fatal("other account works wrong:", other)
	}
}

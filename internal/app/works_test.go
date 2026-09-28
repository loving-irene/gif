package app

// ?????????????????????????????????
// GIF ?????????????????????????????? 30 ??
// ????????????
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

// acceptedDraft ????????????????????????????????
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
	// ??????? GIF????????????????
	id := jobID(t, request(t, a, one, "POST", "/api/generate", motionInput(selfie, draft, receipt, "bike")))
	if j := waitJob(t, a, one, id); j.Status != "succeeded" || len(j.Gif) == 0 {
		t.Fatal(j)
	}
	w := request(t, a, one, "GET", "/api/works", nil)
	var list workList
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list.Items) != 1 {
		t.Fatal("works list wrong:", w.Body.String())
	}
	item := list.Items[0]
	wantName := ""
	for _, a := range defaults(Env{}).Categories[0].Actions {
		if a.ID == "bike" {
			wantName = a.Name
		}
	}
	if item.ID != id || !item.HasGif || !item.HasSheet || item.Name != wantName || item.Category != "male" || item.Action != "bike" {
		t.Fatal("work item wrong:", item)
	}
	// ???????????3??????????????????
	if list.RetentionSeconds != int64(worksRetention.Seconds()) {
		t.Fatal("retention window wrong:", list.RetentionSeconds)
	}
	// ????? GIF ????????
	w = request(t, a, one, "GET", "/api/works/"+id+"/gif", nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/gif" || !strings.HasPrefix(w.Body.String(), "GIF") {
		t.Fatal("work gif wrong:", w.Code, w.Header().Get("Content-Type"))
	}
	w = request(t, a, one, "GET", "/api/works/"+id+"/sheet", nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" {
		t.Fatal("work sheet wrong:", w.Code, w.Header().Get("Content-Type"))
	}
	// ????????????
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
	// ???????????? GIF ????????????????????????
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
	// ??????????????
	if w = request(t, a, one, "POST", "/api/works/remove", map[string]string{"id": "../bad"}); w.Code != 400 {
		t.Fatal("invalid work id should be rejected:", w.Code)
	}
	if w = request(t, a, nil, "GET", "/api/works", nil); w.Code != 401 {
		t.Fatal("works should require auth")
	}
}

// GIF ???????????????????????????????????????
func TestWorkStoresSheetWhenGIFMissing(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "works-sheet")
	selfie, draft, receipt := acceptedDraft(t, a, s)
	// ???????????????????GIF ????
	var buf bytes.Buffer
	png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 64, 32)))
	flat := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	a.providerCall = asProviderCall(func(context.Context, Settings, string, []string) (string, error) {
		return flat, nil
	})
	id := jobID(t, request(t, a, s, "POST", "/api/generate", motionInput(selfie, draft, receipt, "bike")))
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

// ????????? 3 ??????????????????????????
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
			// ????????3????????
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
	// ??????????????? 30 ?????????
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
	// ?????????????????????
	two := loginDevice(t, a, "works-cap-other")
	selfie, draft, receipt := acceptedDraft(t, a, two)
	id := jobID(t, request(t, a, two, "POST", "/api/generate", motionInput(selfie, draft, receipt, "bike")))
	if j := waitJob(t, a, two, id); j.Status != "succeeded" {
		t.Fatal(j)
	}
	var other int
	a.db.QueryRow("SELECT COUNT(*) FROM works WHERE user_id=?", two.User.ID).Scan(&other)
	if other != 1 {
		t.Fatal("other account works wrong:", other)
	}
}

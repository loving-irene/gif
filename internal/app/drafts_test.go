package app

// 云端“我的定稿”同步测试：定稿成功后写入云端、跨设备按账号读取、
// 每账号最多保留 30 张、越权与未登录访问被拒绝。
import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func TestDraftsSyncAcrossDevices(t *testing.T) {
	a := testApp(t)
	instantProvider(a)
	one := loginDevice(t, a, "drafts-one")
	two := loginDevice(t, a, "drafts-two")

	// 设备一生成定稿：成功后云端应出现该定稿。
	id := jobID(t, request(t, a, one, "POST", "/api/generate", draftInput()))
	if j := waitJob(t, a, one, id); j.Status != "succeeded" {
		t.Fatal(j)
	}
	w := request(t, a, one, "GET", "/api/drafts", nil)
	var list struct {
		Items []struct {
			Receipt  string    `json:"receipt"`
			Created  int64     `json:"created"`
			Selection Selection `json:"selection"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list.Items) != 1 {
		t.Fatal("drafts list wrong:", w.Body.String())
	}
	first := list.Items[0]
	if first.Receipt == "" || first.Selection.Clothes == "" {
		t.Fatal("draft item missing receipt/selection:", first)
	}
	// 本人可取回定稿图片。
	w = request(t, a, one, "GET", "/api/drafts/"+first.Receipt+"/image", nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" {
		t.Fatal("draft image wrong:", w.Code, w.Header().Get("Content-Type"))
	}
	// 其他账号看不到也取不到。
	if w = request(t, a, two, "GET", "/api/drafts", nil); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var other struct {
		Items []map[string]any `json:"items"`
	}
	json.Unmarshal(w.Body.Bytes(), &other)
	if len(other.Items) != 0 {
		t.Fatal("drafts leaked to another account")
	}
	if w = request(t, a, two, "GET", "/api/drafts/"+first.Receipt+"/image", nil); w.Code != 404 {
		t.Fatal("draft image should stay private:", w.Code)
	}
	// 未登录不可访问。
	if w = request(t, a, nil, "GET", "/api/drafts", nil); w.Code != 401 {
		t.Fatal("drafts should require auth")
	}
}

func TestDraftsKeepLatestPerUser(t *testing.T) {
	a := testApp(t)
	one := loginDevice(t, a, "drafts-cap")
	uid := one.User.ID
	image := []byte("png-bytes")
	// 连续保存超上限一张：只保留最新 30 张，最早的被淘汰。
	for i := 0; i < draftsPerUser+1; i++ {
		a.saveDraft(uid, fmt.Sprintf("receipt-%02d", i), Selection{Clothes: fmt.Sprintf("衣服%d", i)}, image, context.Background())
	}
	var n int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM drafts WHERE user_id=?", uid).Scan(&n); err != nil || n != draftsPerUser {
		t.Fatal("draft cap wrong:", n, err)
	}
	var oldest string
	if err := a.db.QueryRow("SELECT receipt FROM drafts WHERE user_id=? ORDER BY created DESC,rowid DESC", uid).Scan(&oldest); err != nil || oldest != "receipt-30" {
		t.Fatal("newest draft wrong:", oldest, err)
	}
	var exists int
	a.db.QueryRow("SELECT COUNT(*) FROM drafts WHERE user_id=? AND receipt='receipt-00'", uid).Scan(&exists)
	if exists != 0 {
		t.Fatal("oldest draft should be evicted")
	}
}

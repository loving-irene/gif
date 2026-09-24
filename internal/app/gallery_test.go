package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAdminGalleryListsExistingAssetsAndProtectsBlobs(t *testing.T) {
	a := testApp(t)
	user := loginDevice(t, a, "gallery-user")
	if w := request(t, a, user, "GET", "/api/admin/gallery", nil); w.Code != 403 {
		t.Fatalf("non-admin gallery status=%d", w.Code)
	}
	admin := codeAdmin(t, a, user)
	if _, err := a.db.Exec("UPDATE users SET name=? WHERE id=?", "图库用户", user.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec("INSERT INTO drafts(receipt,user_id,created,selection,image) VALUES(?,?,?,?,?)", "gallery-receipt", user.User.ID, 100, `{"category":"male","clothes":"铠甲","color":"银灰","style":"ink"}`, []byte("draft-image")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec("INSERT INTO works(id,user_id,created,name,category,action,gif,sheet) VALUES(?,?,?,?,?,?,?,?)", "gallery-work", user.User.ID, 101, "蓄力攻击", "male", "attack", []byte("GIF89a"), []byte("sheet-image")); err != nil {
		t.Fatal(err)
	}
	w := request(t, a, admin, "GET", "/api/admin/gallery?q=图库用户", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var got struct {
		Drafts      []map[string]any `json:"drafts"`
		Works       []map[string]any `json:"works"`
		DraftsTotal int              `json:"draftsTotal"`
		WorksTotal  int              `json:"worksTotal"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.DraftsTotal != 1 || got.WorksTotal != 1 || len(got.Drafts) != 1 || len(got.Works) != 1 {
		t.Fatalf("gallery list mismatch: %+v", got)
	}
	if got.Drafts[0]["imageURL"] != "/api/admin/gallery/drafts/gallery-receipt/image" || got.Works[0]["imageURL"] != "/api/admin/gallery/works/gallery-work/gif" {
		t.Fatalf("gallery urls mismatch: %+v", got)
	}
	if got.Works[0]["hasSheet"] != true {
		t.Fatalf("gallery work should keep motion sheet: %+v", got.Works[0])
	}
	if w = request(t, a, admin, "GET", "/api/admin/gallery/drafts/gallery-receipt/image", nil); w.Code != 200 || w.Body.String() != "draft-image" {
		t.Fatalf("draft blob mismatch: %d %q", w.Code, w.Body.String())
	}
	if w = request(t, a, admin, "GET", "/api/admin/gallery/works/gallery-work/gif", nil); w.Code != 200 || !strings.HasPrefix(w.Body.String(), "GIF") {
		t.Fatalf("work blob mismatch: %d %q", w.Code, w.Body.String())
	}
	if w = request(t, a, admin, "GET", "/api/admin/gallery/works/gallery-work/sheet", nil); w.Code != 200 || w.Body.String() != "sheet-image" {
		t.Fatalf("work sheet mismatch: %d %q", w.Code, w.Body.String())
	}
}

package app

import (
	"encoding/json"
	"net/http"
	"strings"
)

// adminGallery 返回当前数据库仍保留的定稿与作品元数据。图片本体通过下面的
// 独立接口按需读取，避免把大量二进制内容编码进列表响应。
func (a *App) adminGallery(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > 254 {
		fail(w, 400, "搜索条件过长")
		return
	}
	page := pageParam(r)
	like := "%" + q + "%"

	var draftsTotal int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM drafts d LEFT JOIN users u ON u.id=d.user_id
		WHERE d.receipt LIKE ? OR d.user_id LIKE ? OR COALESCE(u.name,'') LIKE ?`, like, like, like).Scan(&draftsTotal); err != nil {
		fail(w, 500, "读取失败")
		return
	}
	draftRows, err := a.db.Query(`SELECT d.receipt,d.user_id,COALESCE(u.name,''),d.created,d.selection
		FROM drafts d LEFT JOIN users u ON u.id=d.user_id
		WHERE d.receipt LIKE ? OR d.user_id LIKE ? OR COALESCE(u.name,'') LIKE ?
		ORDER BY d.created DESC,d.rowid DESC LIMIT ? OFFSET ?`, like, like, like, adminPageSize, (page-1)*adminPageSize)
	if err != nil {
		fail(w, 500, "读取失败")
		return
	}
	drafts := []map[string]any{}
	for draftRows.Next() {
		var receipt, uid, userName, selectionJSON string
		var created int64
		if draftRows.Scan(&receipt, &uid, &userName, &created, &selectionJSON) != nil {
			continue
		}
		selection := Selection{}
		_ = json.Unmarshal([]byte(selectionJSON), &selection)
		drafts = append(drafts, map[string]any{
			"id": receipt, "user": uid, "userName": userName, "created": created,
			"selection": selection,
			"imageURL":  "/api/admin/gallery/drafts/" + receipt + "/image",
		})
	}
	if err := draftRows.Err(); err != nil {
		fail(w, 500, "读取失败")
		return
	}
	if err := draftRows.Close(); err != nil {
		fail(w, 500, "读取失败")
		return
	}

	var worksTotal int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM works w LEFT JOIN users u ON u.id=w.user_id
		WHERE w.id LIKE ? OR w.user_id LIKE ? OR COALESCE(u.name,'') LIKE ? OR w.name LIKE ? OR w.category LIKE ? OR w.action LIKE ?`, like, like, like, like, like, like).Scan(&worksTotal); err != nil {
		fail(w, 500, "读取失败")
		return
	}
	workRows, err := a.db.Query(`SELECT w.id,w.user_id,COALESCE(u.name,''),w.created,w.name,w.category,w.action,
		w.gif IS NOT NULL,w.sheet IS NOT NULL
		FROM works w LEFT JOIN users u ON u.id=w.user_id
		WHERE w.id LIKE ? OR w.user_id LIKE ? OR COALESCE(u.name,'') LIKE ? OR w.name LIKE ? OR w.category LIKE ? OR w.action LIKE ?
		ORDER BY w.created DESC,w.rowid DESC LIMIT ? OFFSET ?`, like, like, like, like, like, like, adminPageSize, (page-1)*adminPageSize)
	if err != nil {
		fail(w, 500, "读取失败")
		return
	}
	defer workRows.Close()
	works := []map[string]any{}
	for workRows.Next() {
		var id, uid, userName, name, category, action string
		var created int64
		var hasGIF, hasSheet bool
		if workRows.Scan(&id, &uid, &userName, &created, &name, &category, &action, &hasGIF, &hasSheet) != nil {
			continue
		}
		item := map[string]any{
			"id": id, "user": uid, "userName": userName, "created": created,
			"name": name, "category": category, "action": action,
			"hasGif": hasGIF, "hasSheet": hasSheet,
		}
		if hasGIF {
			item["imageURL"] = "/api/admin/gallery/works/" + id + "/gif"
		} else if hasSheet {
			item["imageURL"] = "/api/admin/gallery/works/" + id + "/sheet"
		}
		works = append(works, item)
	}
	if err := workRows.Err(); err != nil {
		fail(w, 500, "读取失败")
		return
	}
	respond(w, 200, map[string]any{
		"drafts": drafts, "draftsTotal": draftsTotal,
		"works": works, "worksTotal": worksTotal,
		"page": page, "pageSize": adminPageSize,
	})
}

func (a *App) adminDraftImage(w http.ResponseWriter, r *http.Request) {
	var image []byte
	if a.db.QueryRow("SELECT image FROM drafts WHERE receipt=?", r.PathValue("receipt")).Scan(&image) != nil || image == nil {
		fail(w, 404, "定稿不存在")
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(image))
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write(image)
}

func (a *App) adminWorkBlob(w http.ResponseWriter, r *http.Request) {
	column := r.PathValue("blob")
	if column != "gif" && column != "sheet" {
		http.NotFound(w, r)
		return
	}
	var image []byte
	if a.db.QueryRow("SELECT "+column+" FROM works WHERE id=?", r.PathValue("id")).Scan(&image) != nil || image == nil {
		fail(w, 404, "作品不存在")
		return
	}
	if column == "gif" {
		w.Header().Set("Content-Type", "image/gif")
	} else {
		w.Header().Set("Content-Type", http.DetectContentType(image))
	}
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write(image)
}

package app

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// 社区分享池与作品集是两套存储：
//   - 作品集（works）是账号私有的跨设备同步副本，最多保留 worksRetention（3天）；
//   - 社区池（community_shares）是公开浏览的分享副本，GIF 本体在分享时复制进来，永不清理，
//     只由分享人主动取消；作品集到期、被其他设备删除都不影响已经分享出去的条目。
//
// 同一账号同一张作品只保留一条（UNIQUE(sharer,work_id)）：重复分享刷新时间与内容，
// 不产生重复条目，也不会把同一张图刷满整个社区；取消后可以再次分享。
const (
	// shareImageLimit 是单张分享图的解码后大小上限；GIF 动图通常 100KB–2MB。
	shareImageLimit = 4 << 20
	// communityPageSize 是社区列表单页条数，移动端一屏约 3–4 张，够滑两次。
	communityPageSize = 30
	// communityPerUser 是单账号在社区池里的分享条数上限，超出按时间淘汰最旧的。
	communityPerUser = 60
	// communitySharePerHour 限制单账号每小时的分享/刷新次数，防止刷池。
	communitySharePerHour = 60
)

// shareItem 是社区列表与分享结果对外返回的一条分享。
type shareItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Action   string `json:"action"`
	Category string `json:"category"`
	Sharer   string `json:"sharerName"`
	Created  int64  `json:"created"`
	Mine     bool   `json:"mine"`
}

const (
	communityPageTitle       = "社区分享：大家的自拍动图｜拾光 GIF"
	communityPageDescription = "拾光 GIF 社区分享池，浏览用户主动分享出来的自拍动图与专属角色表情包，喜欢即可保存。"
)

var communityTemplate = template.Must(template.ParseFS(web, "web/community.html"))

// communityPage 渲染社区页外壳。列表由页面脚本按 /api/community 分页拉取，
// 这样取消分享、加载更多都不需要整页刷新。
func (a *App) communityPage(w http.ResponseWriter, r *http.Request) {
	var body bytes.Buffer
	err := communityTemplate.Execute(&body, struct{ Title, Description, Canonical string }{
		communityPageTitle, communityPageDescription, a.env.BaseURL + "/community",
	})
	if err != nil {
		fail(w, 500, "页面暂时不可用")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(body.Bytes())
}

// communityList 返回社区分享池：按分享时间倒序的键集分页。
// 需要登录才能浏览：一方面要按登录账号标记“我的分享”，另一方面便于对滥用行为限流。
func (a *App) communityList(w http.ResponseWriter, r *http.Request) {
	uid := current(r).User.ID
	cursor, ok := parseShareCursor(r.URL.Query().Get("cursor"))
	if !ok {
		fail(w, 400, "翻页参数无效")
		return
	}
	// rowid 必须限定 c：两张表都有 rowid，不限定会被解析到 users 上，分页游标随之出错。
	query := "SELECT c.rowid,c.id,c.created,c.name,COALESCE(c.action,''),COALESCE(c.category,''),COALESCE(u.name,''),c.sharer IS ? FROM community_shares c JOIN users u ON u.id=c.sharer"
	args := []any{uid}
	if cursor != nil {
		query += " WHERE c.created<? OR (c.created=? AND c.rowid<?)"
		args = append(args, cursor.created, cursor.created, cursor.rowID)
	}
	query += " ORDER BY c.created DESC,c.rowid DESC LIMIT ?"
	args = append(args, communityPageSize+1)
	rows, err := a.db.Query(query, args...)
	if err != nil {
		a.debug(r.Context(), "community_list_error", map[string]any{"error": errorText(err)})
		fail(w, 500, "社区内容读取失败")
		return
	}
	defer rows.Close()
	// 多取一条用于判断是否还有下一页：只有取满 communityPageSize 条后仍多出记录时才给游标。
	items := []shareItem{}
	type cursorRow struct{ rowID int64 }
	cursors := []cursorRow{}
	for rows.Next() {
		var it shareItem
		var rowID int64
		if rows.Scan(&rowID, &it.ID, &it.Created, &it.Name, &it.Action, &it.Category, &it.Sharer, &it.Mine) != nil {
			continue
		}
		items = append(items, it)
		cursors = append(cursors, cursorRow{rowID: rowID})
	}
	next := ""
	if len(items) > communityPageSize {
		items = items[:communityPageSize]
		last := cursors[communityPageSize-1]
		next = encodeShareCursor(items[communityPageSize-1].Created, last.rowID)
	}
	respond(w, 200, map[string]any{"items": items, "nextCursor": next, "viewerId": uid})
}

// communityShare 把作品分享到社区池。同一账号同一张作品重复分享只刷新已有条目。
// GIF 本体优先取服务端作品副本（省一次上传）；本机独有的作品由页面把 blob 传上来——
// 作品集是 3 天保留的，本机作品没有被保存过时服务端没有副本，所以必须支持上传。
// 页面总是把本机 GIF 一并传上来：服务端副本只保留 3 天，过期后客户端无从判断
// 「服务端还有没有这张图」，只靠作品副本判断会让老作品必然分享失败。
func (a *App) communityShare(w http.ResponseWriter, r *http.Request) {
	s := current(r)
	var in struct {
		ID     string `json:"id"`
		Action string `json:"action"`
		Image  string `json:"image"`
	}
	// 上限按「4MB 图片 base64 后约 5.3MB」留余量；超限时 decode 会失败，因此下面单独提示。
	if decode(w, r, &in, shareImageLimit*2) != nil {
		fail(w, 413, "这张作品太大，暂时无法分享，请换一张")
		return
	}
	if in.ID == "" && in.Image == "" {
		fail(w, 400, "缺少要分享的作品")
		return
	}
	if in.ID != "" && !idPattern.MatchString(in.ID) {
		fail(w, 400, "作品信息无效")
		return
	}
	if !a.limit("share:"+s.User.ID, communitySharePerHour, time.Hour) {
		fail(w, 429, "分享过于频繁，请稍后再试")
		return
	}
	name, action, category := "", strings.TrimSpace(in.Action), ""
	var blob []byte
	if in.ID != "" {
		// 只读取本人作品：编号属于他人时按“没有服务端副本”处理，不泄露他人作品是否存在。
		var gif, sheet []byte
		a.db.QueryRow("SELECT name,COALESCE(action,''),COALESCE(category,''),gif,sheet FROM works WHERE id=? AND user_id=?", in.ID, s.User.ID).Scan(&name, &action, &category, &gif, &sheet)
		blob = gif
		if len(blob) == 0 {
			// 动作原图只在 GIF 合成失败时保存在云端，本机可以先合成再分享。
			blob = sheet
		}
	}
	if len(blob) == 0 {
		raw, err := base64.StdEncoding.DecodeString(in.Image)
		if err != nil || len(raw) == 0 {
			fail(w, 400, "这张作品在本机还没合成为动图，请先点「重新合成 · 免费」再分享")
			return
		}
		blob = raw
	}
	if len(blob) > shareImageLimit {
		fail(w, 413, "这张作品太大，暂时无法分享")
		return
	}
	if !animatedImage(blob) {
		fail(w, 415, "只支持分享会动的 GIF 作品")
		return
	}
	if action == "" {
		action = "作品"
	}
	name = shareName(name, action)
	category = a.categoryLabel(category)
	now := time.Now().Unix()
	shareID := token(16)
	// 重复分享：刷新时间与内容，条目编号保持不变，社区页上原来的位置会顶到最前。
	if _, err := a.db.Exec(`INSERT INTO community_shares(id,sharer,work_id,name,image,action,category,created,updated) VALUES(?,?,?,?,?,?,?,?,?)
 ON CONFLICT(sharer,work_id) DO UPDATE SET name=excluded.name,image=excluded.image,action=excluded.action,category=excluded.category,created=excluded.created,updated=excluded.updated`,
		shareID, s.User.ID, in.ID, name, blob, action, category, now, now); err != nil {
		fail(w, 500, "分享失败，请稍后重试")
		return
	}
	var realID string
	var created int64
	if err := a.db.QueryRow("SELECT id,created FROM community_shares WHERE sharer=? AND work_id=?", s.User.ID, in.ID).Scan(&realID, &created); err != nil {
		fail(w, 500, "分享失败，请稍后重试")
		return
	}
	a.db.Exec("DELETE FROM community_shares WHERE sharer=? AND id NOT IN (SELECT id FROM community_shares WHERE sharer=? ORDER BY created DESC,rowid DESC LIMIT ?)", s.User.ID, s.User.ID, communityPerUser)
	a.audit(s.User.ID, "community_share", in.ID)
	respond(w, 200, shareItem{ID: realID, Name: name, Action: action, Category: category, Sharer: s.User.Name, Created: created, Mine: true})
}

// communityStates 返回当前账号已分享的作品编号与对应分享条目编号，供作品集卡片显示“取消分享”。
func (a *App) communityStates(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query("SELECT work_id,id FROM community_shares WHERE sharer=?", current(r).User.ID)
	if err != nil {
		fail(w, 500, "分享状态读取失败")
		return
	}
	defer rows.Close()
	states := map[string]string{}
	for rows.Next() {
		var workID, shareID string
		if rows.Scan(&workID, &shareID) == nil && workID != "" {
			states[workID] = shareID
		}
	}
	respond(w, 200, map[string]any{"shares": states})
}

// communityUnshare 取消自己的分享：作品集卡片用 workId，社区页用 shareId。
// 只有分享人本人能取消；对不存在的条目同样返回成功，避免探测社区里有哪些条目。
func (a *App) communityUnshare(w http.ResponseWriter, r *http.Request) {
	s := current(r)
	var in struct {
		ShareID string `json:"shareId"`
		WorkID  string `json:"workId"`
	}
	if decode(w, r, &in, 1024) != nil {
		fail(w, 400, "分享信息无效")
		return
	}
	if in.ShareID == "" && in.WorkID == "" {
		fail(w, 400, "分享信息无效")
		return
	}
	if (in.ShareID != "" && !idPattern.MatchString(in.ShareID)) || (in.WorkID != "" && !idPattern.MatchString(in.WorkID)) {
		fail(w, 400, "分享信息无效")
		return
	}
	if in.ShareID != "" {
		a.db.Exec("DELETE FROM community_shares WHERE id=? AND sharer=?", in.ShareID, s.User.ID)
	} else {
		a.db.Exec("DELETE FROM community_shares WHERE work_id=? AND sharer=?", in.WorkID, s.User.ID)
	}
	a.audit(s.User.ID, "community_unshare", in.ShareID+in.WorkID)
	respond(w, 200, map[string]bool{"ok": true})
}

// communityImage 返回社区里一张分享的动图本体。图片只在同源页面内通过 <img> 引用，
// 直接按请求路径鉴权即可，不需要额外的图片凭证。
func (a *App) communityImage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var blob []byte
	if !idPattern.MatchString(id) || a.db.QueryRow("SELECT image FROM community_shares WHERE id=?", id).Scan(&blob) != nil || len(blob) == 0 {
		fail(w, 404, "分享不存在")
		return
	}
	w.Header().Set("Content-Type", imageContentType(blob))
	// 分享可能随时被取消，缓存时间保持很短，避免取消后仍被长期展示。
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Write(blob)
}

// categoryLabel 把人物分类编号换成社区页可直接展示的名称（如 female → 女生）；
// 配置读取失败或分类已下线时原样返回，社区展示不至于因此失败。
func (a *App) categoryLabel(id string) string {
	if id == "" {
		return ""
	}
	cfg, err := a.settings()
	if err != nil {
		return id
	}
	for _, c := range cfg.Categories {
		if c.ID == id {
			return c.Name
		}
	}
	return id
}

// shareName 给社区卡片准备一个短名称：优先沿用作品名（动作名），为空时回落到动作描述。
func shareName(name, action string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = action
	}
	if runes := []rune(name); len(runes) > 20 {
		name = string(runes[:20])
	}
	return name
}

// animatedImage 只接受 GIF，动图池里混入静态图会失去“会动”的意义。
func animatedImage(blob []byte) bool { return imageContentType(blob) == "image/gif" }

func imageContentType(blob []byte) string {
	switch {
	case len(blob) >= 6 && (bytes.HasPrefix(blob, []byte("GIF87a")) || bytes.HasPrefix(blob, []byte("GIF89a"))):
		return "image/gif"
	case len(blob) >= 12 && bytes.HasPrefix(blob, []byte("RIFF")) && bytes.Equal(blob[8:12], []byte("WEBP")):
		return "image/webp"
	}
	return ""
}

// shareCursor 是社区列表的键集分页游标：(created, rowid)。
type shareCursor struct {
	created int64
	rowID   int64
}

func encodeShareCursor(created, rowID int64) string {
	return fmt.Sprintf("%d.%d", created, rowID)
}

func parseShareCursor(raw string) (*shareCursor, bool) {
	if raw == "" {
		return nil, true
	}
	if len(raw) > 40 {
		return nil, false
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return nil, false
	}
	created, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || created <= 0 {
		return nil, false
	}
	rowID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || rowID <= 0 {
		return nil, false
	}
	return &shareCursor{created: created, rowID: rowID}, true
}

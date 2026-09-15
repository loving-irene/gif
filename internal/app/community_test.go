package app

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// gifFixture 用真实的合成结果做样本：社区池只收 GIF，测试也用真的 GIF 而不是伪造字节。
func gifFixture(t *testing.T) []byte {
	t.Helper()
	sheet, err := base64.StdEncoding.DecodeString(strings.SplitN(sampleImage(true), ",", 2)[1])
	if err != nil {
		t.Fatal(err)
	}
	gif, err := synthesizeGIF(sheet, motionSpecOf("4x4"))
	if err != nil {
		t.Fatal(err)
	}
	return gif
}

type communityPageResult struct {
	Items []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Action   string `json:"action"`
		Category string `json:"category"`
		Sharer   string `json:"sharerName"`
		Created  int64  `json:"created"`
		Mine     bool   `json:"mine"`
	} `json:"items"`
	NextCursor string `json:"nextCursor"`
	ViewerID   string `json:"viewerId"`
}

func readCommunity(t *testing.T, a *App, s *testSession, cursor string) communityPageResult {
	t.Helper()
	path := "/api/community"
	if cursor != "" {
		path += "?cursor=" + cursor
	}
	w := request(t, a, s, "GET", path, nil)
	if w.Code != 200 {
		t.Fatalf("community list: %d %s", w.Code, w.Body.String())
	}
	var out communityPageResult
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func shareStates(t *testing.T, a *App, s *testSession) map[string]string {
	t.Helper()
	w := request(t, a, s, "GET", "/api/community/states", nil)
	if w.Code != 200 {
		t.Fatalf("community states: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Shares map[string]string `json:"shares"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Shares
}

// 分享 → 社区可见 → 重复分享去重只刷新时间 → 取消后消失，再分享又能回来。
func TestCommunityShareDedupeAndUnshare(t *testing.T) {
	a := testApp(t)
	one := loginDevice(t, a, "share-one")
	two := loginDevice(t, a, "share-two")
	gif := gifFixture(t)
	a.db.Exec("INSERT INTO works(id,user_id,created,name,category,action,gif) VALUES('work0001',?,1,'挥手问好','male','wave',?)", one.User.ID, gif)

	if states := shareStates(t, a, one); len(states) != 0 {
		t.Fatal("account should start with no shares", states)
	}
	if page := readCommunity(t, a, one, ""); len(page.Items) != 0 {
		t.Fatal("empty pool should list nothing", page.Items)
	}
	if w := request(t, a, one, "POST", "/api/community/share", map[string]string{"id": "work0001"}); w.Code != 200 {
		t.Fatal("share failed", w.Code, w.Body.String())
	}
	shareID := shareStates(t, a, one)["work0001"]
	if shareID == "" {
		t.Fatal("work not marked as shared")
	}

	page := readCommunity(t, a, two, "")
	if len(page.Items) != 1 || page.Items[0].Name != "挥手问好" || page.Items[0].Action != "wave" || page.Items[0].Category != "男生" {
		t.Fatal("share lost its name/action/category", page.Items)
	}
	if page.Items[0].Sharer != one.User.Name || page.Items[0].Mine {
		t.Fatal("other account should see the share as not mine", page.Items)
	}
	if page.ViewerID != two.User.ID {
		t.Fatal("viewer id missing", page)
	}
	if mine := readCommunity(t, a, one, "").Items; len(mine) != 1 || !mine[0].Mine {
		t.Fatal("sharer should see own share as mine", mine)
	}

	// 图片本体可直接取用，并且是 GIF。
	image := request(t, a, two, "GET", "/api/community/"+shareID+"/gif", nil)
	if image.Code != 200 || image.Header().Get("Content-Type") != "image/gif" || image.Body.Len() != len(gif) {
		t.Fatal("community image wrong", image.Code, image.Header().Get("Content-Type"), image.Body.Len())
	}
	if w := request(t, a, two, "GET", "/api/community/"+strings.Repeat("f", 64)+"/gif", nil); w.Code != 404 {
		t.Fatal("unknown share should 404", w.Code)
	}

	// 重复分享：仍然是同一条（编号不变、条数不变），只刷新时间。
	again := request(t, a, one, "POST", "/api/community/share", map[string]string{"id": "work0001"})
	if again.Code != 200 {
		t.Fatal("re-share failed", again.Code, again.Body.String())
	}
	var item map[string]any
	json.Unmarshal(again.Body.Bytes(), &item)
	if item["id"] != shareID {
		t.Fatal("re-share created a new entry", item)
	}
	if page = readCommunity(t, a, one, ""); len(page.Items) != 1 {
		t.Fatal("re-share duplicated the entry", page.Items)
	}

	// 别人的分享取消不掉；本人取消后池子里不再有它。
	if w := request(t, a, two, "POST", "/api/community/unshare", map[string]string{"workId": "work0001"}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if page = readCommunity(t, a, one, ""); len(page.Items) != 1 {
		t.Fatal("other account removed someone else's share", page.Items)
	}
	if w := request(t, a, one, "POST", "/api/community/unshare", map[string]string{"workId": "work0001"}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if page = readCommunity(t, a, one, ""); len(page.Items) != 0 {
		t.Fatal("unshare kept the entry", page.Items)
	}
	if states := shareStates(t, a, one); len(states) != 0 {
		t.Fatal("unshare did not clear state", states)
	}
	// 取消后可以再次分享（作品集里的作品仍在）。
	if w := request(t, a, one, "POST", "/api/community/share", map[string]string{"id": "work0001"}); w.Code != 200 {
		t.Fatal("re-share after unshare failed", w.Code, w.Body.String())
	}
	if page = readCommunity(t, a, one, ""); len(page.Items) != 1 {
		t.Fatal("re-share after unshare missing", page.Items)
	}
}

// 本机独有的作品由页面上传 GIF：服务端没有作品副本时接受上传内容，但只收 GIF。
func TestCommunityShareUploadsOwnGifOnly(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "share-upload")
	gif := base64.StdEncoding.EncodeToString(gifFixture(t))
	w := request(t, a, s, "POST", "/api/community/share", map[string]any{"id": "local01", "action": "可爱点头", "image": gif})
	if w.Code != 200 {
		t.Fatal("upload share failed", w.Code, w.Body.String())
	}
	page := readCommunity(t, a, s, "")
	if len(page.Items) != 1 || page.Items[0].Name != "可爱点头" || page.Items[0].Action != "可爱点头" {
		t.Fatal("uploaded share not listed with its action name", page.Items)
	}
	// 原图（静态 PNG）与坏数据都不收：动图池里混静态图会失去意义。
	png := strings.SplitN(sampleImage(false), ",", 2)[1]
	for name, body := range map[string]map[string]any{
		"静态图":  {"id": "local02", "image": png},
		"坏数据":  {"id": "local03", "image": "not-base64!!"},
		"空内容":  {"id": "local04"},
		"非法编号": {"id": "../bad", "image": gif},
	} {
		if w := request(t, a, s, "POST", "/api/community/share", body); w.Code == 200 {
			t.Fatal("invalid share accepted", name)
		}
	}
	if page := readCommunity(t, a, s, ""); len(page.Items) != 1 {
		t.Fatal("invalid shares leaked into the pool", page.Items)
	}
}

// 服务端没有作品副本时（作品是 3 天前生成的，云端副本早已到期清理），页面必须能只靠
// 本机 GIF 完成分享：这正是「点击分享 → 分享内容无效」的原因——客户端以为服务端有副本、
// 没带图上来，服务端只能回错。这里钉住「带 id 又带 image」这条路必须成功。
func TestCommunityShareWithoutServerWorkCopy(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "share-nocopy")
	gif := gifFixture(t)
	// works 表里没有这条编号：模拟过了 3 天保留期、云端副本已被清理的老作品。
	w := request(t, a, s, "POST", "/api/community/share", map[string]any{
		"id": "expired0001", "action": "可爱点头", "image": base64.StdEncoding.EncodeToString(gif),
	})
	if w.Code != 200 {
		t.Fatal("老作品应能靠本机 GIF 分享成功", w.Code, w.Body.String())
	}
	page := readCommunity(t, a, s, "")
	if len(page.Items) != 1 || page.Items[0].Action != "可爱点头" {
		t.Fatal("分享未进入社区池", page.Items)
	}
	image := request(t, a, s, "GET", "/api/community/"+page.Items[0].ID+"/gif", nil)
	if image.Code != 200 || image.Body.Len() != len(gif) {
		t.Fatal("社区里的动图与服务端副本不一致", image.Code, image.Body.Len(), len(gif))
	}
	// 请求体超限要明确说「太大」，不能落回笼统的「分享内容无效」。
	oversize := request(t, a, s, "POST", "/api/community/share", map[string]any{
		"id": "expired0002", "image": strings.Repeat("A", shareImageLimit*2+1024),
	})
	if oversize.Code != 413 {
		t.Fatal("超限请求应提示体积过大", oversize.Code, oversize.Body.String())
	}
}

// 社区列表按分享时间倒序键集分页，最后一页没有游标；非法游标被拒。
func TestCommunityListPagination(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "share-page")
	gif := gifFixture(t)
	for i := 0; i < communityPageSize+5; i++ {
		id := "page" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		// 时间递减：i 越大越旧，列表顺序完全确定。
		a.db.Exec("INSERT INTO community_shares(id,sharer,work_id,name,image,action,category,created,updated) VALUES(?,?,?,?,?,?,?,?,?)", id, s.User.ID, "work"+id, "动作", gif, "动作", "male", int64(10000-i), int64(10000-i))
	}
	first := readCommunity(t, a, s, "")
	if len(first.Items) != communityPageSize || first.NextCursor == "" {
		t.Fatal("first page wrong", len(first.Items), first.NextCursor)
	}
	second := readCommunity(t, a, s, first.NextCursor)
	if len(second.Items) != 5 || second.NextCursor != "" {
		t.Fatal("second page wrong", len(second.Items), second.NextCursor)
	}
	seen := map[string]bool{}
	for _, it := range append(first.Items, second.Items...) {
		if seen[it.ID] {
			t.Fatal("pagination returned the same share twice", it.ID)
		}
		seen[it.ID] = true
	}
	for i := 1; i < len(first.Items); i++ {
		if first.Items[i-1].Created < first.Items[i].Created {
			t.Fatal("list is not newest first")
		}
	}
	for name, cursor := range map[string]string{"乱码": "abc", "越界": "1.2.3", "负数": "-5.1", "超长": strings.Repeat("9", 64)} {
		if w := request(t, a, s, "GET", "/api/community?cursor="+cursor, nil); w.Code != 400 {
			t.Fatal("bad cursor accepted", name, w.Code)
		}
	}
}

// 社区接口都需要登录，未登录既不返回内容也不接受分享。
func TestCommunityRequiresAccount(t *testing.T) {
	a := testApp(t)
	for _, path := range []string{"/api/community", "/api/community/states"} {
		if w := request(t, a, nil, "GET", path, nil); w.Code != 401 {
			t.Fatal("community list should require auth", path, w.Code)
		}
	}
	if w := request(t, a, nil, "POST", "/api/community/share", map[string]string{"id": "work"}); w.Code != 401 {
		t.Fatal("share should require auth", w.Code)
	}
	if w := request(t, a, nil, "POST", "/api/community/unshare", map[string]string{"workId": "work"}); w.Code != 401 {
		t.Fatal("unshare should require auth", w.Code)
	}
}

// 账号合并后分享跟随合并后的账号：两个账号的分享都保留、都显示为“我的分享”，并都能取消。
func TestCommunitySharesFollowAccountMerge(t *testing.T) {
	a := testApp(t)
	device := loginDevice(t, a, "share-merge")
	gif := gifFixture(t)
	cfg, _ := a.settings()
	cfg.MailHost, cfg.MailPort, cfg.MailUser, cfg.MailFrom = aliyunMailHost, aliyunMailPort, "studio@example.com", "studio@example.com"
	raw, _ := json.Marshal(cfg)
	a.db.Exec("UPDATE settings SET value=? WHERE key='config'", string(raw))
	a.setSecret(aliyunMailPasswordKey, "test-only-mail-password")
	// 已绑定邮箱的设备账号先把作品分享出去。邮箱归属由 users.email 决定（验证码只是凭据），
	// 因此这里直接把邮箱写到该账号上，再由另一台设备走完整的“发送验证码 → 验证”触发合并。
	if _, err := a.db.Exec("UPDATE users SET email=? WHERE id=?", "merger@example.com", device.User.ID); err != nil {
		t.Fatal(err)
	}
	a.db.Exec("INSERT INTO works(id,user_id,created,name,category,action,gif) VALUES('mergework',?,1,'抱熊欢呼','child','celebrate',?)", device.User.ID, gif)
	if w := request(t, a, device, "POST", "/api/community/share", map[string]string{"id": "mergework"}); w.Code != 200 {
		t.Fatal("share failed", w.Code, w.Body.String())
	}
	before := readCommunity(t, a, device, "")
	if len(before.Items) != 1 || !before.Items[0].Mine {
		t.Fatal("share missing before merge", before.Items)
	}
	// 另一台设备注册的新账号绑定同一个邮箱：新账号并入已绑定邮箱的设备账号。
	other := loginDevice(t, a, "share-merge-other")
	a.db.Exec("INSERT INTO works(id,user_id,created,name,category,action,gif) VALUES('otherwork',?,1,'开心鼓掌','female','clap',?)", other.User.ID, gif)
	if w := request(t, a, other, "POST", "/api/community/share", map[string]string{"id": "otherwork"}); w.Code != 200 {
		t.Fatal("share from merging account failed", w.Code, w.Body.String())
	}
	var sent string
	a.mailSend = func(_ Settings, _ string, c string) error { sent = c; return nil }
	if w := request(t, a, other, "POST", "/api/email/send", map[string]string{"email": "merger@example.com"}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var verify map[string]any
	w := request(t, a, other, "POST", "/api/email/verify", map[string]string{"email": "merger@example.com", "code": sent})
	if w.Code != 200 {
		t.Fatal("merge failed", w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &verify)
	merged := verify["user"].(map[string]any)["id"].(string)
	if merged != device.User.ID || verify["mergedFrom"] != other.User.ID {
		t.Fatal("unexpected merge direction", device.User.ID, other.User.ID, verify)
	}
	// 两个账号的分享都并到合并后的账号名下，每个作品编号只留下一条：
	// 既不会丢分享，也不会因 (sharer,work_id) 冲突报错。
	for _, workID := range []string{"mergework", "otherwork"} {
		var count int
		var sharer string
		if err := a.db.QueryRow("SELECT COUNT(*),COALESCE(MAX(sharer),'') FROM community_shares WHERE work_id=?", workID).Scan(&count, &sharer); err != nil || count != 1 || sharer != merged {
			t.Fatal("share did not follow the merged account", workID, count, sharer, err)
		}
	}
	page := readCommunity(t, a, device, "")
	if len(page.Items) != 2 {
		t.Fatal("merged account should own both shares", page.Items)
	}
	for _, item := range page.Items {
		if !item.Mine {
			t.Fatal("merged account should still own the share", item)
		}
		if w := request(t, a, device, "POST", "/api/community/unshare", map[string]string{"shareId": item.ID}); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if page = readCommunity(t, a, device, ""); len(page.Items) != 0 {
		t.Fatal("merged account could not cancel its shares", page.Items)
	}
	var leftover int
	a.db.QueryRow("SELECT COUNT(*) FROM community_shares WHERE sharer=?", other.User.ID).Scan(&leftover)
	if leftover != 0 {
		t.Fatal("merged-away account kept shares", leftover)
	}
}

// 作品集到期清理只删私有副本，社区池里的分享不受影响（社区没有过期时间）。
func TestCommunitySharesOutliveWorksRetention(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "share-retention")
	gif := gifFixture(t)
	a.db.Exec("INSERT INTO works(id,user_id,created,name,category,action,gif) VALUES('oldwork',?,1,'比心','female','heart',?)", s.User.ID, gif)
	if w := request(t, a, s, "POST", "/api/community/share", map[string]string{"id": "oldwork"}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	a.cleanup()
	var works int
	a.db.QueryRow("SELECT COUNT(*) FROM works WHERE user_id=?", s.User.ID).Scan(&works)
	if works != 0 {
		t.Fatal("expired work was not cleaned", works)
	}
	if page := readCommunity(t, a, s, ""); len(page.Items) != 1 {
		t.Fatal("community share should never expire with the work", page.Items)
	}
}

// 社区页是独立页面：内容由脚本拉取，页面本身带 noindex 且不进 sitemap。
func TestCommunityPageRendering(t *testing.T) {
	a := testApp(t)
	w := request(t, a, nil, "GET", "/community", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `id="communityGrid"`) {
		t.Fatal("community page missing", w.Code)
	}
	for _, asset := range []string{"/assets/community.v1.js", "/assets/style.v24.css"} {
		if !strings.Contains(w.Body.String(), asset) {
			t.Fatal("community page missing asset", asset)
		}
		if _, err := web.ReadFile("web/" + strings.TrimPrefix(asset, "/assets/")); err != nil {
			t.Fatal("community asset not embedded", asset, err)
		}
	}
	if w.Header().Get("X-Robots-Tag") != "noindex, follow" {
		t.Fatal("community page should not be indexed", w.Header().Get("X-Robots-Tag"))
	}
	if sitemap := request(t, a, nil, "GET", "/sitemap.xml", nil).Body.String(); strings.Contains(sitemap, "/community") {
		t.Fatal("community page should stay out of the sitemap")
	}
	// 内联脚本会被页面 CSP 拦下，因此社区页同样只能外链脚本。
	if strings.Contains(w.Body.String(), "<script>") {
		t.Fatal("community page must not rely on inline scripts")
	}
}

// 首页把作品集的“原图”换成“分享”，并给出社区入口。
func TestHomepageLinksToCommunity(t *testing.T) {
	a := testApp(t)
	body := request(t, a, nil, "GET", "/", nil).Body.String()
	if !strings.Contains(body, `href="/community"`) || !strings.Contains(body, `class="community-link"`) {
		t.Fatal("homepage missing community entry")
	}
	for _, asset := range []string{"/assets/app.v34.js", "/assets/style.v29.css"} {
		if !strings.Contains(body, asset) {
			t.Fatal("homepage missing updated asset", asset)
		}
		if _, err := web.ReadFile("web/" + strings.TrimPrefix(asset, "/assets/")); err != nil {
			t.Fatal("updated asset not embedded", asset, err)
		}
	}
}

// 首页页头的「个人账户」入口必须始终可点：任务在服务器后台异步执行，任何任务在跑都不该把
// 整个账号入口禁用（曾经用 `$("accountBtn").disabled = tasks.size > 0` 锁成灰按钮，用户在
// 等待生成时点不开账号弹窗，既看不到剩余次数也无法关联邮箱）。真正会丢任务的是「退出登录」，
// 它只在弹窗内单独禁用，并在弹窗里说明原因。
func TestHomepageAccountEntryStaysClickable(t *testing.T) {
	a := testApp(t)
	body := request(t, a, nil, "GET", "/", nil).Body.String()
	if !strings.Contains(body, `<button id="accountBtn"`) {
		t.Fatal("homepage missing account entry")
	}
	if !strings.Contains(body, `id="accountLockedHint"`) {
		t.Fatal("account dialog missing locked hint")
	}
	raw, err := web.ReadFile("web/app.v34.js")
	if err != nil {
		t.Fatal("homepage script not embedded", err)
	}
	source := string(raw)
	if strings.Contains(source, `$("accountBtn").disabled`) {
		t.Fatal("account entry disabled again while tasks run")
	}
	if !strings.Contains(source, "function syncAccountActions()") || !strings.Contains(source, `$("logoutBtn").disabled = busy`) {
		t.Fatal("account switching is no longer guarded inside the dialog")
	}
	if !strings.Contains(source, "syncAccountActions();") {
		t.Fatal("account dialog does not refresh its action state")
	}
}

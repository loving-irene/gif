// 社区页：分页浏览用户分享出来的动图，并允许分享人取消自己的分享。
// 列表用 (created, rowid) 键集游标翻页，新分享或重复分享只会出现在最前，不会把后续页挤乱。
// 动图按需加载：进入视口才挂上 <img>，离开视口即移除，避免一屏之外的动图继续解码。
import {
  $,
  api,
  connect,
  element,
  errorMessage,
  showError,
  toast,
  userNotice,
  watchMediaImage,
} from "./common.v5.js";

let nextCursor = "";
let loading = false;
const cardImages = new WeakMap();

function notice(message) {
  const node = $("communityNotice");
  node.textContent = message;
  node.hidden = false;
}

function timeText(seconds) {
  const at = new Date((Number(seconds) || 0) * 1000);
  if (Number.isNaN(at.getTime())) return "";
  return at.toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function makeCard(item) {
  const card = element("li", { className: "community-card" });
  const media = element("div", { className: "community-media" });
  const image = element("img", {
    alt: item.name + " 动态作品",
    loading: "lazy",
    decoding: "async",
    width: 256,
    height: 256,
  });
  media.append(image);
  card.append(media);

  const head = element("div", { className: "community-meta" });
  head.append(element("b", {}, item.name));
  if (item.mine) head.append(element("span", { className: "community-badge" }, "我分享的"));
  head.append(
    element("span", { className: "community-who" }, item.sharerName || "匿名用户"),
  );
  const detail = [item.action, item.category, timeText(item.created)]
    .filter(Boolean)
    .join(" · ");
  if (detail) head.append(element("small", {}, detail));
  card.append(head);

  if (item.mine) {
    const remove = element(
      "button",
      { className: "text-button community-remove", type: "button" },
      "取消分享",
    );
    remove.onclick = () => unshare(item, card, remove);
    card.append(remove);
  }

  // 进入视口才请求动图，离开后移除以停止解码：社区是长列表，这一条最省手机电量。
  cardImages.set(card, watchMediaImage(image, `/api/community/${encodeURIComponent(item.id)}/gif`, card));
  return card;
}

async function unshare(item, card, button) {
  if (
    !confirm("取消分享后，这张动图会从社区消失，其他人都看不到了。确定取消吗？")
  )
    return;
  button.disabled = true;
  try {
    await api("/api/community/unshare", { shareId: item.id });
    cardImages.get(card)?.();
    cardImages.delete(card);
    card.remove();
    toast("已取消分享。");
    if (!$("communityGrid").children.length) {
      $("communityEmpty").hidden = false;
      $("communityEnd").hidden = true;
    }
  } catch (e) {
    button.disabled = false;
    showError(e);
  }
}

async function load() {
  if (loading) return;
  loading = true;
  const more = $("communityMoreBtn");
  more.disabled = true;
  try {
    const query = nextCursor ? `?cursor=${encodeURIComponent(nextCursor)}` : "";
    const page = await api("/api/community" + query);
    const grid = $("communityGrid");
    for (const item of page.items || []) grid.append(makeCard(item));
    nextCursor = page.nextCursor || "";
    more.hidden = !nextCursor;
    $("communityEnd").hidden = !!nextCursor || !grid.children.length;
    $("communityEmpty").hidden = !!grid.children.length;
  } catch (e) {
    notice(errorMessage(e));
    showError(e);
  } finally {
    more.disabled = false;
    loading = false;
  }
}

$("communityMoreBtn").onclick = load;

async function init() {
  try {
    const session = await connect();
    if (!session?.user) throw userNotice("请刷新页面重新连接账号");
    await load();
    if (!$("communityGrid").children.length && !nextCursor)
      notice("社区里还没有分享，去作品集点「分享」就能成为第一个。");
  } catch (e) {
    notice(errorMessage(e));
    showError(e);
  }
}

init();

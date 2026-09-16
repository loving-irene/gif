export const $ = (id) => document.getElementById(id);
export let account = null;
export const NETWORK_ERROR_MESSAGE = "网络异常，请稍后重试~";
export function userNotice(message) {
  const error = new Error(message);
  error.userFacing = true;
  return error;
}
export function errorMessage(error) {
  return error?.userFacing ? error.message : NETWORK_ERROR_MESSAGE;
}
let csrf = "";
export function setSession(s) {
  if (s.user) account = s.user;
  if (s.csrf) csrf = s.csrf;
}
// 图片按账号和完整 URL 隔离；列表仍实时同步，只有不可变的图片内容走本地优先。
const mediaRequests = new Map();
const mediaWrites = new Map();
const mediaCacheBytes = 128 * 1024 * 1024;
async function storeMedia(cache, name, url, response) {
  const previous = mediaWrites.get(name) || Promise.resolve();
  const pending = previous.then(async () => {
    const blob = await response.blob();
    if (!blob.size || blob.size > mediaCacheBytes) return;
    const keys = await cache.keys();
    let bytes = blob.size;
    const sizes = await Promise.all(keys.map(async (key) =>
      Number((await cache.match(key))?.headers.get("Content-Length")) || 0,
    ));
    bytes += sizes.reduce((sum, size) => sum + size, 0);
    // 仅淘汰这层可重新下载的缓存，不动用户 IndexedDB 中保存的作品。
    let removed = 0;
    while (removed < keys.length && (bytes > mediaCacheBytes || keys.length - removed >= 128)) {
      await cache.delete(keys[removed]);
      bytes -= sizes[removed++];
    }
    await cache.put(url, new Response(blob, { headers: {
      "Content-Type": blob.type,
      "Content-Length": String(blob.size),
    } }));
  }).catch(() => {}); // 隐私模式、配额不足或存储被禁用时仍使用本次接口结果。
  mediaWrites.set(name, pending);
  await pending;
  if (mediaWrites.get(name) === pending) mediaWrites.delete(name);
}
export async function mediaResponse(path) {
  const url = new URL(path, location.origin);
  if (url.origin !== location.origin || !/^\/api\/(?:drafts\/[^/]+\/image|works\/[^/]+\/(?:gif|sheet)|admin\/gallery\/(?:drafts\/[^/]+\/image|works\/[^/]+\/(?:gif|sheet))|community\/[^/]+\/gif)$/.test(url.pathname))
    throw new Error("不支持的图片地址");
  if (!account?.id) throw new Error("请先连接账号");
  const name = `gif-media-v1-${account.id}`;
  const key = `${name}:${url.href}`;
  let pending = mediaRequests.get(key);
  if (!pending) {
    pending = (async () => {
      let cache;
      try {
        cache = await globalThis.caches?.open(name);
        const hit = await cache?.match(url.href);
        if (hit?.ok && /^image\//i.test(hit.headers.get("Content-Type") || "")) return hit;
      } catch { /* 缓存不可用时继续从接口读取。 */ }
      // 避免账号切换后命中 HTTP 层的旧私有响应；Cache API 已自行按账号隔离。
      const response = await fetch(url.href, { credentials: "same-origin", cache: "no-store" });
      if (cache && response.status === 200 && /^image\//i.test(response.headers.get("Content-Type") || ""))
        await storeMedia(cache, name, url.href, response.clone());
      return response;
    })();
    mediaRequests.set(key, pending);
  }
  try {
    return (await pending).clone();
  } finally {
    if (mediaRequests.get(key) === pending) mediaRequests.delete(key);
  }
}
// 视口外释放 Object URL 并停止 GIF 解码；再次进入时优先读磁盘缓存。
// 返回清理函数，翻页、刷新或删除卡片时必须调用，防止旧请求回写已移除节点。
export function watchMediaImage(image, path, target = image) {
  const uid = account?.id;
  let active = false, disposed = false, generation = 0, objectURL;
  function release() {
    generation++;
    image.removeAttribute("src");
    if (objectURL) URL.revokeObjectURL(objectURL);
    objectURL = null;
  }
  async function display() {
    const current = ++generation;
    try {
      const response = await mediaResponse(path);
      if (!response.ok) throw new Error("图片读取失败");
      const blob = await response.blob();
      if (disposed || !active || current !== generation || uid !== account?.id) return;
      objectURL = URL.createObjectURL(blob);
      image.src = objectURL;
    } catch {
      if (!disposed && active && current === generation) image.alt = "图片加载失败，重新进入可重试";
    }
  }
  const observer = typeof IntersectionObserver === "function" ? new IntersectionObserver((entries) => {
    for (const entry of entries) {
      if (disposed || active === entry.isIntersecting) continue;
      active = entry.isIntersecting;
      if (active) void display();
      else release();
    }
  }, { rootMargin: "200px 0px" }) : null;
  if (observer) observer.observe(target);
  else { active = true; void display(); }
  return () => {
    disposed = true;
    observer?.disconnect();
    release();
  };
}
export async function api(path, body) {
  const options = {
    credentials: "same-origin",
    cache: "no-store",
    headers: {},
  };
  if (body !== undefined) {
    options.method = "POST";
    options.headers = {
      "Content-Type": "application/json",
      "X-CSRF-Token": csrf,
    };
    options.body = JSON.stringify(body);
  }
  const r = await fetch(path, options);
  let result;
  try {
    result = await r.json();
  } catch {
    throw new Error(NETWORK_ERROR_MESSAGE);
  }
  if (!r.ok) {
    const e = new Error(result.error || "请求失败");
    e.status = r.status;
    e.userFacing =
      typeof result.error === "string" &&
      [400, 402, 409, 413, 415].includes(r.status);
    // 错误响应里的附加字段（如重复任务提醒）原样保留，供页面按业务分支处理。
    if (result && typeof result === "object") e.api = result;
    throw e;
  }
  return result;
}
function randomDevice() {
  const v = new Uint8Array(32);
  crypto.getRandomValues(v);
  return Array.from(v, (x) => x.toString(16).padStart(2, "0")).join("");
}
export async function connect() {
  let device = localStorage.getItem("gif.device");
  if (!/^[a-f0-9]{64}$/.test(device || "")) {
    device = randomDevice();
    localStorage.setItem("gif.device", device);
  }
  const properties = [
    navigator.userAgent,
    navigator.language,
    navigator.hardwareConcurrency,
    screen.colorDepth,
    Intl.DateTimeFormat().resolvedOptions().timeZone,
  ].join("|");
  const digest = await crypto.subtle.digest(
    "SHA-256",
    new TextEncoder().encode(properties),
  );
  const fingerprint = Array.from(new Uint8Array(digest), (x) =>
    x.toString(16).padStart(2, "0"),
  ).join("");
  const session = await api("/api/session", { device, fingerprint });
  setSession(session);
  return session;
}
export async function refreshAccount() {
  const s = await api("/api/me");
  setSession(s);
  if ($("credits")) $("credits").textContent = account.credits;
  return s;
}
let toastTimer;
export function toast(message) {
  const node = $("toast");
  // 弹窗处于浏览器顶层，普通元素会被遮罩挡住；把提示移入弹窗内保证可见。
  const host = document.querySelector("dialog:modal") || document.body;
  if (node.parentElement !== host) host.appendChild(node);
  node.textContent = message;
  node.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (node.hidden = true), 6500);
}
export function showError(error) {
  toast(errorMessage(error));
}
export async function busyButton(button, fn) {
  const old = button.disabled;
  button.disabled = true;
  try {
    return await fn();
  } catch (e) {
    showError(e);
  } finally {
    button.disabled = old;
  }
}
export function element(tag, props = {}, text) {
  const node = document.createElement(tag);
  Object.assign(node, props);
  if (text !== undefined) node.textContent = text;
  return node;
}
export function dataBlob(data) {
  const [prefix, b64] = data.split(",");
  const raw = atob(b64);
  const bytes = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) bytes[i] = raw.charCodeAt(i);
  return new Blob([bytes], { type: prefix.slice(5).split(";")[0] });
}
export function blobData(blob) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(reader.result);
    reader.onerror = () => reject(new Error("照片读取失败"));
    reader.readAsDataURL(blob);
  });
}
let dbPromise;
function db() {
  return (dbPromise ||= new Promise((resolve, reject) => {
    const r = indexedDB.open("shiguang-gif", 1);
    r.onupgradeneeded = () => {
      const d = r.result;
      d.createObjectStore("profiles", { keyPath: "uid" });
      d.createObjectStore("works", { keyPath: "id" });
      d.createObjectStore("pending", { keyPath: "uid" });
    };
    r.onsuccess = () => resolve(r.result);
    r.onerror = () => reject(new Error("无法打开本地作品库，请允许浏览器存储"));
  }));
}
export async function local(store, method, value) {
  const database = await db();
  return new Promise((resolve, reject) => {
    const t = database.transaction(
      store,
      method === "get" || method === "getAll" ? "readonly" : "readwrite",
    );
    const r = t.objectStore(store)[method](value);
    t.oncomplete = () => resolve(r.result);
    t.onabort = t.onerror = () =>
      reject(new Error("浏览器存储失败或空间不足，请先下载已有作品"));
  });
}
export async function migrateLocal(oldUID, newUID) {
  if (oldUID === newUID) return;
  const all = await local("works", "getAll");
  for (const work of all) {
    if (work.uid === oldUID) {
      work.uid = newUID;
      await local("works", "put", work);
    }
  }
  const p = await local("profiles", "get", oldUID);
  if (p) {
    p.uid = newUID;
    await local("profiles", "put", p);
    await local("profiles", "delete", oldUID);
  }
}
export function download(blob, name) {
  const url = URL.createObjectURL(blob);
  const a = element("a", { href: url, download: name });
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 30000);
}

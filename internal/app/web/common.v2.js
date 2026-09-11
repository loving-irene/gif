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

import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";
import { frontendAsset } from "./frontend-source.mjs";

const source = fs.readFileSync(frontendAsset("common"), "utf8").replace(/\bexport\s+/g, "");
const stores = new Map();
let requests = 0, failOpen = false, failPut = false, status = 200, type = "image/gif";
let deferred = null;
const caches = {
  async open(name) {
    if (failOpen) throw new Error("storage disabled");
    if (!stores.has(name)) stores.set(name, new Map());
    const entries = stores.get(name);
    return {
      async match(key) { return entries.get(String(key))?.clone(); },
      async put(key, value) {
        if (failPut) throw new Error("quota exceeded");
        entries.set(String(key), value.clone());
      },
      async keys() { return [...entries.keys()]; },
      async delete(key) { return entries.delete(String(key)); },
    };
  },
};
const observers = [], revoked = [], created = [];
class MediaURL extends URL {
  static createObjectURL() { const url = `blob:test-${created.length}`; created.push(url); return url; }
  static revokeObjectURL(url) { revoked.push(url); }
}
function context() {
  const ctx = vm.createContext({
    URL: MediaURL, Response, Blob, caches,
    location: { origin: "https://gif.test" },
    fetch: async (_url, options) => {
      requests++;
      assert.equal(options.credentials, "same-origin");
      assert.equal(options.cache, "no-store");
      if (deferred) await deferred;
      return new Response(status === 200 ? "GIF89a" : "failed", { status, headers: { "Content-Type": type } });
    },
    IntersectionObserver: class {
      constructor(callback) { this.callback = callback; observers.push(this); }
      observe(target) { this.target = target; }
      disconnect() { this.disconnected = true; }
      visible(value) { this.callback([{ target: this.target, isIntersecting: value }]); }
    },
  });
  vm.runInContext(source, ctx);
  vm.runInContext('setSession({user:{id:"alice"}})', ctx);
  return ctx;
}
let ctx = context();
const read = (path, target = ctx) => { target.path = path; return vm.runInContext("mediaResponse(path)", target); };
const paths = ["/api/drafts/a/image", "/api/works/a/gif", "/api/works/a/sheet", "/api/admin/gallery/drafts/a/image", "/api/admin/gallery/works/a/gif", "/api/admin/gallery/works/a/sheet", "/api/community/a/gif"];
for (const path of paths) {
  const before = requests;
  const results = await Promise.all([read(path), read(path), read(path)]);
  assert.equal(requests, before + 1, "并发读取同一图片只请求一次");
  for (const response of results) assert.equal(await response.text(), "GIF89a", "每个调用者可独立读取响应体");
  await read(path);
  assert.equal(requests, before + 1, "缓存命中不请求图片接口");
}
ctx = context(); // 模拟页面刷新：仅保留浏览器缓存，不保留模块内状态。
let before = requests;
await read(paths[0]);
assert.equal(requests, before, "刷新后仍复用持久缓存");
vm.runInContext('setSession({user:{id:"bob"}})', ctx);
await read(paths[0]);
assert.equal(requests, before + 1, "不同账号不复用私有缓存");
for (const code of [401, 403, 404, 500]) {
  status = code;
  before = requests;
  assert.equal((await read(`/api/works/error${code}/gif`)).status, code);
  await read(`/api/works/error${code}/gif`);
  assert.equal(requests, before + 2, "失败响应不缓存");
}
status = 200;
type = "text/html";
before = requests;
await read("/api/works/html/gif");
await read("/api/works/html/gif");
assert.equal(requests, before + 2, "非图片成功响应也不缓存");
type = "image/gif";
for (const mode of ["open", "put", "missing"]) {
  failOpen = mode === "open";
  failPut = mode === "put";
  if (mode === "missing") ctx.caches = undefined;
  before = requests;
  assert.equal(await (await read(`/api/works/${mode}/gif`)).text(), "GIF89a");
  assert.equal(await (await read(`/api/works/${mode}/gif`)).text(), "GIF89a");
  assert.equal(requests, before + 2, "缓存不可用或写入失败仍显示接口图片");
}
failOpen = failPut = false;
ctx.caches = caches;
await assert.rejects(read("https://other.test/api/works/a/gif"), /不支持/);
await assert.rejects(read("/api/works"), /不支持/);
vm.runInContext('setSession({user:{id:"bounded"}})', ctx);
for (let n = 0; n < 130; n++) await read(`/api/works/bounded${n}/gif`);
assert.equal(stores.get("gif-media-v1-bounded").size, 128);
assert.ok(!stores.get("gif-media-v1-bounded").has("https://gif.test/api/works/bounded0/gif"));
const cache = await caches.open("gif-media-v1-bounded");
await cache.put("https://gif.test/api/works/large/gif", new Response("GIF", { headers: {
  "Content-Type": "image/gif", "Content-Length": String(128 * 1024 * 1024),
} }));
await read("/api/works/after-large/gif");
assert.ok(!stores.get("gif-media-v1-bounded").has("https://gif.test/api/works/large/gif"), "总字节数超限会淘汰旧缓存");

// 真实异步响应顺序：退出视口/删除卡片后，旧请求不能恢复图片或泄漏 URL。
const flush = async (predicate) => {
  for (let n = 0; n < 100; n++) {
    if (predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 5));
  }
  assert.fail("异步图片操作未完成");
};
const image = { src: "", removeAttribute() { this.src = ""; } };
ctx.image = image;
vm.runInContext('dispose = watchMediaImage(image, "/api/works/visible/gif")', ctx);
const observer = observers.at(-1);
before = requests;
assert.equal(image.src, "");
observer.visible(true);
await flush(() => !!image.src);
const firstURL = image.src;
observer.visible(false);
assert.equal(image.src, "");
assert.ok(revoked.includes(firstURL));
observer.visible(true);
await flush(() => !!image.src);
assert.equal(requests, before + 1, "再次进入视口从缓存读取");
vm.runInContext("dispose()", ctx);
assert.equal(image.src, "");
assert.ok(observer.disconnected);

let finish;
deferred = new Promise((resolve) => { finish = resolve; });
vm.runInContext('dispose = watchMediaImage(image, "/api/works/slow/gif")', ctx);
observers.at(-1).visible(true);
const count = created.length;
vm.runInContext("dispose()", ctx);
finish();
deferred = null;
await read("/api/works/slow/gif");
await new Promise((resolve) => setTimeout(resolve, 10));
assert.equal(created.length, count);
assert.equal(image.src, "");
console.log("Media cache passed: 7 routes, persistent hits, concurrency, account isolation, failure fallback, eviction and image lifecycle");

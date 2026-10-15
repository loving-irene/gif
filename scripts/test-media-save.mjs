import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";
import { frontendAsset } from "./frontend-source.mjs";

const source = fs.readFileSync(frontendAsset("common"), "utf8").replace(/\bexport\s+/g, "");
function setup({ mobile = true, share, canShare, secure = true } = {}) {
  const nodes = [], clicked = [], revoked = [], blobs = [], timers = [];
  function element(tag) {
    const node = {
      tag, children: [], events: {}, attributes: {},
      append(...children) { for (const child of children) { child.parentElement?.children.splice(child.parentElement.children.indexOf(child), 1); child.parentElement = this; this.children.push(child); } },
      appendChild(child) { this.append(child); },
      setAttribute(key, value) { this.attributes[key] = value; },
      removeAttribute(key) { delete this[key]; },
      addEventListener(key, fn) { this.events[key] = fn; },
      showModal() { this.open = true; },
      close() { this.open = false; this.events.close?.(); },
      remove() { this.removed = true; this.parentElement?.children.splice(this.parentElement.children.indexOf(this), 1); this.parentElement = null; },
      contains(child) { return this.children.includes(child); },
      click() { clicked.push(this); return this.onclick?.(); },
    };
    nodes.push(node);
    return node;
  }
  const body = element("body");
  const ctx = vm.createContext({
    Blob, File, isSecureContext: secure,
    navigator: { userAgent: mobile ? "iPhone" : "desktop", maxTouchPoints: mobile ? 1 : 0, share, canShare },
    document: { body, createElement: element, getElementById: (id) => nodes.find(n => n.id === id && !n.removed) },
    URL: { createObjectURL(blob) { blobs.push(blob); return `blob:test-${blobs.length}`; }, revokeObjectURL(url) { revoked.push(url); } },
    setTimeout(fn) { timers.push(fn); },
  });
  vm.runInContext(source, ctx);
  const gif = new Blob(["GIF89a-original-animated-bytes"], { type: "image/gif" });
  return { ctx, nodes, body, clicked, revoked, blobs, timers, gif,
    save: () => ctx.download(gif, "我的动画.gif"),
    button: (label) => nodes.find(n => n.tag === "button" && n.textContent === label),
    dialog: () => nodes.find(n => n.tag === "dialog" && !n.removed),
    status: () => nodes.find(n => n.className === "save-media-status"),
  };
}

// A normal mobile save must offer choices even without the native sharing API.
for (const options of [
  {},
  { share() { assert.fail("unsupported file shared"); }, canShare: () => false },
  { share() { assert.fail("capability failure shared"); }, canShare() { throw new Error("blocked"); } },
  { secure: false, share() { assert.fail("insecure share"); }, canShare: () => true },
]) {
  const t = setup(options);
  await t.save();
  assert.ok(t.dialog().open);
  assert.equal(t.clicked.length, 0, "never silently download on mobile");
  assert.equal(t.button("系统分享"), undefined);
  assert.equal(t.blobs[0], t.gif, "preview uses the original GIF");
  assert.match(t.status().textContent, /不支持/);
  await t.button("下载文件").click();
  const anchor = t.clicked.find(n => n.tag === "a");
  assert.equal(anchor.download, "我的动画.gif");
  assert.equal(t.blobs[1], t.gif);
  assert.ok(t.dialog().open);
  t.dialog().close();
  assert.deepEqual(t.revoked, ["blob:test-1"]);
  assert.equal(t.dialog(), undefined);
  t.timers.forEach(fn => fn());
  assert.deepEqual(t.revoked, ["blob:test-1", "blob:test-2"]);
}

let sent, finishShare;
const t = setup({ canShare: () => true, share(data) { sent = data; return new Promise(resolve => { finishShare = resolve; }); } });
await t.save();
await t.save();
assert.equal(t.nodes.filter(n => n.tag === "dialog").length, 1);
assert.equal(sent, undefined, "only share on the explicit button click");
const pending = t.button("系统分享").click();
assert.ok(sent, "share invoked synchronously within the click to preserve user activation");
assert.ok(t.button("系统分享").disabled);
assert.equal(sent.files[0].name, "我的动画.gif");
assert.equal(sent.files[0].type, "image/gif");
assert.equal(await sent.files[0].text(), await t.gif.text());
finishShare();
await pending;
assert.equal(t.button("系统分享").disabled, false);
assert.match(t.status().textContent, /由系统和浏览器决定/);
assert.ok(t.dialog().open, "keep long-press alternative after native sharing");

for (const name of ["AbortError", "NotAllowedError", "TypeError"]) {
  const t = setup({ share() { throw Object.assign(new Error("share failed"), { name }); } });
  await t.save();
  const original = t.status().textContent;
  await t.button("系统分享").click();
  assert.ok(t.dialog().open);
  assert.equal(t.clicked.filter(n => n.tag === "a").length, 0, "cancellation/failure must never cause a download");
  assert.equal(t.button("系统分享").disabled, false);
  if (name === "AbortError") assert.equal(t.status().textContent, original);
  else assert.match(t.status().textContent, /未能打开/);
}
const desktop = setup({ mobile: false, share() { assert.fail("desktop share"); } });
await desktop.save();
assert.equal(desktop.dialog(), undefined);
assert.equal(desktop.clicked[0].download, "我的动画.gif");
assert.equal(desktop.blobs[0], desktop.gif);
const video = new Blob(["ftyp-mp4-bytes"], { type: "video/mp4" });
const videoSave = setup({ canShare: () => true, share(data) { sent = data; return Promise.resolve(); } });
await videoSave.ctx.download(video, "挥手打招呼.mp4");
const videoDialog = videoSave.dialog();
assert.equal(videoDialog.children.find((node) => node.tag === "video")?.tag, "video");
assert.equal(videoDialog.children.find((node) => node.tag === "h2").textContent, "保存视频");
await videoSave.button("系统分享").click();
assert.equal(sent.files[0].name, "挥手打招呼.mp4");
assert.equal(sent.files[0].type, "video/mp4");
console.log("Media save passed: mobile choices, unsupported/insecure sharing, original GIF bytes, MP4 video preview/share, user activation, cancellation, failure, cleanup and desktop download");

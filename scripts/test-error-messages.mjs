import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";
import path from "node:path";
import { fileURLToPath } from "node:url";
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const source = fs
  .readFileSync(
    process.argv[2] || path.join(root, "internal/app/web/common.v2.js"),
    "utf8",
  )
  .replace(/\bexport\s+/g, "");
const node = { textContent: "", hidden: true };
let response;
const context = vm.createContext({
  document: { getElementById: () => node },
  fetch: async () => {
    if (response instanceof Error) throw response;
    return response;
  },
  setTimeout: () => 1,
  clearTimeout: () => {},
});
vm.runInContext(source, context);
const expected = "网络异常，请稍后重试~";
response = new TypeError("Failed to fetch private backend");
await vm.runInContext('api("/api/generate",{}).catch(showError)', context);
assert.equal(node.textContent, expected);
response = {
  ok: false,
  status: 502,
  json: async () => ({ error: "provider timeout, private diagnostic details" }),
};
await vm.runInContext('api("/api/generate",{}).catch(showError)', context);
assert.equal(node.textContent, expected);
response = {
  ok: true,
  status: 200,
  json: async () => {
    throw new SyntaxError("invalid JSON");
  },
};
await vm.runInContext('api("/api/generate",{}).catch(showError)', context);
assert.equal(node.textContent, expected);
response = {
  ok: false,
  status: 402,
  json: async () => ({ error: "创作次数不足，请先兑换次数" }),
};
await vm.runInContext('api("/api/generate",{}).catch(showError)', context);
assert.equal(node.textContent, "创作次数不足，请先兑换次数");
vm.runInContext(
  'showError(userNotice("请选择 JPG、PNG 或 WebP 照片"))',
  context,
);
assert.equal(node.textContent, "请选择 JPG、PNG 或 WebP 照片");
vm.runInContext(
  'showError(new Error("图片服务失败。排查编号123；本次已扣1次"))',
  context,
);
assert.equal(node.textContent, expected);
console.log(
  "Error messages passed: network/5xx/invalid JSON/generation failures unified; business validation retained",
);

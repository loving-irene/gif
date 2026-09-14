import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";
import { frontendAsset } from "./frontend-source.mjs";

const file = process.argv[2] || frontendAsset("app");
const source = fs
  .readFileSync(file, "utf8")
  .replace(/^import[\s\S]*?from "\.\/common\.v\d+\.js";\s*/, "")
  .replace(/init\(\);\s*$/, "");
const nodes = new Map();
const $ = (id) => {
  if (!nodes.has(id))
    nodes.set(id, {
      hidden: false,
      disabled: false,
      dataset: {},
      value: "",
      textContent: "",
    });
  return nodes.get(id);
};
let accountReads = 0,
  catalogReads = 0;
let currentConfig = {
  configured: true,
  chargeOnFailure: true,
  emailConfigured: false,
};
const context = vm.createContext({
  $,
  document: { querySelectorAll: () => [] },
  account: { credits: 5 },
  commonRefreshAccount: async () => {
    accountReads++;
  },
  api: async (route) => {
    assert.equal(route, "/api/catalog");
    catalogReads++;
    return currentConfig;
  },
  console,
});
vm.runInContext(source, context);
vm.runInContext(
  `catalog={configured:false,chargeOnFailure:true};selfie={photo:'keep-me'};selectedCategory='child';selectedColor='天空蓝与奶油黄';profile={accepted:true};`,
  context,
);
$("clothes").value = "彩色背带裤";
vm.runInContext("updateControls()", context);
assert.equal($("draftBtn").disabled, true);
await Promise.all([
  vm.runInContext("refreshServiceState()", context),
  vm.runInContext("refreshServiceState()", context),
]);
assert.equal(catalogReads, 1, "coalesce overlapping checks");
assert.equal(accountReads, 1, "refresh current session before generating");
assert.equal(
  $("draftBtn").disabled,
  false,
  "enable when configuration becomes ready",
);
assert.equal(vm.runInContext("selfie.photo", context), "keep-me");
assert.equal(vm.runInContext("selectedCategory", context), "child");
assert.equal($("clothes").value, "彩色背带裤");
assert.equal($("serviceStatus").hidden, true);
currentConfig = { ...currentConfig, configured: false };
await vm.runInContext("refreshServiceState()", context);
assert.equal(
  $("draftBtn").disabled,
  true,
  "disable again if configuration is removed",
);
assert.equal(
  $("refreshServiceBtn").hidden,
  false,
  "offer a visible recheck control",
);
assert.equal($("notice").hidden, false);
console.log(
  "Service readiness refresh: unlock, preserve form, coalesce requests, and re-disable passed",
);

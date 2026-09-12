import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";
import { fileURLToPath } from "node:url";
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const source = fs
  .readFileSync(
    process.argv[2] || path.join(root, "internal/app/web/app.v9.js"),
    "utf8",
  )
  .replace(/^import[\s\S]*?from "\.\/common\.v2\.js";\s*/, "")
  .replace(/init\(\);\s*$/, "");
let scheduled = 0,
  cleared = 0;
const nodes = new Map();
const $ = (id) => {
  if (!nodes.has(id)) nodes.set(id, { textContent: "", hidden: true });
  return nodes.get(id);
};
const context = vm.createContext({
  $,
  Date,
  console,
  setInterval() {
    scheduled++;
    return 1;
  },
  clearInterval() {
    cleared++;
  },
});
vm.runInContext(source, context);
context.e = {
  seconds: 100,
  lowSeconds: 70,
  highSeconds: 130,
  samples: 2,
  source: "history",
};
assert.match(vm.runInContext("estimateText(e,3)", context), /5分/);
assert.match(
  vm.runInContext("estimateText({...e,samples:0})", context),
  /初始估计/,
);
assert.match(
  vm.runInContext("elapsedEstimateText(e,150)", context),
  /已超过预估/,
);
assert.doesNotMatch(
  vm.runInContext("elapsedEstimateText(e,150)", context),
  /还需.*0秒/,
);
vm.runInContext("startEstimateClock(e,80)", context);
assert.match($("progressEstimate").textContent, /已等待 1分2[01]秒/);
vm.runInContext("startEstimateClock(e,85)", context);
assert.equal(scheduled, 1, "polling must not accumulate interval timers");
vm.runInContext("stopEstimateClock()", context);
assert.equal(cleared, 1);
assert.equal($("progressEstimate").hidden, true);
console.log(
  "Time display passed: queue totals, initial labels, resumed elapsed time, overdue message, timer cleanup",
);

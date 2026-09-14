import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";
import { frontendAsset } from "./frontend-source.mjs";
const source = fs
  .readFileSync(
    process.argv[2] || frontendAsset("app"),
    "utf8",
  )
  .replace(/^import[\s\S]*?from "\.\/common\.v\d+\.js";\s*/, "")
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
vm.runInContext('tasks.set("test", {startedAt: Date.now()-80000, estimate:e, estimateEl:$("progressEstimate")}); startTaskClock()', context);
assert.match($("progressEstimate").textContent, /已等待 1分2[01]秒/);
vm.runInContext('tasks.get("test").startedAt = Date.now()-85000; startTaskClock()', context);
assert.equal(scheduled, 1, "polling must not accumulate interval timers");
vm.runInContext("tasks.clear(); stopTaskClock()", context);
assert.equal(cleared, 1);
assert.equal(vm.runInContext("taskClock", context), null);
console.log(
  "Time display passed: queue totals, initial labels, resumed elapsed time, overdue message, timer cleanup",
);

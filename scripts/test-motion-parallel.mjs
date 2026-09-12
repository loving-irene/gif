// 动作批次并行调度的测试：加载真实的前端脚本，只驱动并发布局函数，
// 验证「整批按并发额度一次性提交、完成一个补一个、暂停/无额度时保留未提交动作」。
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const file = process.argv[2] || path.join(root, "internal/app/web/app.v19.js");
const source = fs
  .readFileSync(file, "utf8")
  .replace(/^import \{[\s\S]*?\} from "\.\/common\.v4\.js";\s*/, "")
  .replace(/init\(\);\s*$/, "");
const elements = new Map();
const context = vm.createContext({
  $: (id) => {
    if (!elements.has(id)) elements.set(id, { textContent: "", hidden: true });
    return elements.get(id);
  },
  console,
  setTimeout,
  clearTimeout,
  setInterval: () => 1,
  clearInterval() {},
  crypto: { randomUUID: () => "test-id" },
});
vm.runInContext(source, context);
const run = (code) => vm.runInContext(code, context);

assert.equal(typeof run("submitPaced"), "function", "submitPaced missing");
assert.equal(
  typeof run("motionParallelWidth"),
  "function",
  "motionParallelWidth missing",
);

// 1) 并发额度内一次性提交，任意一个完成就立刻补上下一个（不是等整批一起结束）。
const batch = await run(`(async () => {
  const events = [];
  const gates = [];
  let active = 0;
  const start = (item) => {
    events.push("start:" + item);
    active++;
    return new Promise((resolve) => gates.push(() => {
      active--;
      events.push("done:" + item);
      resolve();
    }));
  };
  const tick = () => new Promise((r) => setTimeout(r, 0));
  const job = submitPaced([1, 2, 3, 4, 5], () => active < 2, start);
  const steps = [events.join(" ")];
  while (gates.length) {
    gates.shift()();
    await tick();
    steps.push(events.join(" "));
  }
  return { steps, left: await job };
})()`);
assert.equal(
  JSON.stringify(batch.steps),
  JSON.stringify([
    "start:1 start:2",
    "start:1 start:2 done:1 start:3",
    "start:1 start:2 done:1 start:3 done:2 start:4",
    "start:1 start:2 done:1 start:3 done:2 start:4 done:3 start:5",
    "start:1 start:2 done:1 start:3 done:2 start:4 done:3 start:5 done:4",
    "start:1 start:2 done:1 start:3 done:2 start:4 done:3 start:5 done:4 done:5",
  ]),
  "actions were not pipelined through the free slots",
);
assert.equal(JSON.stringify(batch.left), "[]", "batch should submit every action");

// 2) 没有空闲额度时一个都不提交，未提交的动作原样返回（保留在选择里）。
const blocked = await run(`(async () => {
  let calls = 0;
  const left = await submitPaced([1, 2, 3], () => false, () => {
    calls++;
    return Promise.resolve();
  });
  return { left, calls };
})()`);
assert.equal(blocked.calls, 0, "blocked batch must not submit anything");
assert.equal(JSON.stringify(blocked.left), "[1,2,3]");

// 3) 暂停（canStart 变假）只停止提交新动作，已经发出的照常完成。
const paused = await run(`(async () => {
  const started = [];
  let allow = true;
  const left = await submitPaced([1, 2, 3], () => allow, (item) => {
    started.push(item);
    allow = false;
    return Promise.resolve();
  });
  return { started, left };
})()`);
assert.equal(JSON.stringify(paused.started), "[1]");
assert.equal(JSON.stringify(paused.left), "[2,3]");

// 4) 并行宽度取「账号并发余额」与「服务器生成槽位」的较小值，并用于整批预估。
const widths = await run(`(() => {
  tasks.clear();
  selectedActions.clear();
  catalog = { userConcurrency: 5, generationSlots: 2 };
  const wide = motionParallelWidth(5);
  const single = motionParallelWidth(1);
  tasks.set("draft-1", { kind: "draft" });
  tasks.set("motion-1", { kind: "motion" });
  const busy = motionParallelWidth(5);
  catalog = { userConcurrency: 1, generationSlots: 2 };
  tasks.clear();
  const capped = motionParallelWidth(5);
  return { wide, single, busy, capped };
})()`);
assert.equal(widths.wide, 2, "width should follow the server slots");
assert.equal(widths.single, 1, "single action should not be widened");
assert.equal(widths.busy, 2, "busy account should still be estimated at 2 in parallel");
assert.equal(widths.capped, 1, "account concurrency should cap the width");

// 5) 界面文案：整批用时按轮次（ceil(动作数 / 宽度)）显示，并说明最多同时几个。
const text = await run(`(() => {
  tasks.clear();
  selectedActions.clear();
  for (const id of ["attack", "guard", "idle"]) selectedActions.add(id);
  catalog = {
    userConcurrency: 5,
    generationSlots: 2,
    estimates: { draft: null, motion: { seconds: 100, lowSeconds: 70, highSeconds: 130, samples: 3, source: "history" } },
  };
  renderEstimates();
  return $("motionEstimate").textContent;
})()`);
assert.match(text, /3个动作并行生成（最多同时2个）/);
assert.match(text, /3分20秒/, "batch estimate should cover two rounds");

console.log(
  "Motion concurrency passed: slot-bounded batch submission, refill on completion, pause keeps submitted work, width and batch estimate",
);

// 动作批次并发调度的测试：加载真实的前端脚本，用最小 DOM 与接口替身驱动调度函数与 makeMotions()，验证：
// 1) 整批按账号并发额度一次性提交、完成一个补一个；
// 2) 暂停或没有空闲额度时保留未提交动作，已经发出的照常完成；
// 3) 并行宽度取「账号并发余额」与「服务器生成槽位」的较小值，并用于整批预估；
// 4) 有动作在制作时不再锁住“让我的角色动起来”，可以继续挑动作提交新的批次；
// 5) 制作中的动作标记为“制作中”、不计入已选，且不会被重复提交。
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const file = process.argv[2] || path.join(root, "internal/app/web/app.v21.js");
const source = fs
  .readFileSync(file, "utf8")
  .replace(/^import \{[\s\S]*?\} from "\.\/common\.v4\.js";\s*/, "")
  .replace(/init\(\);\s*$/, "");

// —— 最小 DOM 与接口替身：只覆盖被测代码真正触碰的部分 ——
const elements = new Map();
function makeClassList() {
  const set = new Set();
  return {
    add: (name) => set.add(name),
    remove: (name) => set.delete(name),
    contains: (name) => set.has(name),
    toggle: (name, on) => {
      const next = on === undefined ? !set.has(name) : Boolean(on);
      if (next) set.add(name);
      else set.delete(name);
      return next;
    },
  };
}
function makeNode(tag, attrs = {}, text) {
  const el = {
    tag,
    textContent: text === undefined ? "" : String(text),
    value: "",
    hidden: false,
    disabled: false,
    dataset: {},
    attrs: {},
    children: [],
    classList: makeClassList(),
    setAttribute(name, value) {
      el.attrs[name] = String(value);
    },
    append(...items) {
      el.children.push(...items);
    },
    replaceChildren(...items) {
      el.children = [...items];
    },
    remove() {},
  };
  if (attrs.className)
    for (const name of String(attrs.className).split(" ")) el.classList.add(name);
  return el;
}
const $ = (id) => {
  if (!elements.has(id)) elements.set(id, makeNode(id));
  return elements.get(id);
};

const account = { id: "u1", name: "", credits: 5 };
const toasts = [];
const failures = [];
const submitted = [];
const works = [];
const jobGates = new Map();
let jobSeq = 0;
let uuidSeq = 0;
const api = async (url, body) => {
  if (url === "/api/catalog")
    return {
      configured: true,
      chargeOnFailure: false,
      emailConfigured: true,
      feedbackConfigured: true,
      redeemHelp: "",
      userConcurrency: 5,
      generationSlots: 2,
      estimates: {
        draft: null,
        motion: {
          seconds: 100,
          lowSeconds: 70,
          highSeconds: 130,
          samples: 3,
          source: "history",
        },
      },
    };
  if (url === "/api/generate") {
    const id = `job-${++jobSeq}`;
    submitted.push({ id, input: body });
    return { id, estimate: null };
  }
  if (url.startsWith("/api/jobs/")) {
    // 任务在服务器后台执行：由测试决定何时完成，用来模拟“动作还在制作中”。
    const id = url.slice("/api/jobs/".length);
    return await new Promise((resolve) => jobGates.set(id, resolve));
  }
  return {};
};
const local = async (store, op, arg) => {
  if (op === "getAll") return works.slice();
  if (op === "put" && store === "works") works.push(arg);
  return undefined;
};

const context = vm.createContext({
  $,
  element: (tag, attrs, text) => makeNode(tag, attrs, text),
  document: { createTextNode: (text) => String(text) },
  console,
  setTimeout,
  clearTimeout,
  setInterval: () => 1,
  clearInterval() {},
  crypto: { randomUUID: () => `uuid-${++uuidSeq}` },
  URL: { createObjectURL: () => "blob:test", revokeObjectURL() {} },
  account,
  api,
  local,
  toast: (message) => toasts.push(String(message)),
  showError: (error) => failures.push(error),
  busyButton: async (_button, task) => task(),
  dataBlob: (value) => ({ data: value }),
  blobData: async (value) => `data:${value && value.data ? value.data : value}`,
  migrateLocal: async () => {},
  download() {},
  errorMessage: () => "网络异常，请稍后重试~",
  commonRefreshAccount: async () => {},
  userNotice: (message) => new Error(String(message)),
  NETWORK_ERROR_MESSAGE: "网络异常，请稍后重试~",
  setSession() {},
  connect: async () => {},
});
vm.runInContext(source, context);
const run = (code) => vm.runInContext(code, context);
const snapshot = (expression) =>
  JSON.parse(run(`JSON.stringify(${expression})`));
const tick = () => new Promise((resolve) => setTimeout(resolve, 0));

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
  motionInFlight.clear();
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

// 6) 生成按钮只受并发上限与选中动作限制：有动作在制作、批次正在提交时都不再被锁住。
const gating = await run(`(() => {
  tasks.clear();
  selectedActions.clear();
  motionBatches.clear();
  motionInFlight.clear();
  catalog = { userConcurrency: 5, generationSlots: 2, configured: true, estimates: {} };
  profile = { accepted: true, selection: { category: "male" } };
  selectedActions.add("attack");
  tasks.set("motion-1", { kind: "motion", input: { kind: "motion", action: "guard" }, key: "motion-1" });
  motionInFlight.add("guard");
  updateControls();
  const whileRunning = { disabled: $("motionBtn").disabled, stopHidden: $("stopBtn").hidden };
  motionBatches.add({ paused: false });
  updateControls();
  const whileSubmitting = { disabled: $("motionBtn").disabled, stopHidden: $("stopBtn").hidden };
  tasks.clear();
  motionBatches.clear();
  catalog = { userConcurrency: 1, generationSlots: 2, configured: true, estimates: {} };
  tasks.set("motion-1", { kind: "motion", input: { kind: "motion", action: "guard" }, key: "motion-1" });
  updateControls();
  const atLimit = $("motionBtn").disabled;
  selectedActions.clear();
  tasks.clear();
  updateControls();
  const nothingSelected = $("motionBtn").disabled;
  return { whileRunning, whileSubmitting, atLimit, nothingSelected };
})()`);
assert.equal(gating.whileRunning.disabled, false, "有动作在制作时也要能继续提交新的动作");
assert.equal(gating.whileRunning.stopHidden, true, "没有批次在提交时不显示停止按钮");
assert.equal(gating.whileSubmitting.disabled, false, "批次提交中不应锁住生成按钮");
assert.equal(gating.whileSubmitting.stopHidden, false, "批次提交中要显示“停止提交新动作”");
assert.equal(gating.atLimit, true, "达到账号并发上限时才禁用生成按钮");
assert.equal(gating.nothingSelected, true, "没有选中动作时生成按钮保持禁用");

// 7) 制作中的动作：卡片带“制作中”标记、不计入已选，继续挑别的动作不受影响。
const cards = await run(`(() => {
  tasks.clear();
  selectedActions.clear();
  motionBatches.clear();
  motionInFlight.clear();
  catalog = {
    userConcurrency: 5,
    generationSlots: 2,
    configured: true,
    estimates: {},
    categories: [{ id: "male", actions: [
      { id: "attack", icon: "A", name: "武者致意" },
      { id: "guard", icon: "G", name: "格挡防御" },
    ] }],
  };
  profile = { accepted: true, selection: { category: "male" }, draft: "d", receipt: "r", selfie: "s" };
  selectedActions.add("guard");
  motionInFlight.add("attack");
  showActions();
  const [busy, picked] = $("actions").children;
  const state = {
    busy: {
      busy: busy.classList.contains("busy"),
      selected: busy.classList.contains("selected"),
      labelHidden: busy.stateEl.hidden,
      label: busy.stateEl.textContent,
    },
    picked: {
      busy: picked.classList.contains("busy"),
      selected: picked.classList.contains("selected"),
    },
    selectedCount: $("selectedCount").textContent,
  };
  selectedActions.clear();
  updateControls();
  state.hint = $("motionEstimate").textContent;
  return state;
})()`);
// 替身对象来自 vm 领域：按 JSON 比较，避免原型不同的干扰。
assert.equal(
  JSON.stringify(cards.busy),
  JSON.stringify({ busy: true, selected: false, labelHidden: false, label: "制作中" }),
);
assert.equal(
  JSON.stringify(cards.picked),
  JSON.stringify({ busy: false, selected: true }),
);
assert.equal(cards.selectedCount, 1, "制作中的动作不应计入已选");
assert.match(cards.hint, /已有 1 个动作在制作中/, "没有已选动作时要说明还能继续挑动作提交");

// 8) 端到端：先提交 1 个动作，在它制作中时再挑第 2 个动作提交，
//    两个批次都要真正发到服务器（旧的“批次进行中不再接收新批次”会在这里挡住第二个）。
await run(`(() => {
  tasks.clear();
  selectedActions.clear();
  motionBatches.clear();
  motionInFlight.clear();
  catalog = {
    userConcurrency: 5,
    generationSlots: 2,
    configured: true,
    chargeOnFailure: false,
    estimates: {},
    categories: [{ id: "male", actions: [
      { id: "attack", icon: "A", name: "武者致意" },
      { id: "guard", icon: "G", name: "格挡防御" },
    ] }],
  };
  profile = { accepted: true, selection: { category: "male" }, draft: "draft", receipt: "receipt", selfie: "selfie" };
  selfie = "selfie";
  account.credits = 5;
  selectedActions.add("attack");
  showActions();
})()`);
works.length = 0;
submitted.length = 0;
toasts.length = 0;
failures.length = 0;
jobGates.clear();
jobSeq = 0;
const first = run("makeMotions()");
await tick();
assert.equal(submitted.length, 1, "第一个动作应该已经提交");
assert.equal(submitted[0].input.action, "attack");
assert.equal(snapshot("tasks.size"), 1, "第一个动作应该在制作中");
assert.deepEqual(snapshot("[...selectedActions]"), [], "已提交的动作要离开已选");
assert.deepEqual(snapshot("[...motionInFlight]"), ["attack"], "已提交的动作应标记为制作中");

// 制作中继续挑第二个动作：按钮应当重新可用，第二次点击要能发出第二个任务。
await run(`selectedActions.add("guard"); syncActionSelection();`);
assert.equal(snapshot(`$("motionBtn").disabled`), false, "动作在制作中时选中新动作即可再次提交");
const second = run("makeMotions()");
await tick();
assert.equal(submitted.length, 2, "第二个批次也应该提交到服务器");
assert.deepEqual(
  submitted.map((item) => item.input.action),
  ["attack", "guard"],
);
assert.equal(snapshot("tasks.size"), 2, "两个动作应同时在制作中");
assert.equal(snapshot(`$("stopBtn").hidden`), false, "批次提交中要显示停止按钮");

// 两个任务都完成：本机作品集各存一份，制作中与已选都清空。
for (const id of [...jobGates.keys()])
  jobGates.get(id)({
    status: "succeeded",
    image: `image-${id}`,
    gif: `gif-${id}`,
    estimate: null,
  });
await Promise.all([first, second]);
assert.equal(snapshot("tasks.size"), 0, "两个任务都应结束");
assert.equal(snapshot("motionBatches.size"), 0, "批次结束后不应再显示停止按钮");
assert.equal(snapshot("motionInFlight.size"), 0, "完成后动作要恢复可选");
assert.deepEqual(snapshot("[...selectedActions]"), [], "完成的动作不再保留在已选里");
assert.equal(works.length, 2, "两个动作的结果都要存进本机作品集");
assert.equal(failures.length, 0, `unexpected failures: ${failures.map(String).join(", ")}`);
assert.match(toasts.join(" | "), /你的动态小分身已经做好/);
assert.equal(snapshot(`$("stopBtn").hidden`), true, "批次结束后隐藏停止按钮");

console.log(
  "Motion concurrency passed: slot-bounded batch submission, refill on completion, pause keeps submitted work, width and batch estimate, new batches while actions are in flight, in-flight action marking and end-to-end double batch",
);

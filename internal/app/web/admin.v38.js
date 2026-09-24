import {
  $,
  api,
  setSession,
  connect,
  toast,
  showError,
  busyButton,
  element,
  download,
  errorMessage,
  userNotice,
  watchMediaImage,
} from "./common.v5.js";
let settings,
  editors = [],
  codeText = "";
const fields = [
  "defaultCredits",
  "redeemHelp",
  "userConcurrency",
  "apiBase",
  "model",
  "quality",
  "motionGrid",
  "mailUser",
  "feedbackEmail",
  "draftPrompt",
  "motionPrompt",
];
// 规格切换时同步清理提示词中旧的固定网格描述；其余自定义动作约束原样保留。
function syncMotionPromptGrid() {
  const input = $("motionPrompt");
  const grid = $("motionGrid").value;
  const specs = {
    "4x4": "输出一张1024×1024透明PNG，严格4列×4行共16格，每格256×256。从左到右、从上到下排列同一次完整动作：1—4准备，5—8展开，9—12动作重点，13—16收势回位。",
    "5x5": "输出一张1024×1024透明PNG，严格5列×5行共25格，合成后每帧128×128。从左到右、从上到下排列同一次完整动作：1—6准备，7—12展开，13—18动作重点，19—25收势回位。25格必须是按时间等间隔采样的连续动作，相邻格只允许小步长变化，不得跳过中间姿态或重复静止帧。保持镜头、人物水平中心、脚底基准线和人物整体尺寸稳定。",
    "10x10": "输出一张2048×2048透明PNG，严格10列×10行共100格，合成后每帧256×256。从左到右、从上到下排列同一次完整动作：1—25准备，26—50展开，51—75动作重点，76—100收势回位。100格必须覆盖同一个完整动作周期并按时间等间隔采样，相邻格只允许极小步长变化，不得跳过中间姿态、重复静止帧或把多个动作拼在一起。保持镜头、人物水平中心、脚底基准线和人物整体尺寸稳定。",
  };
  const clauses = [
    ...Object.values(specs),
    "输出一张1024×1024透明PNG，严格10列×10行共100格，合成后每帧128×128。从左到右、从上到下排列同一次完整动作：1—25准备，26—50展开，51—75动作重点，76—100收势回位。100格必须覆盖同一个完整动作周期并按时间等间隔采样，相邻格只允许极小步长变化，不得跳过中间姿态、重复静止帧或把多个动作拼在一起。保持镜头、人物水平中心、脚底基准线和人物整体尺寸稳定。",
    "输出一张1024×1024透明PNG，严格4列×4行共16格，每格256×256。",
    "严格4列×4行共16格，每格256×256。",
    "输出一张1024x1024透明PNG，严格4列x4行共16格，每格256x256。",
    "严格4列x4行共16格，每格256x256。",
    "输出一张1024*1024透明PNG，严格4列*4行共16格，每格256*256。",
    "严格4列*4行共16格，每格256*256。",
    "1—4准备，5—8展开，9—12动作重点，13—16收势回位。",
    "1-4准备，5-8展开，9-12动作重点，13-16收势回位。",
    "输出一张1024×1024透明PNG，严格5列×5行共25格，每格128×128。",
    "严格5列×5行共25格，每格128×128。",
    "输出一张1024×1024透明PNG，严格5列×5行共25格，合成后每帧128×128。",
    "严格5列×5行共25格，合成后每帧128×128。",
    "输出一张1024*1024透明PNG，严格5列*5行共25格，每格128*128。",
    "严格5列*5行共25格，每格128*128。",
    "1—6准备，7—12展开，13—18动作重点，19—25收势回位。",
    "1-6准备，7-12展开，13-18动作重点，19-25收势回位。",
    "25格必须是按时间等间隔采样的连续动作，相邻格只允许小步长变化，不得跳过中间姿态或重复静止帧。保持镜头、人物水平中心、脚底基准线和人物整体尺寸稳定；除动作本身必需的连续位移、起跳和落地外，不得左右漂移、上下抖动或忽大忽小。",
    "输出一张1024×1024透明PNG，严格10列×10行共100格，合成后每帧128×128。",
    "严格10列×10行共100格，合成后每帧128×128。",
    "输出一张1024x1024透明PNG，严格10列x10行共100格，合成后每帧128x128。",
    "严格10列x10行共100格，合成后每帧128x128。",
    "输出一张1024*1024透明PNG，严格10列*10行共100格，合成后每帧128*128。",
    "严格10列*10行共100格，合成后每帧128*128。",
    "输出一张2048×2048透明PNG，严格10列×10行共100格，合成后每帧128×128。",
    "严格10列×10行共100格，源图2048×2048，合成后每帧128×128。",
    "输出一张2048x2048透明PNG，严格10列x10行共100格，合成后每帧128x128。",
    "输出一张2048*2048透明PNG，严格10列*10行共100格，合成后每帧128*128。",
    "1—25准备，26—50展开，51—75动作重点，76—100收势回位。",
    "1-25准备，26-50展开，51-75动作重点，76-100收势回位。",
    "100格必须覆盖同一个完整动作周期并按时间等间隔采样，相邻格只允许极小步长变化，不得跳过中间姿态、重复静止帧或把多个动作拼在一起。保持镜头、人物水平中心、脚底基准线和人物整体尺寸稳定；除动作本身必需的连续位移、起跳和落地外，不得左右漂移、上下抖动或忽大忽小。",
  ];
  let value = input.value;
  for (const clause of clauses) value = value.replaceAll(clause, "");
  const spec = specs[grid] || specs["4x4"];
  input.value = value.trim() + (value.trim() ? "\n" : "") + spec;
}
function labelInput(title, value, multiline = false) {
  const label = element("label", {}, title);
  const input = element(multiline ? "textarea" : "input", {
    value: value ?? "",
  });
  if (multiline) input.rows = 3;
  label.append(input);
  return { label, input };
}
async function loadSettings() {
  const s = await api("/api/admin/settings");
  settings = s.settings;
  for (const f of fields) $(f).value = settings[f];
  $("mailUser").value = settings.mailUser || settings.mailFrom || "";
  syncMotionPromptGrid();
  $("chargeOnFailure").value = String(settings.chargeOnFailure);
  $("assetHosts").value = settings.assetHosts.join("\n");
  $("keyState").textContent = s.apiKeySet ? "已保存密钥（不回显）" : "尚未配置";
  $("mailState").textContent = s.mailPasswordSet
    ? "已保存密码（不回显）"
    : "尚未配置";
  $("apiKey").value = "";
  $("mailPassword").value = "";
  $("categoryEditors").replaceChildren();
  editors = [];
  for (const c of settings.categories) {
    const details = element("details");
    details.append(element("summary", {}, `${c.icon} ${c.name} · 动作`));
    const actions = [];
    const actionSection = element("section", {
      className: "action-editor-section",
    });
    const actionHeading = element("div", {
      className: "action-editor-toolbar",
    });
    const count = element("span", { className: "hint" });
    const add = element(
      "button",
      { type: "button", className: "secondary" },
      "＋ 新增动作",
    );
    const actionGrid = element("div", { className: "action-editor-grid" });
    const updateActions = () => {
      count.textContent = `${actions.length} / 12 个动作 · 名称和过程成对保存`;
      add.disabled = actions.length >= 12;
      for (const item of actions) item.remove.disabled = actions.length <= 1;
    };
    actionHeading.append(element("h3", {}, "动作设置"), count, add);
    actionSection.append(actionHeading, actionGrid);
    for (const action of c.actions) {
      appendActionEditor(actions, actionGrid, action, updateActions);
    }
    add.onclick = () => {
      if (actions.length >= 12) return;
      appendActionEditor(
        actions,
        actionGrid,
        {
          id: "action-" + crypto.randomUUID(),
          name: "",
          prompt: "",
          icon: "✦",
        },
        updateActions,
      );
      updateActions();
      actions.at(-1).name.focus();
    };
    updateActions();
    details.append(actionSection);
    $("categoryEditors").append(details);
    editors.push({ c, actions });
  }
}
$("motionGrid").addEventListener("change", syncMotionPromptGrid);
function appendActionEditor(actions, grid, action, update) {
  const card = element("article", { className: "action-editor-card" });
  const name = labelInput("动作名称", action.name);
  name.input.maxLength = 50;
  name.input.placeholder = "例如：开心挥手";
  const prompt = labelInput("动作过程", action.prompt, true);
  prompt.input.placeholder = "描述准备、动作展开、收势回位的完整过程";
  const remove = element(
    "button",
    { type: "button", className: "text-button action-remove" },
    "删除动作",
  );
  const item = { action, name: name.input, prompt: prompt.input, remove, card };
  remove.onclick = () => {
    if (actions.length <= 1) return;
    actions.splice(actions.indexOf(item), 1);
    card.remove();
    update();
  };
  card.append(name.label, prompt.label, remove);
  grid.append(card);
  actions.push(item);
}
function lines(v) {
  return v
    .split("\n")
    .map((x) => x.trim())
    .filter(Boolean);
}
async function saveSettings() {
  for (const editor of editors)
    for (const item of editor.actions) {
      const empty = !item.name.value.trim()
        ? item.name
        : !item.prompt.value.trim()
          ? item.prompt
          : null;
      if (empty) {
        item.card.closest("details").open = true;
        empty.focus();
        throw userNotice(`${editor.c.name}分类的动作名称和动作过程都需要填写`);
      }
    }
  const value = structuredClone(settings);
  for (const f of fields) value[f] = $(f).value;
  value.defaultCredits = Number(value.defaultCredits);
  value.userConcurrency = Number(value.userConcurrency);
  value.chargeOnFailure = $("chargeOnFailure").value === "true";
  value.assetHosts = lines($("assetHosts").value);
  // 画风与服装等字段不再在后台编辑，保存时沿用已有配置。
  value.categories = editors.map((e) => ({
    ...e.c,
    actions: e.actions.map((a) => ({
      ...a.action,
      name: a.name.value.trim(),
      prompt: a.prompt.value.trim(),
    })),
  }));
  await api("/api/admin/settings", {
    settings: value,
    apiKey: $("apiKey").value,
    mailPassword: $("mailPassword").value,
    clearApiKey: false,
  });
  toast("配置已保存，新请求立即生效");
  try {
    localStorage.setItem("gif.settings.updated", String(Date.now()));
  } catch {
    /* 首页仍会在重新获得焦点时检查。 */
  }
  await loadSettings();
}
function td(row, text) {
  const value = String(text ?? "—");
  const cell = element("td", { title: value });
  const button = element(
    "button",
    {
      type: "button",
      className: "admin-cell-value",
      ariaLabel: "查看完整内容：" + value,
    },
    value,
  );
  button.onclick = () => {
    const table = cell.closest("table");
    $("adminValueTitle").textContent =
      table?.tHead?.rows[0]?.cells[cell.cellIndex]?.textContent || "完整内容";
    $("adminValueText").textContent = value;
    $("adminValueDialog").showModal();
  };
  cell.append(button);
  row.append(cell);
  return cell;
}
function statusCell(row, text, status) {
  const cell = td(row, text);
  const tone = {
    queued: "neutral",
    pending_upstream: "neutral",
    unredeemed: "neutral",
    running: "accent",
    marked: "accent",
    succeeded: "success",
    redeemed: "success",
    failed: "warning",
    interrupted: "warning",
  }[status] || "neutral";
  cell.replaceChildren(
    element("span", { className: "admin-badge " + tone }, text),
  );
}
// 各列表统一展示创建时间，固定为北京时间（Asia/Shanghai），不随浏览器所在时区变化；缺失或无效时显示占位符，避免出现 Invalid Date。
function timeText(seconds) {
  return seconds
    ? new Date(seconds * 1000).toLocaleString("zh-CN", {
        timeZone: "Asia/Shanghai",
      })
    : "—";
}
// 各列表统一分页，页大小与后端约定为 100。
const pager = {
  jobs: { page: 1, total: 0 },
  history: { page: 1, total: 0 },
  codes: { page: 1, total: 0 },
  credits: { page: 1, total: 0 },
  users: { page: 1, total: 0 },
  audit: { page: 1, total: 0 },
  gallery: { page: 1, total: 0 },
};
function totalPages(key) {
  return Math.max(1, Math.ceil(pager[key].total / 100));
}
function renderPager(key) {
  const state = pager[key];
  const pages = totalPages(key);
  state.page = Math.min(state.page, pages);
  $(key + "PageInfo").textContent = `第 ${state.page} / ${pages} 页 · 共 ${state.total} 条`;
  $(key + "Prev").disabled = state.page <= 1;
  $(key + "Next").disabled = state.page >= pages;
}
function bindPager(key, load) {
  $(key + "Prev").onclick = () => {
    if (pager[key].page > 1) {
      pager[key].page--;
      load().catch(showError);
    }
  };
  $(key + "Next").onclick = () => {
    if (pager[key].page < totalPages(key)) {
      pager[key].page++;
      load().catch(showError);
    }
  };
}
async function loadUsers() {
  const state = pager.users;
  const res = await api(
    "/api/admin/users?q=" +
      encodeURIComponent($("userSearch").value) +
      "&page=" +
      state.page,
  );
  state.total = res.total;
  $("usersRows").replaceChildren();
  for (const u of res.items) {
    const row = element("tr");
    td(row, u.id);
    td(row, u.name || "-");
    td(row, u.email || "设备账号");
    // 注册来源 IP：历史账号未记录，显示“-”。
    td(row, u.ip || "-");
    td(row, u.credits);
    const add = element("input", {
      type: "number",
      min: "0",
      max: "10000",
      value: "0",
      ariaLabel: "增加次数",
    });
    const addCell = element("td");
    addCell.append(add);
    row.append(addCell);
    const check = element("input", {
      type: "checkbox",
      checked: u.disabled,
      ariaLabel: "停用账号",
    });
    const checkCell = element("td");
    checkCell.append(check);
    row.append(checkCell);
    td(row, timeText(u.created));
    const button = element("button", { className: "secondary" }, "保存");
    button.onclick = () =>
      busyButton(button, async () => {
        await api("/api/admin/users", {
          id: u.id,
          add: Number(add.value),
          disabled: check.checked,
        });
        toast("账号已更新");
        await loadUsers();
      });
    const cell = element("td", { className: "operation" });
    cell.append(button);
    row.append(cell);
    $("usersRows").append(row);
  }
  renderPager("users");
}
async function loadCodes() {
  const state = pager.codes;
  const res = await api("/api/admin/codes?page=" + state.page);
  state.total = res.total;
  $("codesRows").replaceChildren();
  for (const c of res.items) {
    const row = element("tr", { className: "code-row-" + c.status });
    for (const v of [c.id.slice(0, 12), c.label, c.credits]) td(row, v);
    statusCell(
      row,
      { unredeemed: "未兑换", marked: "已Mark", redeemed: "已兑换" }[c.status] || c.status,
      c.status,
    );
    for (const v of [
      c.usedBy || "—",
      c.usedByName || "—",
      timeText(c.created),
    ])
      td(row, v);
    const operation = element("td", { className: "operation" });
    const buttons = element("div", { className: "code-operation-buttons" });
    const copy = element(
      "button",
      { type: "button", className: "secondary" },
      "复制",
    );
    copy.onclick = () =>
      busyButton(copy, async () => {
        if (await copyCode(c)) toast("兑换码已复制");
      });
    buttons.append(copy);
    if (c.status !== "redeemed") {
      const marked = c.status !== "marked";
      const button = element(
        "button",
        {
          className: marked ? "secondary" : "secondary marked-button",
          type: "button",
        },
        marked ? "Mark" : "已Mark · 取消",
      );
      button.onclick = () =>
        busyButton(button, async () => {
          if (marked && !(await copyCode(c))) return;
          await api("/api/admin/codes/mark", { id: c.id, marked });
          toast(marked ? "已复制并Mark，兑换码仍可正常兑换" : "已取消Mark");
          await loadCodes();
        });
      buttons.append(button);
    }
    operation.append(buttons);
    if (!c.copyAvailable)
      operation.append(
        element("small", { className: "hint" }, "旧码需补录原码后复制"),
      );
    row.append(operation);
    $("codesRows").append(row);
  }
  renderPager("codes");
}
async function copyCode(c) {
  if (!c.copyAvailable) {
    if (!(await restoreLegacyCode(c))) return false;
  }
  const result = await api("/api/admin/codes/copy", { id: c.id });
  if (await writeClipboard(result.code)) return true;
  return manualCopy(result.code);
}

function restoreLegacyCode(c) {
  return new Promise((resolve) => {
    const dialog = $("restoreCodeDialog");
    const form = $("restoreCodeForm");
    const field = $("restoreCodeInput");
    const error = $("restoreCodeError");
    const submit = $("restoreCodeSubmit");
    const cancel = $("restoreCodeCancel");
    let saved = false,
      saving = false;
    $("restoreCodeID").textContent = c.id.slice(0, 12);
    field.value = "";
    error.hidden = true;
    submit.disabled = cancel.disabled = false;
    cancel.onclick = () => dialog.close();
    dialog.oncancel = (event) => {
      if (saving) event.preventDefault();
    };
    dialog.onclose = () => {
      field.value = "";
      form.onsubmit = dialog.onclose = dialog.oncancel = null;
      resolve(saved);
    };
    form.onsubmit = async (event) => {
      event.preventDefault();
      if (saving) return;
      saving = true;
      submit.disabled = cancel.disabled = true;
      error.hidden = true;
      try {
        await api("/api/admin/codes/restore", { id: c.id, code: field.value });
        c.copyAvailable = saved = true;
        dialog.close();
      } catch (e) {
        error.textContent = errorMessage(e);
        error.hidden = false;
      } finally {
        saving = false;
        submit.disabled = cancel.disabled = false;
      }
    };
    dialog.showModal();
    field.focus();
  });
}

async function writeClipboard(code) {
  try {
    await navigator.clipboard.writeText(code);
    return true;
  } catch {
    // 浏览器拒绝异步剪贴板操作时，尝试同页兼容方案；失败不能冒充复制成功。
    const focused = document.activeElement;
    const field = element("textarea", {
      value: code,
      className: "clipboard-buffer",
      readOnly: true,
    });
    document.body.append(field);
    field.select();
    let copied = false;
    try {
      copied = document.execCommand("copy");
    } catch {
      copied = false;
    } finally {
      field.remove();
      focused?.focus();
    }
    return copied;
  }
}

function manualCopy(code) {
  return new Promise((resolve) => {
    const dialog = $("manualCopyDialog");
    const field = $("manualCopyValue");
    const button = $("manualCopyButton");
    const error = $("manualCopyError");
    const cancel = $("manualCopyCancel");
    let copied = false;
    field.value = code;
    error.hidden = true;
    button.disabled = false;
    cancel.disabled = false;
    cancel.onclick = () => dialog.close();
    dialog.oncancel = (event) => {
      if (button.disabled) event.preventDefault();
    };
    dialog.onclose = () => {
      field.value = "";
      dialog.onclose = null;
      dialog.oncancel = button.onclick = null;
      resolve(copied);
    };
    button.onclick = async () => {
      button.disabled = true;
      cancel.disabled = true;
      // 再次点击提供新的用户操作上下文，兼容限制异步剪贴板的浏览器。
      try {
        copied = await writeClipboard(code);
        if (copied) dialog.close();
        else {
          error.hidden = false;
          field.focus();
          field.select();
        }
      } finally {
        button.disabled = false;
        cancel.disabled = false;
      }
    };
    dialog.showModal();
    field.focus();
    field.select();
  });
}
async function loadAudit() {
  const state = pager.audit;
  const res = await api("/api/admin/audit?page=" + state.page);
  state.total = res.total;
  $("auditRows").replaceChildren();
  for (const e of res.items) {
    const row = element("tr");
    for (const v of [
      timeText(e.created),
      e.actor,
      e.actorName || "-",
      e.event,
      e.target,
    ])
      td(row, v);
    $("auditRows").append(row);
  }
  renderPager("audit");
}

// 兑换记录展示账号获得次数的三类来源：注册赠送、兑换码兑换与后台手动增加。
const creditEventText = {
  register: "注册赠送",
  redeem: "兑换码",
  admin: "后台增加",
};
async function loadCredits() {
  const state = pager.credits;
  const res = await api(
    "/api/admin/credits?q=" +
      encodeURIComponent($("creditSearch").value) +
      "&page=" +
      state.page,
  );
  state.total = res.total;
  $("creditsRows").replaceChildren();
  for (const h of res.items) {
    const row = element("tr");
    for (const v of [
      timeText(h.created),
      creditEventText[h.event] || h.event,
      h.user,
      h.userName || "-",
      "+" + h.credits,
      h.note || "—",
    ])
      td(row, v);
    $("creditsRows").append(row);
  }
  renderPager("credits");
}

function jobDurationText(seconds) {
  const s = Math.max(0, seconds || 0);
  const minutes = Math.floor(s / 60);
  return minutes ? `${minutes}分${s % 60}秒` : `${s}秒`;
}
function actionName(kind, action) {
  if (kind !== "motion" || !settings) return "—";
  for (const c of settings.categories || [])
    for (const a of c.actions || []) if (a.id === action) return a.name;
  return action || "—";
}
// 任务列表只展示排队与进行中的任务，完成或失败后自动从列表消失。
async function loadJobs() {
  const state = pager.jobs;
  const res = await api("/api/admin/jobs?page=" + state.page);
  state.total = res.total;
  $("jobsRows").replaceChildren();
  $("jobsEmpty").hidden = res.items.length > 0;
  for (const j of res.items) {
    const row = element("tr");
    const label =
      j.status === "queued"
        ? "排队中"
        : j.status === "running"
          ? "进行中"
          : j.status;
    statusCell(row, label, j.status);
    td(row, j.kind === "draft" ? "角色定稿" : "动作图");
    td(row, actionName(j.kind, j.action));
    td(row, j.user);
    td(row, j.userName || "-");
    td(row, timeText(j.created));
    td(row, jobDurationText(j.elapsedSeconds));
    const estimate = j.estimate;
    td(
      row,
      estimate
        ? estimate.samples
          ? `约${jobDurationText(estimate.seconds)}（${estimate.samples}次成功样本，实测最短${jobDurationText(estimate.minSeconds)}、最长${jobDurationText(estimate.maxSeconds)}）`
          : `约${jobDurationText(estimate.seconds)}（初始估计，参考${jobDurationText(estimate.lowSeconds)}～${jobDurationText(estimate.highSeconds)}）`
        : "—",
    );
    $("jobsRows").append(row);
  }
  renderPager("jobs");
}
// 任务历史展示所有账号的全部任务（含完成、失败与中断），失败/中断任务在最后一列显示原因。
const historyStatusText = {
  queued: "排队中",
  running: "进行中",
  pending_upstream: "等待上游",
  succeeded: "已完成",
  failed: "失败",
  interrupted: "已中断",
};
async function loadHistory() {
  const state = pager.history;
  const res = await api("/api/admin/jobs/history?page=" + state.page);
  state.total = res.total;
  $("historyRows").replaceChildren();
  $("historyEmpty").hidden = res.items.length > 0;
  for (const j of res.items) {
    const row = element("tr");
    statusCell(row, historyStatusText[j.status] || j.status, j.status);
    td(row, j.kind === "draft" ? "角色定稿" : "动作图");
    td(row, actionName(j.kind, j.action));
    td(row, j.user);
    td(row, j.userName || "-");
    td(row, timeText(j.created));
    td(row, String(j.cost || 0));
    const failed = j.status === "failed" || j.status === "interrupted";
    const reason = td(row, failed ? j.reason || "—（未记录原因）" : "—");
    if (failed) reason.className = "warning-text";
    $("historyRows").append(row);
  }
  renderPager("history");
}
let galleryImages = [];
let galleryLoad = 0;
function galleryImageSize(image) {
  const label = element("p", { hidden: true });
  image.addEventListener("load", () => {
    if (!image.naturalWidth || !image.naturalHeight) return;
    label.textContent = `原始尺寸：${image.naturalWidth} × ${image.naturalHeight} px`;
    label.hidden = false;
  });
  return label;
}
function galleryCard(item, kind) {
  const card = element("article", { className: "admin-gallery-card" });
  if (item.imageURL) {
    const image = element("img", {
      loading: "lazy",
      decoding: "async",
      alt: kind === "draft" ? "角色定稿图" : item.hasGif ? "动作 GIF" : "动作原图",
    });
    card.append(image, galleryImageSize(image));
    galleryImages.push(watchMediaImage(image, item.imageURL, card));
  } else {
    card.append(element("div", { className: "admin-gallery-missing" }, "图片文件不存在"));
  }
  if (kind === "work" && item.hasGif) {
    const source = element("div", { className: "admin-gallery-source" });
    source.append(element("p", {}, item.hasSheet ? "动作序列原图（点击查看原尺寸）" : "动作序列原图：未保存"));
    if (item.hasSheet) {
      const sourceURL = `/api/admin/gallery/works/${encodeURIComponent(item.id)}/sheet`;
      const link = element("a", {
        href: sourceURL,
        target: "_blank",
        rel: "noopener noreferrer",
        title: "查看动作序列原图原尺寸",
      });
      const image = element("img", {
        loading: "lazy",
        decoding: "async",
        alt: "生成此 GIF 的动作序列原图",
      });
      link.append(image);
      source.append(link, galleryImageSize(image));
      galleryImages.push(watchMediaImage(image, sourceURL, source));
    }
    card.append(source);
  }
  card.append(element("h3", {}, kind === "draft" ? "角色定稿" : item.name || item.action || "动作作品"));
  const meta = [`账号：${item.userName || item.user}`, `时间：${timeText(item.created)}`];
  if (kind === "draft") {
    const s = item.selection || {};
    meta.push(`造型：${s.category || "—"} · ${s.style || "default"}`);
    meta.push(`服装：${s.clothes || "—"} · 配色：${s.color || "—"}`);
    if (s.weapon) meta.push(`武器：${s.weapon}`);
    meta.push(`编号：${item.id}`);
  } else {
    meta.push(`分类：${item.category || "—"} · 动作：${item.action || "—"}`);
    meta.push(item.hasGif ? "格式：GIF 动图" : "格式：仅动作原图（GIF 合成缺失）");
    meta.push(`编号：${item.id}`);
  }
  for (const line of meta) card.append(element("p", { title: line }, line));
  return card;
}
async function loadGallery() {
  const loading = ++galleryLoad;
  const state = pager.gallery;
  const q = encodeURIComponent($("gallerySearch").value.trim());
  const res = await api(`/api/admin/gallery?page=${state.page}&q=${q}`);
  if (loading !== galleryLoad) return;
  for (const dispose of galleryImages) dispose();
  galleryImages = [];
  state.total = Math.max(res.draftsTotal || 0, res.worksTotal || 0);
  $("galleryDraftCount").textContent = `（${res.draftsTotal || 0} 张）`;
  $("galleryWorkCount").textContent = `（${res.worksTotal || 0} 件）`;
  const drafts = res.drafts || [];
  const works = res.works || [];
  $("galleryDrafts").replaceChildren(...drafts.map((item) => galleryCard(item, "draft")));
  $("galleryDraftsEmpty").hidden = drafts.length > 0;
  $("galleryWorks").replaceChildren(...works.map((item) => galleryCard(item, "work")));
  $("galleryWorksEmpty").hidden = works.length > 0;
  const pages = Math.max(1, Math.ceil(state.total / 100));
  state.page = Math.min(state.page, pages);
  $("galleryPageInfo").textContent = `第 ${state.page} / ${pages} 页 · 定稿 ${res.draftsTotal || 0} 张 · 作品 ${res.worksTotal || 0} 件`;
  $("galleryPrev").disabled = state.page <= 1;
  $("galleryNext").disabled = state.page >= pages;
}
// 数据看板：汇总核心运营指标（新增用户、消耗次数、本日分布、兑换码与 TOP 榜单）。
function dashRow(label, value, muted = "") {
  const row = element("div", { className: "dash-mini" });
  row.append(element("span", { className: "dash-label" }, label));
  const valueWrap = element("span", { className: "dash-value" });
  valueWrap.append(element("b", { title: String(value ?? 0) }, String(value ?? 0)));
  if (muted) valueWrap.append(element("small", { title: muted }, muted));
  row.append(valueWrap);
  return row;
}
function dashCard(title, children) {
  const card = element("section", { className: "dash-card" });
  card.append(element("h3", {}, title));
  for (const child of children) card.append(child);
  return card;
}
function dashTable(head, rows) {
  const wrap = element("div", { className: "admin-table-wrap" });
  const table = element("table", { className: "admin-table" });
  const thead = element("thead");
  const headRow = element("tr");
  for (const h of head) headRow.append(element("th", {}, h));
  thead.append(headRow);
  const tbody = element("tbody");
  for (const cells of rows) {
    const row = element("tr");
    for (const cell of cells) td(row, cell);
    tbody.append(row);
  }
  table.append(thead, tbody);
  wrap.append(table);
  return wrap;
}
function dashAccount(name, user) {
  return name || user || "—";
}
async function loadDashboard() {
  const d = await api("/api/admin/dashboard");
  const host = $("dashContent");
  host.replaceChildren();
  const ranges = d.ranges || {};
  const codes = d.codes || {};

  const metrics = element("div", { className: "dash-cards" });
  metrics.append(
    dashCard("新增用户", [
      dashRow("本月", ranges.month?.newUsers),
      dashRow("本周", ranges.week?.newUsers),
      dashRow("本日", ranges.day?.newUsers),
    ]),
    dashCard("消耗次数", [
      dashRow("本月", ranges.month?.consumed),
      dashRow("本周", ranges.week?.consumed),
      dashRow("本日", ranges.day?.consumed),
      dashRow("累计", d.totalConsumed),
    ]),
    dashCard("兑换码", [
      dashRow("兑换码个数", codes.total),
      dashRow("已兑换", codes.redeemed, `累计 ${codes.redeemedCredits ?? 0} 次`),
      dashRow("未兑换", codes.unredeemed),
      dashRow("累计次数", codes.credits),
    ]),
  );
  host.append(metrics);

  const dist = element("div", { className: "dash-split" });
  dist.append(
    dashCard("本日消耗 · 任务类型", [
      dashTable(
        ["类型", "次数"],
        (d.today?.kinds || []).map((k) => [k.name, k.count]),
      ),
    ]),
    dashCard("本日消耗 · 人物分类", [
      dashTable(
        ["分类", "合计", "定稿图 / GIF 动图"],
        (d.today?.categories || []).map((c) => [
          c.name,
          c.draft + c.motion,
          `定稿图 ${c.draft} · GIF ${c.motion}`,
        ]),
      ),
    ]),
  );
  host.append(dist);

  const tops = element("div", { className: "dash-split" });
  tops.append(
    dashCard("TOP5 · 消耗次数", [
      dashTable(
        ["排名", "账号", "消耗次数"],
        (d.topConsume || []).map((r, i) => [
          i + 1,
          dashAccount(r.name, r.user),
          r.count,
        ]),
      ),
    ]),
    dashCard("TOP5 · 兑换次数", [
      dashTable(
        ["排名", "账号", "累计次数", "兑换码个数"],
        (d.topRedeem || []).map((r, i) => [
          i + 1,
          dashAccount(r.name, r.user),
          r.credits,
          r.count,
        ]),
      ),
    ]),
  );
  host.append(tops);
}

async function loadDeploymentVersion() {
  $("deploymentOutput").value = "正在读取...";
  const result = await api("/api/admin/deployment-version");
  $("deploymentOutput").value = result.output;
}
// 页签切换：每个面板独立展示，切换时才加载对应列表数据。
const tabLoaders = {
  dashSection: loadDashboard,
  gallerySection: loadGallery,
  jobSection: loadJobs,
  historySection: loadHistory,
  codeSection: loadCodes,
  creditSection: loadCredits,
  userSection: loadUsers,
  auditSection: loadAudit,
  deploymentSection: loadDeploymentVersion,
};
for (const wrap of document.querySelectorAll(".admin-panel > .admin-table-wrap")) {
  wrap.before(element(
    "p",
    { className: "admin-table-hint" },
    "左右滑动查看全部字段 · 点按数据查看完整内容",
  ));
  wrap.tabIndex = 0;
  wrap.setAttribute("role", "region");
  wrap.setAttribute(
    "aria-label",
    wrap.closest(".admin-panel").querySelector("h2").textContent + "，可横向滚动",
  );
}
function showTab(id) {
  for (const b of $("adminTabs").children) {
    b.classList.toggle("active", b.dataset.tab === id);
    b.setAttribute("aria-pressed", String(b.dataset.tab === id));
  }
  for (const section of document.querySelectorAll(".admin-panel"))
    section.hidden = section.id !== id;
  const loader = tabLoaders[id];
  if (loader) loader().catch(showError);
}
for (const b of $("adminTabs").children)
  b.onclick = () => showTab(b.dataset.tab);
bindPager("jobs", loadJobs);
bindPager("history", loadHistory);
bindPager("codes", loadCodes);
bindPager("credits", loadCredits);
bindPager("users", loadUsers);
bindPager("audit", loadAudit);
$("galleryPrev").onclick = () => {
  if (pager.gallery.page > 1) {
    pager.gallery.page--;
    loadGallery().catch(showError);
  }
};
$("galleryNext").onclick = () => {
  pager.gallery.page++;
  loadGallery().catch(showError);
};
$("refreshGallery").onclick = () => loadGallery().catch(showError);
let gallerySearchTimer;
$("gallerySearch").oninput = () => {
  pager.gallery.page = 1;
  clearTimeout(gallerySearchTimer);
  gallerySearchTimer = setTimeout(() => loadGallery().catch(showError), 250);
};
async function ready() {
  $("loginSection").hidden = true;
  $("adminContent").hidden = false;
  $("adminLogout").hidden = false;
  // 先加载配置，任务列表里的动作名称映射依赖最新的分类设置。
  await loadSettings();
  // 内容拆分为独立页签，进入时只加载当前页签的数据，其余在切换时加载。
  showTab("dashSection");
}
$("loginForm").onsubmit = (e) => {
  e.preventDefault();
  busyButton($("loginBtn"), async () => {
    const s = await api("/api/admin/login", {
      password: $("adminPassword").value,
    });
    setSession(s);
    $("adminPassword").value = "";
    await ready();
  });
};
$("settingsForm").onsubmit = (e) => {
  e.preventDefault();
  const buttons = [...e.target.querySelectorAll('button[type="submit"]')];
  buttons.forEach((b) => (b.disabled = true));
  saveSettings()
    .catch(showError)
    .finally(() => buttons.forEach((b) => (b.disabled = false)));
};
$("codesForm").onsubmit = (e) => {
  e.preventDefault();
  busyButton($("createCodesBtn"), async () => {
    const s = await api("/api/admin/codes", {
      count: Number($("codeCount").value),
      credits: Number($("codeCredits").value),
      label: $("codeLabel").value,
    });
    codeText =
      "编号\t兑换码\n" +
      s.items.map((item) => `${item.id.slice(0, 12)}\t${item.code}`).join("\n");
    $("newCodes").value = codeText;
    $("newCodesSection").hidden = false;
    toast("兑换码已创建，请立即下载保存");
    await loadCodes();
  });
};
$("downloadCodes").onclick = () =>
  download(
    new Blob([codeText], { type: "text/plain;charset=utf-8" }),
    "gif-兑换码-" + new Date().toISOString().slice(0, 10) + ".txt",
  );
$("searchForm").onsubmit = (e) => {
  e.preventDefault();
  pager.users.page = 1;
  loadUsers().catch(showError);
};
$("creditSearchForm").onsubmit = (e) => {
  e.preventDefault();
  pager.credits.page = 1;
  loadCredits().catch(showError);
};
// 刷新按钮点击反馈：按下有缩放动画，刷新中禁用并显示“刷新中…”，
// 完成后短暂显示“✓ 已刷新 时间”，让手动刷新是否生效一目了然。
function bindRefreshButton(button, idle, busy, load) {
  let timer;
  button.onclick = () => {
    if (button.disabled) return;
    clearTimeout(timer);
    button.disabled = true;
    button.textContent = busy;
    button.animate(
      [{ transform: "scale(0.9)" }, { transform: "scale(1)" }],
      { duration: 160, easing: "ease-out" },
    );
    const restore = () => {
      button.disabled = false;
      button.textContent = idle;
    };
    load()
      .then(() => {
        button.textContent = "✓ 已刷新 " + new Date().toTimeString().slice(0, 8);
        timer = setTimeout(restore, 2000);
      })
      .catch((error) => {
        restore();
        showError(error);
      });
  };
}
bindRefreshButton($("refreshAudit"), "刷新记录", "刷新中…", loadAudit);
bindRefreshButton($("refreshDash"), "↻ 立即刷新", "↻ 刷新中…", loadDashboard);
$("sendDashEmail").onclick = () =>
  busyButton($("sendDashEmail"), async () => {
    const old = $("sendDashEmail").textContent;
    $("sendDashEmail").textContent = "✉ 发送中…";
    try {
      const result = await api("/api/admin/dashboard/email", {});
      toast(`数据看板已发送至 ${result.recipient}`);
    } finally {
      $("sendDashEmail").textContent = old;
    }
  });
bindRefreshButton($("refreshJobs"), "↻ 立即刷新", "↻ 刷新中…", loadJobs);
bindRefreshButton(
  $("refreshHistory"),
  "↻ 立即刷新",
  "↻ 刷新中…",
  loadHistory,
);
bindRefreshButton($("refreshCredits"), "↻ 立即刷新", "↻ 刷新中…", loadCredits);
bindRefreshButton($("refreshUsers"), "↻ 立即刷新", "↻ 刷新中…", loadUsers);
bindRefreshButton(
  $("refreshDeployment"),
  "↻ 立即刷新",
  "↻ 刷新中…",
  loadDeploymentVersion,
);
// 任务列表自动刷新：仅在页面可见、任务页签展示中且已登录管理时轮询。
setInterval(() => {
  if (
    document.visibilityState === "visible" &&
    !$("adminContent").hidden &&
    !$("jobSection").hidden &&
    settings
  )
    loadJobs().catch(() => {});
}, 8000);
$("adminLogout").onclick = () =>
  busyButton($("adminLogout"), async () => {
    await api("/api/logout", {});
    location.reload();
  });
connect()
  .then((s) => {
    if (s.admin) return ready();
  })
  .catch(showError);

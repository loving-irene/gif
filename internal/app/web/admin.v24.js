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
} from "./common.v4.js";
let settings,
  editors = [],
  styleEditors = [],
  codeText = "";
const fields = [
  "defaultCredits",
  "redeemHelp",
  "userConcurrency",
  "apiBase",
  "model",
  "quality",
  "motionGrid",
  "mailHost",
  "mailPort",
  "mailUser",
  "mailFrom",
  "feedbackEmail",
  "identityPrompt",
  "draftPrompt",
  "motionPrompt",
];
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
  $("chargeOnFailure").value = String(settings.chargeOnFailure);
  $("assetHosts").value = settings.assetHosts.join("\n");
  $("keyState").textContent = s.apiKeySet ? "已保存密钥（不回显）" : "尚未配置";
  $("mailState").textContent = s.mailPasswordSet
    ? "已保存授权码（不回显）"
    : "尚未配置";
  $("apiKey").value = "";
  $("mailPassword").value = "";
  // 画风提示词固定为默认、Q版、水墨风格三项，编号、名称、说明与图标由服务端提供。
  $("styleEditors").replaceChildren();
  styleEditors = [];
  for (const s of settings.styles || []) {
    const prompt = labelInput(
      `画风提示词 · ${s.icon ? s.icon + " " : ""}${s.name}${s.subtitle ? `（${s.subtitle}）` : ""}`,
      s.prompt,
      true,
    );
    $("styleEditors").append(prompt.label);
    styleEditors.push({ style: s, prompt: prompt.input });
  }
  $("categoryEditors").replaceChildren();
  editors = [];
  for (const c of settings.categories) {
    const details = element("details");
    details.append(element("summary", {}, `${c.icon} ${c.name} · 造型与动作`));
    const prompt = labelInput("角色风格提示词", c.prompt, true);
    details.append(prompt.label);
    const clothes = labelInput(
      "服装选项（每行一个）",
      c.clothes.join("\n"),
      true,
    );
    const colors = labelInput(
      "配色选项（每行一个）",
      c.colors.join("\n"),
      true,
    );
    details.append(clothes.label, colors.label);
    let weapons = null;
    if (c.id === "male") {
      weapons = labelInput("武器选项（每行一个）", c.weapons.join("\n"), true);
      details.append(weapons.label);
    }
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
    editors.push({
      c,
      prompt: prompt.input,
      clothes: clothes.input,
      colors: colors.input,
      weapons: weapons?.input,
      actions,
    });
  }
}
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
  if (styleEditors.length) {
    for (const editor of styleEditors) {
      if (!editor.prompt.value.trim()) {
        editor.prompt.focus();
        throw userNotice(`「${editor.style.name}」的画风提示词不能为空`);
      }
    }
    value.styles = styleEditors.map((e) => ({
      ...e.style,
      prompt: e.prompt.value.trim(),
    }));
  }
  value.categories = editors.map((e) => ({
    ...e.c,
    prompt: e.prompt.value,
    clothes: lines(e.clothes.value),
    colors: lines(e.colors.value),
    weapons: e.weapons ? lines(e.weapons.value) : [],
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
  row.append(element("td", {}, text));
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
    for (const v of [
      c.id.slice(0, 12),
      c.label,
      c.credits,
      { unredeemed: "未兑换", marked: "已Mark", redeemed: "已兑换" }[c.status],
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
    td(row, label);
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
    td(row, historyStatusText[j.status] || j.status);
    td(row, j.kind === "draft" ? "角色定稿" : "动作图");
    td(row, actionName(j.kind, j.action));
    td(row, j.user);
    td(row, j.userName || "-");
    td(row, timeText(j.created));
    td(row, String(j.cost || 0));
    const failed = j.status === "failed" || j.status === "interrupted";
    const reason = element("td", {
      className: failed ? "warning-text" : "",
      title: j.reason || "",
    });
    reason.textContent = failed ? j.reason || "—（未记录原因）" : "—";
    row.append(reason);
    $("historyRows").append(row);
  }
  renderPager("history");
}
// 数据看板：汇总核心运营指标（新增用户、消耗次数、本日分布、兑换码与 TOP 榜单）。
function dashRow(label, value, muted = "") {
  const row = element("div", { className: "dash-mini" });
  row.append(element("span", { className: "dash-label" }, label));
  const valueWrap = element("span", { className: "dash-value" });
  valueWrap.append(element("b", {}, String(value ?? 0)));
  if (muted) valueWrap.append(element("small", {}, muted));
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
    for (const cell of cells) row.append(element("td", {}, String(cell ?? "—")));
    tbody.append(row);
  }
  table.append(thead, tbody);
  wrap.append(table);
  return wrap;
}
function dashAccount(name, user) {
  return name || (user ? user.slice(0, 12) : "—");
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
// 页签切换：每个面板独立展示，切换时才加载对应列表数据。
const tabLoaders = {
  dashSection: loadDashboard,
  jobSection: loadJobs,
  historySection: loadHistory,
  codeSection: loadCodes,
  creditSection: loadCredits,
  userSection: loadUsers,
  auditSection: loadAudit,
};
function showTab(id) {
  for (const b of $("adminTabs").children)
    b.classList.toggle("active", b.dataset.tab === id);
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
bindRefreshButton($("refreshJobs"), "↻ 立即刷新", "↻ 刷新中…", loadJobs);
bindRefreshButton(
  $("refreshHistory"),
  "↻ 立即刷新",
  "↻ 刷新中…",
  loadHistory,
);
bindRefreshButton($("refreshCredits"), "↻ 立即刷新", "↻ 刷新中…", loadCredits);
bindRefreshButton($("refreshUsers"), "↻ 立即刷新", "↻ 刷新中…", loadUsers);
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

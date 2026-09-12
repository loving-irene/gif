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
} from "./common.v2.js";
let settings,
  editors = [],
  codeText = "";
const fields = [
  "defaultCredits",
  "redeemHelp",
  "registrationDailyLimit",
  "userConcurrency",
  "apiBase",
  "model",
  "quality",
  "mailHost",
  "mailPort",
  "mailUser",
  "mailFrom",
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
  value.registrationDailyLimit = Number(value.registrationDailyLimit);
  value.userConcurrency = Number(value.userConcurrency);
  value.chargeOnFailure = $("chargeOnFailure").value === "true";
  value.assetHosts = lines($("assetHosts").value);
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
// 各列表统一展示创建时间；缺失或无效时显示占位符，避免出现 Invalid Date。
function timeText(seconds) {
  return seconds ? new Date(seconds * 1000).toLocaleString("zh-CN") : "—";
}
// 各列表统一分页，页大小与后端约定为 100。
const pager = {
  jobs: { page: 1, total: 0 },
  codes: { page: 1, total: 0 },
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
    const cell = element("td");
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
    const operation = element("td");
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
        ? `约${jobDurationText(estimate.seconds)}（${jobDurationText(estimate.lowSeconds)}～${jobDurationText(estimate.highSeconds)}）`
        : "—",
    );
    $("jobsRows").append(row);
  }
  renderPager("jobs");
}
// 页签切换：每个面板独立展示，切换时才加载对应列表数据。
const tabLoaders = {
  jobSection: loadJobs,
  codeSection: loadCodes,
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
bindPager("codes", loadCodes);
bindPager("users", loadUsers);
bindPager("audit", loadAudit);
async function ready() {
  $("loginSection").hidden = true;
  $("adminContent").hidden = false;
  $("adminLogout").hidden = false;
  // 先加载配置，任务列表里的动作名称映射依赖最新的分类设置。
  await loadSettings();
  // 内容拆分为独立页签，进入时只加载当前页签的数据，其余在切换时加载。
  showTab("jobSection");
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
$("refreshAudit").onclick = () => loadAudit().catch(showError);
$("refreshJobs").onclick = () => loadJobs().catch(showError);
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

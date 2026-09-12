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
async function loadUsers() {
  const users = await api(
    "/api/admin/users?q=" + encodeURIComponent($("userSearch").value),
  );
  $("usersRows").replaceChildren();
  for (const u of users) {
    const row = element("tr");
    td(row, u.id);
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
}
async function loadCodes() {
  const codes = await api("/api/admin/codes");
  $("codesRows").replaceChildren();
  for (const c of codes) {
    const row = element("tr", { className: "code-row-" + c.status });
    for (const v of [
      c.id.slice(0, 12),
      c.label,
      c.credits,
      { unredeemed: "未兑换", marked: "已Mark", redeemed: "已兑换" }[c.status],
      c.usedBy || "—",
      new Date(c.created * 1000).toLocaleString("zh-CN"),
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
  const events = await api("/api/admin/audit");
  $("auditRows").replaceChildren();
  for (const e of events) {
    const row = element("tr");
    for (const v of [
      new Date(e.created * 1000).toLocaleString("zh-CN"),
      e.actor,
      e.event,
      e.target,
    ])
      td(row, v);
    $("auditRows").append(row);
  }
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
  const jobs = await api("/api/admin/jobs");
  $("jobsRows").replaceChildren();
  $("jobsEmpty").hidden = jobs.length > 0;
  for (const j of jobs) {
    const row = element("tr");
    const state =
      j.status === "queued"
        ? "排队中"
        : j.status === "running"
          ? "进行中"
          : j.status;
    td(row, state);
    td(row, j.kind === "draft" ? "角色定稿" : "动作图");
    td(row, actionName(j.kind, j.action));
    td(row, j.user);
    td(row, new Date(j.created * 1000).toLocaleString("zh-CN"));
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
}
async function ready() {
  $("loginSection").hidden = true;
  $("adminContent").hidden = false;
  $("adminLogout").hidden = false;
  // 先加载配置，任务列表里的动作名称映射依赖最新的分类设置。
  await loadSettings();
  await Promise.all([loadUsers(), loadCodes(), loadAudit(), loadJobs()]);
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
  loadUsers().catch(showError);
};
$("refreshAudit").onclick = () => loadAudit().catch(showError);
$("refreshJobs").onclick = () => loadJobs().catch(showError);
// 任务列表自动刷新：仅在页面可见且已登录管理时轮询。
setInterval(() => {
  if (
    document.visibilityState === "visible" &&
    !$("adminContent").hidden &&
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

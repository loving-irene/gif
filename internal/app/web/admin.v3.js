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
} from "./common.v1.js";
let settings,
  editors = [],
  codeText = "";
const fields = [
  "defaultCredits",
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
        throw new Error(`${editor.c.name}分类的动作名称和动作过程都需要填写`);
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
        await copyCode(c);
        toast("兑换码已复制");
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
        marked ? "Mark 并复制" : "已Mark · 取消",
      );
      button.onclick = () =>
        busyButton(button, async () => {
          if (marked) await copyCode(c);
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
    const code = prompt(
      `旧码 ${c.id.slice(0, 12)} 仅有摘要，请粘贴导出清单中的对应原码：`,
    );
    if (!code) throw new Error("已取消，未复制或更改Mark状态");
    await api("/api/admin/codes/restore", { id: c.id, code });
    c.copyAvailable = true;
  }
  const result = await api("/api/admin/codes/copy", { id: c.id });
  try {
    await navigator.clipboard.writeText(result.code);
    return;
  } catch {
    // 浏览器拒绝异步剪贴板操作时，尝试同页兼容方案；失败不能冒充复制成功。
    const focused = document.activeElement;
    const field = element("textarea", {
      value: result.code,
      className: "clipboard-buffer",
      readOnly: true,
    });
    document.body.append(field);
    field.select();
    let copied = false;
    try {
      copied = document.execCommand("copy");
    } finally {
      field.remove();
      focused?.focus();
    }
    if (!copied)
      throw new Error(
        "浏览器未允许复制，请允许剪贴板权限后重试；Mark状态未改变",
      );
  }
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
async function ready() {
  $("loginSection").hidden = true;
  $("adminContent").hidden = false;
  $("adminLogout").hidden = false;
  await Promise.all([loadSettings(), loadUsers(), loadCodes(), loadAudit()]);
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

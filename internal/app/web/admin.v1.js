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
    for (const action of c.actions) {
      const name = labelInput("动作名称 · " + action.id, action.name);
      const desc = labelInput("动作过程", action.prompt, true);
      details.append(name.label, desc.label);
      actions.push({ action, name: name.input, prompt: desc.input });
    }
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
function lines(v) {
  return v
    .split("\n")
    .map((x) => x.trim())
    .filter(Boolean);
}
async function saveSettings() {
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
      name: a.name.value,
      prompt: a.prompt.value,
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
    const row = element("tr");
    for (const v of [
      c.label,
      c.credits,
      c.usedBy ? "已使用" : "未使用 · 永久有效",
      c.usedBy || "—",
      new Date(c.created * 1000).toLocaleString("zh-CN"),
    ])
      td(row, v);
    $("codesRows").append(row);
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
    codeText = s.codes.join("\n");
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

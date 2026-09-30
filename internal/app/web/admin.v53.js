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
  mediaResponse,
} from "./common.v5.js";
let settings,
  editors = [],
  codeText = "",
  providers = [];
const fields = [
  "defaultCredits",
  "redeemHelp",
  "userConcurrency",
  "serverSlots",
  "apiBase",
  "model",
  "quality",
  "motionGrid",
  "mailUser",
  "feedbackEmail",
  "draftPrompt",
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
function normalizeBase(url) {
  return String(url || "").trim().replace(/\/+$/, "");
}
function findProvider(apiBase) {
  const base = normalizeBase(apiBase);
  return providers.find((p) => normalizeBase(p.apiBase) === base) || providers[0];
}
function fillProviderSelect(selectedId) {
  const node = $("provider");
  if (!node) return;
  node.replaceChildren();
  for (const p of providers) {
    const opt = element("option", { value: p.id }, p.name);
    if (p.id === selectedId) opt.selected = true;
    node.append(opt);
  }
}
function fillModelSelect(models, selected) {
  const node = $("model");
  node.replaceChildren();
  let matched = false;
  for (const m of models || []) {
    const label = m.vendor ? `${m.vendor} · ${m.name}` : m.name || m.id;
    const opt = element("option", { value: m.id }, label);
    if (m.id === selected) {
      opt.selected = true;
      matched = true;
    }
    node.append(opt);
  }
  if (!matched && node.options.length) node.selectedIndex = 0;
}
function applyProvider(p, { fillHosts = false } = {}) {
  if (!p) return;
  $("apiBase").value = p.apiBase;
  fillModelSelect(p.models, settings?.model);
  settings = {
    ...(settings || {}),
    apiBase: p.apiBase,
    model: $("model").value,
  };
  if (fillHosts && Array.isArray(p.assetHosts) && p.assetHosts.length) {
    $("assetHosts").value = p.assetHosts.join("\n");
  }
}
function sameHostList(a, b) {
  const norm = (list) =>
    (list || [])
      .map((x) => String(x).trim().toLowerCase())
      .filter(Boolean)
      .sort()
      .join("|");
  return norm(a) === norm(b);
}
function wireProviderControls() {
  $("provider").onchange = () => {
    const p = providers.find((x) => x.id === $("provider").value);
    if (!p) return;
    const prev = findProvider(settings?.apiBase);
    const currentHosts = lines($("assetHosts").value);
    const fillHosts =
      !currentHosts.length ||
      (prev && sameHostList(currentHosts, prev.assetHosts));
    settings = { ...(settings || {}), apiBase: p.apiBase };
    applyProvider(p, { fillHosts });
  };
  $("apiBase").onchange = () => {
    const p = findProvider($("apiBase").value);
    if (!p) return;
    $("provider").value = p.id;
    fillModelSelect(p.models, $("model").value);
  };
}
async function loadSettings() {
  const s = await api("/api/admin/settings");
  settings = s.settings;
  providers = Array.isArray(s.providers) && s.providers.length ? s.providers : [];
  for (const f of fields) {
    if (f === "model" || f === "apiBase") continue;
    if ($(f)) $(f).value = settings[f] ?? "";
  }
  $("mailUser").value = settings.mailUser || settings.mailFrom || "";
  $("chargeOnFailure").value = String(settings.chargeOnFailure);
  $("assetHosts").value = (settings.assetHosts || []).join("\n");
  $("keyState").textContent = s.apiKeySet ? "已保存密钥（不回显）" : "尚未配置";
  $("mailState").textContent = s.mailPasswordSet
    ? "已保存密码（不回显）"
    : "尚未配置";
  $("apiKey").value = "";
  $("mailPassword").value = "";
  const current = findProvider(settings.apiBase);
  fillProviderSelect(current?.id);
  $("apiBase").value = settings.apiBase || current?.apiBase || "";
  fillModelSelect(current?.models || [], settings.model);
  wireProviderControls();
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
      appendActionEditor(actions, actionGrid, action, updateActions, c);
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
        c,
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
function appendActionEditor(actions, grid, action, update, category) {
  const card = element("article", { className: "action-editor-card" });
  const name = labelInput("动作名称", action.name);
  name.input.maxLength = 50;
  name.input.placeholder = "例如：开心挥手";
  const prompt = labelInput("动作过程", action.prompt, true);
  prompt.input.placeholder = "描述准备、动作展开、收势回位的完整过程";
  const footer = element("div", { className: "action-editor-actions" });
  const copy = element(
    "button",
    { type: "button", className: "text-button action-copy-prompt" },
    "复制提示词",
  );
  const remove = element(
    "button",
    { type: "button", className: "text-button action-remove" },
    "删除动作",
  );
  const item = {
    action,
    name: name.input,
    prompt: prompt.input,
    copy,
    remove,
    card,
  };
  copy.onclick = async () => {
    const actionPrompt = prompt.input.value.trim();
    if (!actionPrompt) {
      prompt.input.focus();
      toast("请先填写动作过程");
      return;
    }
    copy.disabled = true;
    try {
      const data = await api("/api/admin/action-prompts", {
        category: category.id,
        actionName: name.input.value.trim(),
        actionPrompt,
        draftPrompt: $("draftPrompt")?.value || "",
        motionGrid: $("motionGrid")?.value || settings?.motionGrid || "5x5",
        clothes: "default",
        color: category.colors?.[0] || "",
        weapon: category.id === "male" ? category.weapons?.[0] || "" : "",
      });
      if (!(await writeClipboard(data.export || ""))) {
        throw userNotice("复制失败，请重试或手动选择文本");
      }
      toast("已复制定稿图与动作序列图完整提示词");
    } catch (e) {
      showError(e);
    } finally {
      copy.disabled = false;
    }
  };
  remove.onclick = () => {
    if (actions.length <= 1) return;
    actions.splice(actions.indexOf(item), 1);
    card.remove();
    update();
  };
  footer.append(copy, remove);
  card.append(name.label, prompt.label, footer);
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
  value.serverSlots = Number(value.serverSlots);
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
// 后台统一展示格式：所有页签都使用「卡片 + 标签/数值行」，与「数据看板 · 新增用户」一致。
// 字段完整展示并自动换行，因此不再有点按单元格弹出「完整内容」弹窗的交互。
const statusTone = {
  queued: "neutral",
  pending_upstream: "neutral",
  unredeemed: "neutral",
  running: "accent",
  marked: "accent",
  succeeded: "success",
  redeemed: "success",
  failed: "warning",
  interrupted: "warning",
};
function badge(text, status) {
  return element(
    "span",
    { className: "admin-badge " + (statusTone[status] || "neutral") },
    text,
  );
}
// 强调数值行：右侧放大加粗，用于次数、条数等关键数字，可附一行灰色补充说明。
function dashRow(label, value, muted = "") {
  const row = element("div", { className: "dash-mini" });
  row.append(element("span", { className: "dash-label" }, label));
  const wrap = element("span", { className: "dash-value" });
  if (value instanceof Node) wrap.append(value);
  else {
    wrap.append(element("b", { title: String(value ?? 0) }, String(value ?? 0)));
    if (muted) wrap.append(element("small", { title: muted }, muted));
  }
  row.append(wrap);
  return row;
}
// 普通字段行：与数值行同样的左右布局，但不放大加粗，长文本（时间、邮箱、失败原因等）自动换行。
function fieldRow(label, value, tone = "") {
  const row = element("div", {
    className: "dash-mini field" + (tone ? " " + tone : ""),
  });
  row.append(element("span", { className: "dash-label" }, label));
  const wrap = element("span", { className: "dash-text" });
  if (value instanceof Node) wrap.append(value);
  else wrap.textContent = String(value ?? "—");
  row.append(wrap);
  return row;
}
function dashEmpty(text = "暂无数据") {
  return element("p", { className: "hint dash-empty" }, text);
}
function cardTitle(...parts) {
  const title = document.createDocumentFragment();
  for (const part of parts) {
    if (part == null || part === "") continue;
    title.append(part instanceof Node ? part : document.createTextNode(part));
  }
  return title;
}
function dashCard(title, children, className = "") {
  const card = element("section", {
    className: "dash-card" + (className ? " " + className : ""),
  });
  const head = element("h3");
  if (title instanceof Node) head.append(title);
  else head.textContent = title;
  card.append(head);
  for (const child of children) if (child) card.append(child);
  return card;
}
// 列表页的记录卡：标题是这条记录的主体信息，字段沿用同一套标签/数值行，操作按钮收在卡片底部。
function recordCard(title, rows, actions, tone = "") {
  const card = dashCard(title, rows, "record-card" + (tone ? " " + tone : ""));
  if (actions && actions.length) {
    const bar = element("div", { className: "record-actions" });
    for (const action of actions) bar.append(action);
    card.append(bar);
  }
  return card;
}
function renderRecords(id, cards, emptyId) {
  $(id).replaceChildren(...cards);
  if (emptyId) $(emptyId).hidden = cards.length > 0;
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
  const cards = res.items.map((u) => {
    const add = element("input", {
      type: "number",
      min: "0",
      max: "10000",
      value: "0",
      ariaLabel: "增加次数",
    });
    const check = element("input", {
      type: "checkbox",
      checked: u.disabled,
      ariaLabel: "停用账号",
    });
    const save = element("button", { type: "button", className: "secondary" }, "保存");
    save.onclick = () =>
      busyButton(save, async () => {
        await api("/api/admin/users", {
          id: u.id,
          add: Number(add.value),
          disabled: check.checked,
        });
        toast("账号已更新");
        await loadUsers();
      });
    return recordCard(
      cardTitle(
        u.name || u.id,
        badge(u.disabled ? "已停用" : "正常", u.disabled ? "failed" : "succeeded"),
      ),
      [
        fieldRow("账号", u.id),
        fieldRow("用户名", u.name || "-"),
        fieldRow("邮箱", u.email || "设备账号"),
        // 注册来源 IP：历史账号未记录，显示“-”。
        fieldRow("注册IP", u.ip || "-"),
        dashRow("余次", u.credits),
        fieldRow("增加次数", add),
        fieldRow("停用账号", check),
        fieldRow("创建时间", timeText(u.created)),
      ],
      [save],
    );
  });
  renderRecords("usersRows", cards);
  renderPager("users");
}
async function loadCodes() {
  const state = pager.codes;
  const res = await api("/api/admin/codes?page=" + state.page);
  state.total = res.total;
  const statusText = {
    unredeemed: "未兑换",
    marked: "已Mark",
    redeemed: "已兑换",
  };
  const cards = res.items.map((c) => {
    const actions = [];
    const copy = element("button", { type: "button", className: "secondary" }, "复制");
    copy.onclick = () =>
      busyButton(copy, async () => {
        if (await copyCode(c)) toast("兑换码已复制");
      });
    actions.push(copy);
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
      actions.push(button);
    }
    const rows = [
      fieldRow("编号", c.id.slice(0, 12)),
      fieldRow("备注", c.label || "—"),
      dashRow("次数", c.credits),
      fieldRow("使用账号", c.usedBy || "—"),
      fieldRow("用户名", c.usedByName || "—"),
      fieldRow("创建时间", timeText(c.created)),
    ];
    if (!c.copyAvailable)
      rows.push(fieldRow("复制", "旧码需补录原码后复制"));
    return recordCard(
      cardTitle(
        badge(statusText[c.status] || c.status, c.status),
        "兑换码 " + c.id.slice(0, 12),
      ),
      rows,
      actions,
      c.status === "marked" || c.status === "redeemed" ? c.status : "",
    );
  });
  renderRecords("codesRows", cards);
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
  const cards = res.items.map((e) =>
    recordCard(cardTitle(e.event), [
      fieldRow("时间", timeText(e.created)),
      fieldRow("操作账号", e.actor),
      fieldRow("用户名", e.actorName || "-"),
      fieldRow("详情", e.target || "—"),
    ]),
  );
  renderRecords("auditRows", cards);
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
  // 类型从表格列移到卡片标题，信息不重复。
  const cards = res.items.map((h) =>
    recordCard(
      cardTitle(creditEventText[h.event] || h.event, badge("+" + h.credits + " 次", "succeeded")),
      [
        fieldRow("时间", timeText(h.created)),
        fieldRow("账号", h.user),
        fieldRow("用户名", h.userName || "-"),
        dashRow("次数", "+" + h.credits),
        fieldRow("备注", h.note || "—"),
      ],
    ),
  );
  renderRecords("creditsRows", cards);
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
  const cards = res.items.map((j) => {
    const label =
      j.status === "queued"
        ? "排队中"
        : j.status === "running"
          ? "进行中"
          : j.status;
    const estimate = j.estimate;
    return recordCard(
      cardTitle(
        badge(label, j.status),
        j.kind === "draft" ? "角色定稿" : "动作图",
      ),
      [
        fieldRow("动作", actionName(j.kind, j.action)),
        fieldRow("账号", j.user),
        fieldRow("用户名", j.userName || "-"),
        fieldRow("创建时间", timeText(j.created)),
        dashRow("已等待", jobDurationText(j.elapsedSeconds)),
        fieldRow(
          "预计耗时",
          estimate
            ? estimate.samples
              ? `约${jobDurationText(estimate.seconds)}（${estimate.samples}次成功样本，实测最短${jobDurationText(estimate.minSeconds)}、最长${jobDurationText(estimate.maxSeconds)}）`
              : `约${jobDurationText(estimate.seconds)}（初始估计，参考${jobDurationText(estimate.lowSeconds)}～${jobDurationText(estimate.highSeconds)}）`
            : "—",
        ),
      ],
    );
  });
  renderRecords("jobsRows", cards, "jobsEmpty");
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
  const cards = res.items.map((j) => {
    const failed = j.status === "failed" || j.status === "interrupted";
    return recordCard(
      cardTitle(
        badge(historyStatusText[j.status] || j.status, j.status),
        j.kind === "draft" ? "角色定稿" : "动作图",
      ),
      [
        fieldRow("动作", actionName(j.kind, j.action)),
        fieldRow("账号", j.user),
        fieldRow("用户名", j.userName || "-"),
        fieldRow("创建时间", timeText(j.created)),
        dashRow("扣次", String(j.cost || 0)),
        fieldRow(
          "失败原因",
          failed ? j.reason || "—（未记录原因）" : "—",
          failed ? "warning-text" : "",
        ),
      ],
    );
  });
  renderRecords("historyRows", cards, "historyEmpty");
  renderPager("history");
}
let galleryImages = [];
let galleryLoad = 0;
let galleryPreviewDispose = null;
let galleryPreviewShowGrid = false;
let galleryPreviewResizeObserver = null;
let galleryPreviewToken = 0;
let galleryPreviewObjectURL = null;
let galleryPreviewPair = false;
function galleryImageSize(image) {
  const label = element("p", { hidden: true });
  image.addEventListener("load", () => {
    if (!image.naturalWidth || !image.naturalHeight) return;
    label.textContent = `原始尺寸：${image.naturalWidth} × ${image.naturalHeight} px`;
    label.hidden = false;
  });
  return label;
}
function motionGridCols() {
  const raw = String(
    $("motionGrid")?.value || settings?.motionGrid || "5x5",
  ).trim();
  const match = /^(\d+)\s*[x×]\s*(\d+)$/i.exec(raw);
  const cols = match ? Number(match[1]) : 5;
  return cols === 4 || cols === 5 || cols === 10 ? cols : 5;
}
function clearGalleryPreviewGrid() {
  const canvas = $("galleryPreviewGrid");
  const hint = $("galleryPreviewHint");
  if (canvas) {
    canvas.hidden = true;
    const ctx = canvas.getContext("2d");
    if (ctx) ctx.clearRect(0, 0, canvas.width, canvas.height);
  }
  if (hint) {
    hint.hidden = true;
    hint.textContent = "";
  }
}
function paintGalleryPreviewGrid() {
  const image = $("galleryPreviewImage");
  const canvas = $("galleryPreviewGrid");
  const hint = $("galleryPreviewHint");
  if (!galleryPreviewShowGrid || !image || !canvas) {
    clearGalleryPreviewGrid();
    return;
  }
  const cols = motionGridCols();
  const nw = image.naturalWidth;
  const nh = image.naturalHeight;
  const dw = image.clientWidth;
  const dh = image.clientHeight;
  if (!nw || !nh || !dw || !dh || nw !== nh || nw < cols) {
    clearGalleryPreviewGrid();
    if (hint && galleryPreviewShowGrid) {
      hint.hidden = false;
      hint.textContent = "当前图无法按方形网格切格，未绘制辅助线";
    }
    return;
  }
  const dpr = Math.max(1, window.devicePixelRatio || 1);
  canvas.width = Math.round(dw * dpr);
  canvas.height = Math.round(dh * dpr);
  canvas.style.width = `${dw}px`;
  canvas.style.height = `${dh}px`;
  canvas.hidden = false;
  const ctx = canvas.getContext("2d");
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, dw, dh);
  // 与 synthesizeGIF 一致：边界 = floor(i * size / cols)
  ctx.strokeStyle = "rgba(255, 56, 56, 0.9)";
  ctx.lineWidth = Math.max(1, 1 / dpr);
  ctx.setLineDash([]);
  for (let i = 1; i < cols; i++) {
    const x = (Math.floor((i * nw) / cols) * dw) / nw;
    const y = (Math.floor((i * nh) / cols) * dh) / nh;
    ctx.beginPath();
    ctx.moveTo(x + 0.5, 0);
    ctx.lineTo(x + 0.5, dh);
    ctx.stroke();
    ctx.beginPath();
    ctx.moveTo(0, y + 0.5);
    ctx.lineTo(dw, y + 0.5);
    ctx.stroke();
  }
  // 外框提示实际切格范围
  ctx.strokeStyle = "rgba(255, 56, 56, 0.55)";
  ctx.strokeRect(0.5, 0.5, dw - 1, dh - 1);
  if (hint) {
    const cell = Math.floor(nw / cols);
    hint.hidden = false;
    hint.textContent = `切格辅助线：${cols}×${cols} · 源图 ${nw}×${nh} · 约 ${cell}×${cell}/格（与合成切格边界一致）`;
  }
}
function closeGalleryPreview() {
  const dialog = $("galleryPreviewDialog");
  if (dialog?.open) dialog.close();
}
// 关闭或切换预览时统一收尾：停掉懒加载、撤销 Object URL、清掉 2 倍预览留下的行内尺寸。
function disposeGalleryPreview() {
  galleryPreviewToken++;
  galleryPreviewResizeObserver?.disconnect();
  galleryPreviewResizeObserver = null;
  if (galleryPreviewDispose) {
    galleryPreviewDispose();
    galleryPreviewDispose = null;
  }
  if (galleryPreviewObjectURL) {
    URL.revokeObjectURL(galleryPreviewObjectURL);
    galleryPreviewObjectURL = null;
  }
  for (const id of ["galleryPreviewImage", "galleryPreviewImage2x"]) {
    const node = $(id);
    if (!node) continue;
    node.style.removeProperty("width");
    node.style.removeProperty("height");
  }
  galleryPreviewShowGrid = false;
  galleryPreviewPair = false;
}
function setGalleryPreviewCaption(id, text) {
  const node = $(id);
  if (!node) return;
  node.textContent = text;
  node.hidden = !text;
}
// 并排对照里的尺寸按自然像素写死：左图 1 倍，右图 2 倍，避免 CSS 再缩放导致看着不像 2 倍。
function paintGalleryPreviewPair() {
  const image = $("galleryPreviewImage");
  const image2x = $("galleryPreviewImage2x");
  if (!galleryPreviewPair || !image || !image2x) return;
  const nw = image.naturalWidth;
  const nh = image.naturalHeight;
  if (!nw || !nh) return;
  image.style.width = `${nw}px`;
  image.style.height = `${nh}px`;
  image2x.style.width = `${nw * 2}px`;
  image2x.style.height = `${nh * 2}px`;
  setGalleryPreviewCaption("galleryPreviewCaption1", `原尺寸 ${nw} × ${nh} px`);
  setGalleryPreviewCaption("galleryPreviewCaption2", `放大 2 倍 ${nw * 2} × ${nh * 2} px`);
}
// GIF 走这条路径：只取一次图片内容，两个 <img> 共用同一个 Object URL，
// 让左右两幅的 GIF 解码共用同一份资源，动画帧不会各走各的。
async function loadGalleryPreviewPair(url) {
  const image = $("galleryPreviewImage");
  const image2x = $("galleryPreviewImage2x");
  const token = ++galleryPreviewToken;
  const onLoad = () => paintGalleryPreviewPair();
  image.addEventListener("load", onLoad);
  galleryPreviewDispose = () => {
    image.removeEventListener("load", onLoad);
    image.removeAttribute("src");
    image2x?.removeAttribute("src");
  };
  try {
    const response = await mediaResponse(url);
    if (!response.ok) throw new Error("图片读取失败");
    const blob = await response.blob();
    if (token !== galleryPreviewToken) return;
    galleryPreviewObjectURL = URL.createObjectURL(blob);
    image.src = galleryPreviewObjectURL;
    if (image2x) image2x.src = galleryPreviewObjectURL;
  } catch {
    if (token === galleryPreviewToken) {
      setGalleryPreviewCaption("galleryPreviewCaption1", "图片加载失败，重新进入可重试");
      setGalleryPreviewCaption("galleryPreviewCaption2", "");
    }
  }
}
function openGalleryPreview(url, alt, options = {}) {
  const dialog = $("galleryPreviewDialog");
  const stage = $("galleryPreviewStage");
  const image = $("galleryPreviewImage");
  const image2x = $("galleryPreviewImage2x");
  const panel2 = $("galleryPreviewPanel2");
  if (!dialog || !image) return;
  disposeGalleryPreview();
  // duo2x：GIF 用「原尺寸 + 放大 2 倍」左右并排；切格辅助线只用于动作序列原图，两者互斥。
  const pair = !!options.duo2x && !!image2x && !!panel2;
  galleryPreviewPair = pair;
  galleryPreviewShowGrid = !!options.grid && !pair;
  if (panel2) panel2.hidden = !pair;
  stage?.classList.toggle("pair", pair);
  clearGalleryPreviewGrid();
  galleryPreviewObjectURL = null;
  image.alt = alt || "预览图";
  if (image2x) image2x.alt = `${alt || "预览图"}（放大 2 倍）`;
  setGalleryPreviewCaption("galleryPreviewCaption1", pair ? "原尺寸" : "");
  setGalleryPreviewCaption("galleryPreviewCaption2", pair ? "放大 2 倍" : "");
  if (pair) {
    void loadGalleryPreviewPair(url);
  } else {
    image.removeAttribute("src");
    const onLoad = () => paintGalleryPreviewGrid();
    image.addEventListener("load", onLoad);
    const watchDispose = watchMediaImage(image, url, dialog);
    galleryPreviewDispose = () => {
      image.removeEventListener("load", onLoad);
      watchDispose();
    };
    if (typeof ResizeObserver !== "undefined") {
      galleryPreviewResizeObserver?.disconnect();
      galleryPreviewResizeObserver = new ResizeObserver(() => {
        if (galleryPreviewShowGrid) paintGalleryPreviewGrid();
      });
      galleryPreviewResizeObserver.observe(image);
    }
  }
  if (!dialog.open) dialog.showModal();
  if (pair) {
    if (image.complete && image.naturalWidth) paintGalleryPreviewPair();
  } else if (image.complete && image.naturalWidth) {
    paintGalleryPreviewGrid();
  }
}
function galleryPreviewTrigger(url, label, alt, className = "", options = {}) {
  const button = element("button", {
    type: "button",
    className: className || "admin-gallery-main-link",
    title: label,
  });
  button.onclick = () => openGalleryPreview(url, alt, options);
  return button;
}
function galleryCard(item, kind) {
  const card = element("article", { className: "admin-gallery-card" });
  const meta = element("div", { className: "admin-gallery-meta" });
  const media = element("div", { className: "admin-gallery-media" });
  if (item.imageURL) {
    const sheetOnly = kind === "work" && !item.hasGif && item.hasSheet;
    const mainLabel =
      kind === "draft"
        ? "定稿图（点击查看原尺寸）"
        : item.hasGif
          ? "GIF（点击对比原尺寸与放大 2 倍）"
          : "动作原图（点击查看原尺寸）";
    const alt = kind === "draft" ? "角色定稿图" : item.hasGif ? "动作 GIF" : "动作原图";
    const trigger = galleryPreviewTrigger(
      item.imageURL,
      mainLabel,
      alt,
      "",
      sheetOnly ? { grid: true } : item.hasGif ? { duo2x: true } : {},
    );
    const image = element("img", {
      className: "admin-gallery-main",
      loading: "lazy",
      decoding: "async",
      alt,
    });
    trigger.append(image);
    media.append(
      element("p", { className: "admin-gallery-main-hint" }, mainLabel),
      trigger,
      galleryImageSize(image),
    );
    galleryImages.push(watchMediaImage(image, item.imageURL, media));
  } else {
    media.append(element("div", { className: "admin-gallery-missing" }, "图片文件不存在"));
  }
  if (kind === "work" && item.hasGif) {
    const source = element("div", { className: "admin-gallery-source" });
    source.append(element("p", {}, item.hasSheet ? "动作序列原图（点击查看原尺寸）" : "动作序列原图：未保存"));
    if (item.hasSheet) {
      const sourceURL = `/api/admin/gallery/works/${encodeURIComponent(item.id)}/sheet`;
      const trigger = galleryPreviewTrigger(
        sourceURL,
        "查看动作序列原图原尺寸",
        "生成此 GIF 的动作序列原图",
        "admin-gallery-source-link",
        { grid: true },
      );
      const image = element("img", {
        loading: "lazy",
        decoding: "async",
        alt: "生成此 GIF 的动作序列原图",
      });
      trigger.append(image);
      source.append(trigger, galleryImageSize(image));
      galleryImages.push(watchMediaImage(image, sourceURL, source));
    }
    media.append(source);
  }
  meta.append(element("h3", {}, kind === "draft" ? "角色定稿" : item.name || item.action || "动作作品"));
  const lines = [`账号：${item.userName || item.user}`, `时间：${timeText(item.created)}`];
  if (kind === "draft") {
    const s = item.selection || {};
    lines.push(`造型：${s.category || "—"} · ${s.style || "default"}`);
    lines.push(`服装：${s.clothes || "—"} · 配色：${s.color || "—"}`);
    if (s.weapon) lines.push(`武器：${s.weapon}`);
    lines.push(`编号：${item.id}`);
  } else {
    lines.push(`分类：${item.category || "—"} · 动作：${item.action || "—"}`);
    lines.push(item.hasGif ? "格式：GIF 动图" : "格式：仅动作原图（GIF 合成缺失）");
    lines.push(`编号：${item.id}`);
  }
  for (const line of lines) meta.append(element("p", { title: line }, line));
  card.append(meta, media);
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
// 三个部分都用同一套「标签 + 数值」卡片，与「新增用户」一致，不再有表格与点按弹窗。
function dashAccount(name, user) {
  return name || user || "—";
}
async function loadDashboard() {
  const d = await api("/api/admin/dashboard");
  const host = $("dashContent");
  host.replaceChildren();
  const ranges = d.ranges || {};
  const codes = d.codes || {};

  const metrics = element("div", { className: "dash-row dash-cards" });
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

  const kinds = d.today?.kinds || [];
  const categories = d.today?.categories || [];
  const dist = element("div", { className: "dash-row dash-split" });
  dist.append(
    dashCard(
      "本日消耗 · 任务类型",
      kinds.length
        ? kinds.map((k) => dashRow(k.name, k.count))
        : [dashEmpty("本日还没有消耗记录。")],
    ),
    dashCard(
      "本日消耗 · 人物分类",
      categories.length
        ? categories.map((c) =>
            dashRow(c.name, c.draft + c.motion, `定稿图 ${c.draft} · GIF ${c.motion}`),
          )
        : [dashEmpty("本日还没有消耗记录。")],
    ),
  );
  host.append(dist);

  const topConsume = d.topConsume || [];
  const topRedeem = d.topRedeem || [];
  const tops = element("div", { className: "dash-row dash-split" });
  tops.append(
    dashCard(
      "TOP5 · 消耗次数",
      topConsume.length
        ? topConsume.map((r, i) =>
            dashRow(`${i + 1}. ${dashAccount(r.name, r.user)}`, r.count),
          )
        : [dashEmpty("还没有消耗数据。")],
    ),
    dashCard(
      "TOP5 · 兑换次数",
      topRedeem.length
        ? topRedeem.map((r, i) =>
            dashRow(
              `${i + 1}. ${dashAccount(r.name, r.user)}`,
              r.credits,
              `兑换码 ${r.count} 个`,
            ),
          )
        : [dashEmpty("还没有兑换数据。")],
    ),
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
for (const wrap of document.querySelectorAll(".admin-panel > .admin-records")) {
  wrap.setAttribute("role", "region");
  wrap.setAttribute(
    "aria-label",
    wrap.closest(".admin-panel").querySelector("h2").textContent,
  );
}
function showTab(id) {
  for (const b of $("adminTabs").children) {
    if (!b.dataset.tab) continue;
    b.classList.toggle("active", b.dataset.tab === id);
    b.setAttribute("aria-pressed", String(b.dataset.tab === id));
  }
  for (const section of document.querySelectorAll(".admin-panel"))
    section.hidden = section.id !== id;
  const loader = tabLoaders[id];
  if (loader) loader().catch(showError);
}
for (const b of $("adminTabs").children) {
  if (!b.dataset.tab) continue;
  b.onclick = () => showTab(b.dataset.tab);
}
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
$("galleryPreviewClose").onclick = () => closeGalleryPreview();
$("galleryPreviewDialog").addEventListener("click", (event) => {
  if (event.target === $("galleryPreviewDialog")) closeGalleryPreview();
});
$("galleryPreviewDialog").addEventListener("close", () => {
  disposeGalleryPreview();
});
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

import {
  $,
  account,
  api,
  setSession,
  connect,
  refreshAccount as commonRefreshAccount,
  toast,
  showError,
  busyButton,
  element,
  dataBlob,
  blobData,
  local,
  migrateLocal,
  download,
  errorMessage,
  userNotice,
  NETWORK_ERROR_MESSAGE,
} from "./common.v4.js";
// 包装剩余次数刷新：同步更新“让我的角色动起来”处的“剩余 X 次”展示。
async function refreshAccount() {
  await commonRefreshAccount();
  if ($("remainingCost")) $("remainingCost").textContent = account.credits;
}
let catalog,
  profile = null,
  selectedCategory = "",
  selectedColor = "",
  selectedStyle = "default",
  selfie = null;
const selectedActions = new Set();
let galleryURLs = [];
let photoURL, draftURL;
let serviceRefresh = null;
// 进行中的任务：每个任务独立提交、独立轮询、独立显示进度。
// 同一账号可并行多个任务（上限取后台配置），因此不再用单个 running 标记整体锁界面。
const tasks = new Map();
let taskClock = null;
// 动作批次：每批只负责点击时选中的动作，按空闲并发额度并行提交、完成一个补一个。
// 批次之间互不阻塞——有动作在制作时仍可继续挑动作提交新的批次，只受账号并发上限与剩余次数限制。
const motionBatches = new Set();
// 已经交给服务器的动作：卡片标记为“制作中”，不再计入已选，也不会被重复提交。
const motionInFlight = new Set();

// 动作序列图规格（后台配置，经 /api/catalog 下发）：
// 4x4 共16格、每帧256×256；5x5 共25格、每帧128×128。未下发时回落 4×4。
const MOTION_SPECS = {
  "4x4": { cols: 4, frames: 16, size: 256 },
  "5x5": { cols: 5, frames: 25, size: 128 },
};
function motionSpec() {
  return MOTION_SPECS[catalog?.motionGrid] || MOTION_SPECS["4x4"];
}

function durationText(value) {
  const seconds = Math.max(0, Math.ceil(value));
  const minutes = Math.floor(seconds / 60);
  return minutes
    ? `${minutes}分${seconds % 60 ? `${seconds % 60}秒` : ""}`
    : `${seconds}秒`;
}
function estimateText(value, count = 1) {
  if (!value) return "正在获取时间预估…";
  const basis = value.samples
    ? `基于${value.samples}次成功调用`
    : "初始估计，暂无成功样本";
  return `预计约${durationText(value.seconds * count)}（参考${durationText(value.lowSeconds * count)}～${durationText(value.highSeconds * count)}；${basis}）`;
}
function elapsedEstimateText(value, elapsed) {
  if (!value) return `已等待 ${durationText(elapsed)}`;
  if (elapsed >= value.highSeconds)
    return `已等待 ${durationText(elapsed)} · 已超过预估，服务仍在处理中`;
  return `已等待 ${durationText(elapsed)} · 预计还需 ${durationText(Math.max(0, value.lowSeconds - elapsed))}～${durationText(Math.max(1, value.highSeconds - elapsed))}（${value.samples ? "历史参考" : "初始估计"}）`;
}
function renderEstimates() {
  $("draftEstimate").textContent = estimateText(catalog?.estimates?.draft);
  const count = selectedActions.size;
  if (!count) {
    // 制作中的动作已经不在“已选”里：这里说明还能继续挑动作提交，避免看成“被锁住了”。
    $("motionEstimate").textContent = motionInFlight.size
      ? `已有 ${motionInFlight.size} 个动作在制作中，可以继续勾选新的动作提交，只受并发额度与剩余次数限制。`
      : "选择动作后显示整组预计用时。";
    return;
  }
  const width = motionParallelWidth(count);
  const rounds = Math.ceil(count / width);
  $("motionEstimate").textContent = `${count}个动作并行生成（最多同时${width}个）：${estimateText(catalog?.estimates?.motion, rounds)}`;
}
// 每个进行中的任务各自刷新等待时间与参考剩余，可同时存在多个任务。
function updateTaskClock() {
  for (const task of tasks.values()) {
    if (!task.startedAt || !task.estimateEl) continue;
    const elapsed = Math.max(0, (Date.now() - task.startedAt) / 1000);
    task.estimateEl.textContent = elapsedEstimateText(task.estimate, elapsed);
  }
}
function startTaskClock() {
  if (!taskClock) taskClock = setInterval(updateTaskClock, 1000);
  updateTaskClock();
}
function stopTaskClock() {
  clearInterval(taskClock);
  taskClock = null;
}

function renderServiceStatus() {
  const help = (catalog?.redeemHelp || "").trim();
  $("redeemHelpSection").hidden = !help;
  $("redeemHelpContent").textContent = help;
  const available = Boolean(catalog?.configured);
  $("serviceStatus").hidden = available;
  $("refreshServiceBtn").hidden = available;
  if (!available) {
    $("notice").textContent =
      "图像服务尚未配置或密钥不可用，请联系管理员。配置完成后可点击“重新检查服务”，不会扣除次数。";
    $("notice").dataset.source = "service";
    $("notice").hidden = false;
  } else if ($("notice").dataset.source === "service") {
    $("notice").hidden = true;
  }
  $("chargeHint").textContent =
    (catalog.chargeOnFailure
      ? "每次发出图像生成请求使用1次，失败也计次；定稿与每个动作分别计次。合成与下载不扣次。"
      : "每次成功生成图片使用1次，失败退回；定稿与每个动作分别计次。合成与下载不扣次。") +
    `当前账号最多可同时执行 ${catalog.userConcurrency || 5} 个任务，动作图会按并发额度并行生成；有动作在制作时也能继续修改造型、提交新的定稿或挑新动作，只受并发额度与剩余次数限制。`;
}

async function refreshServiceState() {
  if (serviceRefresh) return serviceRefresh;
  serviceRefresh = (async () => {
    // 后台登录可能更新同源会话Cookie，提交前一并更新CSRF和剩余次数。
    await refreshAccount();
    const latest = await api("/api/catalog");
    // 仅同步服务状态，不重建表单，保留已上传照片和用户选择。
    catalog.configured = latest.configured;
    catalog.chargeOnFailure = latest.chargeOnFailure;
    catalog.emailConfigured = latest.emailConfigured;
    catalog.feedbackConfigured = latest.feedbackConfigured;
    catalog.estimates = latest.estimates;
    catalog.redeemHelp = latest.redeemHelp || "";
    catalog.userConcurrency = latest.userConcurrency;
    catalog.generationSlots = latest.generationSlots;
    catalog.motionGrid = latest.motionGrid;
    renderServiceStatus();
    updateControls();
    return latest.configured;
  })();
  try {
    return await serviceRefresh;
  } finally {
    serviceRefresh = null;
  }
}
const colors = {
  玄黑与暗金: "#837244",
  银灰与藏蓝: "#8090a3",
  深红与铁灰: "#994f4c",
  奶油黄: "#e4cb83",
  雾粉: "#dfb1b4",
  浅紫: "#c8b5dd",
  薄荷绿: "#b1c7b7",
  天空蓝与奶油黄: "#a7c6da",
  桃粉与米白: "#dbb2bb",
  薄荷绿与浅黄: "#b9c9a2",
};
function category() {
  return catalog?.categories.find((c) => c.id === selectedCategory);
}
function selection() {
  return {
    category: selectedCategory,
    clothes: $("clothes").value,
    color: selectedColor,
    weapon: selectedCategory === "male" ? $("weapon").value : "",
    style: selectedStyle,
  };
}
function validSelection() {
  const s = selection();
  return (
    selfie &&
    s.category &&
    s.clothes &&
    s.color &&
    s.style &&
    (s.category !== "male" || s.weapon)
  );
}
function updateControls() {
  renderEstimates();
  // 任务在服务器后台异步执行：等待中不再锁定生成按钮，可继续调整造型并提交下一个任务，
  // 只受同账号并发上限、剩余次数和必填项限制；服务端会再次校验并发与次数。
  const atLimit = taskLimitReached();
  $("draftBtn").disabled = !validSelection() || !catalog?.configured || atLimit;
  $("redraftBtn").disabled = !profile?.draft || atLimit;
  const count = selectedActions.size;
  $("selectedCount").textContent = count;
  $("cost").textContent = count;
  // 动作批次进行中同样不锁生成按钮：有动作在制作时也能继续挑动作提交新的批次，
  // 只受并发上限与剩余次数限制（已达上限时按钮才禁用）。
  $("motionBtn").disabled =
    !count || !profile?.accepted || !catalog?.configured || atLimit;
  $("stopBtn").hidden = !motionSubmitting();
  // 切换账号会让进行中的任务失去归属，仍在执行时先禁用。
  $("accountBtn").disabled = tasks.size > 0;
  updateFeedback();
}
// 首页反馈入口：最多200字，实时显示字数；未配置反馈邮箱时说明原因并禁用提交。
function updateFeedback() {
  const configured = Boolean(catalog?.feedbackConfigured);
  const length = [...$("feedbackContent").value].length;
  $("feedbackCount").textContent = `${length} / 200 字`;
  $("feedbackState").hidden = configured;
  $("feedbackState").textContent = configured
    ? ""
    : "反馈邮箱尚未配置，暂时无法提交，请联系管理员。";
  $("feedbackSubmitBtn").disabled =
    !configured || !$("feedbackContent").value.trim();
}
function step(n) {
  for (let i = 1; i <= 3; i++) $("step" + i).classList.toggle("active", i === n);
  $("setup").hidden = n !== 1;
  $("draftSection").hidden = n !== 2;
  $("motionSection").hidden = n !== 3;
  // 步骤可自由跳转：内容尚未就绪的步骤展示引导空态，就绪后正常展示。
  const hasDraft = Boolean(profile?.draft);
  $("draftEmpty").hidden = hasDraft;
  $("draftInfo").hidden = !hasDraft;
  $("draftImageWrap").hidden = !hasDraft;
  const ready = Boolean(profile?.accepted);
  $("motionEmpty").hidden = ready;
  $("motionReady").hidden = !ready;
}
function renderCategories() {
  const node = $("categories");
  node.replaceChildren();
  for (const c of catalog.categories) {
    const b = element("button", {
      className: "category",
      type: "button",
      title: c.subtitle,
    });
    b.append(element("span", {}, c.icon), element("b", {}, c.name));
    b.setAttribute("aria-pressed", "false");
    b.onclick = () => chooseCategory(c.id);
    b.dataset.id = c.id;
    node.append(b);
  }
}
function chooseCategory(id, restore = false) {
  selectedCategory = id;
  selectedColor = "";
  for (const b of $("categories").children) {
    b.classList.toggle("selected", b.dataset.id === id);
    b.setAttribute("aria-pressed", String(b.dataset.id === id));
  }
  const c = category();
  fillOptions($("clothes"), c.clothes, "请选择服装");
  fillOptions($("weapon"), c.weapons, "请选择武器");
  $("weaponField").hidden = id !== "male";
  $("colors").replaceChildren();
  for (const name of c.colors) {
    const b = element("button", { className: "color-option", type: "button" });
    const dot = element("span", { className: "swatch" }); // 颜色通过固定 CSS 类映射，保持严格 CSP。
    dot.classList.add("tone-" + Object.keys(colors).indexOf(name));
    b.append(dot, document.createTextNode(name));
    b.setAttribute("aria-pressed", "false");
    b.onclick = () => {
      selectedColor = name;
      for (const x of $("colors").children) {
        x.classList.toggle("selected", x === b);
        x.setAttribute("aria-pressed", String(x === b));
      }
      updateControls();
    };
    b.dataset.value = name;
    $("colors").append(b);
  }
  if (!restore) selectedActions.clear();
  updateControls();
}
function fillOptions(select, items, placeholder) {
  select.replaceChildren(element("option", { value: "" }, placeholder));
  for (const name of items)
    select.append(element("option", { value: name }, name));
}
// 画风由服务端下发，缺失时回落到“默认”，保证上传照片后始终有可用选项。
function styleList() {
  return catalog?.styles?.length
    ? catalog.styles
    : [{ id: "default", name: "默认", subtitle: "轻度Q版 · 保留本人特点", icon: "✧" }];
}
function updateStyleHint() {
  const current = styleList().find((s) => s.id === selectedStyle);
  $("styleHint").textContent = current
    ? `当前画风：${current.name}${current.subtitle ? " · " + current.subtitle : ""}。画风同时作用于角色定稿和动作 GIF。`
    : "画风同时作用于角色定稿和动作 GIF。";
}
// restore=true 用于恢复已保存的造型（含切换历史定稿），此时不清理已选动作。
function selectStyle(id, restore = false) {
  const list = styleList();
  selectedStyle = list.some((s) => s.id === id) ? id : "default";
  for (const b of $("styles").children) {
    b.classList.toggle("selected", b.dataset.id === selectedStyle);
    b.setAttribute("aria-pressed", String(b.dataset.id === selectedStyle));
  }
  if (!restore) selectedActions.clear();
  updateStyleHint();
  updateControls();
}
function chooseStyle(id) {
  if (id === selectedStyle) return;
  selectStyle(id);
}
function renderStyles() {
  const node = $("styles");
  node.replaceChildren();
  for (const s of styleList()) {
    const b = element("button", {
      className: "style-option",
      type: "button",
      title: s.subtitle || s.name,
    });
    b.append(
      element("span", {}, s.icon || "✧"),
      element("b", {}, s.name),
      element("small", {}, s.subtitle || ""),
    );
    b.onclick = () => chooseStyle(s.id);
    b.dataset.id = s.id;
    node.append(b);
  }
  selectStyle(selectedStyle, true);
}
async function preparePhoto(file) {
  if (!file || !["image/jpeg", "image/png", "image/webp"].includes(file.type))
    throw userNotice("请选择 JPG、PNG 或 WebP 照片");
  if (file.size > 50 * 1024 * 1024)
    throw userNotice("原始照片过大，请选择小于50MB的照片");
  if (file.size <= 5 * 1024 * 1024) {
    // 小图直接保留原始File，不经过Canvas、重编码或元数据处理。
    selfie = file;
    showPhoto(file);
    $("photoInfo").textContent =
      `照片大小 ${(file.size / 1024 / 1024).toFixed(2)} MB，将按原文件上传，不压缩、不缩放、不调整格式。`;
    step(1);
    updateControls();
    persistSelfie();
    return;
  }
  const bitmap = await createImageBitmap(file, {
    imageOrientation: "from-image",
  });
  try {
    if (bitmap.width * bitmap.height > 50000000)
      throw userNotice("照片像素过大，请先缩小后再选择");
    let scale = Math.min(1, 2048 / Math.max(bitmap.width, bitmap.height));
    let blob;
    for (let attempt = 0; attempt < 5; attempt++) {
      const canvas = document.createElement("canvas");
      canvas.width = Math.max(1, Math.round(bitmap.width * scale));
      canvas.height = Math.max(1, Math.round(bitmap.height * scale));
      const ctx = canvas.getContext("2d");
      ctx.fillStyle = "#fff";
      ctx.fillRect(0, 0, canvas.width, canvas.height);
      ctx.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
      blob = await new Promise((resolve) =>
        canvas.toBlob(resolve, "image/jpeg", 0.92 - attempt * 0.08),
      );
      if (blob && blob.size <= 5 * 1024 * 1024) break;
      scale *= 0.8;
    }
    if (!blob || blob.size > 5 * 1024 * 1024)
      throw new Error("自动压缩后仍超过5MB，请换一张照片");
    selfie = blob;
    showPhoto(blob);
    $("photoInfo").textContent =
      `原图超过5 MB，已在浏览器压缩至 ${(blob.size / 1024 / 1024).toFixed(2)} MB。`;
    step(1);
    updateControls();
    persistSelfie();
  } finally {
    bitmap.close();
  }
}
function persistSelfie() {
  // 上传成功即存入本机IndexedDB，关闭网页后重进页面可恢复上次照片；保存失败不影响本次使用。
  local(
    "profiles",
    "put",
    profile ? { ...profile, selfie } : { uid: account.id, selfie },
  ).catch(() =>
    toast("照片暂存本机失败，关闭页面后需要重新上传"),
  );
}
function showPhoto(blob) {
  if (photoURL) URL.revokeObjectURL(photoURL);
  photoURL = URL.createObjectURL(blob);
  $("photoPreview").src = photoURL;
  $("photoPreview").hidden = false;
  $("uploadEmpty").hidden = true;
  $("replacePhoto").hidden = false;
}
function showDraft() {
  if (draftURL) URL.revokeObjectURL(draftURL);
  draftURL = URL.createObjectURL(profile.draft);
  $("draftImage").src = draftURL;
  $("saveDraft").href = draftURL;
  renderCandidates();
  step(2);
  updateControls();
}
let candidateURLs = [];
// 已生成的定稿放在创作大框架内，与当前处于哪一步无关：只要生成过定稿，
// 第1、2、3步都能看到并按需切换回对应的定稿。
function renderCandidates() {
  for (const u of candidateURLs) URL.revokeObjectURL(u);
  candidateURLs = [];
  const list = profile?.candidates || [];
  $("draftStrip").hidden = list.length === 0;
  $("draftStripCount").textContent = list.length
    ? `已生成 ${list.length} 张 · 点击切换到对应定稿`
    : "";
  const strip = $("draftCandidates");
  strip.replaceChildren();
  for (const c of list) {
    const b = element("button", { className: "candidate", type: "button" });
    const img = element("img", { alt: "历史定稿候选" });
    const url = URL.createObjectURL(c.draft);
    candidateURLs.push(url);
    img.src = url;
    b.append(img);
    const s = c.selection || {};
    b.title = `${s.clothes || ""} · ${s.color || ""}${c.accepted ? " · 已确认" : ""}`;
    b.setAttribute("aria-pressed", String(c.draft === profile.draft));
    b.classList.toggle("selected", c.draft === profile.draft);
    b.onclick = () => switchCandidate(c);
    strip.append(b);
  }
}
async function switchCandidate(c) {
  if (c.draft === profile.draft) return;
  profile.draft = c.draft;
  profile.receipt = c.receipt;
  profile.selection = c.selection;
  profile.accepted = Boolean(c.accepted);
  // 历史定稿可能使用其他画风：切回第 1 步时同步选中，重新生成才会沿用同一画风。
  selectStyle(c.selection?.style || "default", true);
  await local("profiles", "put", profile);
  showDraft();
}
// 动作卡片状态：勾选 = 已选中、等待提交；制作中 = 已经交给服务器，不再计入已选，完成后自动恢复可选。
// 因为制作中的动作不在已选里，继续挑别的动作提交新批次时不会重复提交同一个动作。
function syncActionSelection() {
  for (const b of $("actions").children) {
    const id = b.dataset.id;
    const busy = motionInFlight.has(id);
    const on = selectedActions.has(id) && !busy;
    b.classList.toggle("selected", on);
    b.classList.toggle("busy", busy);
    b.setAttribute("aria-pressed", String(on));
    if (b.stateEl) b.stateEl.hidden = !busy;
  }
  updateControls();
}
function showActions() {
  const c = catalog.categories.find((c) => c.id === profile.selection.category);
  $("actions").replaceChildren();
  for (const a of c.actions) {
    const b = element("button", { className: "action", type: "button" });
    b.dataset.id = a.id;
    b.append(element("span", {}, a.icon), document.createTextNode(a.name));
    // 制作中的标记：卡片仍然可见（进度区也在跑），但不再计入已选，避免同一个动作被重复提交。
    b.stateEl = element("small", { className: "action-state" }, "制作中");
    b.stateEl.hidden = true;
    b.append(b.stateEl);
    b.onclick = () => {
      if (motionInFlight.has(a.id)) {
        toast("这个动作正在制作中，完成后可以再做一次。");
        return;
      }
      if (selectedActions.has(a.id)) selectedActions.delete(a.id);
      else selectedActions.add(a.id);
      syncActionSelection();
    };
    $("actions").append(b);
  }
  syncActionSelection();
  step(3);
}
function actionLabel(id) {
  for (const c of catalog?.categories || [])
    for (const a of c.actions) if (a.id === id) return a.name;
  return id || "自定义动作";
}
// 上游“图片文件或模式无效”类错误对用户而言就是自拍照不可用，改写为可操作的提示。
// 按前缀匹配（Invalid image file or mode for image 1/2/…），不同序号都能命中。
function callFailReason(message) {
  if (!message) return "";
  if (message.includes("Invalid image file or mode for image"))
    return "自拍照数据异常，请重新上传后尝试";
  return message;
}
async function loadCalls() {
  $("callsList").replaceChildren();
  $("callsEmpty").hidden = true;
  try {
    const res = await fetch("/api/calls");
    if (!res.ok) throw new Error(res.status);
    const { items } = await res.json();
    $("callsEmpty").hidden = items.length > 0;
    const statusText = {
      queued: "排队中",
      running: "进行中",
      succeeded: "已完成",
      failed: "失败",
    };
    for (const c of items) {
      const li = element("li");
      const main = element("div", { className: "call-main" });
      main.append(
        element(
          "b",
          {},
          (c.kind === "motion" ? "动作 GIF · " : "角色定稿 · ") +
            (c.kind === "motion" ? actionLabel(c.action) : "生成专属角色"),
        ),
        element(
          "span",
          {},
          new Date(c.created * 1000).toLocaleString("zh-CN"),
        ),
      );
      // 失败时在标题下展示失败原因，其余状态只保留时间。
      const reason = c.status === "failed" ? callFailReason(c.error) : "";
      if (reason)
        main.append(
          element("span", { className: "call-reason" }, `失败原因：${reason}`),
        );
      li.append(
        main,
        element(
          "span",
          {
            className:
              "call-status" + (c.status === "failed" ? " failed" : ""),
          },
          (statusText[c.status] || c.status) +
            (c.cost > 0 ? ` · 消耗 ${c.cost} 次` : " · 未扣次"),
        ),
      );
      $("callsList").append(li);
    }
  } catch {
    $("callsEmpty").hidden = false;
  }
}
// —— 任务列表与进度 ——
// 会消耗创作次数的任务（定稿与动作）用于并发上限与次数计算；本机合成等纯前端步骤不计入。
function activeCreditTasks() {
  return [...tasks.values()].filter(
    (t) => t.kind === "draft" || t.kind === "motion",
  ).length;
}
// 同账号并发上限来自后台配置（默认5）：达到上限时禁用提交按钮，服务端仍会再次校验。
// 触达上限的提示统一为“已达任务上限X”（与服务端 409 文案一致），不使用通用网络错误文案。
function concurrencyLimit() {
  return Number(catalog?.userConcurrency) || 5;
}
function taskLimitReached() {
  return activeCreditTasks() >= concurrencyLimit();
}
// 是否还有动作批次在提交：只用于“停止提交新动作”按钮的显隐，生成按钮不再被进行中的批次锁住。
function motionSubmitting() {
  return motionBatches.size > 0;
}
// 已提交但尚未完成的任务同样占用次数，避免连续点击超出剩余次数。
function availableCredits() {
  return account.credits - activeCreditTasks();
}
// 动作批次的空闲并发额度：可同时提交的动作数不会超过账号并发上限的剩余部分。
function freeMotionSlots() {
  return Math.max(0, concurrencyLimit() - activeCreditTasks());
}
// 整批动作的实际并行宽度：还受服务器生成槽位限制，超出槽位的提交只会排队等待。
// 该值只用于估算整批用时，提交仍按账号并发额度进行（额度内的动作一次性提交，页面可关闭）。
function motionParallelWidth(count) {
  const slots = Math.max(1, Number(catalog?.generationSlots) || 1);
  return Math.max(1, Math.min(count, freeMotionSlots() || 1, slots));
}
// 按空闲额度并行提交一批动作：canStart() 为真就继续发下一个；否则等任意一个已提交的动作
// 完成腾出额度再补位，保持并发通道一直是满的。start(item) 必须返回该动作的完成 Promise，
// 失败要在 start 内部记录（调度器不处理拒绝）。返回没能提交的动作（暂停、出错或次数不足时）。
async function submitPaced(items, canStart, start) {
  const pending = items.slice();
  const running = new Set();
  const pump = () => {
    while (pending.length && canStart()) {
      const job = start(pending.shift())
        .catch(() => {})
        .finally(() => running.delete(job));
      running.add(job);
    }
  };
  pump();
  while (running.size) {
    await Promise.race([...running]);
    pump();
  }
  return pending;
}
function newTask({ key, kind, name, title, detail, input, id, estimate, startedAt }) {
  return {
    key: key || `${account.id}:${crypto.randomUUID()}`,
    kind,
    name,
    title,
    detail,
    baseDetail: detail,
    input: input || null,
    id: id || "",
    estimate: estimate || catalog?.estimates?.[kind] || null,
    startedAt: startedAt || Date.now(),
  };
}
function taskRow(task) {
  const row = element("div", { className: "progress-item" });
  const box = element("div", { className: "progress-text" });
  task.titleEl = element("b", {}, task.title);
  task.detailEl = element("p", {}, task.detail);
  task.estimateEl = element("p", { className: "hint" });
  box.append(task.titleEl, task.detailEl, task.estimateEl);
  row.append(element("span", { className: "spinner" }), box);
  task.row = row;
  return row;
}
function startTask(task) {
  tasks.set(task.key, task);
  $("progressItems").append(taskRow(task));
  $("progress").hidden = false;
  startTaskClock();
  updateControls();
}
function setTaskText(task, title, detail) {
  if (title) task.title = title;
  if (detail) task.detail = detail;
  if (task.titleEl) task.titleEl.textContent = task.title;
  if (task.detailEl) task.detailEl.textContent = task.detail;
}
function setTaskEstimate(task, estimate, elapsed = 0) {
  if (estimate) task.estimate = estimate;
  task.startedAt = Date.now() - Math.max(0, elapsed) * 1000;
  updateTaskClock();
}
async function finishTask(task) {
  tasks.delete(task.key);
  if (task.row) task.row.remove();
  // 动作任务结束后卡片恢复可选：同一个动作想再做一次时可以重新勾选。
  if (task.input?.kind === "motion" && task.input.action) {
    motionInFlight.delete(task.input.action);
    syncActionSelection();
  }
  if (!tasks.size) {
    stopTaskClock();
    $("progress").hidden = true;
  }
  updateControls();
  await saveTasks().catch(() => {});
}
// 进行中的任务按账号写回IndexedDB：刷新或重开页面后继续查询同一批任务，不会重复扣次。
function resumableTasks() {
  return [...tasks.values()]
    .filter((t) => (t.kind === "draft" || t.kind === "motion") && t.input)
    .map((t) => ({
      key: t.key,
      kind: t.kind,
      name: t.name,
      input: t.input,
      id: t.id,
      estimate: t.estimate,
      startedAt: t.startedAt,
    }));
}
// 并行任务会同时写回待恢复列表：按调用顺序串行落盘，避免旧快照覆盖新快照。
let taskSaveQueue = Promise.resolve();
function saveTasks() {
  const run = taskSaveQueue.then(async () => {
    const list = resumableTasks();
    if (list.length)
      await local("pending", "put", { uid: account.id, tasks: list });
    else await local("pending", "delete", account.id);
  });
  // 队列本身吞掉失败，调用方仍能从返回的 Promise 上拿到错误。
  taskSaveQueue = run.catch(() => {});
  return run;
}
// 二次提醒：同账号已有完全相同的任务在排队或执行时，由用户确认是否再创建一个。
// 弹窗在页面内构建，不依赖浏览器原生 confirm，窄屏与后台标签页都能正常显示。
function confirmDuplicateTask(info) {
  const kind = info?.existingKind === "motion" ? "动作" : "角色定稿";
  const action =
    info?.existingKind === "motion" && info?.existingAction
      ? actionLabel(info.existingAction)
      : "";
  const when = info?.existingAt
    ? new Date(info.existingAt * 1000).toLocaleString("zh-CN")
    : "";
  const dialog = element("dialog", { className: "confirm-dialog" });
  dialog.append(
    element("span", { className: "eyebrow" }, "JUST CHECKING"),
    element("h2", {}, "这个配置已经在制作中"),
    element(
      "p",
      {},
      `同一个账号里已经有一个配置完全相同的${kind}任务${action ? `（${action}）` : ""}在排队或生成${when ? `，创建于 ${when}` : ""}。`,
    ),
    element(
      "p",
      {},
      "再创建一次会同时存在两个相同任务，并再消耗 1 次创作次数。如果只是多点了一次或页面重复提交，请选择取消，原来的任务会继续执行。",
    ),
  );
  const row = element("div", { className: "button-row" });
  const cancel = element(
    "button",
    { className: "secondary", type: "button" },
    "取消，不重复创建",
  );
  const confirmBtn = element(
    "button",
    { className: "primary", type: "button" },
    "仍然再创建一个 ✦",
  );
  row.append(cancel, confirmBtn);
  dialog.append(row);
  const answer = new Promise((resolve) => {
    cancel.onclick = () => resolve(false);
    confirmBtn.onclick = () => resolve(true);
    dialog.addEventListener("cancel", (event) => {
      event.preventDefault();
      resolve(false);
    });
  });
  document.body.append(dialog);
  dialog.showModal();
  return answer.finally(() => {
    dialog.close();
    dialog.remove();
  });
}
async function submitGenerate(task) {
  const result = await api("/api/generate", task.input);
  task.id = result.id;
  setTaskEstimate(task, result.estimate, 0);
}
async function poll(task) {
  if (!task.id) {
    try {
      await submitGenerate(task);
    } catch (e) {
      // 服务端检测到相同配置的任务正在制作：二次确认后换一个请求编号重新提交。
      if (e.status !== 409 || !e.api?.duplicate) throw e;
      if (!(await confirmDuplicateTask(e.api)))
        throw userNotice("已取消创建，原有任务继续执行。");
      task.input = {
        ...task.input,
        requestId: crypto.randomUUID(),
        allowDuplicate: true,
      };
      await submitGenerate(task);
    }
    await saveTasks();
    await refreshAccount();
  }
  // 任务在服务器后台执行：排队和生成都计入等待时间，页面关闭不影响任务。
  const deadline = Date.now() + 30 * 60 * 1000;
  while (Date.now() < deadline) {
    let result;
    try {
      result = await api("/api/jobs/" + task.id);
    } catch (e) {
      if (e.status) throw e;
      setTaskText(
        task,
        null,
        "使用同一任务继续查询，不会重复生成或扣次。",
      );
      await new Promise((r) => setTimeout(r, 4000));
      continue;
    }
    if (result.status === "queued") {
      setTaskEstimate(
        task,
        result.estimate || task.estimate,
        result.elapsedSeconds || 0,
      );
      setTaskText(
        task,
        null,
        "生成通道忙碌，任务正在服务器排队等待，开始后会自动继续。现在也可以关闭页面，稍后回来查看。",
      );
    }
    // pending_upstream：图片服务已接单、仍在生成，服务器会继续认领同一个任务。
    if (result.status === "pending_upstream") {
      setTaskEstimate(
        task,
        result.estimate || task.estimate,
        result.elapsedSeconds || 0,
      );
      setTaskText(
        task,
        null,
        "图片服务已接单，这一张生成得比较久，服务器会继续认领同一个任务，不需要重新提交，也不会重复扣次。可以关闭页面稍后回来查看。",
      );
    }
    if (result.status === "running") {
      setTaskEstimate(
        task,
        result.estimate || task.estimate,
        result.elapsedSeconds || 0,
      );
      setTaskText(task, null, task.baseDetail);
    }
    if (result.status === "succeeded" || result.status === "failed") {
      if (result.estimate) {
        catalog.estimates ||= {};
        catalog.estimates[task.input.kind] = result.estimate;
        renderEstimates();
      }
    }
    if (result.status === "succeeded") return result;
    if (result.status === "failed" || result.status === "expired")
      throw new Error(NETWORK_ERROR_MESSAGE);
    await new Promise((r) => setTimeout(r, 2500));
  }
  throw new Error("任务仍在处理中，请刷新页面恢复查询，不会重复创建或扣次。");
}
// 提交并跟踪一个任务；完成后按任务类型保存结果，多个任务互不阻塞。
async function runTask(task) {
  startTask(task);
  await saveTasks().catch(() => {});
  try {
    const result = await poll(task);
    if (task.kind === "draft") await receiveDraft(result, task);
    else await saveMotion(result, task);
  } catch (e) {
    showError(e);
    if (task.kind === "motion") await renderGallery().catch(showError);
  } finally {
    await finishTask(task);
    await refreshAccount().catch(showError);
  }
}
const DRAFT_TITLE = "正在制作专属于你的角色";
const DRAFT_DETAIL =
  "我们会保留你的面部特征，再为你换上喜欢的造型。通常需要几分钟。任务在服务器后台执行，你可以关闭页面稍后回来看。";
async function makeDraft() {
  try {
    if (taskLimitReached()) {
      toast(`已达任务上限${concurrencyLimit()}，请等待部分任务完成后再提交`);
      return;
    }
    if (availableCredits() < 1) {
      $("redeemDialog").showModal();
      return;
    }
    if (!(await refreshServiceState())) {
      toast(NETWORK_ERROR_MESSAGE);
      return;
    }
    if (availableCredits() < 1) {
      $("redeemDialog").showModal();
      return;
    }
    const input = {
      requestId: crypto.randomUUID(),
      kind: "draft",
      selection: selection(),
      selfie: await blobData(selfie),
      draft: "",
      receipt: "",
      action: "",
    };
    // 提交后立即返回：等待中可以修改服装、配色或画风，再提交下一个定稿任务。
    runTask(
      newTask({
        kind: "draft",
        name: "角色定稿",
        title: DRAFT_TITLE,
        detail: DRAFT_DETAIL,
        input,
      }),
    ).catch(showError);
  } catch (e) {
    showError(e);
  }
}
async function receiveDraft(result, task) {
  const draft = dataBlob(result.image);
  const base = profile || { uid: account.id, selfie: null };
  const entry = {
    draft,
    receipt: result.receipt,
    selection: task.input.selection,
    accepted: false,
  };
  profile = {
    ...base,
    uid: account.id,
    selfie: selfie || base.selfie,
    // 保留历史定稿为候选，修改造型重新生成不会丢失之前的定稿图（与云端同步上限一致：30 张）。
    candidates: [
      entry,
      ...(base.candidates || []).filter((c) => c.receipt !== result.receipt),
    ].slice(0, 30),
  };
  // 同时提交多个定稿时只把结果加入「我的定稿」，避免打断当前操作；
  // 最后一个定稿任务完成后再自动展示，与只提交一个任务的体验保持一致。
  const parallel = [...tasks.values()].some(
    (t) => t !== task && t.kind === "draft",
  );
  if (!parallel) {
    profile.draft = draft;
    profile.receipt = result.receipt;
    profile.selection = task.input.selection;
    profile.accepted = false;
  }
  await local("profiles", "put", profile);
  renderCandidates();
  if (!parallel && !motionSubmitting()) {
    showDraft();
    toast("你的专属角色做好啦，先看看像不像你。");
  } else {
    toast("新的角色定稿已完成，可在「我的定稿」中查看或切换。");
    updateControls();
  }
}
async function encode(sheet) {
  // 按后台配置的动作序列图规格切格：4×4 共16格输出256×256帧，5×5 共25格输出128×128帧。
  // 上游序列图固定为 1024×1024，5×5 无法整除时按比例取整划分格边界，与服务器合成一致。
  const { cols, frames: count, size } = motionSpec();
  const bitmap = await createImageBitmap(sheet);
  try {
    if (bitmap.width !== bitmap.height || bitmap.width < cols)
      throw new Error(
        `动作图未形成可均匀切分的方形${count}格，请下载原图检查；本地合成不再扣次。`,
      );
    const frames = [];
    for (let n = 0; n < count; n++) {
      const col = n % cols,
        row = Math.floor(n / cols);
      const sx = Math.floor((col * bitmap.width) / cols);
      const sy = Math.floor((row * bitmap.height) / cols);
      const ex = Math.floor(((col + 1) * bitmap.width) / cols);
      const ey = Math.floor(((row + 1) * bitmap.height) / cols);
      const canvas = document.createElement("canvas");
      canvas.width = size;
      canvas.height = size;
      const ctx = canvas.getContext("2d", { willReadFrequently: true });
      ctx.drawImage(bitmap, sx, sy, ex - sx, ey - sy, 0, 0, size, size);
      frames.push(ctx.getImageData(0, 0, size, size).data.buffer);
    }
    return await new Promise((resolve, reject) => {
      const worker = new Worker("/assets/gif-worker.v3.js", { type: "module" });
      worker.onmessage = (e) => {
        worker.terminate();
        if (e.data.error) reject(new Error(e.data.error));
        else resolve(new Blob([e.data.bytes], { type: "image/gif" }));
      };
      worker.onerror = () => {
        worker.terminate();
        reject(new Error("动图合成失败，动作原图已保存在本机，可重新合成"));
      };
      worker.postMessage({ frames, width: size, height: size }, frames);
    });
  } finally {
    bitmap.close();
  }
}
async function saveMotion(result, task) {
  const work = {
    id: task.id,
    uid: account.id,
    name: task.name,
    category: task.input.selection.category,
    created: Date.now(),
    sheet: dataBlob(result.image),
    // 服务器已合成GIF时直接使用；旧任务或合成失败时退回本机合成。
    gif: result.gif ? dataBlob(result.gif) : null,
  };
  await local("works", "put", work);
  if (!work.gif) {
    setTaskText(
      task,
      "图片已生成，正在本机合成 GIF",
      `${motionSpec().frames} 帧连续动作正在拼接。这一步不调用图像服务，也不扣次数。`,
    );
    work.gif = await encode(work.sheet);
    await local("works", "put", work);
  }
  await renderGallery();
  return work;
}
async function makeMotions() {
  // 每次点击只提交本次选中的动作：已经有动作在制作时也能继续挑动作提交新的批次。
  if (!selectedActions.size) return;
  if (selectedActions.size > availableCredits()) {
    toast("所选动作超过剩余次数，请减少动作或先兑换次数");
    $("redeemDialog").showModal();
    return;
  }
  if (taskLimitReached()) {
    toast(`已达任务上限${concurrencyLimit()}，请等待部分任务完成后再提交`);
    return;
  }
  const c = catalog.categories.find((c) => c.id === profile.selection.category);
  const actions = c.actions.filter((a) => selectedActions.has(a.id));
  // 点击就把这一批从“已选”取下：连点两次或再次点击都不会重复提交同一批动作；
  // 没有真正提交出去的动作（暂停、额度不足、失败）会在批次结束时放回选择里。
  for (const a of actions) selectedActions.delete(a.id);
  const batch = { paused: false };
  motionBatches.add(batch);
  $("stopBtn").textContent = "停止提交新动作";
  $("stopBtn").disabled = false;
  syncActionSelection();
  // 固定本批次使用的定稿：并行任务的完成不影响后续动作所用的定稿与凭证。
  const source = {
    selection: profile.selection,
    draft: profile.draft,
    receipt: profile.receipt,
    selfie: profile.selfie,
  };
  const launched = new Set();
  const retry = new Set();
  let failure = null;
  try {
    if (!(await refreshServiceState())) {
      toast(NETWORK_ERROR_MESSAGE);
      return;
    }
    if (actions.length > availableCredits()) {
      $("redeemDialog").showModal();
      return;
    }
    // 整批动作共用同一张自拍与定稿，只转换一次图片数据，避免并行提交时重复编码。
    const selfie = await blobData(source.selfie);
    const draft = await blobData(source.draft);
    let started = 0;
    const launch = (action) => {
      started++;
      launched.add(action.id);
      motionInFlight.add(action.id);
      syncActionSelection();
      const task = newTask({
        kind: "motion",
        name: action.name,
        title: `正在制作 ${started}/${actions.length} · ${action.name}`,
        detail:
          "每个动作单独生成，完成后自动保存到本机作品集。任务在服务器后台执行，可以关闭页面稍后回来继续。",
        input: {
          requestId: crypto.randomUUID(),
          kind: "motion",
          selection: source.selection,
          action: action.id,
          selfie,
          draft,
          receipt: source.receipt,
        },
      });
      startTask(task);
      saveTasks().catch(() => {});
      return poll(task)
        .then(async (result) => {
          await saveMotion(result, task);
          selectedActions.delete(action.id);
          await refreshAccount();
        })
        .catch((e) => {
          // 单个动作失败后不再提交新动作，已经在跑的任务照常查询完成；
          // 失败的动作放回选择里，方便直接重试（服务器按配置退回次数）。
          if (!failure) failure = e;
          retry.add(action.id);
        })
        .finally(() => finishTask(task));
    };
    // 有空闲并发额度就把下一个动作发出去；额度用满后，一个动作结束再补上下一个。
    const left = await submitPaced(
      actions,
      () =>
        !batch.paused &&
        !failure &&
        !taskLimitReached() &&
        availableCredits() > 0,
      launch,
    );
    if (failure) throw failure;
    if (batch.paused)
      toast("已停止提交新动作，已提交的动作会继续完成。");
    else if (left.length)
      // 还有动作没提交：只可能是额度被其他任务占用或次数不足，保留选择让用户稍后再发起。
      toast(
        availableCredits() > 0
          ? "并发额度已被其他任务占用，其余动作仍保留在选择里"
          : "剩余次数不足，其余动作仍保留在选择里",
      );
    else toast("你的动态小分身已经做好，记得下载保存！");
  } catch (e) {
    showError(e);
    await renderGallery().catch(showError);
  } finally {
    // 没提交出去的动作（暂停、额度不足、失败）放回选择里，方便稍后再发起。
    for (const a of actions)
      if (!launched.has(a.id) || retry.has(a.id)) selectedActions.add(a.id);
    motionBatches.delete(batch);
    if (!motionSubmitting()) {
      $("stopBtn").hidden = true;
      $("stopBtn").disabled = false;
    }
    syncActionSelection();
    showActions();
  }
}
async function renderGallery() {
  const works = (await local("works", "getAll"))
    .filter((w) => w.uid === account.id)
    .sort((a, b) => b.created - a.created);
  for (const url of galleryURLs) URL.revokeObjectURL(url);
  galleryURLs = [];
  $("gallery").replaceChildren();
  $("galleryCount").textContent = works.length;
  $("galleryEmpty").hidden = works.length > 0;
  for (const work of works) {
    const card = element("article", { className: "gallery-card" });
    const url = URL.createObjectURL(work.gif || work.sheet);
    galleryURLs.push(url);
    card.append(
      element("img", {
        src: url,
        alt: work.name + " 动态作品",
        loading: "lazy",
      }),
      element("h3", {}, work.name),
      element(
        "p",
        {},
        new Date(work.created).toLocaleString("zh-CN") +
          (work.gif ? " · 已存本机" : " · 待合成"),
      ),
    );
    const row = element("div", { className: "button-row" });
    if (work.gif) {
      row.append(
        element(
          "a",
          { href: url, download: work.name + ".gif", className: "download" },
          "↓ 保存 GIF",
        ),
      );
    } else {
      const retry = element(
        "button",
        { className: "download" },
        "重新合成 · 免费",
      );
      retry.onclick = () =>
        busyButton(retry, async () => {
          work.gif = await encode(work.sheet);
          await local("works", "put", work);
          await renderGallery();
        });
      row.append(retry);
    }
    const sheet = element("button", { className: "text-button" }, "原图");
    sheet.onclick = () => download(work.sheet, work.name + "-动作原图.png");
    row.append(sheet);
    const remove = element("button", { className: "text-button" }, "移除");
    remove.onclick = async () => {
      if (confirm("仅从本机作品集移除这张作品？请先确保已下载保存。")) {
        await local("works", "delete", work.id);
        await renderGallery();
      }
    };
    row.append(remove);
    card.append(row);
    $("gallery").append(card);
  }
}
async function restoreProfile() {
  profile = await local("profiles", "get", account.id);
  if (!profile) return;
  if (!profile.selection) {
    // 仅上传过照片、尚未生成定稿：恢复照片预览，停留在第一步。
    selfie = profile.selfie;
    if (selfie) {
      showPhoto(selfie);
      $("photoInfo").textContent = "已恢复上次上传的照片，可直接使用或换一张。";
    }
    return;
  }
  if (profile.draft && !profile.candidates) {
    // 兼容旧数据：把已有定稿转入候选列表。
    profile.candidates = [
      {
        draft: profile.draft,
        receipt: profile.receipt,
        selection: profile.selection,
        accepted: profile.accepted,
      },
    ];
  }
  selfie = profile.selfie;
  // 跨设备同步来的定稿可能没有本机自拍：仅在有照片时恢复预览。
  if (selfie) showPhoto(selfie);
  chooseCategory(profile.selection.category, true);
  $("clothes").value = profile.selection.clothes;
  $("weapon").value = profile.selection.weapon;
  selectedColor = profile.selection.color;
  for (const b of $("colors").children) {
    b.classList.toggle("selected", b.dataset.value === selectedColor);
    b.setAttribute("aria-pressed", String(b.dataset.value === selectedColor));
  }
  // 旧记录没有画风字段时按默认画风恢复。
  selectStyle(profile.selection.style || "default", true);
  // 定稿列表独立于当前步骤渲染：已确认直接进入第3步时也要能看到。
  renderCandidates();
  if (profile.accepted) showActions();
  else showDraft();
}
// 同一账号跨设备同步“我的定稿”：定稿生成成功时服务器会保存云端副本，
// 这里拉取云端列表，把本机没有的定稿（按定稿凭证去重）合并进候选并落盘。
async function syncDrafts() {
  const res = await fetch("/api/drafts");
  if (!res.ok) return;
  const { items } = await res.json();
  if (!items?.length) return;
  const known = new Set(
    (profile?.candidates || []).map((c) => c.receipt).filter(Boolean),
  );
  const entries = [];
  for (const d of items) {
    if (known.has(d.receipt)) continue;
    // 单张拉取失败只跳过该张，下次进入页面会再次尝试同步。
    const r = await fetch(
      `/api/drafts/${encodeURIComponent(d.receipt)}/image`,
    ).catch(() => null);
    if (!r?.ok) continue;
    entries.push({
      draft: await r.blob(),
      receipt: d.receipt,
      selection: d.selection,
      accepted: false,
    });
  }
  if (!entries.length) return;
  const base = profile || { uid: account.id, selfie: null };
  // 云端在前（新到旧），本机独有在后，同样最多保留 30 张。
  const candidates = [
    ...entries,
    ...(base.candidates || []).filter(
      (c) => !c.receipt || !entries.some((e) => e.receipt === c.receipt),
    ),
  ].slice(0, 30);
  profile = { ...base, uid: account.id, candidates };
  if (!base.draft) {
    // 本机还没有定稿（如新设备登录）：直接切到云端最新定稿展示。
    profile.draft = candidates[0].draft;
    profile.receipt = candidates[0].receipt;
    profile.selection = candidates[0].selection;
    profile.accepted = false;
    showDraft();
    toast("已从云端同步你在其他设备生成的定稿。");
  } else {
    toast("已同步云端的定稿，可在「我的定稿」中切换查看。");
  }
  await local("profiles", "put", profile);
  renderCandidates();
  updateControls();
}
// 恢复刷新前提交的任务：同一账号可能同时存在多个任务，逐个继续查询同一个任务编号。
function pendingList(stored) {
  if (!stored) return [];
  if (Array.isArray(stored.tasks))
    return stored.tasks.filter((t) => t && t.input && t.kind);
  // 兼容旧版本存放的单个任务记录。
  if (stored.input)
    return [
      {
        key: `${account.id}:${stored.input.requestId || "legacy"}`,
        kind: stored.input.kind,
        name: stored.name || "角色定稿",
        input: stored.input,
        id: stored.id,
        estimate: stored.estimate,
        startedAt: stored.startedAt,
      },
    ];
  return [];
}
async function resume() {
  const list = pendingList(await local("pending", "get", account.id));
  if (!list.length) return;
  if (!selfie) {
    // 上次已上传照片：恢复照片预览。
    const withPhoto = list.find((item) => item.input?.selfie);
    if (withPhoto) {
      selfie = dataBlob(withPhoto.input.selfie);
      showPhoto(selfie);
    }
  }
  let restored = 0;
  for (const item of list) {
    // 刷新前已经在制作的动作同样按“制作中”标记：恢复后不会被再次勾选提交，避免重复创建。
    if (item.kind === "motion" && item.input?.action) {
      motionInFlight.add(item.input.action);
      restored++;
    }
    runTask(
      newTask({
        key: item.key,
        kind: item.kind,
        name: item.name,
        title:
          item.kind === "draft" ? "正在恢复上一次创作" : `正在继续 ${item.name}`,
        detail:
          "任务在服务器后台继续执行，这里继续查询原任务，不会额外创建任务或重复扣次。",
        input: item.input,
        id: item.id,
        estimate: item.estimate,
        startedAt: item.startedAt,
      }),
    ).catch(showError);
  }
  if (restored) syncActionSelection();
}
function events() {
  $("refreshServiceBtn").onclick = () =>
    busyButton($("refreshServiceBtn"), async () => {
      const available = await refreshServiceState();
      toast(
        available
          ? "服务配置已更新，可以继续创作。"
          : "图像服务仍未配置，请联系管理员。",
      );
    });
  const checkService = () => {
    if (catalog && !tasks.size) refreshServiceState().catch(showError);
  };
  window.addEventListener("focus", checkService);
  document.addEventListener("visibilitychange", () => {
    if (!document.hidden) checkService();
  });
  window.addEventListener("storage", (event) => {
    if (event.key === "gif.settings.updated") checkService();
  });
  $("photo").onchange = (e) => preparePhoto(e.target.files[0]).catch(showError);
  $("dropzone").ondragover = (e) => {
    e.preventDefault();
    $("dropzone").classList.add("drag");
  };
  $("dropzone").ondragleave = () => $("dropzone").classList.remove("drag");
  $("dropzone").ondrop = (e) => {
    e.preventDefault();
    $("dropzone").classList.remove("drag");
    preparePhoto(e.dataTransfer.files[0]).catch(showError);
  };
  $("clothes").onchange = $("weapon").onchange = updateControls;
  $("draftBtn").onclick = $("redraftBtn").onclick = makeDraft;
  $("editBtn").onclick = () => step(1);
  // 三个创作步骤可任意跳转：已就绪的步骤恢复内容渲染，未就绪的展示引导空态。
  $("step1").onclick = () => step(1);
  $("step2").onclick = () => (profile?.draft ? showDraft() : step(2));
  $("step3").onclick = () => (profile?.accepted ? showActions() : step(3));
  $("draftEmptyBtn").onclick = () => step(1);
  $("motionEmptyBtn").onclick = () => (profile?.draft ? showDraft() : step(2));
  $("acceptBtn").onclick = () =>
    busyButton($("acceptBtn"), async () => {
      const result = await api("/api/accept", { receipt: profile.receipt });
      profile.receipt = result.receipt;
      profile.accepted = true;
      for (const c of profile.candidates || [])
        if (c.draft === profile.draft) {
          c.receipt = result.receipt;
          c.accepted = true;
        }
      await local("profiles", "put", profile);
      showActions();
    });
  $("backDraft").onclick = showDraft;
  $("motionBtn").onclick = makeMotions;
  $("stopBtn").onclick = () => {
    if (!motionSubmitting()) return;
    // 暂停只停止提交新动作，已经提交的动作照常查询到完成，不会重复提交。
    for (const b of motionBatches) b.paused = true;
    $("stopBtn").textContent = "停止提交新动作中…";
    $("stopBtn").disabled = true;
  };
  $("refreshGallery").onclick = () => renderGallery().catch(showError);
  $("feedbackOpenBtn").onclick = () => {
    updateFeedback();
    if (!$("feedbackDialog").open) $("feedbackDialog").showModal();
  };
  $("feedbackContent").oninput = updateFeedback;
  $("feedbackForm").onsubmit = (e) => {
    e.preventDefault();
    busyButton($("feedbackSubmitBtn"), async () => {
      const content = $("feedbackContent").value.trim();
      if (!content) {
        toast("请先写点内容再提交");
        return;
      }
      if ([...content].length > 200) {
        toast("反馈内容请控制在200字以内");
        return;
      }
      await api("/api/feedback", { content });
      $("feedbackContent").value = "";
      updateFeedback();
      $("feedbackDialog").close();
      toast("反馈已提交，谢谢你的建议！");
    });
  };
  function renderAccountInfo() {
    $("accountInfo").textContent =
      `账号 ${account.name || account.id.slice(0, 8)} · 剩余 ${account.credits} 次${account.email ? " · " + account.email : " · 当前为设备账号"}`;
  }
  const namePattern = /^[\p{L}\p{Nd}_·-]+$/u;
  $("nameForm").onsubmit = (e) => {
    e.preventDefault();
    busyButton($("saveNameBtn"), async () => {
      const name = $("accountName").value.trim();
      if (!name) {
        toast("请输入账户名");
        return;
      }
      if ([...name].length > 20 || !namePattern.test(name)) {
        toast(
          "账户名最长 20 个字符，仅支持中文、字母、数字及 _ - ·，不含空格或其他特殊符号",
        );
        return;
      }
      const s = await api("/api/account/name", { name });
      setSession(s);
      await refreshAccount();
      renderAccountInfo();
      $("accountName").value = account.name || "";
      toast(`账户名已保存为「${account.name}」`);
    });
  };
  $("accountBtn").onclick = () => {
    renderAccountInfo();
    $("accountName").value = account.name || "";
    $("email").value = account.email || "";
    $("accountDialog").showModal();
  };
  $("callsOpenBtn").onclick = () => {
    $("callsDialog").showModal();
    loadCalls();
  };
  $("creditsBtn").onclick = () => $("redeemDialog").showModal();
  $("redeemOpenBtn").onclick = () => $("redeemDialog").showModal();
  $("redeemContactBtn").onclick = () => {
    $("redeemDialog").close();
    $("contactOpenBtn").click();
  };
  $("redeemHelpSection").onclick = async () => {
    const text = $("redeemHelpContent").textContent.trim();
    if (!text) return;
    let copied = false;
    try {
      await navigator.clipboard.writeText(text);
      copied = true;
    } catch {
      const helper = document.createElement("textarea");
      helper.value = text;
      helper.style.position = "fixed";
      helper.style.opacity = "0";
      document.body.appendChild(helper);
      helper.select();
      try {
        copied = document.execCommand("copy");
      } catch {
        copied = false;
      }
      helper.remove();
    }
    toast(
      copied
        ? "已复制获取兑换码内容，可直接粘贴。"
        : "复制失败，请手动选中文字复制。",
    );
  };
  for (const b of document.querySelectorAll("[data-close]"))
    b.onclick = () => b.closest("dialog").close();
  // “我的账号”“最近的调用记录”：点击弹窗外的遮罩区域等同点关闭按钮。
  // 用坐标判断是否点在弹窗边界之外（backdrop），避免误触弹窗内 32px 内边距。
  for (const id of ["accountDialog", "callsDialog"]) {
    const d = $(id);
    d.addEventListener("click", (e) => {
      const r = d.getBoundingClientRect();
      if (
        e.clientX < r.left ||
        e.clientX > r.right ||
        e.clientY < r.top ||
        e.clientY > r.bottom
      )
        d.close();
    });
  }
  $("sendCodeBtn").onclick = () =>
    busyButton($("sendCodeBtn"), async () => {
      if (!$("email").checkValidity()) {
        $("email").reportValidity();
        return;
      }
      await api("/api/email/send", { email: $("email").value });
      toast("验证码已发送，请查看邮箱");
    });
  $("emailForm").onsubmit = (e) => {
    e.preventDefault();
    busyButton($("verifyBtn"), async () => {
      const s = await api("/api/email/verify", {
        email: $("email").value,
        code: $("emailCode").value,
      });
      setSession(s);
      await migrateLocal(s.mergedFrom, s.user.id);
      await refreshAccount();
      await restoreProfile();
      // 账号合并后云端可能多出另一账号的定稿，同步进本机候选。
      await syncDrafts().catch(() => {});
      await renderGallery();
      $("accountDialog").close();
      toast("邮箱已关联，账号和次数已同步。");
    });
  };
  $("redeemForm").onsubmit = (e) => {
    e.preventDefault();
    busyButton($("redeemBtn"), async () => {
      const s = await api("/api/redeem", { code: $("redeemCode").value });
      setSession(s);
      await refreshAccount();
      $("redeemDialog").close();
      $("redeemCode").value = "";
      toast(`兑换成功，增加 ${s.added} 次创作！`);
    });
  };
  $("logoutBtn").onclick = () =>
    busyButton($("logoutBtn"), async () => {
      if (
        !account.email &&
        !confirm(
          "当前账号尚未绑定邮箱，退出后无法恢复此设备账号与剩余次数。建议先关联邮箱。仍要退出吗？",
        )
      )
        return;
      await api("/api/logout", {});
      localStorage.removeItem("gif.device");
      location.reload();
    });
}
async function init() {
  try {
    await connect();
    catalog = await api("/api/catalog");
    events();
    renderCategories();
    renderStyles();
    await refreshAccount();
    renderServiceStatus();
    await restoreProfile();
    // 云端同步其他设备生成的定稿；失败不影响使用本机已有数据。
    await syncDrafts().catch(() => {});
    // 没有本地定稿、或只上传过照片时，同步隐藏定稿列表。
    renderCandidates();
    await renderGallery();
    updateControls();
    await resume();
  } catch (e) {
    $("notice").textContent = errorMessage(e);
    $("notice").hidden = false;
    showError(e);
  }
}
init();

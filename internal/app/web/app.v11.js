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
} from "./common.v2.js";
// 包装剩余次数刷新：同步更新“让我的角色动起来”处的“剩余 X 次”展示。
async function refreshAccount() {
  await commonRefreshAccount();
  if ($("remainingCost")) $("remainingCost").textContent = account.credits;
}
let catalog,
  profile = null,
  selectedCategory = "",
  selectedColor = "",
  selfie = null,
  running = false,
  paused = false;
const selectedActions = new Set();
let galleryURLs = [];
let photoURL, draftURL;
let serviceRefresh = null;
let estimateClock = null;
let estimateState = null;
let queueRemaining = 0;
let progressBaseDetail = "";

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
    ? `基于近期${value.samples}次成功调用`
    : "初始估计，暂无成功样本";
  return `预计约${durationText(value.seconds * count)}（参考${durationText(value.lowSeconds * count)}～${durationText(value.highSeconds * count)}；${basis}）`;
}
function elapsedEstimateText(value, elapsed) {
  if (!value) return `已等待 ${durationText(elapsed)}`;
  if (elapsed >= value.highSeconds)
    return `已等待 ${durationText(elapsed)} · 已超过预估，服务仍在处理中`;
  return `已等待 ${durationText(elapsed)} · 预计还需 ${durationText(Math.max(0, value.lowSeconds - elapsed))}～${durationText(Math.max(1, value.highSeconds - elapsed))}（${value.samples ? "近期参考" : "初始估计"}）`;
}
function renderEstimates() {
  $("draftEstimate").textContent = estimateText(catalog?.estimates?.draft);
  $("motionEstimate").textContent = selectedActions.size
    ? `${selectedActions.size}个动作串行生成：${estimateText(catalog?.estimates?.motion, selectedActions.size)}`
    : "选择动作后显示整组预计用时。";
}
function updateEstimateClock() {
  if (!estimateState) return;
  const elapsed = Math.max(0, (Date.now() - estimateState.started) / 1000);
  let text = elapsedEstimateText(estimateState.estimate, elapsed);
  if (queueRemaining > 0)
    text += ` · 之后还有${queueRemaining}个动作，${estimateText(catalog?.estimates?.motion, queueRemaining)}`;
  $("progressEstimate").textContent = text;
  $("progressEstimate").hidden = false;
}
function startEstimateClock(estimate, elapsed = 0) {
  estimateState = {
    estimate,
    started: Date.now() - Math.max(0, elapsed) * 1000,
  };
  if (!estimateClock) estimateClock = setInterval(updateEstimateClock, 1000);
  updateEstimateClock();
}
function stopEstimateClock() {
  clearInterval(estimateClock);
  estimateClock = estimateState = null;
  $("progressEstimate").hidden = true;
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
  $("chargeHint").textContent = catalog.chargeOnFailure
    ? "每次发出图像生成请求使用1次，失败也计次；定稿与每个动作分别计次。合成与下载不扣次。"
    : "每次成功生成图片使用1次，失败退回；定稿与每个动作分别计次。合成与下载不扣次。";
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
    catalog.estimates = latest.estimates;
    catalog.redeemHelp = latest.redeemHelp || "";
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
  };
}
function validSelection() {
  const s = selection();
  return (
    selfie &&
    s.category &&
    s.clothes &&
    s.color &&
    (s.category !== "male" || s.weapon)
  );
}
function updateControls() {
  renderEstimates();
  // 任务在服务器后台异步执行，创作步骤不再整体锁定：可继续调整造型、挑选动作或跳转步骤，
  // 仅禁用会触发新任务的按钮，避免同一账号并发创建任务。
  $("draftBtn").disabled = running || !validSelection() || !catalog?.configured;
  $("redraftBtn").disabled = running;
  $("acceptBtn").disabled = running;
  const count = selectedActions.size;
  $("selectedCount").textContent = count;
  $("cost").textContent = count;
  $("motionBtn").disabled =
    running || !count || !profile?.accepted || !catalog?.configured;
  $("stopBtn").hidden = !running || !profile?.accepted;
  $("accountBtn").disabled = running;
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
function renderCandidates() {
  for (const u of candidateURLs) URL.revokeObjectURL(u);
  candidateURLs = [];
  const strip = $("draftCandidates");
  const list = profile?.candidates || [];
  strip.hidden = list.length < 2;
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
  await local("profiles", "put", profile);
  showDraft();
}
function showActions() {
  const c = catalog.categories.find((c) => c.id === profile.selection.category);
  $("actions").replaceChildren();
  for (const a of c.actions) {
    const b = element("button", { className: "action", type: "button" });
    b.append(element("span", {}, a.icon), document.createTextNode(a.name));
    b.classList.toggle("selected", selectedActions.has(a.id));
    b.setAttribute("aria-pressed", String(selectedActions.has(a.id)));
    b.onclick = () => {
      if (selectedActions.has(a.id)) selectedActions.delete(a.id);
      else selectedActions.add(a.id);
      b.classList.toggle("selected", selectedActions.has(a.id));
      b.setAttribute("aria-pressed", String(selectedActions.has(a.id)));
      updateControls();
    };
    $("actions").append(b);
  }
  step(3);
  updateControls();
}
function actionLabel(id) {
  for (const c of catalog?.categories || [])
    for (const a of c.actions) if (a.id === id) return a.name;
  return id || "自定义动作";
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
function progress(title, detail) {
  progressBaseDetail = detail;
  $("progress").hidden = false;
  $("progressTitle").textContent = title;
  $("progressDetail").textContent = detail;
}
async function poll(pending) {
  startEstimateClock(
    pending.estimate || catalog?.estimates?.[pending.input.kind],
  );
  if (!pending.id) {
    let result;
    try {
      result = await api("/api/generate", pending.input);
    } catch (e) {
      if (e.status && e.status !== 500 && e.status !== 502 && e.status !== 504)
        await local("pending", "delete", account.id);
      throw e;
    }
    pending.id = result.id;
    pending.estimate = result.estimate;
    await local("pending", "put", pending);
    await refreshAccount();
  }
  // 任务在服务器后台执行：排队和生成都计入等待时间，页面关闭不影响任务。
  const deadline = Date.now() + 30 * 60 * 1000;
  while (Date.now() < deadline) {
    let result;
    try {
      result = await api("/api/jobs/" + pending.id);
    } catch (e) {
      if (e.status) throw e;
      progress(
        NETWORK_ERROR_MESSAGE,
        "使用同一任务继续查询，不会重复生成或扣次。",
      );
      await new Promise((r) => setTimeout(r, 4000));
      continue;
    }
    if (result.status === "queued") {
      startEstimateClock(
        result.estimate || pending.estimate,
        result.elapsedSeconds || 0,
      );
      progress(
        $("progressTitle").textContent,
        "生成通道忙碌，任务正在服务器排队等待，开始后会自动继续。现在也可以关闭页面，稍后回来查看。",
      );
    }
    if (result.status === "running") {
      startEstimateClock(
        result.estimate || pending.estimate,
        result.elapsedSeconds || 0,
      );
      progress($("progressTitle").textContent, progressBaseDetail);
    }
    if (result.status === "succeeded" || result.status === "failed") {
      if (result.estimate) {
        catalog.estimates ||= {};
        catalog.estimates[pending.input.kind] = result.estimate;
        renderEstimates();
      }
    }
    if (result.status === "succeeded") return result;
    if (result.status === "failed") {
      await local("pending", "delete", account.id);
      throw new Error(NETWORK_ERROR_MESSAGE);
    }
    if (result.status === "expired") {
      await local("pending", "delete", account.id);
      throw new Error(NETWORK_ERROR_MESSAGE);
    }
    await new Promise((r) => setTimeout(r, 2500));
  }
  throw new Error("任务仍在处理中，请刷新页面恢复查询，不会重复创建或扣次。");
}
async function makeDraft() {
  if (running) return;
  queueRemaining = 0;
  if (account.credits < 1) {
    $("redeemDialog").showModal();
    return;
  }
  running = true;
  updateControls();
  progress(
    "正在制作专属于你的角色",
    "我们会保留你的面部特征，再为你换上喜欢的造型。通常需要几分钟。任务在服务器后台执行，你可以关闭页面稍后回来看。",
  );
  try {
    if (!(await refreshServiceState())) {
      toast(NETWORK_ERROR_MESSAGE);
      return;
    }
    if (account.credits < 1) {
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
    const pending = { uid: account.id, input, name: "角色定稿" };
    await local("pending", "put", pending);
    const result = await poll(pending);
    await receiveDraft(result, pending);
    toast("你的专属角色做好啦，先看看像不像你。");
  } catch (e) {
    showError(e);
  } finally {
    running = false;
    stopEstimateClock();
    $("progress").hidden = true;
    await refreshAccount().catch(showError);
    updateControls();
  }
}
async function receiveDraft(result, pending) {
  const previous = (profile?.candidates || []).filter(
    (c) => c.receipt !== result.receipt,
  );
  const draft = dataBlob(result.image);
  profile = {
    uid: account.id,
    selfie: dataBlob(pending.input.selfie),
    draft,
    receipt: result.receipt,
    selection: pending.input.selection,
    accepted: false,
    // 保留历史定稿为候选，修改造型重新生成不会丢失之前的定稿图。
    candidates: [
      {
        draft,
        receipt: result.receipt,
        selection: pending.input.selection,
        accepted: false,
      },
      ...previous,
    ].slice(0, 8),
  };
  await local("profiles", "put", profile);
  await local("pending", "delete", account.id);
  showDraft();
}
async function encode(sheet) {
  const bitmap = await createImageBitmap(sheet);
  try {
    if (bitmap.width !== bitmap.height || bitmap.width % 4 !== 0)
      throw new Error(
        "动作图未形成可均匀切分的方形16格，请下载原图检查；本地合成不再扣次。",
      );
    const size = 256,
      frames = [];
    const tile = bitmap.width / 4;
    for (let n = 0; n < 16; n++) {
      const canvas = document.createElement("canvas");
      canvas.width = size;
      canvas.height = size;
      const ctx = canvas.getContext("2d", { willReadFrequently: true });
      ctx.drawImage(
        bitmap,
        (n % 4) * tile,
        Math.floor(n / 4) * tile,
        tile,
        tile,
        0,
        0,
        size,
        size,
      );
      frames.push(ctx.getImageData(0, 0, size, size).data.buffer);
    }
    return await new Promise((resolve, reject) => {
      const worker = new Worker("/assets/gif-worker.v1.js", { type: "module" });
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
async function saveMotion(result, pending) {
  stopEstimateClock();
  const work = {
    id: pending.id,
    uid: account.id,
    name: pending.name,
    category: pending.input.selection.category,
    created: Date.now(),
    sheet: dataBlob(result.image),
    // 服务器已合成GIF时直接使用；旧任务或合成失败时退回本机合成。
    gif: result.gif ? dataBlob(result.gif) : null,
  };
  await local("works", "put", work);
  await local("pending", "delete", account.id);
  if (!work.gif) {
    progress(
      "图片已生成，正在本机合成 GIF",
      "16 帧连续动作正在拼接。这一步不调用图像服务，也不扣次数。",
    );
    work.gif = await encode(work.sheet);
    await local("works", "put", work);
  }
  await renderGallery();
  return work;
}
async function makeMotions() {
  if (running || !selectedActions.size) return;
  if (selectedActions.size > account.credits) {
    toast("所选动作超过剩余次数，请减少动作或先兑换次数");
    $("redeemDialog").showModal();
    return;
  }
  running = true;
  paused = false;
  $("stopBtn").textContent = "完成当前后暂停";
  updateControls();
  const c = catalog.categories.find((c) => c.id === profile.selection.category);
  const actions = c.actions.filter((a) => selectedActions.has(a.id));
  try {
    if (!(await refreshServiceState())) {
      toast(NETWORK_ERROR_MESSAGE);
      return;
    }
    if (actions.length > account.credits) {
      $("redeemDialog").showModal();
      return;
    }
    for (let i = 0; i < actions.length; i++) {
      if (paused) break;
      queueRemaining = actions.length - i - 1;
      const action = actions[i];
      progress(
        `正在制作 ${i + 1}/${actions.length} · ${action.name}`,
        "每个动作单独生成，完成后自动保存到本机作品集。任务在服务器后台执行，可以关闭页面稍后回来继续。",
      );
      const input = {
        requestId: crypto.randomUUID(),
        kind: "motion",
        selection: profile.selection,
        action: action.id,
        selfie: await blobData(profile.selfie),
        draft: await blobData(profile.draft),
        receipt: profile.receipt,
      };
      const pending = { uid: account.id, input, name: action.name };
      await local("pending", "put", pending);
      const result = await poll(pending);
      await saveMotion(result, pending);
      selectedActions.delete(action.id);
      await refreshAccount();
    }
    toast(
      paused
        ? "已完成当前动作，其余动作已暂停。"
        : "你的动态小分身已经做好，记得下载保存！",
    );
  } catch (e) {
    showError(e);
    await renderGallery().catch(showError);
  } finally {
    running = false;
    stopEstimateClock();
    $("progress").hidden = true;
    await refreshAccount().catch(showError);
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
  showPhoto(selfie);
  chooseCategory(profile.selection.category, true);
  $("clothes").value = profile.selection.clothes;
  $("weapon").value = profile.selection.weapon;
  selectedColor = profile.selection.color;
  for (const b of $("colors").children) {
    b.classList.toggle("selected", b.dataset.value === selectedColor);
    b.setAttribute("aria-pressed", String(b.dataset.value === selectedColor));
  }
  if (profile.accepted) showActions();
  else showDraft();
}
async function resume() {
  const pending = await local("pending", "get", account.id);
  if (!pending) return;
  if (!selfie && pending.input?.selfie) {
    // 上次已上传照片但定稿尚未完成：恢复照片预览。
    selfie = dataBlob(pending.input.selfie);
    showPhoto(selfie);
  }
  running = true;
  updateControls();
  progress(
    "正在恢复上一次创作",
    "任务在服务器后台继续执行，这里继续查询原任务，不会额外创建任务或重复扣次。",
  );
  try {
    const result = await poll(pending);
    if (pending.input.kind === "draft") await receiveDraft(result, pending);
    else await saveMotion(result, pending);
  } catch (e) {
    showError(e);
  } finally {
    running = false;
    stopEstimateClock();
    $("progress").hidden = true;
    await refreshAccount().catch(showError);
    updateControls();
  }
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
    if (catalog && !running) refreshServiceState().catch(showError);
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
    paused = true;
    queueRemaining = 0;
    updateEstimateClock();
    $("stopBtn").textContent = "完成当前后暂停中…";
  };
  $("refreshGallery").onclick = () => renderGallery().catch(showError);
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
    await refreshAccount();
    renderServiceStatus();
    await restoreProfile();
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

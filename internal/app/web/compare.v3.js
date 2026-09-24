const $ = (id) => document.getElementById(id);

function log(...args) {
  console.log("[compare]", ...args);
}

async function api(method, path, body) {
  const opts = { method, credentials: "same-origin", headers: {} };
  if (body !== undefined) {
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  log(method, path, body !== undefined ? `body≈${opts.body.length}B` : "");
  let res;
  try {
    res = await fetch(path, opts);
  } catch (err) {
    log("fetch network error", method, path, err);
    const tip =
      err && /Failed to fetch|NetworkError|Load failed/i.test(String(err.message || err))
        ? `网络请求失败（${method} ${path}）。请确认地址栏是 http://127.0.0.1:8096（与 GIF_BASE_URL 一致）、已管理员登录，并查看服务端 [gif-debug] 日志。`
        : err.message || String(err);
    throw new Error(tip);
  }
  const text = await res.text();
  let data = {};
  try {
    data = text ? JSON.parse(text) : {};
  } catch (err) {
    log("json parse fail", res.status, text.slice(0, 200));
    throw new Error(`响应不是 JSON（HTTP ${res.status}）`);
  }
  if (!res.ok) {
    log("http error", res.status, data);
    throw new Error(data.error || `HTTP ${res.status}`);
  }
  log("ok", method, path, res.status);
  return data;
}

function fileToDataURL(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(reader.result);
    reader.onerror = reject;
    reader.readAsDataURL(file);
  });
}

function statusText(status, ms, err) {
  const sec = ms ? ` · ${(ms / 1000).toFixed(1)}s` : "";
  if (status === "succeeded") return `完成${sec}`;
  if (status === "failed") return `失败${sec}${err ? `：${err}` : ""}`;
  if (status === "running") return `生成中…${sec}`;
  if (status === "skipped") return "已跳过";
  return "排队中";
}

let models = [];
let motionPresets = [];
let selfieDataURL = "";

function fillMotionSelect() {
  const sel = $("motionSelect");
  sel.replaceChildren();
  for (const p of motionPresets) {
    const opt = document.createElement("option");
    opt.value = p.id;
    opt.textContent = p.name;
    sel.append(opt);
  }
  applyMotionPreset();
}

function applyMotionPreset() {
  const id = $("motionSelect").value;
  const p = motionPresets.find((x) => x.id === id) || motionPresets[0];
  if (p) $("motionPrompt").value = p.prompt;
}

async function loadModels() {
  const data = await api("GET", "/api/admin/compare/models");
  models = (data.models || []).filter((m) => m.supportsRef);
  motionPresets = data.motionPresets || [];
  $("draftPrompt").value = data.draftPrompt || "";
  fillMotionSelect();

  const box = $("modelList");
  box.replaceChildren();
  for (const m of models) {
    const id = `m_${m.id}`;
    const label = document.createElement("label");
    label.innerHTML = `<input type="checkbox" id="${id}" value="${m.id}" ${m.latest ? "checked" : ""}/><span><b>${m.name}</b><br/><small>${m.vendor}</small></span>`;
    box.append(label);
  }
  log("models loaded", models.length);
}

function selectedModels() {
  return [...$("modelList").querySelectorAll("input:checked")].map((el) => el.value);
}

function cellMedia(src, alt) {
  const wrap = document.createElement("div");
  wrap.className = "cell-media";
  if (src) {
    const img = document.createElement("img");
    img.src = src;
    img.alt = alt || "";
    img.onerror = () => log("img load error", src);
    wrap.append(img);
  } else {
    wrap.textContent = "…";
  }
  return wrap;
}

function renderBatch(batch) {
  $("resultWrap").hidden = false;
  const jobs = batch.jobs || [];

  const head = $("resultHead");
  head.replaceChildren();
  const corner = document.createElement("th");
  corner.className = "row-label";
  corner.textContent = "";
  head.append(corner);
  for (const job of jobs) {
    const th = document.createElement("th");
    th.className = "col-head";
    th.innerHTML = `${job.name}<small>${job.vendor}</small>`;
    head.append(th);
  }

  const draftRow = $("draftRow");
  draftRow.replaceChildren();
  const draftLabel = document.createElement("td");
  draftLabel.className = "row-label";
  draftLabel.textContent = "定稿图";
  draftRow.append(draftLabel);
  for (const job of jobs) {
    const td = document.createElement("td");
    td.append(cellMedia(job.draftUrl, `${job.name} 定稿`));
    const st = document.createElement("div");
    st.className = "cell-status";
    st.textContent = statusText(job.draftStatus, job.draftMs, job.draftError);
    td.append(st);
    if (job.draftStatus === "succeeded" || job.animStatus === "running" || job.animStatus === "succeeded") {
      const arrow = document.createElement("div");
      arrow.className = "arrow-hint";
      arrow.textContent = "↓";
      td.append(arrow);
    }
    draftRow.append(td);
  }

  const animRow = $("animRow");
  animRow.replaceChildren();
  const animLabel = document.createElement("td");
  animLabel.className = "row-label";
  animLabel.textContent = "成品动画";
  animRow.append(animLabel);
  for (const job of jobs) {
    const td = document.createElement("td");
    td.append(cellMedia(job.animUrl, `${job.name} 动画`));
    const st = document.createElement("div");
    st.className = "cell-status";
    st.textContent = statusText(job.animStatus, job.animMs, job.animError);
    td.append(st);
    animRow.append(td);
  }
}

function batchPending(batch) {
  return (batch.jobs || []).some((j) => {
    const d = j.draftStatus === "queued" || j.draftStatus === "running";
    const a = j.animStatus === "queued" || j.animStatus === "running";
    return d || a;
  });
}

async function poll(batchId) {
  for (;;) {
    const batch = await api("GET", `/api/admin/compare/batches/${batchId}`);
    renderBatch(batch);
    if (!batchPending(batch)) {
      $("runHint").textContent = "本批对比已完成。";
      $("runBtn").disabled = false;
      log("batch complete", batchId);
      return;
    }
    await new Promise((r) => setTimeout(r, 2500));
  }
}

$("selfieInput").onchange = async () => {
  const file = $("selfieInput").files[0];
  if (!file) return;
  log("selfie selected", file.name, file.type, file.size);
  selfieDataURL = await fileToDataURL(file);
  const img = $("selfiePreview");
  img.src = selfieDataURL;
  img.hidden = false;
  $("selfiePlaceholder").hidden = true;
};

$("motionSelect").onchange = applyMotionPreset;

$("selectLatest").onclick = () => {
  for (const m of models) {
    const el = document.getElementById(`m_${m.id}`);
    if (el) el.checked = !!m.latest;
  }
};
$("clearModels").onclick = () => {
  for (const el of $("modelList").querySelectorAll("input")) el.checked = false;
};

$("compareForm").onsubmit = async (e) => {
  e.preventDefault();
  if (!selfieDataURL) {
    $("runHint").textContent = "请先上传自拍照。";
    return;
  }
  const modelsSel = selectedModels();
  if (!modelsSel.length) {
    $("runHint").textContent = "请至少选择一个模型。";
    return;
  }
  $("runBtn").disabled = true;
  $("runHint").textContent = "已提交：各模型将依次生成定稿图，再生成成品动画…";
  try {
    const payload = {
      selfie: selfieDataURL,
      draftPrompt: $("draftPrompt").value.trim(),
      motionPrompt: $("motionPrompt").value.trim(),
      motionId: $("motionSelect").value,
      models: modelsSel,
      quality: $("quality").value,
    };
    log("submit", {
      models: modelsSel,
      motionId: payload.motionId,
      quality: payload.quality,
      selfieChars: payload.selfie.length,
      location: location.href,
      origin: location.origin,
    });
    const batch = await api("POST", "/api/admin/compare/run", payload);
    renderBatch(batch);
    await poll(batch.id);
  } catch (err) {
    log("submit failed", err);
    $("runHint").textContent = err.message || String(err);
    $("runBtn").disabled = false;
  }
};

(async () => {
  log("boot", { href: location.href, origin: location.origin });
  try {
    await api("GET", "/api/admin/settings");
    await loadModels();
  } catch (err) {
    log("auth/boot failed", err);
    $("authHint").hidden = false;
    $("compareForm").hidden = true;
  }
})();

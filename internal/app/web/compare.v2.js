const $ = (id) => document.getElementById(id);

async function api(method, path, body) {
  const opts = { method, credentials: "same-origin", headers: {} };
  if (body !== undefined) {
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(path, opts);
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`);
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
    td.append(cellMedia(job.draftImage, `${job.name} 定稿`));
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
    td.append(cellMedia(job.animGif, `${job.name} 动画`));
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
      return;
    }
    await new Promise((r) => setTimeout(r, 2500));
  }
}

$("selfieInput").onchange = async () => {
  const file = $("selfieInput").files[0];
  if (!file) return;
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
    const batch = await api("POST", "/api/admin/compare/run", {
      selfie: selfieDataURL,
      draftPrompt: $("draftPrompt").value.trim(),
      motionPrompt: $("motionPrompt").value.trim(),
      motionId: $("motionSelect").value,
      models: modelsSel,
      quality: $("quality").value,
    });
    renderBatch(batch);
    await poll(batch.id);
  } catch (err) {
    $("runHint").textContent = err.message || String(err);
    $("runBtn").disabled = false;
  }
};

(async () => {
  try {
    await api("GET", "/api/admin/settings");
    await loadModels();
  } catch {
    $("authHint").hidden = false;
    $("compareForm").hidden = true;
  }
})();

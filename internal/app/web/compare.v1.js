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

let models = [];

async function loadModels() {
  const data = await api("GET", "/api/admin/compare/models");
  models = data.models || [];
  const box = $("modelList");
  box.replaceChildren();
  for (const m of models) {
    const id = `m_${m.id}`;
    const label = document.createElement("label");
    label.innerHTML = `<input type="checkbox" id="${id}" value="${m.id}" ${m.latest ? "checked" : ""}/><span><b>${m.name}</b><br/><small>${m.vendor}${m.supportsRef ? " · 可参考图" : " · 仅文生图"}</small></span>`;
    box.append(label);
  }
}

function selectedModels() {
  return [...$("modelList").querySelectorAll("input:checked")].map((el) => el.value);
}

function renderBatch(batch) {
  const grid = $("resultGrid");
  grid.replaceChildren();
  for (const job of batch.jobs || []) {
    const card = document.createElement("article");
    card.className = "compare-card";
    card.dataset.job = job.id;
    const status = document.createElement("div");
    status.className = "status-pill";
    status.textContent = `${job.status}${job.durationMs ? ` · ${(job.durationMs / 1000).toFixed(1)}s` : ""}`;
    const title = document.createElement("h3");
    title.textContent = job.name;
    const vendor = document.createElement("div");
    vendor.className = "hint";
    vendor.textContent = `${job.vendor} · ${job.model}`;
    card.append(status, title, vendor);
    if (job.image) {
      const img = document.createElement("img");
      img.alt = job.name;
      img.src = job.image;
      card.append(img);
    } else if (job.error) {
      const err = document.createElement("p");
      err.className = "hint";
      err.textContent = job.error;
      card.append(err);
    } else {
      const wait = document.createElement("p");
      wait.className = "hint";
      wait.textContent = "生成中…";
      card.append(wait);
    }
    grid.append(card);
  }
}

async function poll(batchId) {
  for (;;) {
    const batch = await api("GET", `/api/admin/compare/batches/${batchId}`);
    renderBatch(batch);
    const pending = (batch.jobs || []).some((j) => j.status === "queued" || j.status === "running");
    if (!pending) {
      $("runHint").textContent = "本批对比已完成。";
      $("runBtn").disabled = false;
      return;
    }
    await new Promise((r) => setTimeout(r, 2500));
  }
}

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
  $("runBtn").disabled = true;
  $("runHint").textContent = "已提交，正在并行请求 GeekAI…";
  try {
    const body = {
      prompt: $("prompt").value.trim(),
      models: selectedModels(),
      quality: $("quality").value,
    };
    const file = $("refImage").files[0];
    if (file) body.image = await fileToDataURL(file);
    const batch = await api("POST", "/api/admin/compare/run", body);
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

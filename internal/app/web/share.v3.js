import { $, errorMessage, NETWORK_ERROR_MESSAGE } from "./common.v2.js";

// 分享独立于账号初始化，即使用户尚未登录也可查看和保存推广物料。
let config;
const content = fetch("/assets/share-config.v1.json", { cache: "force-cache" })
  .then((response) => {
    if (!response.ok) throw new Error("分享内容暂时无法读取，请刷新后重试");
    return response.json();
  })
  .then((value) => {
    config = value;
    return value;
  });
content.catch(() => {});

function selectMode(mode) {
  $("shareFeedback").hidden = true;
  for (const name of ["text", "poster"]) {
    const selected = name === mode;
    $("share" + (name === "text" ? "Text" : "Poster") + "Tab").setAttribute(
      "aria-selected",
      String(selected),
    );
    $("share" + (name === "text" ? "Text" : "Poster") + "Panel").hidden =
      !selected;
  }
  if (mode === "poster" && config) loadPoster();
}
function loadPoster() {
  const image = $("sharePosterPreview");
  if (image.getAttribute("src")) return;
  image.onload = () => {
    $("sharePosterStatus").hidden = true;
    $("downloadPoster").hidden = false;
  };
  image.onerror = () => {
    $("sharePosterStatus").hidden = false;
    $("sharePosterStatus").textContent = NETWORK_ERROR_MESSAGE;
    image.removeAttribute("src");
  };
  image.src = config.poster;
  $("downloadPoster").href = config.poster;
}
async function openShare() {
  const dialog = $("shareDialog");
  if (!dialog.open) dialog.showModal();
  selectMode("text");
  $("shareError").hidden = true;
  try {
    await content;
    $("shareText").value = config.text;
    $("shareSiteLink").href = config.url;
    $("shareSiteLink").textContent = config.url;
    $("copyShareText").disabled = false;
    $("sharePosterTab").disabled = false;
  } catch (e) {
    $("shareError").textContent = errorMessage(e);
    $("shareError").hidden = false;
  }
}
async function copyText() {
  const field = $("shareText");
  let copied = false;
  try {
    await navigator.clipboard.writeText(field.value);
    copied = true;
  } catch {
    field.focus();
    field.select();
    try {
      copied = document.execCommand("copy");
    } catch {
      copied = false;
    }
  }
  if (copied) {
    $("shareError").hidden = true;
    $("shareFeedback").textContent = "✓ 推广文案已复制，可以粘贴分享啦！";
    $("shareFeedback").hidden = false;
  } else {
    field.focus();
    field.select();
    $("shareError").textContent = NETWORK_ERROR_MESSAGE;
    $("shareError").hidden = false;
  }
}

$("shareOpenBtn").onclick = openShare;
$("shareCloseBtn").onclick = () => $("shareDialog").close();
$("shareTextTab").onclick = () => selectMode("text");
$("sharePosterTab").onclick = () => selectMode("poster");
$("copyShareText").onclick = copyText;
$("selectShareText").onclick = () => {
  $("shareText").focus();
  $("shareText").select();
};
$("shareDialog").onclose = () => {
  $("shareError").hidden = true;
};
for (const tab of [$("shareTextTab"), $("sharePosterTab")]) {
  tab.onkeydown = (event) => {
    if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    const target =
      event.key === "Home"
        ? $("shareTextTab")
        : event.key === "End"
          ? $("sharePosterTab")
          : tab === $("shareTextTab")
            ? $("sharePosterTab")
            : $("shareTextTab");
    if (!target.disabled) {
      target.click();
      target.focus();
    }
  };
}

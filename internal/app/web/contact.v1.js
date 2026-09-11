import { $, NETWORK_ERROR_MESSAGE } from "./common.v2.js";

const dialog = $("contactDialog");
const image = $("contactQR");
const status = $("contactStatus");
const save = $("saveContactQR");
const source = "/assets/wechat.v1.jpg";

image.onload = () => {
  status.hidden = true;
  save.hidden = false;
};
image.onerror = () => {
  status.textContent = NETWORK_ERROR_MESSAGE;
  status.hidden = false;
  save.hidden = true;
  image.removeAttribute("src");
};
$("contactOpenBtn").onclick = () => {
  if (!dialog.open) dialog.showModal();
  if (!image.getAttribute("src")) {
    status.textContent = "正在载入二维码…";
    status.hidden = false;
    image.src = source;
  }
};
$("contactCloseBtn").onclick = () => dialog.close();

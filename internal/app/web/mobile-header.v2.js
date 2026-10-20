const menu = document.getElementById("mobileAccountMenu");
const mobile = window.matchMedia("(max-width: 800px)");

// 原生 popover 负责开关、点外收起、Escape 以及焦点返回。
// 菜单入口复用主脚本的弹窗逻辑，避免账号或反馈状态出现两套实现。
menu.addEventListener("click", (event) => {
  const item = event.target.closest("button, a");
  if (!item) return;
  menu.hidePopover();
  if (item.dataset.headerAction) {
    document.getElementById(item.dataset.headerAction).click();
  }
});

mobile.addEventListener("change", () => {
  if (!mobile.matches && menu.matches(":popover-open")) menu.hidePopover();
});

document.getElementById("mobileRedeemBtn").addEventListener("click", () => {
  document.getElementById("creditsBtn").click();
});

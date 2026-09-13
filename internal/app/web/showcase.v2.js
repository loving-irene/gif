// 首页“示例作品”动图加载器。
//
// 每张示例卡片先显示静态首帧（无损 WebP，只有十几 KB），卡片进入可视区域后才挂上
// 动画版 WebP；离开视口就卸载，让浏览器停止解码与合成。两张 256×256、16 帧的动图
// 在手机上同时循环是实打实的 CPU 与电量开销，交给浏览器按需加载最省。
// 没有动画数据、或用户偏好减少动态效果时，卡片就停在静态首帧。
const gifPattern = /\.gif/i;

// 只在确实提供静态图片动画资源时接管；换成 GIF 或缺少动画文件时保持静态首帧。
const animatedSource = (poster) => {
  const source = poster.dataset.animSrc || "";
  return source && !gifPattern.test(source) ? source : "";
};

function mountCard(card) {
  const poster = card.querySelector(".showcase-poster");
  // 动画层必须挂在锁了 1:1 的 .showcase-media 上：它是卡片里唯一的定位祖先，
  // 挂到未定位的卡片上时 inset:0 会相对初始包含块解析，动画会画到页面左上角。
  const media = card.querySelector(".showcase-media");
  if (!poster || !media) return;
  const source = animatedSource(poster);
  if (!source) return;
  const active = () => card.dataset.showcaseActive === "1";
  let live = null;
  let shown = "";
  const detach = () => {
    if (!live) return;
    live.remove();
    live = null;
    shown = "";
  };
  const attach = () => {
    if (shown === source) return;
    const image = document.createElement("img");
    image.className = "showcase-live";
    image.alt = "";
    image.decoding = "async";
    image.addEventListener("load", () => {
      // 加载期间卡片可能已经离开视口，此时丢弃这张。
      if (!active()) return;
      detach();
      live = image;
      shown = source;
      media.append(image);
    });
    image.addEventListener("error", () => image.remove());
    image.src = source;
  };
  const show = () => {
    card.dataset.showcaseActive = "1";
    if (!matchMedia("(prefers-reduced-motion: reduce)").matches) attach();
  };
  const release = () => {
    card.dataset.showcaseActive = "0";
    detach();
  };
  if (!("IntersectionObserver" in window)) {
    show();
    return;
  }
  // 提前 200px 预取动画，卡片滑入时基本已经在动。
  new IntersectionObserver(
    (entries) => {
      for (const entry of entries) {
        if (entry.isIntersecting) show();
        else release();
      }
    },
    { rootMargin: "200px 0px" },
  ).observe(card);
}

for (const card of document.querySelectorAll(".showcase-card")) mountCard(card);

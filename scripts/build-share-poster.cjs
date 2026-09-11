// 开发期生成推广物料；运行网站无需 Node、外部二维码服务或第三方前端脚本。
const fs = require("node:fs");
const path = require("node:path");
const QRCode = require("qrcode");
const { Resvg } = require("@resvg/resvg-js");
const { PNG } = require("pngjs");
const jsQR = require("jsqr");

const root = path.resolve(process.argv[2] || path.join(__dirname, ".."));
const assets = path.join(root, "internal/app/web");
const config = JSON.parse(
  fs.readFileSync(path.join(assets, "share-config.v1.json"), "utf8"),
);
const escape = (s) =>
  String(s).replace(
    /[&<>"']/g,
    (c) =>
      ({
        "&": "&amp;",
        "<": "&lt;",
        ">": "&gt;",
        '"': "&quot;",
        "'": "&apos;",
      })[c],
  );

function character(x, y, fill, clothing, label, kind) {
  const hat =
    kind === "warrior"
      ? '<path d="M74 119 Q74 44 146 44 Q218 44 218 119 L199 100 Q146 73 93 100 Z" fill="#697b83"/><path d="M144 37 L160 67 L148 90 L135 68 Z" fill="#cfaf71"/>'
      : '<path d="M73 126 Q55 46 144 49 Q234 42 220 129 L196 87 Q141 114 93 88 Z" fill="#655246"/>';
  const ornament =
    kind === "girl"
      ? '<path d="M203 59 Q174 35 172 64 Q174 89 203 67 Q232 89 234 64 Q231 39 203 59" fill="#cb8a83"/>'
      : "";
  return `<g transform="translate(${x} ${y})"><rect width="280" height="350" rx="26" fill="#fffdfa" stroke="#e7dccb" stroke-width="2"/><rect x="16" y="16" width="248" height="266" rx="20" fill="${fill}"/><g transform="translate(-6 27)"><path d="M83 249 Q86 184 146 184 Q207 184 211 249" fill="${clothing}"/><ellipse cx="146" cy="133" rx="70" ry="76" fill="#f0c6a8"/><ellipse cx="74" cy="137" rx="11" ry="17" fill="#f0c6a8"/><ellipse cx="218" cy="137" rx="11" ry="17" fill="#f0c6a8"/>${hat}${ornament}<ellipse cx="120" cy="134" rx="5" ry="7" fill="#4c3e35"/><ellipse cx="172" cy="134" rx="5" ry="7" fill="#4c3e35"/><path d="M133 159 Q146 172 159 159" stroke="#9b6b52" stroke-width="4" fill="none" stroke-linecap="round"/><ellipse cx="105" cy="153" rx="14" ry="7" fill="#e8a994"/><ellipse cx="187" cy="153" rx="14" ry="7" fill="#e8a994"/>${kind === "child" ? '<circle cx="146" cy="220" r="18" fill="#f4df9e"/><path d="M135 220 Q145 231 156 220" stroke="#9a885e" stroke-width="3" fill="none"/>' : ""}</g><text x="140" y="321" font-size="26" fill="#78695a" text-anchor="middle">${label}</text></g>`;
}

(async () => {
  if (config.url !== "https://gif.jcc666.top/")
    throw new Error(
      "Verify the public share destination before regenerating assets",
    );
  const qr = await QRCode.toBuffer(config.url, {
    type: "png",
    width: 288,
    margin: 4,
    errorCorrectionLevel: "M",
    color: { dark: "#000000", light: "#ffffff" },
  });
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="1080" height="1440" viewBox="0 0 1080 1440"><rect width="1080" height="1440" fill="#fcf8f2"/><circle cx="1006" cy="274" r="270" fill="#f6ecdd"/><circle cx="56" cy="794" r="180" fill="#eef0e4"/><g font-family="Microsoft YaHei, PingFang SC, sans-serif"><g transform="translate(80 66)"><rect width="66" height="66" rx="20" fill="#de936d"/><path d="M33 11 V55 M14 21 L52 45 M14 45 L52 21" stroke="#fffaf2" stroke-width="6" stroke-linecap="round"/></g><text x="164" y="111" font-size="40" font-weight="bold" fill="#4c4239">拾光 <tspan fill="#bc714d">GIF</tspan></text><text x="998" y="106" text-anchor="end" font-size="19" letter-spacing="3" fill="#a69a89">一点灵感，很多可爱</text><text x="80" y="250" font-size="78" font-weight="bold" fill="#433a33">让小小的你，</text><text x="80" y="351" font-size="78" font-weight="bold" fill="#bf714e">可爱地动起来。</text><text x="84" y="412" font-size="27" fill="#978777">一张自拍，开启你的专属动态表情</text>${character(72, 492, "#e8ece9", "#8c9c98", "古风武将", "warrior")}${character(400, 463, "#f7e7df", "#d8a49a", "可爱日常", "girl")}${character(728, 492, "#edf0df", "#b0bb8c", "童趣时刻", "child")}<g font-size="25" fill="#897864"><circle cx="105" cy="919" r="21" fill="#ecdfcf"/><text x="105" y="927" text-anchor="middle" font-size="22">1</text><text x="143" y="928">上传自拍</text><text x="340" y="928" fill="#c2b29a">→</text><circle cx="437" cy="919" r="21" fill="#ecdfcf"/><text x="437" y="927" text-anchor="middle" font-size="22">2</text><text x="475" y="928">确认角色</text><text x="666" y="928" fill="#c2b29a">→</text><circle cx="758" cy="919" r="21" fill="#ecdfcf"/><text x="758" y="927" text-anchor="middle" font-size="22">3</text><text x="796" y="928">选择动作</text></g><rect x="72" y="997" width="936" height="361" rx="32" fill="#fffdfa" stroke="#e8ddcc" stroke-width="2"/><text x="115" y="1082" font-size="37" font-weight="bold" fill="#5c4b3c">把小小心情，</text><text x="115" y="1140" font-size="37" font-weight="bold" fill="#5c4b3c">变成会动的你。</text><text x="116" y="1210" font-size="24" fill="#988571">扫码制作专属 GIF</text><text x="116" y="1260" font-size="23" fill="#b87651">${escape(new URL(config.url).hostname)}</text><image x="690" y="1034" width="288" height="288" href="data:image/png;base64,${qr.toString("base64")}"/><text x="540" y="1405" text-anchor="middle" font-size="19" fill="#a59683">专属角色 · 多种动作 · 下载珍藏</text></g></svg>`;
  const fonts = process.env.GIF_POSTER_FONT
    ? [process.env.GIF_POSTER_FONT]
    : ["C:/Windows/Fonts/msyh.ttc", "C:/Windows/Fonts/msyhbd.ttc"].filter(
        fs.existsSync,
      );
  const poster = new Resvg(svg, {
    font: {
      fontFiles: fonts,
      loadSystemFonts: true,
      defaultFontFamily: "Microsoft YaHei",
    },
  })
    .render()
    .asPng();
  const bitmap = PNG.sync.read(poster);
  const decoded = jsQR(
    new Uint8ClampedArray(bitmap.data),
    bitmap.width,
    bitmap.height,
  );
  if (!decoded || decoded.data !== config.url)
    throw new Error("Poster QR verification failed");
  fs.writeFileSync(path.join(assets, "share-poster.v1.png"), poster);
  fs.writeFileSync(path.join(assets, "share-qr.v1.png"), qr);
  console.log(
    JSON.stringify({
      width: bitmap.width,
      height: bitmap.height,
      bytes: poster.length,
      qrDestination: decoded.data,
    }),
  );
})().catch((e) => {
  console.error(e.message);
  process.exitCode = 1;
});

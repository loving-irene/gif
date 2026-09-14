import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const web = fileURLToPath(new URL("../internal/app/web/", import.meta.url));

// 跟随首页实际引用，避免回归测试一直验证已下线的历史脚本。
export function frontendAsset(name) {
  const html = fs.readFileSync(path.join(web, "index.html"), "utf8");
  const app = html.match(/\/assets\/(app\.v\d+\.js)/)?.[1];
  if (!app) throw new Error("首页缺少带版本号的 app 脚本");
  if (name === "app") return path.join(web, app);
  const source = fs.readFileSync(path.join(web, app), "utf8");
  const common = source.match(/from "\.\/(common\.v\d+\.js)"/)?.[1];
  if (name !== "common" || !common) throw new Error("首页缺少 common 模块引用");
  return path.join(web, common);
}

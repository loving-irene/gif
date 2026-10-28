# 拾光 GIF 文档索引

从自拍生成专属角色，再制作动作 GIF 的轻量 Web 项目。完整的功能规则、部署与运维说明见仓库根目录的 [`README.md`](../README.md)，变更记录见 [`CHANGELOG.md`](../CHANGELOG.md)。

## 目录结构

- `cmd/server`：服务入口。
- `internal/app`：业务逻辑、提示词、GIF 合成与内嵌的静态页面（`web/`）。
- `scripts`：部署、清理与测试脚本。

## 快速开始与测试

```powershell
cd D:\personal\work\gif
Copy-Item .env.example .env   # 设置 GIF_SECRET（≥32字符）与 GIF_ADMIN_PASSWORD（≥16字符）
$env:GOCACHE = Join-Path (Get-Location) '.cache/go-build'
go run ./cmd/server -port 8096
go test ./... -count=1
```

## 提示词结构

一次动作生成的上游提示词由三段拼成（`internal/app/generate.go` 的 `buildMotionPrompt`）：

1. `builtInMotionPrefix`：图1/图2 分工、5:1 头身比、单人、全身入格，只在这里声明一次。
2. 动作过程：`internal/app/{male,female,child,daily,emotion}_action_prompts.go` 与 `daily_timeline_prompts.go`。日常动作只写起始、过程、结束和必要道具约束，用“准备、展开、重点、收势”描述阶段；情绪动作则直接描述对应表情与肢体变化。
3. `motionSpecPrompt`（`internal/app/gif.go`）：网格尺寸、分格规则、背景与边缘规则、简化要求和帧间连续要求，4×4、5×5、10×10 共用。

定稿提示词是 `internal/app/compare_prompts.go` 的 `compareDraftPrompt`，服装由 `{{clothes}}` 占位符填入。

### 为什么这样写

GIF 只表达情绪和动作感，成片每帧 128—256 像素，细节看不出来。切格合成后出现“杂质”的常见原因是：

- 内容跨格：下一格的头顶出现在本格脚底，或道具、特效越过格线。
- 半透明光晕、白边、外发光、投影、残影被切进帧边缘。
- 漂浮的爱心、星星、光点等小元素落在格边缘。

规格段对这几类都有明确禁令，动作文案也不再要求相关装饰。该约束仅依靠提示词，无法保证上游模型每次都遵守；程序端另有 `clearBottomBleed` 与主体包围盒裁切作为兜底。

### 分类与动作迁移

默认分类为男生、女生、小朋友、日常和情绪。情绪分类内置开心、疑惑、惊讶、愤怒、鄙视五个动作。读取旧数据库配置时，日常分类中的「惊讶后仰」「生气跺脚」按原动作编号迁移到情绪分类；自定义动作过程会保留，使用旧内置提示词的动作会升级为情绪分类版本。

### 已有部署的升级

后台动作与定稿提示词存放在数据库。读取配置时，仍与上一版出厂原文一致的条目会自动升级为新版（`internal/app/prompt_migration.go`，按 SHA-256 前 16 位比对）；管理员改过的条目不会被覆盖。

## 常见问题

- 生成的序列图仍有跨格内容：先在后台“动作序列图规格”试 4×4（单格像素更多），并查看管理端 `/compare` 横评不同模型。
- `npm test` 中 `scripts/test-selfie-draft-reset.mjs` 在本次改动前就失败（断言 `actual: 1, expected: 0`），与提示词无关，尚未处理。

## 友情链接

- 首页与社区页页脚友情链接：剧查查（`https://jcc666.top`）、大树记忆法（`https://card.jcc666.top`）、蓝光港湾（`https://movies.jcc666.top`）。
- 相关文件：`internal/app/web/index.html`、`internal/app/web/community.html`、`internal/app/web/footer.v2.css`。

## 创作次数获取

- **扫描添加好友**：页头「免费」入口（GIF 图标 +「免费」）与「增加次数」弹窗均可打开微信二维码；「免费」弹窗提示「扫码添加可获赠5次创作机会」。实际发放由运营侧确认后通过兑换码或后台加次完成。
- **分享到社区**：`POST /api/community/share` 成功后，服务端按北京时间自然日赠送 1 次（记入赠送额度 `gift`），每人每天最多 1 次。响应字段 `rewardCredits` 为本次是否发放，`credits` 为最新余额。去重表为 `share_daily_rewards(user_id, day)`。
- 次数来源历史事件：`register` / `redeem` / `admin` / `share`。

## 管理台媒体保存

管理台“图库”直接使用内置的 `internal/app/web/222.mp4` 作为微信聊天场景视频，不再转换 GIF。点击“保存”会沿用首页公共媒体保存模块：桌面端直接下载，移动端打开视频预览、系统分享和下载文件选项。

静态资源采用一年 immutable 缓存，因此修改公共保存逻辑时必须递增 common.vN.js 以及引用它的首页、管理台脚本版本，不能只覆盖原文件。

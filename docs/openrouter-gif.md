# OpenRouter 模型与直接 GIF

## 使用

后台 `/who` 选择 OpenRouter 并配置 API Key，默认模型为 **GPT Image 2.6 Sunburst**（`openai/gpt-image-2.6-sunburst`）；Flare（`openai/gpt-image-2.6-flare`）也支持 GIF。密钥沿用后台加密保存或 `OPENROUTER_API_KEY`，没有新环境变量。

上传自拍、选择服装与动作，点击「一键生成 GIF」。已有定稿可复用；没有定稿时自动生成并确认定稿，再提交动作。定稿与每个动作分别扣一次，失败沿用后台既有退款策略。一次点击并不代表只有一次模型调用。

2.6 定稿发送 `output_format: png`，动作发送 `output_format: gif`，仍使用 `POST https://openrouter.ai/api/v1/images` 与原有参考图顺序。动作提示词要求透明、多帧、约两秒、无限循环，不追加网格规格。GIF 的实际尺寸、帧数、时长、透明度、disposal 和循环信息均由上游返回，程序保留原始字节，不把提示词期望当作已验证效果。`xhigh` / `max` 对 2.6 原样发送。

其他模型继续生成序列图并按 4×4 / 5×5 / 10×10 切格合成；后台网格选项不影响直接 GIF。横评页 `/compare` 默认只勾选 Sunburst，可自行多选。横评也按各模型能力分别走直接 GIF 或序列图路径。

## 模型目录

2026-11-06 从 [OpenRouter 图像模型接口](https://openrouter.ai/api/v1/images/models) 核对 63 个模型的 ID 和参数，保存于 `internal/app/openrouter_models.json`。选择器显示其中 43 个支持至少两张参考图且非纯 SVG 输出的模型，因为动作阶段需要定稿与自拍。参考图容量不足的模型和纯矢量模型不作为新选项。

2.6 的 GIF、透明背景和质量枚举另经 [Sunburst 端点能力接口](https://openrouter.ai/api/v1/images/models/openai/gpt-image-2.6-sunburst/endpoints) 核对。目录声明支持 GIF 格式，不等于所有请求必然返回有效动画。

目录在构建时嵌入，不在每次用户请求时访问模型列表。刷新命令在项目根目录运行：

```powershell
node scripts/update-openrouter-models.mjs
go test ./... -count=1
go vet ./...
git diff -- internal/app/openrouter_models.json
```

脚本只读取公开目录，不使用密钥或调用生图；获取失败、重复 ID、无效记录或默认模型缺失时不覆盖快照。刷新后须检查能力变化、更新核对日期并重新构建服务。请求按快照删除不支持的参数，必要时选择模型声明支持的位图输出格式。历史 ID 继续通过兼容校验，后台保留历史已选值。

## 升级、恢复与回滚

- 新安装默认使用 2.6 Sunburst。已有 OpenRouter 配置仍为旧默认 2.5 Sunburst（或空模型）时，启动事务一次性更新模型，并写入 `settings.image_default_26` 标记。其他模型、GeekAI、密钥和提示词不变；升级后管理员明确选回 2.5 不会再次被改回。
- 新任务输入额外保存 `apiBase`、`model`、`outputFormat`，队列执行与恢复沿用提交时的模型和输出格式。旧输入没有这些字段时沿用原静态图流程，不把旧网格提示词用于直接 GIF。格式未作为公共生成接口参数开放。
- 上游 base64 响应缺少 `media_type` 时按内容识别 MIME。直接 GIF 必须是有效多帧文件，累计帧延时大于零；PNG、单帧 GIF、损坏或过大文件按失败处理，不自动再次生成。
- GIF 限制：压缩数据不超过 20 MiB，画布最长边 2048，最多 300 帧，累计解码帧像素不超过 32 Mi，画布像素乘帧数不超过 128 Mi。预先扫描结构预算再完整解码，避免仅依靠压缩大小限制内存。
- 原生 GIF 写入任务文件和云端作品的 GIF 字段，无序列原图副本；原始 GIF 保留在作品中。保存失败会令任务失败。旧模型的序列原图与合成规则保持兼容。
- 如需切回旧模式，在后台选择 GPT Image 2.5 Sunburst 或其他序列图模型即可。代码回滚前先让新版在途任务结束，再切回已支持的旧模型，避免旧二进制误处理尚未完成的直接 GIF 任务。迁移保留现有配置格式，没有删除表或用户数据。

## 验证和排障

```powershell
go test ./internal/app -run 'TestDirectGIF|TestCompareDirectGIF|TestMotionJob' -count=1
go test ./... -count=1
go vet ./...
go build -buildvcs=false -trimpath -o .cache/gif-server-check.exe ./cmd/server
node scripts/test-direct-gif.mjs
npm test
```

本地 HTTP 替身验证真实适配器发送 PNG/GIF 请求、缺失 MIME 的识别、原始动画保存、云端持久化、缓存丢失后的恢复以及静态结果失败且不重试；已有测试覆盖其他模型切格合成。前端测试验证直接 GIF 不进入 Canvas，保存和恢复保持同一 Blob。

没有调用真实付费生图，因此实际动画质量、透明效果、耗时与费用尚未实测。若上游返回静态结果，管理端任务历史会显示具体原因，用户端沿用统一错误提示。可在后台切换另一 GIF 模型或序列图模型。

`npm test` 目前在 `test-selfie-draft-reset.mjs:112` 有既有断言失败（`1 !== 0`）；使用改动前的 `app.v68.js` 可复现。本次未修改该行为；直接 GIF、并行动作、媒体缓存和保存等检查分别通过。

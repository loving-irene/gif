# 拾光 GIF

从自拍生成可辨识的专属角色，再制作动作 GIF 的轻量 Web 项目。目标域名：`https://gif.jcc666.top`。

## 技术栈与发布约定

- Go 1.22.12 + SQLite，与 `D:\personal\work\card` 保持一致。
- 原生 ES modules、IndexedDB、Canvas、Web Worker；无 CDN、无前端运行时依赖。
- 静态文件嵌入 Go 二进制，样式文件使用递增版本名。
- GitHub 私有仓库 `loving-irene/gif`，`main` 分支。
- `/var/www/gif` 拉取源码，`/var/lib/gif/gif.db` 保存账号与管理数据。
- systemd `gif` 服务、Nginx、Certbot；cron 每5分钟 fetch + pull --ff-only + 部署。
- Nginx 已存在时不覆盖，避免删除 HTTPS 配置；cron 只修改 `GIF AUTO DEPLOY` 标记块。

## 功能与业务规则

1. 首访由浏览器指纹加随机设备凭证建立账号，默认5次；后台可调整新账号默认额度，所有新账号均按该额度赠送，不按网络限制每日赠送。指纹不是密码，不允许仅凭指纹接管账号。每网络每小时硬性限制30个新账号，防止批量注册薅取赠送额度。
2. JPEG/PNG/WebP 自拍不超过5MiB（含等于）时按原文件上传，保留原始字节、尺寸、格式与元数据；仅超过5MiB时通过浏览器重编码和缩放，压缩到5MiB以内。服务端支持原始WebP，仍校验实际格式与像素上限（5000万像素，最长边16384），不会暗中调整原图。
3. 上传自拍后必选画风：默认、Q版、水墨风格三项，默认保持原有的轻度Q版效果。画风同时作用于角色定稿与动作图，并写入定稿凭证，改画风须重新生成定稿。再必选男生/女生/小朋友、服装、配色；男生必须选武器。男生古代铠甲，女生与儿童可爱风，各预设6个动作。
4. 一次生成定稿返回一张PNG，包含脸部近景和全身造型。用户确认后才允许制作动作。已生成的定稿（含重新生成的，最多保留30张）集中展示在“开始你的小小创作”区顶部，与当前处于第几步无关，第1、2、3步都能看到并点击切换回对应定稿。
5. 动作多选后按账号并发额度并行提交：一次把额度内的动作一起发出去，不再一个一个等前一个做完。服务器同时执行的生成任务数以 `generationSlots` 为上限（当前2个），超出的按创建顺序排队；任意一个动作完成后立刻补提交下一个，直到发完。有动作正在制作时也能继续勾选动作再提交一批，批次之间互不阻塞，只受账号并发上限与剩余次数限制。一个动作生成一张动作序列图（上游固定输出1024×1024方形网格图），切格规格由后台配置：默认 4×4 共16格、每帧256×256；可选 5×5 共25格、每帧128×128（1024 不能被 5 整除，按比例取整划分格边界）。服务器与浏览器本机合成都按同一规格切格，先通过循环五帧局部中位轨迹轻量稳定人物水平中心、脚底与尺寸，再用共享调色板和统一80ms帧间隔合成无限循环GIF。5×5动作提示词额外要求等时间采样、相邻帧小步长变化和人物位置尺寸稳定；规格说明仍会覆盖模板里写死的格数描述。合成不额外调用AI。动作批次不影响同时提交定稿任务。
6. 创建生成任务时扣1次；定稿与每个动作分别计次。默认失败也计次，后台支持失败退回模式；任务沿用提交时保存的退款策略，之后修改配置不会改变已提交任务的规则。在线失败及超时清理的终态、退款与消耗统计在同一事务更新，重复清理不重复退款。服务正常关停时等待生成协程退出，保留在途任务输入与上游任务号供重启恢复，不因关停取消而直接判失败；旧任务缺少可恢复输入时按中断处理。查询状态、定稿确认、本机合成、下载均不扣次。生成POST不自动重试，同一请求编号并发重传也返回原任务，不会重复执行或扣费。
7. 兑换码随机生成，永久有效，每码只可兑换一次。后台显示未兑换、已Mark、已兑换三种状态，支持复制；Mark先复制，成功后标记并变色，也可取消Mark。Mark不影响兑换，已兑换码不可改标记。新码使用AES-GCM加密保存，列表不返回原码；管理员按需通过受权限与CSRF保护的接口复制。旧码只有摘要，须使用原导出清单补录匹配原码后才能复制，无法凭摘要恢复。导出清单编号与列表对应，升级自动补充字段。
8. 邮箱验证码有效10分钟、最多5次验证；登录已有邮箱时关联设备，合并兑换与管理员增加的次数，不重复合并新设备免费次数。已绑定邮箱不能直接改绑另一邮箱。
9. 管理后台 `/who` 支持新账号额度、模型、质量、动作序列图规格（4×4/5×5）、API密钥、阿里云邮件推送（含反馈接收邮箱）、三种画风提示词、三类服装配色武器、各步骤提示词和动作内容、兑换码、账号停用/增加次数、审计记录、兑换记录（注册赠送、兑换码兑换、后台手动增加三类次数来源，按时间倒序可搜索；升级前数据自动回溯，其中注册赠送为估算值并已标注），以及只读的部署版本检查。动作名称与过程在同一卡片中编辑，桌面每行3张，平板2张、手机1张；支持新增/删除，每类1—12个动作，点击保存后生效。
10. 首页页头提供反馈入口，反馈内容最多200字（按字符计），提交后发送通知邮件并把原文写入审计记录；单账号每小时最多3条、单IP每小时最多10条。未配置邮件服务时弹窗说明无法提交，不会静默失败。
11. 同账号可同时执行多个任务，上限由后台“单用户并发任务数”配置（默认5）。“让我的角色动起来”按这个上限并行提交，且**批次之间互不阻塞**：有动作正在制作时，仍可以继续勾选动作再点一次按钮，新选中的动作作为新批次立刻提交，不必先等前面的动作结束、也不用先点“停止”。每次点击只提交当时选中的动作，点击后这批动作离开“已选”并显示为“制作中”，既不会占用“已选 N 个动作”的计数，也不会被后面的批次重复提交；动作完成后卡片恢复可选，想做第二次重新勾选即可。选中多个动作时先一次性提交额度内的动作，额度用满后每完成一个动作补提交下一个。点“停止提交新动作”会同时停止所有批次里还没发出的动作，已经提交的动作继续完成，未提交的动作保留在选择里，之后再点按钮即可重新提交。等待生成期间可以继续修改服装、配色或画风并提交新的定稿，不必等前一个任务结束；并行完成的定稿全部进入“我的定稿”列表，只提交一个时仍自动展示结果。进度区按任务分行显示各自的等待时间与参考剩余；生成按钮只在达到账号并发上限、没有选中动作或服务未配置时禁用，服务端仍会再次校验并拒绝超额请求。刷新或重开页面后，未完成的任务逐个继续查询同一任务编号，恢复中的动作同样显示为“制作中”，不会重复创建或重复扣次。
12. 再次提交同一账号下配置完全相同的任务（同一张自拍或同一张定稿、同一画风、服装、配色与动作）时，服务端先返回409而不创建、不扣次，页面弹出二次确认：确认后才以同一配置创建第二个任务并照常计次，取消则保持原任务继续执行。配置摘要排除请求编号，因此重复点击、刷新后重新提交或改完造型又改回原样都能被识别；请求编号相同的重传仍按幂等处理，直接返回原任务。不同账号之间不互相拦截，检测只覆盖排队中与进行中的任务，任务结束后同一配置可以正常再次提交。

## 画风（风格）选项

上传自拍后必选一种画风，`/api/catalog` 只下发编号、名称、说明与图标，提示词只保存在服务端：

| 编号 | 名称 | 说明 | 画风效果 |
| --- | --- | --- | --- |
| `default` | 默认 | 轻度Q版 · 保留本人特点 | 与站点原有轻度Q版效果一致，也是请求未带画风时的默认值 |
| `chibi` | Q版 | 大头小身 · 更可爱 | 头身比约1:3，头部放大、四肢短圆，保留本人脸型、发型与眼镜 |
| `ink` | 水墨风格 | 宣纸墨色 · 写意国风 | 宣纸质感、墨色浓淡与飞白笔触，毛笔线条、矿物淡彩、大面积留白 |

画风提示词紧跟在“照片处理 / 人物特征保留”提示词之后，分别加入角色定稿与动作图两个阶段的提示词，并声明与前文冲突时以画风为准。画风编号保存在定稿凭证中：动作阶段必须与定稿一致，换画风要先重新生成定稿；请求未带画风时按 `default` 处理，未知编号返回400。

三种画风在后台“角色模板与各步骤提示词”中分别编辑，不可增删。兼容旧数据：数据库中缺少画风字段时读取后自动补入默认值，旧后台页面提交不含画风的配置也不会清空画风（保存前补入默认值，仅当提交的画风不完整或提示词过短时才拒绝）。

## 用户反馈入口

首页页头“分享”右侧提供“反馈”入口（窄屏收成图标按钮），弹窗内填写反馈内容，最多200字并按字符实时显示字数，空内容不能提交。

- 提交走 `POST /api/feedback`，需要已建立的账号会话；单账号每小时最多3条、单IP每小时最多10条，超限返回429且不发信。
- 内容在服务端先去除首尾空白再校验长度，空内容或超过200字返回400。
- 邮件固定通过阿里云邮件推送华东1（杭州）的 `smtpdm.aliyun.com:465` 发送，使用 SSL/TLS；后台填写的完整发信地址同时作为 SMTP 账号、信封发件人和邮件头发件人。收件人取“反馈接收邮箱”，留空时发送到发信地址；主题为`拾光 GIF 用户反馈`（非ASCII按RFC 2047编码），正文包含反馈原文、提交时间、账号名、账号编号与绑定邮箱，便于必要时回复。
- 邮件头强制单行、正文换行统一为CRLF，反馈原文只出现在正文里，不会被当成邮件头注入。
- 未配置阿里云发信地址或 SMTP 密码时返回503，`/api/catalog` 的`feedbackConfigured` 为false，弹窗据此说明原因并禁用提交按钮。
- 发送成功后额外写一条`feedback_sent`审计记录（原文即审计目标），管理员可在后台“审计记录”中复查；发送失败返回502且不写审计，用户可重试。

## 数据边界

**服务器数据库保存**：账号/邮箱、设备凭证摘要、额度、次数来源历史（注册赠送/兑换码/后台增加）、兑换码摘要与加密原码、配置、任务编号/状态/内容摘要、用户反馈原文以及审计记录。会话和限流计数按时效清理；调用记录接口最多返回最近100条，后台裁剪历史终态记录时保留在途任务。

**服务器图片保存**：任务输入（含自拍）写入数据库同目录的 `files` 文件夹，以支持排队与重启恢复；创建失败和任务正常结束后删除对应输入，孤立文件超过3天由清理器兜底删除。生成结果文件保留最多3天；内存结果缓存最多10分钟、合计64MiB。云端定稿每账号最多30张，目前按数量淘汰，没有3天到期清理；云端作品每账号最多30件，并在3天后清理。用户主动分享的社区图片独立保存，不参加3天清理，可由本人取消分享；单账号超过60条时淘汰最旧分享。

**浏览器保存**：自拍和定稿（方便继续动作）、待处理任务、动作原图、GIF，全部通过账号ID关联到IndexedDB。浏览器刷新会恢复；不同账号作品隔离；邮箱合并会更新本机作品所属账号。同账号可通过云端接口同步定稿和保留期内的作品，本机照片仍应妥善保留，作品请及时下载。

**第三方处理**：照片和提示词会发送给 GeekAI 以生成图片，第三方处理受其服务约定约束。

## 分享文案与二维码海报

首页“分享”按钮提供两种方式：复制带emoji及正式网站链接的推广文案，或预览/下载1080×1440的PNG海报。海报二维码直接指向`https://gif.jcc666.top/`，不经过第三方短链或二维码服务。分享无需登录，不调用生图接口、不扣次数；海报仅在选择海报页签后加载。

分享内容位于`internal/app/web/share-config.v1.json`，海报生成脚本为`scripts/build-share-poster.cjs`。Node依赖只用于开发期生成物料，生产部署继续使用Go构建，PNG直接嵌入二进制。

```powershell
cd D:\personal\work\gif
npm ci
npm run build:share
```

脚本使用`qrcode`生成二维码、`resvg`渲染海报，再用独立的`jsqr`解码最终海报验证跳转地址。Windows默认使用微软雅黑；其他系统可通过`GIF_POSTER_FONT`指定中文字体文件。修改分享文案或海报后递增对应静态资源版本，避免浏览器缓存旧物料。

## 动态预计时间

定稿按钮旁显示预计用时，多选动作时显示整组并行生图的预计用时：估算用的并行宽度取账号并发余额与 `generationSlots` 的较小值，整批按 `ceil(动作数 / 并行宽度)` 轮估算；提交按账号并发余额进行，超出生成槽位的任务在服务器排队，因此实际用时可能略有出入。运行时每秒显示已等待时间和参考剩余范围，页面恢复查询后使用服务端实际已耗时校正。可同时存在多个任务：进度区按任务分行，各自显示自己的已等待时间与参考剩余。超出参考上限时提示“已超过预估，服务仍在处理中”，不会把倒计时到零当成任务完成。

预估按定稿/动作、模型和画质分组，只保存聚合统计（`generation_timing_stats`）：成功样本数、实测最短/最长耗时与移动平均（`新平均 = 0.7 × 原平均 + 0.3 × 本次耗时`，最近一次成功调用占30%权重），不再保留每次请求的耗时。同一任务只计入一次（`jobs.timing_recorded` 标记，替代原来按任务编号去重的逐次记录表），相同任务重跑不会重复计入；失败不写入耗时样本，因此快速认证失败不会把预估拉低。

估计值取移动平均；参考区间在样本达到5次后直接取实测最短/最长，样本不足时按估计值上下30%（至少上下15秒）补足宽度，都不是完成时间保证。`/api/catalog` 与任务接口的 `estimate` 新增 `minSeconds`/`maxSeconds`（实测最短/最长，无成功样本时为0）；`samples` 是累计成功样本数（不再只是最近20次），`source` 为 `initial`/`history`。升级时会把旧库 `generation_timings` 里的逐次记录汇总成上述聚合值，然后删除该表。

无成功样本时，定稿初始估计2分钟（参考1—3分钟），动作4分钟（参考2—6分钟）。预计时间包含上游生图、轮询和结果下载；浏览器本地GIF合成另行显示，不计入模型耗时样本。

### 等待上游结果与超时续查

单次等待上游的时间上限仍为8分钟（`generationTimeout`）。上游动作图较慢、8分钟内没返回图片时，任务**不再直接判失败**，而是转为“等待上游结果”（`pending_upstream`）：

- 提交成功时就把上游任务号写入 `jobs.upstream_task_id`，续查只按该任务号轮询结果，**不会再发一次生成请求，也不会重复计次**。
- 调度器优先补做等待上游结果的任务，每轮最多再等8分钟，直到拿到图片（照常合成GIF/签发定稿凭证）、上游报失败，或从任务创建起满**20分钟**后按失败收口。排队也占用这20分钟预算，收口后按任务提交时保存的退款规则决定是否退次数。
- 超出“等待上游结果”的任务仍占用单账号并发额度与全局生成槽位（服务端全局2个），避免同一账号无限堆积等待任务；服务器重启会把等待中的任务标记为 `interrupted`，与排队任务一样按需退回次数，不会静默漏掉。
- 页面在该状态下显示“图片服务已接单，这一张生成得比较久，服务器会继续认领同一个任务”，进度按已等待时间继续累计；`/api/jobs/{id}` 返回 `status=pending_upstream` 与 `upstream=true`。
- 调试日志事件：`job_waiting_upstream`（本轮等超时，转入等待）、`provider_resume`（按原上游任务号续查）、`upstream_task_id` 落库失败时记录 `status_write_error`。

## 获取兑换码内容

在`/who`后台“基础设置与图像接口”填写“获取兑换码内容”，保存全部配置后，会同步到前台兑换弹窗。支持多行文字和emoji，最多2000字；留空隐藏。内容以纯文本显示，不执行HTML或脚本，可填写领取规则、联系方式等说明。

## 本地运行

```powershell
cd D:\personal\work\gif
Copy-Item .env.example .env
# 编辑 .env，设置至少32字符的 GIF_SECRET 和至少16字符的 GIF_ADMIN_PASSWORD。
$env:GOCACHE = Join-Path (Get-Location) '.cache/go-build'
go run ./cmd/server -port 8096
```

打开 `http://127.0.0.1:8096`，后台为 `http://127.0.0.1:8096/who`。
首次未配置GeekAI时可以访问页面、兑换和管理，但生成功能关闭，不扣次。

## GeekAI 配置

后台填写 API Key，选择 `gpt-image-2.5-sunburst` 或 `gpt-image-2.5-flare`。接口固定为 `https://geekai.co/api/v1`，图片质量可选 low/medium/high/xhigh/max。

实现依据：[GPT Image 2.5](https://docs.geekai.co/cn/docs/image/openai/gpt-image-2.5)、[画图 API](https://docs.geekai.co/cn/api/image/generations)。

- POST `/images/generations`，单图 `image`，多图 `images`，均传入data URL。
- `async=true`，`retries=0`，`n=1`，`size=1024x1024`，PNG，透明背景。
- 默认请求 `b64_json`；若上游返回URL，则在允许的公开HTTPS域名内下载。
- GET `/images/{task_id}` 查询任务，最多8分钟。服务端全局最多2个并行任务，每账号同时1个。
- 不允许客户端指定上游URL、模型、提示词或下载地址；配置来自服务端后台。

未填写真实API Key时，不能验证供应商账号余额、模型开通、真实生成质量或身份相似度。

## 首次部署

与card采用相同脚本划分。先将域名DNS解析到服务器，确保8096未被占用；如被占用，在下列各步骤一致地修改PORT。

```bash
cd /var/www
git clone git@github.com:loving-irene/gif.git gif
cd /var/www/gif
chmod +x scripts/*.sh
./scripts/check_env.sh
./scripts/init_env.sh
# 使用服务器编辑器查看/修改.env，保存初始管理密码，不要把密码粘到日志中。
sudoedit /var/www/gif/.env
DOMAIN=gif.jcc666.top PORT=8096 SERVICE_NAME=gif ./scripts/setup_nginx.sh
PORT=8096 APP_DIR=/var/www/gif SERVICE_NAME=gif ./scripts/deploy.sh
sudo certbot --nginx -d gif.jcc666.top
sudo nginx -t
sudo systemctl reload nginx
curl -fsS https://gif.jcc666.top/healthz
./scripts/install_cron.sh
```

自动部署应由拥有仓库SSH读取权限、Go环境以及必要sudo权限的现有部署账号执行，与card一致。首次Certbot会要求填写证书联系邮箱及接受其条款，由部署者完成。运行账号www-data不应拥有源码写权限，数据库目录由部署脚本单独授权。

`deploy.sh` 会先测试与构建，再原子替换二进制，重启并检查健康；健康失败自动恢复上一二进制并返回失败。自动部署只有成功后才更新 `.last_deployed_commit`。

### 注册真实 IP

服务仅监听回环地址，默认启用 `GIF_TRUST_PROXY=true`，读取本机 Nginx 设置的 `X-Real-IP`，用于新账号的注册 IP 与按 IP 限流。非回环来源的请求头不会被信任；缺失或无效的请求头回退到连接地址，不读取任意 `X-Forwarded-For`。

已有部署的 `.env` 不会被初始化或更新脚本覆盖。如果仍有 `GIF_TRUST_PROXY=false`，需要改成 `true`；同名进程环境变量优先于 `.env`。站点实际生效的 Nginx `location /` 中必须包含：

```nginx
proxy_set_header X-Real-IP $remote_addr;
```

`setup_nginx.sh` 已包含此配置，但会保留已有站点配置；请直接补充实际代理位置，保留 HTTPS 配置。如前方还有 CDN 或其他代理，应先在 Nginx 按可信代理网段还原真实客户端地址。

```bash
sudoedit /var/www/gif/.env
# 将 GIF_TRUST_PROXY 改为 true，并检查实际站点的 X-Real-IP 配置。
sudo nginx -t && sudo systemctl reload nginx
sudo systemctl restart gif
```

通过公网用新的浏览器设备账号访问，再到 `/who` 检查新账号的注册 IP。直接从服务器本机访问得到回环地址是正常情况。历史账号的注册 IP 不会在登录时更新，已有的 `127.0.0.1` 无法仅凭数据库恢复成当时的真实 IP。

### 自动部署数据库备份

每次实际执行部署，在测试/构建完成后、替换二进制和重启前，通过Python标准库的SQLite在线备份接口创建一致快照，包含WAL中已提交的数据。默认目录为`/var/backups/gif`，只保留最近 **2份** `gif-auto-*.sqlite3` 自动快照；普通cron检查没有新版本、不执行部署时不会新增备份。手动执行`deploy.sh`同样受到此备份保护。

脚本从应用`.env`读取`GIF_DATABASE_PATH`，相对路径按应用目录解析，不执行配置内容。备份先写入临时文件，校验完成后原子更名，再清理旧自动快照；手动备份、其他项目文件和符号链接不会被清理。备份失败会中止部署，不替换二进制、不重启、不更新部署成功记录；首次没有数据库时记录跳过。备份默认限时60秒，文件默认仅备份执行用户可读写。

服务器需安装Python 3.7+且包含sqlite3模块（Ubuntu/Debian可用`sudo apt-get install python3`），`check_env.sh`会检查备份能力。默认通过sudo运行备份，避免SQLite目录权限阻止读取。

```bash
# 查看自动备份
sudo ls -lh /var/backups/gif/

# 自定义本次部署的备份目录
BACKUP_DIR=/var/backups/gif ./scripts/deploy.sh
```

恢复时先停止`gif`服务，确认实际数据库路径，将选定快照恢复到该路径并处理旧WAL/SHM，再恢复www-data所有权后启动。密钥配置`.env`需独立妥善保管；数据库中的加密配置依赖原`GIF_SECRET`，本备份不会复制或输出密钥。

```bash
systemctl status gif
journalctl -u gif -n 100 --no-pager
tail -n 50 /var/www/gif/logs/auto_deploy-$(date +%F).log
BRANCH=main SCHEDULE='*/5 * * * *' ./scripts/install_cron.sh
```

上线后打开 `/who` 配置 GeekAI 和阿里云邮件推送。先在华东1（杭州）邮件推送控制台创建发信地址并设置 SMTP 密码，再把完整发信地址和该密码填入后台；旧 SMTP 密码不会自动复用。也可以在首次启动前配置：

```dotenv
ALIYUN_DM_SENDER=notice@example.com
ALIYUN_DM_SMTP_PASSWORD=replace-with-console-smtp-password
```

不要提交真实密码；不要复制 card 的会话密钥，GIF 应使用独立随机密钥。

### 磁盘清理与部署版本检查

`scripts/clean_disk.sh` 在 `go build` 报“no space left on device”或部署卡死时释放空间，只回收可安全重建的内容：

| 档位 | 清理内容 |
| --- | --- |
| 默认（应用目录） | Go构建缓存（优先`GOCACHE`，否则`go env GOCACHE`，再回退`$HOME/.cache/go-build`）、构建中断残留的`gif-server.next`与本机`*.exe`、`logs/`下超过`AVS_LOG_KEEP_DAYS`天（默认2）的`auto_deploy-*.log`（`auto_deploy.sh`自身用`-mtime +1`保留今天与昨天，本脚本按“超过N天”判定，默认值略保守，只多留一天） |
| 默认（系统，需root或免密sudo） | npm下载缓存`$HOME/.npm/_cacache`、apt包缓存、systemd journal（`journalctl --vacuum-size=${AVS_JOURNAL_MAX_SIZE}`，默认50M） |
| `--aggressive`追加 | Go模块缓存（优先`GOMODCACHE`，下次构建重新下载）、snap包缓存`/var/lib/snapd/cache`、logrotate轮转的历史系统日志、`/var/backups/gif`中超过`AVS_BACKUP_KEEP_COUNT`（默认3）份的自动快照 |

```bash
sudo ./scripts/clean_disk.sh              # 生产环境释放空间
./scripts/clean_disk.sh --dry-run         # 只统计可回收空间，不删除任何文件
./scripts/clean_disk.sh --aggressive      # 追加模块缓存、snap缓存与历史系统日志
./scripts/clean_disk.sh --no-system       # 只处理应用目录内的文件
```

选项与退出码：`--dry-run`（每条清理项带`[dry-run]`前缀，不删除）、`--aggressive`、`--no-system`、`-h|--help`，以及可选位置参数应用目录（默认`${APP_DIR:-/var/www/gif}`）。退出码`0`成功、`1`有文件删除失败、`64`用法错误或拒绝危险的清理目录（`/`、空、`.`）、`66`应用目录不存在或不像gif应用（既无`go.mod`也无`.git`）。输出为 application/mode → 每条“标签+回收大小+路径” → summary汇总与`df -h <app>`；剩余空间低于`AVS_MIN_FREE_MB`（默认300）时额外warning。所有阈值（`AVS_LOG_KEEP_DAYS`、`AVS_BACKUP_KEEP_COUNT`、`AVS_JOURNAL_MAX_SIZE`、`AVS_MIN_FREE_MB`、`AVS_NPM_CACHE_DIR`、`AVS_APT_CACHE_DIR`、`AVS_JOURNAL_DIR`、`AVS_SNAP_CACHE_DIR`、`AVS_ROTATED_LOG_DIR`、`AVS_BACKUP_DIR`）都可用环境变量覆盖，便于测试注入。

安全边界：`gif.db`（含`-wal`/`-shm`）、`gif-server`、`gif-server.previous`、`.env`、`node_modules/`、`work/`、`files/`与源码永远不会被删除；无root且无免密sudo时只跳过apt/journal/snap/系统日志/备份这些需要提权的部分（npm缓存位于用户目录，仍会清理）并warning，不影响应用目录清理。清理对象只用固定的文件（`gif-server.next`、`*.exe`、`auto_deploy-*.log`）与缓存目录，不做递归通配；缓存路径必须是绝对路径且不等于应用目录或家目录，否则拒绝删除（例如`GOCACHE=off`）。

`scripts/check_deploy_version.sh` 判断线上是否已经运行`origin/<branch>`的最新提交。`gif-server`不支持`version`子命令，所以“已部署提交”只能取自`auto_deploy.sh`部署成功后写入的`.last_deployed_commit`状态文件（可用`DEPLOY_STATE_FILE`覆盖，相对路径按应用目录解析）。

管理后台“部署版本”页签以`--non-interactive`执行同一脚本，并原样展示脚本的标准输出与错误输出，不再在Go或前端重复解析Git状态。Web进程不会执行`git fetch`、回滚或部署；应用目录、分支与状态文件分别沿用`APP_DIR`、`BRANCH`、`DEPLOY_STATE_FILE`，默认值为当前工作目录、`main`与`.last_deployed_commit`。

```bash
./scripts/check_deploy_version.sh                 # 完整诊断输出
./scripts/check_deploy_version.sh --quiet         # 只输出状态关键字，便于cron或监控
./scripts/check_deploy_version.sh --non-interactive # 输出完整结果，但不询问或执行回滚部署
./scripts/check_deploy_version.sh --fetch         # 先git fetch，比较远端最新提交
./scripts/check_deploy_version.sh --branch main   # 指定跟踪分支（默认main，也支持BRANCH环境变量）
```

输出字段对齐为 application / branch / state file / deployed commit（`git log -1 --format='%h %s'`摘要，解析不到显示unknown）/ local HEAD / origin分支 / 运行二进制（`gif-server`与`gif-server.previous`是否存在）/ status。状态与退出码：

| 退出码 | 状态 | 含义与处理 |
| --- | --- | --- |
| 0 | `up-to-date` | 状态文件记录的提交等于`origin/<branch>` |
| 3 | `newer-commit-available` | `origin/<branch>`有未上线提交，等待5分钟cron或手工执行`scripts/auto_deploy.sh` |
| 4 | `stuck` | HEAD已等于`origin/<branch>`，但状态文件仍是旧提交：上一次部署失败或被跳过。先跑`scripts/clean_disk.sh`释放空间，再手工`scripts/auto_deploy.sh`（它比较的是状态文件，所以会重新构建） |
| 1 | `unknown` | 状态文件缺失或内容无法解析、无法解析`origin/<branch>`、`--fetch`失败、或不是git仓库 |
| 64 | 用法错误 | 未知选项、`--branch`缺参数、分支名为空 |

每种非0状态都会附带中文处理提示。

`/var/backups/gif`的保留策略现状：由`scripts/deploy.sh`调用的`scripts/backup_database.py`在每次部署前创建一致性快照，并在备份成功后只保留最近 **2份**`gif-auto-*.sqlite3`自动快照（`prune_backups`，手动或其他文件不动）。`clean_disk.sh`不接管这个策略；它的`--aggressive`只会清理严格匹配同一命名规则、且超出`AVS_BACKUP_KEEP_COUNT`（默认3）的历史遗留快照，由于默认阈值高于备份脚本自身的2份，常规运行不会删除任何备份。

这两个脚本的测试（自包含、无网络、打桩系统命令与`go`）为`scripts/tests/clean_disk_test.sh`与`scripts/tests/check_deploy_version_test.sh`。目前`auto_deploy.sh`只构建并调用`deploy.sh`，尚未接入这两个测试，需要在上线前或手工排查时按“本地检查”一节显式运行；如需让每次部署都跑，可在`auto_deploy.sh`的部署分支中加两行调用。

## 安全措施与边界

- HttpOnly/SameSite=Strict会话，生产Secure Cookie；Origin+CSRF双重校验。
- 所有SQL使用参数绑定；兑换、扣次、账号合并使用事务；任务幂等编号和单账号运行中唯一索引。
- 管理会话2小时过期；管理员密码仅部署配置；API Key和阿里云SMTP密码使用AES-GCM加密，页面不回显；旧邮件服务密码与阿里云密码使用不同密钥槽，不会被自动复用。
- 注册/接口/验证码/兑换/生成限流，图片MIME和像素校验，JSON体积上限，不接受SVG或用户提供的图片URL。
- 上游固定GeekAI，下载主机白名单、解析后校验公网地址并绑定实际连接IP、防止DNS重绑定/SSRF、拒绝重定向。
- CSP、禁止嵌入、no-referrer、不缓存API；无图片/密钥/验证码日志。
- Nginx只代理到回环服务；仅信任回环代理传来的X-Real-IP。若前面另有CDN，需正确配置Nginx真实客户端IP，不能直接信任任意X-Forwarded-For。
- 角色定稿凭证与自拍/定稿内容摘要、账号和造型绑定，有效期30天；过期后原作品仍可本机查看下载，新增动作需重新定稿。
- 浏览器指纹可变化或被伪造，限流降低滥用风险但不能防住分布式攻击；公网流量防护仍需服务器/云厂商/CDN配合。

## 验证

### 生图 debug 日志

在`.env`设置`GIF_DEBUG=true`并重启服务，可记录脱敏JSON诊断日志。默认关闭；本地排查时可开启，完成后改回`false`。

```env
GIF_DEBUG=true
```

日志包含同一`job_id`下的排队/请求参数摘要、实际模型ID、图片数量和字节数、提交/轮询HTTP状态、上游任务ID与request-id、错误码与脱敏错误消息、DNS/TCP/TLS结果、下载结果、耗时和是否扣次。还会记录本地生成接口的HTTP状态，帮助区分前置校验失败与上游请求失败。

不写入请求正文、自拍、生成图片、完整提示词、Authorization、API Key或带签名的完整图片URL。非JSON错误只记录类型、状态及长度，不保存原始HTML。生图POST仍不自动重试，排查日志不会额外调用模型。

本地预览日志：

```powershell
Get-Content D:\personal\work\gif\.cache\preview.err.log -Tail 100 -Wait
```

生产systemd日志：

```bash
journalctl -u gif -n 100 -f --no-pager
```

常见定位：`provider_http_end`的HTTP状态为0表示未取得上游响应，查看相邻DNS/TCP/TLS和error；400查看参数错误码，401/403查看认证或权限，429查看限流；`task_status=failed`查看上游错误消息；`download_rejected`查看图片域名白名单。具体判断以实际日志为准。

页面异常统一显示“网络异常，请稍后重试~”，不展示内部原因或排查编号。详细原因按日志中的`job_id`关联查询；正常表单校验与额度提示保留。旧版本没有保存的上游失败正文无法追溯，需要在开启日志后重现。

### SEO / GEO

首页提供可见的GIF制作说明与FAQ，支持无需JavaScript读取；其JSON-LD与可见FAQ使用同一份内容。canonical、Open Graph和站点地图使用`GIF_BASE_URL`，不使用请求Host生成地址。生产环境请设置为`https://gif.jcc666.top`。

`/robots.txt`允许公开页面并禁止API抓取，`/sitemap.xml`只包含首页；后台与API使用noindex。`/llms.txt`提供公开功能和数据边界摘要，不包含账号信息或管理入口，也不包含隐藏提权指令。

这些措施帮助搜索工具理解实际内容，不承诺索引、排名或AI推荐；llms.txt不是Google AI搜索的必要条件。依据：[Google AI搜索建议](https://developers.google.com/search/docs/appearance/ai-features)、[Google反垃圾政策](https://developers.google.com/search/docs/essentials/spam-policies)。

### 本地检查

```powershell
$env:GOCACHE = Join-Path (Get-Location) '.cache/go-build'
go test ./... -count=1
go vet ./...
npm test
```

安装Node时，Go测试还会运行真实浏览器GIF编码器，再用Go标准GIF解码器验证16/25帧、尺寸、循环、统一80ms时序、主体稳定处理和透明处置。没有Node的生产机跳过该编码器交叉验证；不影响应用部署。

```bash
bash scripts/tests/install_cron_test.sh
bash scripts/tests/clean_disk_test.sh
bash scripts/tests/check_deploy_version_test.sh
for f in scripts/*.sh scripts/tests/*.sh; do bash -n "$f"; done
```

`npm test` 运行预计时间、服务就绪、照片上传、动作批次并发及错误提示5组测试。测试自动读取首页当前引用的版本，避免一直测试历史资源。动作批次测试用最小DOM与接口替身驱动实际调度函数，并连续提交两个批次验证同时制作，不需要浏览器与网络；可单独运行：

```bash
node scripts/test-motion-parallel.mjs
```

Go测试使用临时SQLite数据库、实际HTTP处理器和模拟上游图片验证生成、退款、恢复与GIF合成；Node测试使用DOM和接口替身验证前端行为。这些检查不代表真实浏览器交互或GeekAI线上生图验证。当前代码中没有 `TestBrowserHarness`，不要使用旧的8097测试服务启动说明。

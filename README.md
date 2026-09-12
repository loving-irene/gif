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

1. 首访由浏览器指纹加随机设备凭证建立账号，默认5次；后台可调整新账号默认额度。指纹不是密码，不允许仅凭指纹接管账号。同一网络默认24小时最多3个新设备领取免费额度；超过后仍可创建零额度账号登录已有邮箱或兑换，每网络每小时硬性限制30个新账号。
2. JPEG/PNG/WebP 自拍不超过5MiB（含等于）时按原文件上传，保留原始字节、尺寸、格式与元数据；仅超过5MiB时通过浏览器重编码和缩放，压缩到5MiB以内。服务端支持原始WebP，仍校验实际格式与像素上限（5000万像素，最长边16384），不会暗中调整原图。
3. 必选男生/女生/小朋友、服装、配色；男生必须选武器。男生古代铠甲，女生与儿童可爱风，各预设6个动作。
4. 一次生成定稿返回一张PNG，包含脸部近景和全身造型。用户确认后才允许制作动作。
5. 动作多选后串行执行；一个动作生成一张4×4的16帧图。浏览器将其切分为256×256帧，用共享调色板合成无限循环GIF。合成不额外调用AI。
6. 默认每次实际提交图像生成请求扣1次（包括失败）；定稿与每个动作分别计次。后台也支持失败退回模式，服务器重启时对该模式下中断任务的额度执行一次性退回。查询状态、定稿确认、本机合成、下载均不扣次。生成POST不自动重试，携带相同请求编号不会重复执行或扣费。
7. 兑换码随机生成，永久有效，每码只可兑换一次。后台显示未兑换、已Mark、已兑换三种状态，支持复制；Mark先复制，成功后标记并变色，也可取消Mark。Mark不影响兑换，已兑换码不可改标记。新码使用AES-GCM加密保存，列表不返回原码；管理员按需通过受权限与CSRF保护的接口复制。旧码只有摘要，须使用原导出清单补录匹配原码后才能复制，无法凭摘要恢复。导出清单编号与列表对应，升级自动补充字段。
8. 邮箱验证码有效10分钟、最多5次验证；登录已有邮箱时关联设备，合并兑换与管理员增加的次数，不重复合并新设备免费次数。已绑定邮箱不能直接改绑另一邮箱。
9. 管理后台 `/who` 支持新账号额度、模型、质量、API密钥、SMTP、三类服装配色武器、各步骤提示词和动作内容、兑换码、账号停用/增加次数、审计记录。动作名称与过程在同一卡片中编辑，桌面每行3张，平板2张、手机1张；支持新增/删除，每类1—12个动作，点击保存后生效。

## 数据边界

**服务器永久保存**：账号/邮箱、设备凭证摘要、额度、兑换码摘要与加密原码、配置、会话、限流计数、任务编号/状态/内容摘要以及审计记录。

**服务器不持久保存**：自拍、定稿图、动作图、GIF。处理中的图片只经过内存，任务完成结果暂留内存最多10分钟，所有结果合计64MiB，超出时提前清理最旧结果。服务重启会清理临时图片。Nginx 请求/响应缓冲和临时文件已禁用，systemd 禁用核心转储和进程交换内存。

**浏览器保存**：自拍和定稿（方便继续动作）、待处理任务、动作原图、GIF，全部通过账号ID关联到IndexedDB。浏览器刷新会恢复；不同账号作品隔离；邮箱合并会更新本机作品所属账号。图片不会跨设备同步。页面始终提示及时下载。

**第三方处理**：照片和提示词会发送给 GeekAI 以生成图片。本站“不持久保存图片”不代表第三方也不保存，第三方处理受其服务约定约束。

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

定稿按钮旁显示预计用时，多选动作时显示整组串行生图的预计用时；运行时每秒显示已等待时间和参考剩余范围，页面恢复查询后使用服务端实际已耗时校正。超出参考上限时提示“已超过预估，服务仍在处理中”，不会把倒计时到零当成任务完成。

`generation_timings`只保存任务编号、类型、模型、画质、耗时与结果状态，不保存图片。每次正常结束的调用（含失败）记录一次，相同任务不会重复计入。预估按定稿/动作、模型和画质分组，取最近20次成功调用，按从旧到新`新估计 = 0.7 × 原估计 + 0.3 × 本次耗时`更新。参考区间为估计值上下30%（至少上下15秒），不是完成时间保证。失败耗时单独记录，避免快速认证失败把预估拉低。

无成功样本时，定稿初始估计2分钟（参考1—3分钟），动作4分钟（参考2—6分钟）。预计时间包含上游生图、轮询和结果下载；浏览器本地GIF合成另行显示，不计入模型耗时样本。现有8分钟任务超时规则不变。

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

上线后打开 `/who` 配置GeekAI和SMTP。SMTP参数可手动使用card的配置值；不要复制card的会话密钥，GIF应使用独立随机密钥。

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

```bash
./scripts/check_deploy_version.sh                 # 完整诊断输出
./scripts/check_deploy_version.sh --quiet         # 只输出状态关键字，便于cron或监控
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
- 管理会话2小时过期；管理员密码仅部署配置；API Key和SMTP密码使用AES-GCM加密，页面不回显。
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
```

安装Node时，Go测试还会运行真实浏览器GIF编码器，再用Go标准GIF解码器验证16帧、尺寸、循环、时长和透明处置。没有Node的生产机跳过该编码器交叉验证；不影响应用部署。

```bash
bash scripts/tests/install_cron_test.sh
bash scripts/tests/clean_disk_test.sh
bash scripts/tests/check_deploy_version_test.sh
for f in scripts/*.sh scripts/tests/*.sh; do bash -n "$f"; done
```

浏览器集成测试工具只在Go测试二进制内存在，生产二进制无法启用：

```powershell
$env:GIF_BROWSER_TEST='1'
$env:GIF_TEST_OUTPUT=Join-Path (Get-Location) 'work/browser'
go test ./internal/app -run '^TestBrowserHarness$' -v -timeout 30m
```

该测试服务监听8097，使用合成图片模拟上游，不代表真实GeekAI测试。

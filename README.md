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
2. JPEG/PNG/WebP 自拍在浏览器重编码，移除元数据，最大长边2048。超过5MiB自动进一步压缩；上传到服务器的每张图片不得超过5MiB。
3. 必选男生/女生/小朋友、服装、配色；男生必须选武器。男生古代铠甲，女生与儿童可爱风，各预设6个动作。
4. 一次生成定稿返回一张PNG，包含脸部近景和全身造型。用户确认后才允许制作动作。
5. 动作多选后串行执行；一个动作生成一张4×4的16帧图。浏览器将其切分为256×256帧，用共享调色板合成无限循环GIF。合成不额外调用AI。
6. 默认每次实际提交图像生成请求扣1次（包括失败）；定稿与每个动作分别计次。后台也支持失败退回模式，服务器重启时对该模式下中断任务的额度执行一次性退回。查询状态、定稿确认、本机合成、下载均不扣次。生成POST不自动重试，携带相同请求编号不会重复执行或扣费。
7. 兑换码随机生成，永久有效，每码只可兑换一次。服务端只存校验摘要，完整兑换码仅创建时显示，须立即导出。
8. 邮箱验证码有效10分钟、最多5次验证；登录已有邮箱时关联设备，合并兑换与管理员增加的次数，不重复合并新设备免费次数。已绑定邮箱不能直接改绑另一邮箱。
9. 管理后台 `/admin` 支持新账号额度、模型、质量、API密钥、SMTP、三类服装配色武器、各步骤提示词和动作内容、兑换码、账号停用/增加次数、审计记录。

## 数据边界

**服务器永久保存**：账号/邮箱、设备凭证摘要、额度、兑换码摘要、配置、会话、限流计数、任务编号/状态/内容摘要以及审计记录。

**服务器不持久保存**：自拍、定稿图、动作图、GIF。处理中的图片只经过内存，任务完成结果暂留内存最多10分钟，所有结果合计64MiB，超出时提前清理最旧结果。服务重启会清理临时图片。Nginx 请求/响应缓冲和临时文件已禁用，systemd 禁用核心转储和进程交换内存。

**浏览器保存**：自拍和定稿（方便继续动作）、待处理任务、动作原图、GIF，全部通过账号ID关联到IndexedDB。浏览器刷新会恢复；不同账号作品隔离；邮箱合并会更新本机作品所属账号。图片不会跨设备同步。页面始终提示及时下载。

**第三方处理**：照片和提示词会发送给 GeekAI 以生成图片。本站“不持久保存图片”不代表第三方也不保存，第三方处理受其服务约定约束。

## 本地运行

```powershell
cd D:\personal\work\gif
Copy-Item .env.example .env
# 编辑 .env，设置至少32字符的 GIF_SECRET 和至少16字符的 GIF_ADMIN_PASSWORD。
$env:GOCACHE = Join-Path (Get-Location) '.cache/go-build'
go run ./cmd/server -port 8096
```

打开 `http://127.0.0.1:8096`，后台为 `http://127.0.0.1:8096/admin`。
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

```bash
systemctl status gif
journalctl -u gif -n 100 --no-pager
tail -n 50 /var/www/gif/auto_deploy.log
BRANCH=main SCHEDULE='*/5 * * * *' ./scripts/install_cron.sh
```

上线后打开 `/admin` 配置GeekAI和SMTP。SMTP参数可手动使用card的配置值；不要复制card的会话密钥，GIF应使用独立随机密钥。

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

```powershell
$env:GOCACHE = Join-Path (Get-Location) '.cache/go-build'
go test ./... -count=1
go vet ./...
```

安装Node时，Go测试还会运行真实浏览器GIF编码器，再用Go标准GIF解码器验证16帧、尺寸、循环、时长和透明处置。没有Node的生产机跳过该编码器交叉验证；不影响应用部署。

```bash
bash scripts/tests/install_cron_test.sh
for f in scripts/*.sh; do bash -n "$f"; done
```

浏览器集成测试工具只在Go测试二进制内存在，生产二进制无法启用：

```powershell
$env:GIF_BROWSER_TEST='1'
$env:GIF_TEST_OUTPUT=Join-Path (Get-Location) 'work/browser'
go test ./internal/app -run '^TestBrowserHarness$' -v -timeout 30m
```

该测试服务监听8097，使用合成图片模拟上游，不代表真实GeekAI测试。

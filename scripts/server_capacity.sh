#!/usr/bin/env bash
# 探测服务器资源配置，并估算本服务可支持的并发请求数。
# 面向 Linux 生产环境（/proc、systemd、nginx），无需 root，信息缺失时按未知降级。
# 用法: ./scripts/server_capacity.sh [--port 8096] [--service gif] [--app-dir /var/www/gif] [--per-request-mib 12]
set -euo pipefail

PORT="${PORT:-8096}"
SERVICE_NAME="${SERVICE_NAME:-gif}"
APP_DIR="${APP_DIR:-/var/www/gif}"
# 单个重请求（生成/上传接口）内存峰值估算（MiB）。依据：生成请求JSON体上限15MiB、
# 自拍与定稿图各5MiB、base64解码与上游提交/轮询下载(≤20MiB)的临时缓冲。
PER_REQUEST_MIB="${PER_REQUEST_MIB:-12}"
# 预留内存：进程基础(Go运行时+SQLite页缓存)64MiB + 结果缓存上限64MiB(README数据边界)。
BASE_MIB=64
RESULTS_MIB=64
# 轻量请求按约0.1MiB/连接计；推荐运行值为理论上限的70%，为突发与GC留余量。
SAFE_PERCENT=70

while [ $# -gt 0 ]; do
  case "$1" in
    --port) PORT="${2:?missing value for --port}"; shift 2;;
    --service) SERVICE_NAME="${2:?missing value for --service}"; shift 2;;
    --app-dir) APP_DIR="${2:?missing value for --app-dir}"; shift 2;;
    --per-request-mib) PER_REQUEST_MIB="${2:?missing value for --per-request-mib}"; shift 2;;
    *) echo "unknown option: $1" >&2; exit 1;;
  esac
done

is_num() { case "${1:-}" in ''|*[!0-9]*) return 1;; *) return 0;; esac; }
for v in "$PORT" "$PER_REQUEST_MIB" "$BASE_MIB" "$RESULTS_MIB"; do
  is_num "$v" || { echo "invalid number: $v" >&2; exit 1; }
done
[[ "$SERVICE_NAME" =~ ^[a-zA-Z0-9_-]+$ ]] || { echo "invalid service name" >&2; exit 1; }

if [ "$(uname -s)" != "Linux" ]; then
  echo '本脚本面向 Linux 生产环境，需要 /proc 与 systemd。' >&2
  exit 1
fi

fmt_kib() { awk -v k="$1" 'BEGIN{ if(k>=1048576) printf "%.2f GiB",k/1048576; else if(k>=1024) printf "%.1f MiB",k/1024; else printf "%d KiB",k }'; }
show_kib() { if is_num "$1"; then fmt_kib "$1"; else echo '未知'; fi; }

# ---- 系统资源探测 ----
cpu_cores=$(nproc 2>/dev/null || grep -c '^processor' /proc/cpuinfo 2>/dev/null || true)
cpu_model=$(awk -F: '/model name/{print $2; exit}' /proc/cpuinfo 2>/dev/null | sed 's/^ *//' || true)
loadavg=$(cut -d' ' -f1-3 /proc/loadavg 2>/dev/null || true)
mem_total_kib=$(awk '/^MemTotal:/{print $2}' /proc/meminfo 2>/dev/null || true)
mem_avail_kib=$(awk '/^MemAvailable:/{print $2}' /proc/meminfo 2>/dev/null || true)
is_num "$mem_avail_kib" || mem_avail_kib=$(awk '/^MemFree:/{f=$2}/^Buffers:/{b=$2}/^Cached:/{c=$2}END{print f+b+c}' /proc/meminfo 2>/dev/null || true)
swap_total_kib=$(awk '/^SwapTotal:/{print $2}' /proc/meminfo 2>/dev/null || true)
somaxconn=$(cat /proc/sys/net/core/somaxconn 2>/dev/null || true)
file_max=$(cat /proc/sys/fs/file-max 2>/dev/null || true)
port_lo=$(awk '{print $1}' /proc/sys/net/ipv4/ip_local_port_range 2>/dev/null || true)
port_hi=$(awk '{print $2}' /proc/sys/net/ipv4/ip_local_port_range 2>/dev/null || true)

disk_dir="$APP_DIR"
disk_total_kb=$(df -Pk "$disk_dir" 2>/dev/null | awk 'NR==2{print $2}' || true)
disk_avail_kb=$(df -Pk "$disk_dir" 2>/dev/null | awk 'NR==2{print $4}' || true)
is_num "$disk_total_kb" || { disk_dir=/; disk_total_kb=$(df -Pk / 2>/dev/null | awk 'NR==2{print $2}' || true); disk_avail_kb=$(df -Pk / 2>/dev/null | awk 'NR==2{print $4}' || true); }

# ---- systemd 有效限制与进程状态 ----
svc_mem_max_bytes=''; svc_nofile=''; svc_tasks=''; main_pid=''
if command -v systemctl >/dev/null 2>&1; then
  svc_mem_max_bytes=$(systemctl show -p MemoryMax --value "$SERVICE_NAME" 2>/dev/null || true)
  svc_nofile=$(systemctl show -p LimitNOFILE --value "$SERVICE_NAME" 2>/dev/null || true)
  svc_tasks=$(systemctl show -p TasksMax --value "$SERVICE_NAME" 2>/dev/null || true)
  main_pid=$(systemctl show -p MainPID --value "$SERVICE_NAME" 2>/dev/null || true)
fi
[ "$svc_mem_max_bytes" = '[not set]' ] && svc_mem_max_bytes=''
[ "$svc_nofile" = 'infinity' ] && svc_nofile=''
[ "$svc_tasks" = 'infinity' ] && svc_tasks=''

rss_kib=''; svc_threads=''; svc_fds=''
if is_num "$main_pid" && [ "$main_pid" != '0' ] && [ -r "/proc/$main_pid/status" ]; then
  rss_kib=$(awk '/^VmRSS:/{print $2}' "/proc/$main_pid/status" 2>/dev/null || true)
  svc_threads=$(awk '/^Threads:/{print $2}' "/proc/$main_pid/status" 2>/dev/null || true)
  svc_fds=$( (ls "/proc/$main_pid/fd" 2>/dev/null || true) | wc -l )
fi

health=''
if command -v curl >/dev/null 2>&1; then
  health=$(curl -fsS --max-time 2 "http://127.0.0.1:${PORT}/healthz" 2>/dev/null || true)
fi
cur_conns=''
if command -v ss >/dev/null 2>&1; then
  cur_conns=$(ss -Htn state established "( sport = :${PORT} )" 2>/dev/null | wc -l || true)
fi

nofile=''
if is_num "$svc_nofile"; then nofile=$svc_nofile; else nofile=$(ulimit -n 2>/dev/null || true); fi

# ---- Nginx 接入层 ----
nginx_workers=''; nginx_conn=''
if [ -r /etc/nginx/nginx.conf ]; then
  w=$(sed -n 's/^[[:space:]]*worker_processes[[:space:]]\+\([^;[:space:]]\+\);.*/\1/p' /etc/nginx/nginx.conf 2>/dev/null | head -n1 || true)
  if [ "$w" = 'auto' ]; then nginx_workers=${cpu_cores:-}; elif is_num "$w"; then nginx_workers=$w; fi
  c=$(grep -hs 'worker_connections' /etc/nginx/nginx.conf /etc/nginx/conf.d/*.conf /etc/nginx/sites-enabled/* 2>/dev/null | tail -n1 | grep -o '[0-9]\+' | head -n1 || true)
  is_num "$c" && nginx_conn=$c || true
fi

# ---- 并发估算 ----
# 1) 重请求（生成/上传）：应用层硬限制，见 internal/app/app.go 的 slots/uploads 通道。
GEN_SLOTS=2
UPLOAD_SLOTS=2
heavy_app_cap=$((GEN_SLOTS + UPLOAD_SLOTS))

# 2) 内存可支撑的重请求数：预算 = systemd MemoryMax（否则物理内存+15%系统预留），扣除基础与结果缓存。
mem_budget_kib=''; mem_reserve_kib=''; mem_src='未知'
if is_num "$svc_mem_max_bytes"; then
  mem_budget_kib=$((svc_mem_max_bytes / 1024)); mem_src="systemd MemoryMax"
elif is_num "$mem_total_kib"; then
  mem_budget_kib=$mem_total_kib; mem_src="物理内存(预留15%系统)"
  mem_reserve_kib=$((mem_total_kib * 15 / 100))
fi
if is_num "$mem_budget_kib"; then
  if is_num "$mem_total_kib" && [ "$mem_budget_kib" -gt "$mem_total_kib" ]; then mem_budget_kib=$mem_total_kib; fi
  mem_reserve_kib=$(( ${mem_reserve_kib:-0} + (BASE_MIB + RESULTS_MIB) * 1024 ))
  heavy_by_mem=$(( (mem_budget_kib - mem_reserve_kib) / (PER_REQUEST_MIB * 1024) ))
  [ "$heavy_by_mem" -lt 0 ] && heavy_by_mem=0 || true
fi

# 3) 轻量请求（页面/登录/状态轮询/兑换，IO为主）：取各维度最小值。
light_fd=''
if is_num "$nofile"; then
  light_fd=$(( nofile > 256 ? nofile - 256 : 0 ))  # 预留监听/SQLite/上游等fd
fi
light_nginx=''
if is_num "$nginx_workers" && is_num "$nginx_conn"; then
  light_nginx=$(( nginx_workers * nginx_conn / 2 ))  # 每个代理请求占用2个连接
fi
light_cpu=$(( ${cpu_cores:-1} * 50 ))  # 参考：IO型轻请求约1ms CPU、平均时延100ms、目标占用50%
light_max=''
for v in "$light_fd" "$light_nginx" "$light_cpu"; do
  is_num "$v" || continue
  if [ -z "$light_max" ] || [ "$v" -lt "$light_max" ]; then light_max=$v; fi
done
light_reco=''
if is_num "$light_max"; then light_reco=$(( light_max * SAFE_PERCENT / 100 )); fi

# ---- 报告 ----
echo "================ 服务器资源配置 ================"
printf 'CPU:        %s 核  %s\n' "${cpu_cores:-未知}" "${cpu_model:-}"
[ -n "$loadavg" ] && printf '负载:       %s\n' "$loadavg" || true
printf '内存:       总计 %s | 可用 %s | 交换 %s\n' "$(show_kib "$mem_total_kib")" "$(show_kib "$mem_avail_kib")" "$(show_kib "$swap_total_kib")"
printf '磁盘(%s): 共 %s | 可用 %s\n' "$disk_dir" \
  "$(if is_num "$disk_total_kb"; then fmt_kib $((disk_total_kb * 1024)); else echo 未知; fi)" \
  "$(if is_num "$disk_avail_kb"; then fmt_kib $((disk_avail_kb * 1024)); else echo 未知; fi)"
printf '内核限制:   fs.file-max=%s | somaxconn=%s | 临时端口 %s-%s\n' \
  "${file_max:-未知}" "${somaxconn:-未知}" "${port_lo:-?}" "${port_hi:-?}"

echo ""
echo "================ 服务运行状态 ($SERVICE_NAME) ================"
printf 'healthz(:%s): %s\n' "$PORT" "${health:-无响应（服务未运行或端口不一致）}"
printf '内存上限:   %s (%s)\n' "$(show_kib "$mem_budget_kib")" "$mem_src"
printf 'NOFILE:     %s | TasksMax: %s\n' "${svc_nofile:-${nofile:-未知}}" "${svc_tasks:-未知}"
if [ -n "$rss_kib$svc_threads" ]; then
  printf '进程状态:   RSS %s | 线程 %s | fd %s\n' "$(show_kib "$rss_kib")" "${svc_threads:-未知}" "${svc_fds:-需root}"
fi
[ -n "$cur_conns" ] && printf '当前ESTAB连接(:%s): %s\n' "$PORT" "$cur_conns" || true

echo ""
echo "================ Nginx 接入层 ================"
if is_num "$nginx_workers" && is_num "$nginx_conn"; then
  printf 'worker_processes %s × worker_connections %s → 代理并发上限 ≈ %s（每请求占2连接）\n' \
    "$nginx_workers" "$nginx_conn" "$((nginx_workers * nginx_conn / 2))"
else
  echo '未读取到 /etc/nginx 配置，跳过（不影响后端估算）。'
fi

echo ""
echo "================ 并发估算 ================"
echo "重请求（生成/上传，业务硬限制）:"
echo "  - 全局并行生成任务 $GEN_SLOTS 个 + 上传通道 $UPLOAD_SLOTS 个 = $heavy_app_cap 个并行（internal/app/app.go，与机器配置无关）"
echo "  - 每账号同时 1 个生成任务；注册/限流进一步约束真实负载"
if is_num "${heavy_by_mem:-}"; then
  echo "  - 内存校验: 预算 $(fmt_kib "$mem_budget_kib") − 预留 $(fmt_kib "$mem_reserve_kib")，按 ${PER_REQUEST_MIB}MiB/个 → 理论可支撑 $heavy_by_mem 个重请求"
  if [ "$heavy_by_mem" -ge "$heavy_app_cap" ]; then
    echo "  - 结论: 内存远未成为重请求瓶颈（瓶颈是业务硬限制）"
  else
    echo "  - 警告: 内存不足以支撑 $heavy_app_cap 个重请求，需调低 systemd MemoryMax 下的并发或增加内存"
  fi
else
  echo "  - 内存信息不足，跳过内存校验"
fi
echo ""
echo "轻量请求（首页/登录/状态轮询/下载，IO为主）:"
[ -n "$light_fd" ] && echo "  - 文件描述符: NOFILE ${nofile} − 256 预留 → $light_fd" || true
[ -n "$light_nginx" ] && echo "  - Nginx 代理: → $light_nginx" || true
echo "  - CPU 参考: ${cpu_cores:-?} 核 → $light_cpu"
if [ -n "$light_max" ]; then
  echo "  理论上限 = $light_max；建议运行值（${SAFE_PERCENT}%）= $light_reco"
else
  echo '  信息不足，无法估算'
fi
echo ""
echo "说明: 估算基于静态资源探测与代码内限制（15MiB请求体/5MiB图片/64MiB结果缓存/64MiB基础内存），"
echo "      非压测结论。如需提升生成并发，须修改 slots/uploads 容量并按上表重新核对内存。"

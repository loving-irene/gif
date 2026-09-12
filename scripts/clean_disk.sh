#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
usage: clean_disk.sh [--dry-run] [--aggressive] [--no-system] [application-directory]

回收根分区上可安全重建的构建缓存与系统缓存。默认清理：

  - Go 构建缓存（GOCACHE；未设置时用 `go env GOCACHE`，默认 $HOME/.cache/go-build）
  - 构建中断残留的 gif-server.next 与本机 *.exe 构建产物
  - logs/ 下超过 AVS_LOG_KEEP_DAYS 天（默认 2）的 auto_deploy-*.log
  - npm 下载缓存（$HOME/.npm/_cacache）
  - apt 包缓存与 systemd journal（需要 root 或免密 sudo）

--aggressive 额外清理：

  - Go 模块缓存（GOMODCACHE；下次构建重新下载）
  - snapd 包缓存（/var/lib/snapd/cache）
  - logrotate 轮转的历史系统日志（/var/log）
  - /var/backups/gif 中超过 AVS_BACKUP_KEEP_COUNT 份（默认 3）的自动快照

选项：

  --dry-run     只统计可回收空间，不删除任何文件
  --aggressive  一并清理模块缓存、snap 缓存、历史系统日志与超量备份
  --no-system   只处理应用目录内的文件，跳过 apt/journal/npm/snap/系统日志/备份

gif.db、gif.db-wal、gif.db-shm、gif-server、gif-server.previous、.env、
node_modules/、work/ 以及源码永远不会被删除。
EOF
}

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DRY_RUN=0
AGGRESSIVE=0
INCLUDE_SYSTEM=1
APP_DIR="${APP_DIR:-}"

while [ "$#" -gt 0 ]; do
  case "$1" in
    -h|--help)
      usage
      exit 0
      ;;
    --dry-run)
      DRY_RUN=1
      ;;
    --aggressive)
      AGGRESSIVE=1
      ;;
    --no-system)
      INCLUDE_SYSTEM=0
      ;;
    -*)
      echo "unknown option: $1" >&2
      usage
      exit 64
      ;;
    *)
      if [ -n "$APP_DIR" ]; then
        echo "unexpected argument: $1" >&2
        usage
        exit 64
      fi
      APP_DIR="$1"
      ;;
  esac
  shift
done

APP_DIR="${APP_DIR:-/var/www/gif}"
case "$APP_DIR" in
  /|""|.)
    echo "refusing to clean application directory: ${APP_DIR}" >&2
    exit 64
    ;;
esac
if [ ! -d "$APP_DIR" ]; then
  echo "application directory does not exist: ${APP_DIR}" >&2
  exit 66
fi
if [ ! -f "${APP_DIR}/go.mod" ] && [ ! -d "${APP_DIR}/.git" ]; then
  echo "refusing to clean ${APP_DIR}: it does not look like the GIF application" >&2
  exit 66
fi
APP_DIR="$(cd "$APP_DIR" && pwd)"

export LC_ALL=C

HOME_DIR="${HOME:-/root}"
LOG_KEEP_DAYS="${AVS_LOG_KEEP_DAYS:-2}"
BACKUP_KEEP_COUNT="${AVS_BACKUP_KEEP_COUNT:-3}"
JOURNAL_MAX_SIZE="${AVS_JOURNAL_MAX_SIZE:-50M}"
MIN_FREE_MB="${AVS_MIN_FREE_MB:-300}"
NPM_CACHE_DIR="${AVS_NPM_CACHE_DIR:-${HOME_DIR}/.npm}"
APT_CACHE_DIR="${AVS_APT_CACHE_DIR:-/var/cache/apt}"
JOURNAL_DIR="${AVS_JOURNAL_DIR:-/var/log/journal}"
SNAP_CACHE_DIR="${AVS_SNAP_CACHE_DIR:-/var/lib/snapd/cache}"
ROTATED_LOG_DIR="${AVS_ROTATED_LOG_DIR:-/var/log}"
BACKUP_DIR="${AVS_BACKUP_DIR:-/var/backups/gif}"

if ! [[ "$LOG_KEEP_DAYS" =~ ^[0-9]+$ ]]; then
  echo "invalid AVS_LOG_KEEP_DAYS: ${LOG_KEEP_DAYS}; use a non-negative integer" >&2
  exit 64
fi
if ! [[ "$BACKUP_KEEP_COUNT" =~ ^[1-9][0-9]*$ ]]; then
  echo "invalid AVS_BACKUP_KEEP_COUNT: ${BACKUP_KEEP_COUNT}; use a positive integer" >&2
  exit 64
fi

# Go 缓存优先取环境变量，其次问 go 自己，最后回退到约定路径；go 不存在时不能中断清理。
resolve_cache_dir() {
  local name="$1"
  local value="${2:-}"
  local fallback="$3"
  if [ -n "$value" ]; then
    printf '%s\n' "$value"
    return 0
  fi
  if command -v go >/dev/null 2>&1; then
    value="$(go env "$name" 2>/dev/null | tr -d '\r\n' || true)"
  fi
  if [ -n "$value" ]; then
    printf '%s\n' "$value"
  else
    printf '%s\n' "$fallback"
  fi
}

GOCACHE_DIR="$(resolve_cache_dir GOCACHE "${GOCACHE:-}" "${HOME_DIR}/.cache/go-build")"
GOMODCACHE_DIR="$(resolve_cache_dir GOMODCACHE "${GOMODCACHE:-}" "${HOME_DIR}/go/pkg/mod")"

RECLAIMED_KB=0
FAILED=0

# 系统缓存需要 root 或免密 sudo；拿不到权限时只清理应用目录并给出提示
PRIVILEGE_PREFIX=()
if [ "$(id -u)" -ne 0 ]; then
  if command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then
    PRIVILEGE_PREFIX=(sudo -n)
  fi
fi
SYSTEM_ALLOWED=1
if [ "$INCLUDE_SYSTEM" -eq 1 ] && [ "$(id -u)" -ne 0 ] && [ "${#PRIVILEGE_PREFIX[@]}" -eq 0 ]; then
  SYSTEM_ALLOWED=0
  echo "warning: no root privileges; apt, journal, snap and backup cleanup will be skipped" >&2
fi
if [ "$INCLUDE_SYSTEM" -eq 0 ]; then
  SYSTEM_ALLOWED=0
fi

human_kb() {
  local kb="${1:-0}"
  if command -v numfmt >/dev/null 2>&1; then
    numfmt --to=iec-i --suffix=B --from-unit=1024 "$kb"
  else
    printf '%s KiB' "$kb"
  fi
}

measure_kb() {
  local path="$1"
  local needs_root="${2:-0}"
  local kb=""
  if [ ! -e "$path" ]; then
    printf '0\n'
    return 0
  fi
  if [ "$needs_root" -eq 1 ]; then
    kb="$("${PRIVILEGE_PREFIX[@]}" du -sk "$path" 2>/dev/null | awk 'NR==1 {print $1}')" || true
  else
    kb="$(du -sk "$path" 2>/dev/null | awk 'NR==1 {print $1}')" || true
  fi
  printf '%s\n' "${kb:-0}"
}

# 哪怕缓存路径来自环境变量或 go env，也不允许删到应用目录、家目录、根目录本身，
# 更不允许删除相对路径（例如 GOCACHE=off 会被 go env 原样返回）。
is_protected_path() {
  local path="$1"
  case "$path" in
    ""|/|.|..)
      return 0
      ;;
    /*) ;;
    *)
      return 0
      ;;
  esac
  if [ "$path" = "$APP_DIR" ] || [ "$path" = "$HOME_DIR" ]; then
    return 0
  fi
  return 1
}

report() {
  local label="$1"
  local size_kb="$2"
  local detail="$3"
  if [ "$DRY_RUN" -eq 1 ]; then
    printf '[dry-run] %-24s %10s  %s\n' "$label" "$(human_kb "$size_kb")" "$detail"
  else
    printf '%-34s %10s  %s\n' "$label" "$(human_kb "$size_kb")" "$detail"
  fi
}

reclaim_path() {
  local label="$1"
  local path="$2"
  local needs_root="${3:-0}"
  if [ ! -e "$path" ]; then
    return 0
  fi
  if is_protected_path "$path"; then
    echo "warning: refusing to remove protected path: ${path}" >&2
    return 0
  fi
  local size_kb
  size_kb="$(measure_kb "$path" "$needs_root")"
  if [ "$DRY_RUN" -eq 0 ]; then
    if [ "$needs_root" -eq 1 ]; then
      if ! "${PRIVILEGE_PREFIX[@]}" rm -rf "$path"; then
        echo "failed to remove: ${path}" >&2
        FAILED=1
        return 0
      fi
    elif ! rm -rf "$path"; then
      echo "failed to remove: ${path}" >&2
      FAILED=1
      return 0
    fi
  fi
  report "$label" "$size_kb" "$path"
  RECLAIMED_KB=$((RECLAIMED_KB + size_kb))
}

reclaim_cache_dir() {
  local label="$1"
  local path="$2"
  local needs_root="${3:-0}"
  if [ ! -e "$path" ]; then
    return 0
  fi
  if is_protected_path "$path"; then
    echo "warning: refusing to remove protected path: ${path}" >&2
    return 0
  fi
  reclaim_path "$label" "$path" "$needs_root"
  if [ "$DRY_RUN" -eq 0 ]; then
    mkdir -p "$path"
  fi
}

reclaim_command() {
  local label="$1"
  local path="$2"
  shift 2
  if [ ! -e "$path" ]; then
    return 0
  fi
  local before_kb after_kb saved_kb
  before_kb="$(measure_kb "$path" 1)"
  if [ "$DRY_RUN" -eq 1 ]; then
    report "$label" "$before_kb" "$*"
    RECLAIMED_KB=$((RECLAIMED_KB + before_kb))
    return 0
  fi
  "${PRIVILEGE_PREFIX[@]}" "$@" >/dev/null 2>&1 || true
  after_kb="$(measure_kb "$path" 1)"
  saved_kb=$((before_kb - after_kb))
  if [ "$saved_kb" -lt 0 ]; then
    saved_kb=0
  fi
  report "$label" "$saved_kb" "$*"
  RECLAIMED_KB=$((RECLAIMED_KB + saved_kb))
}

# 备份保留由 scripts/backup_database.py 负责（始终只留最近 2 份 gif-auto-*.sqlite3）。
# 这里只清理历史遗留的、超出 AVS_BACKUP_KEEP_COUNT 的同名格式快照；默认 3 高于
# backup_database.py 的 2，因此常规运行不会删除任何备份。只删严格匹配自动快照
# 命名规则、且不是符号链接的普通文件，手动备份与其他文件永不触碰。
prune_auto_backups() {
  if [ ! -d "$BACKUP_DIR" ]; then
    return 0
  fi
  local -a doomed=()
  local mtime path base
  local index=0
  while IFS=$'\t' read -r mtime path; do
    [ -n "${path:-}" ] || continue
    base="$(basename "$path")"
    if [[ "$base" =~ ^gif-auto-[0-9]{8}T[0-9]{12}Z-[0-9a-f]{8}\.sqlite3$ ]] && [ -f "$path" ] && [ ! -L "$path" ]; then
      index=$((index + 1))
      if [ "$index" -gt "$BACKUP_KEEP_COUNT" ]; then
        doomed+=("$path")
      fi
    fi
  done < <(find "$BACKUP_DIR" -maxdepth 1 -type f -name 'gif-auto-*.sqlite3' -printf '%T@\t%p\n' 2>/dev/null | sort -rn)

  if [ "${#doomed[@]}" -eq 0 ]; then
    return 0
  fi

  local size_kb=0
  local saved_kb=0
  for path in "${doomed[@]}"; do
    size_kb="$(measure_kb "$path" 1)"
    saved_kb=$((saved_kb + size_kb))
  done

  if [ "$DRY_RUN" -eq 1 ]; then
    report "stale database backup" "$saved_kb" "keep the newest ${BACKUP_KEEP_COUNT} gif-auto-*.sqlite3 in ${BACKUP_DIR} (${#doomed[@]} removable)"
    RECLAIMED_KB=$((RECLAIMED_KB + saved_kb))
    return 0
  fi

  local removed=0
  for path in "${doomed[@]}"; do
    if "${PRIVILEGE_PREFIX[@]}" rm -f "$path"; then
      removed=$((removed + 1))
    else
      echo "failed to remove: ${path}" >&2
      FAILED=1
    fi
  done
  report "stale database backup" "$saved_kb" "removed ${removed}, kept the newest ${BACKUP_KEEP_COUNT} in ${BACKUP_DIR}"
  RECLAIMED_KB=$((RECLAIMED_KB + saved_kb))
}

printf 'application: %s\n' "$APP_DIR"
if [ "$DRY_RUN" -eq 1 ]; then
  printf 'mode:        dry-run (nothing will be removed)\n\n'
else
  printf 'mode:        clean\n\n'
fi

reclaim_cache_dir "go build cache" "$GOCACHE_DIR"
reclaim_path "stale build binary" "${APP_DIR}/gif-server.next"

while IFS= read -r -d '' exe_binary; do
  reclaim_path "local windows binary" "$exe_binary"
done < <(find "$APP_DIR" -maxdepth 1 -type f -name '*.exe' -print0 2>/dev/null)

if [ -d "${APP_DIR}/logs" ]; then
  while IFS= read -r -d '' old_log; do
    reclaim_path "old deploy log" "$old_log"
  done < <(find "${APP_DIR}/logs" -maxdepth 1 -type f -name 'auto_deploy-*.log' -mtime +"$LOG_KEEP_DAYS" -print0 2>/dev/null)
fi

if [ "$AGGRESSIVE" -eq 1 ]; then
  reclaim_cache_dir "go module cache" "$GOMODCACHE_DIR"
fi

if [ "$INCLUDE_SYSTEM" -eq 1 ]; then
  reclaim_path "npm download cache" "${NPM_CACHE_DIR}/_cacache"
fi

if [ "$SYSTEM_ALLOWED" -eq 1 ]; then
  if command -v apt-get >/dev/null 2>&1; then
    reclaim_command "apt package cache" "$APT_CACHE_DIR" apt-get clean
  fi
  if command -v journalctl >/dev/null 2>&1; then
    reclaim_command "systemd journal" "$JOURNAL_DIR" journalctl --vacuum-size="$JOURNAL_MAX_SIZE"
  fi
  if [ "$AGGRESSIVE" -eq 1 ]; then
    reclaim_path "snap package cache" "$SNAP_CACHE_DIR" 1
    while IFS= read -r -d '' rotated_log; do
      reclaim_path "rotated system log" "$rotated_log" 1
    done < <(find "$ROTATED_LOG_DIR" -maxdepth 1 -type f \
      \( -name '*.gz' -o -name '*.1' -o -name '*.2' -o -name '*.[0-9].gz' \) \
      -mtime +"$LOG_KEEP_DAYS" -print0 2>/dev/null)
    prune_auto_backups
  fi
fi

available_mb="$(df -Pm "$APP_DIR" | awk 'NR==2 {print $4}')"

printf '\n===== summary =====\n'
if [ "$DRY_RUN" -eq 1 ]; then
  printf 'reclaimable: %s\n' "$(human_kb "$RECLAIMED_KB")"
else
  printf 'reclaimed:   %s\n' "$(human_kb "$RECLAIMED_KB")"
fi
df -h "$APP_DIR"

if [[ "$available_mb" =~ ^[0-9]+$ ]] && [ "$available_mb" -lt "$MIN_FREE_MB" ]; then
  printf '\nwarning: only %s MiB available; a deploy needs at least %s MiB (AVS_MIN_FREE_MB).\n' \
    "$available_mb" "$MIN_FREE_MB"
  printf 'try --aggressive, clean /var/lib/snapd, or grow the root volume.\n'
fi

if [ "$FAILED" -ne 0 ]; then
  exit 1
fi

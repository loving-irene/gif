#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
TEST_DIR="$(mktemp -d)"
trap 'rm -rf "$TEST_DIR"' EXIT

APP_DIR="${TEST_DIR}/app"
FAKE_BIN="${TEST_DIR}/bin"
HOME_DIR="${TEST_DIR}/home"
COMMAND_LOG="${TEST_DIR}/system-commands.log"
APT_DIR="${TEST_DIR}/apt"
JOURNAL_DIR="${TEST_DIR}/journal"
NPM_DIR="${TEST_DIR}/npm"
SNAP_DIR="${TEST_DIR}/snap"
ROTATED_LOG_DIR="${TEST_DIR}/syslog"
BACKUP_DIR="${TEST_DIR}/backups"
GOCACHE_DIR="${TEST_DIR}/gocache"
GOMODCACHE_DIR="${TEST_DIR}/gomodcache"
GO_ENV_GOCACHE="${TEST_DIR}/go-env-gocache"
GO_ENV_GOMODCACHE="${TEST_DIR}/go-env-gomodcache"

mkdir -p "$APP_DIR/logs" "$APP_DIR/files" "$APP_DIR/node_modules/pkg" "$APP_DIR/work" \
  "$FAKE_BIN" "$HOME_DIR" "${NPM_DIR}/_cacache" "${APT_DIR}/archives" "$JOURNAL_DIR" \
  "$SNAP_DIR" "$ROTATED_LOG_DIR" "$BACKUP_DIR" "$GOCACHE_DIR" "$GOMODCACHE_DIR" \
  "$GO_ENV_GOCACHE" "$GO_ENV_GOMODCACHE"
: >"$COMMAND_LOG"

# 假应用目录：既像真的 gif 应用，又包含所有必须被保留的文件
printf '%s\n' 'module gif' >"${APP_DIR}/go.mod"
printf '%s\n' 'package main' >"${APP_DIR}/main.go"
printf '%s\n' 'secret' >"${APP_DIR}/.env"
printf '%s\n' 'binary' >"${APP_DIR}/gif-server"
printf '%s\n' 'previous' >"${APP_DIR}/gif-server.previous"
chmod 755 "${APP_DIR}/gif-server" "${APP_DIR}/gif-server.previous"
printf '%s\n' 'partial' >"${APP_DIR}/gif-server.next"
printf '%s\n' 'windows build' >"${APP_DIR}/gif-server.exe"
printf '%s\n' 'database' >"${APP_DIR}/gif.db"
printf '%s\n' 'wal' >"${APP_DIR}/gif.db-wal"
printf '%s\n' 'shm' >"${APP_DIR}/gif.db-shm"
printf '%s\n' 'result' >"${APP_DIR}/files/result.bin"
printf '%s\n' 'scratch' >"${APP_DIR}/work/scratch.txt"
printf '%s\n' 'module file' >"${APP_DIR}/node_modules/pkg/index.js"
printf '%s\n' 'nested exe' >"${APP_DIR}/node_modules/pkg/tool.exe"

head -c 4096 /dev/zero >"${GOCACHE_DIR}/cache.bin"
head -c 8192 /dev/zero >"${GOMODCACHE_DIR}/mod.bin"
head -c 4096 /dev/zero >"${GO_ENV_GOCACHE}/cache.bin"
head -c 4096 /dev/zero >"${GO_ENV_GOMODCACHE}/mod.bin"
head -c 4096 /dev/zero >"${NPM_DIR}/_cacache/blob.bin"
head -c 4096 /dev/zero >"${APT_DIR}/archives/fake.deb"
head -c 4096 /dev/zero >"${JOURNAL_DIR}/system.journal"
head -c 4096 /dev/zero >"${SNAP_DIR}/some.snap"

# 系统日志：旧的轮转文件可清理，新的和未轮转的必须保留
head -c 4096 /dev/zero >"${ROTATED_LOG_DIR}/syslog.1"
touch -d '2020-01-01 00:00:00' "${ROTATED_LOG_DIR}/syslog.1"
head -c 4096 /dev/zero >"${ROTATED_LOG_DIR}/kern.log.1"
head -c 4096 /dev/zero >"${ROTATED_LOG_DIR}/daemon.log"

# 自动部署日志：过期删除，今天与昨天的保留，非 auto_deploy-*.log 不动
printf '%s\n' 'expired' >"${APP_DIR}/logs/auto_deploy-2020-01-01.log"
touch -d '2020-01-01 00:00:00' "${APP_DIR}/logs/auto_deploy-2020-01-01.log"
printf '%s\n' 'yesterday' >"${APP_DIR}/logs/auto_deploy-$(date -d 'yesterday' +%F).log"
printf '%s\n' 'current' >"${APP_DIR}/logs/auto_deploy-$(date +%F).log"
printf '%s\n' 'other' >"${APP_DIR}/logs/other.log"
touch -d '2020-01-01 00:00:00' "${APP_DIR}/logs/other.log"

# 备份目录：5 份合法自动快照 + 1 份手动备份（手动备份永不清理）
for index in 1 2 3 4 5; do
  snapshot="gif-auto-2020010${index}T000000000000Z-aaaa000${index}.sqlite3"
  printf '%s\n' 'snapshot' >"${BACKUP_DIR}/${snapshot}"
  touch -d "2020-01-0${index} 00:00:00" "${BACKUP_DIR}/${snapshot}"
done
printf '%s\n' 'manual' >"${BACKUP_DIR}/manual-backup.sqlite3"

# 系统命令打桩：即便脚本误调用也不会影响真机，同时可以断言调用情况
cat >"${FAKE_BIN}/sudo" <<'EOF'
#!/usr/bin/env bash
while [ "${1:-}" = "-n" ]; do shift; done
exec "$@"
EOF

cat >"${FAKE_BIN}/apt-get" <<'EOF'
#!/usr/bin/env bash
printf 'apt-get %s\n' "$*" >>"$FAKE_COMMAND_LOG"
EOF

cat >"${FAKE_BIN}/journalctl" <<'EOF'
#!/usr/bin/env bash
printf 'journalctl %s\n' "$*" >>"$FAKE_COMMAND_LOG"
EOF

cat >"${FAKE_BIN}/go" <<'EOF'
#!/usr/bin/env bash
if [ "${1:-}" = "env" ]; then
  case "${2:-}" in
    GOCACHE) printf '%s\n' "${FAKE_GO_GOCACHE:-}" ;;
    GOMODCACHE) printf '%s\n' "${FAKE_GO_GOMODCACHE:-}" ;;
  esac
fi
exit 0
EOF
chmod 755 "${FAKE_BIN}/"*

run_clean_disk() {
  local directory="$1"
  local gocache="$2"
  local gomodcache="$3"
  shift 3
  PATH="${FAKE_BIN}:${PATH}" \
    HOME="$HOME_DIR" \
    GOCACHE="$gocache" \
    GOMODCACHE="$gomodcache" \
    FAKE_COMMAND_LOG="$COMMAND_LOG" \
    FAKE_GO_GOCACHE="$GO_ENV_GOCACHE" \
    FAKE_GO_GOMODCACHE="$GO_ENV_GOMODCACHE" \
    AVS_NPM_CACHE_DIR="$NPM_DIR" \
    AVS_APT_CACHE_DIR="$APT_DIR" \
    AVS_JOURNAL_DIR="$JOURNAL_DIR" \
    AVS_SNAP_CACHE_DIR="$SNAP_DIR" \
    AVS_ROTATED_LOG_DIR="$ROTATED_LOG_DIR" \
    AVS_BACKUP_DIR="$BACKUP_DIR" \
    AVS_BACKUP_KEEP_COUNT="${AVS_BACKUP_KEEP_COUNT:-3}" \
    AVS_LOG_KEEP_DAYS="${AVS_LOG_KEEP_DAYS:-2}" \
    /usr/bin/bash "$ROOT_DIR/scripts/clean_disk.sh" "$@" "$directory"
}

clean_disk() {
  run_clean_disk "$APP_DIR" "$GOCACHE_DIR" "$GOMODCACHE_DIR" "$@"
}

clean_disk_via_go_env() {
  run_clean_disk "$APP_DIR" "" "" "$@"
}

expect_code() {
  local expected="$1"
  shift
  local code=0
  "$@" >/dev/null 2>&1 || code=$?
  if [ "$code" -ne "$expected" ]; then
    echo "expected exit ${expected} but got ${code}: $*" >&2
    exit 1
  fi
}

count_backups() {
  find "$BACKUP_DIR" -maxdepth 1 -type f -name 'gif-auto-*.sqlite3' | wc -l
}

assert_untouched() {
  [ -f "${APP_DIR}/gif-server" ]
  [ -f "${APP_DIR}/gif-server.previous" ]
  [ -f "${APP_DIR}/gif.db" ]
  [ -f "${APP_DIR}/gif.db-wal" ]
  [ -f "${APP_DIR}/gif.db-shm" ]
  [ -f "${APP_DIR}/.env" ]
  [ -f "${APP_DIR}/go.mod" ]
  [ -f "${APP_DIR}/main.go" ]
  [ -f "${APP_DIR}/files/result.bin" ]
  [ -f "${APP_DIR}/work/scratch.txt" ]
  [ -f "${APP_DIR}/node_modules/pkg/index.js" ]
  [ -f "${APP_DIR}/node_modules/pkg/tool.exe" ]
  [ -f "${BACKUP_DIR}/manual-backup.sqlite3" ]
}

# 1) --dry-run 只统计，不删除任何文件，也不调用系统清理命令
dry_run_output="$(clean_disk --dry-run --aggressive)"
assert_untouched
[ -f "${APP_DIR}/gif-server.next" ]
[ -f "${APP_DIR}/gif-server.exe" ]
[ -s "${GOCACHE_DIR}/cache.bin" ]
[ -s "${GOMODCACHE_DIR}/mod.bin" ]
[ -s "${NPM_DIR}/_cacache/blob.bin" ]
[ -s "${SNAP_DIR}/some.snap" ]
[ -f "${ROTATED_LOG_DIR}/syslog.1" ]
[ -f "${APP_DIR}/logs/auto_deploy-2020-01-01.log" ]
[ "$(count_backups)" -eq 5 ]
grep -qF '[dry-run]' <<<"$dry_run_output"
grep -qF 'reclaimable' <<<"$dry_run_output"
grep -qF 'stale database backup' <<<"$dry_run_output"
if [ -s "$COMMAND_LOG" ]; then
  echo "dry-run executed system cleanup commands" >&2
  cat "$COMMAND_LOG" >&2
  exit 1
fi

# 2) 默认档（--no-system）：清理构建缓存、残留产物与过期日志，保留二进制与数据库
clean_disk --no-system >/dev/null
assert_untouched
[ ! -e "${APP_DIR}/gif-server.next" ]
[ ! -e "${APP_DIR}/gif-server.exe" ]
[ -d "${GOCACHE_DIR}" ]
[ -z "$(ls -A "${GOCACHE_DIR}")" ]
[ -s "${GOMODCACHE_DIR}/mod.bin" ]
[ ! -e "${APP_DIR}/logs/auto_deploy-2020-01-01.log" ]
[ -f "${APP_DIR}/logs/auto_deploy-$(date +%F).log" ]
[ -f "${APP_DIR}/logs/auto_deploy-$(date -d 'yesterday' +%F).log" ]
[ -f "${APP_DIR}/logs/other.log" ]
[ -s "${NPM_DIR}/_cacache/blob.bin" ]
[ -s "${SNAP_DIR}/some.snap" ]
[ -f "${ROTATED_LOG_DIR}/syslog.1" ]
[ "$(count_backups)" -eq 5 ]
if [ -s "$COMMAND_LOG" ]; then
  echo "--no-system executed system cleanup commands" >&2
  exit 1
fi

# 3) --aggressive --no-system 追加模块缓存，仍然不碰系统目录
head -c 4096 /dev/zero >"${GOCACHE_DIR}/cache.bin"
clean_disk --aggressive --no-system >/dev/null
assert_untouched
[ -d "${GOMODCACHE_DIR}" ]
[ -z "$(ls -A "${GOMODCACHE_DIR}")" ]
[ -s "${SNAP_DIR}/some.snap" ]
[ -f "${ROTATED_LOG_DIR}/syslog.1" ]
[ "$(count_backups)" -eq 5 ]
if [ -s "$COMMAND_LOG" ]; then
  echo "--aggressive --no-system executed system cleanup commands" >&2
  exit 1
fi

# 4) 默认档开启系统清理：调用 apt/journal、清理 npm 缓存，但不动 snap、系统日志与备份
clean_disk >/dev/null
assert_untouched
grep -qF 'apt-get clean' "$COMMAND_LOG"
grep -qF 'journalctl --vacuum-size=50M' "$COMMAND_LOG"
[ ! -e "${NPM_DIR}/_cacache" ]
[ -s "${SNAP_DIR}/some.snap" ]
[ -f "${ROTATED_LOG_DIR}/syslog.1" ]
[ "$(count_backups)" -eq 5 ]

# 5) --aggressive 开启系统清理：snap 缓存、过期系统日志与超量备份一并清理
clean_disk --aggressive >/dev/null
assert_untouched
[ ! -e "${SNAP_DIR}/some.snap" ]
[ ! -e "${ROTATED_LOG_DIR}/syslog.1" ]
[ -f "${ROTATED_LOG_DIR}/kern.log.1" ]
[ -f "${ROTATED_LOG_DIR}/daemon.log" ]
[ "$(count_backups)" -eq 3 ]
[ ! -e "${BACKUP_DIR}/gif-auto-20200101T000000000000Z-aaaa0001.sqlite3" ]
[ ! -e "${BACKUP_DIR}/gif-auto-20200102T000000000000Z-aaaa0002.sqlite3" ]
[ -f "${BACKUP_DIR}/gif-auto-20200103T000000000000Z-aaaa0003.sqlite3" ]
[ -f "${BACKUP_DIR}/gif-auto-20200105T000000000000Z-aaaa0005.sqlite3" ]
[ -f "${BACKUP_DIR}/manual-backup.sqlite3" ]

# 6) 未设置 GOCACHE/GOMODCACHE 时改用 `go env` 报告的目录（go 打桩，不碰真机缓存）
head -c 4096 /dev/zero >"${GOCACHE_DIR}/fresh.bin"
head -c 4096 /dev/zero >"${GO_ENV_GOCACHE}/fresh.bin"
clean_disk_via_go_env --no-system >/dev/null
[ -s "${GOCACHE_DIR}/fresh.bin" ]
[ -d "${GO_ENV_GOCACHE}" ]
[ -z "$(ls -A "${GO_ENV_GOCACHE}")" ]
[ -s "${GO_ENV_GOMODCACHE}/mod.bin" ]

# 7) 非法参数、危险目录与非应用目录必须被拒绝
expect_code 64 clean_disk --bogus
expect_code 64 run_clean_disk "/" "$GOCACHE_DIR" "$GOMODCACHE_DIR" --no-system
expect_code 64 run_clean_disk "." "$GOCACHE_DIR" "$GOMODCACHE_DIR" --no-system
expect_code 64 run_clean_disk "$APP_DIR" "$GOCACHE_DIR" "$GOMODCACHE_DIR" "$APP_DIR"
expect_code 66 run_clean_disk "${TEST_DIR}/missing" "$GOCACHE_DIR" "$GOMODCACHE_DIR" --no-system
mkdir -p "${TEST_DIR}/not-app"
expect_code 66 run_clean_disk "${TEST_DIR}/not-app" "$GOCACHE_DIR" "$GOMODCACHE_DIR" --no-system
expect_code 64 env AVS_LOG_KEEP_DAYS=forever /usr/bin/bash "$ROOT_DIR/scripts/clean_disk.sh" --no-system "$APP_DIR"
expect_code 64 env AVS_BACKUP_KEEP_COUNT=0 /usr/bin/bash "$ROOT_DIR/scripts/clean_disk.sh" --no-system "$APP_DIR"
expect_code 0 /usr/bin/bash "$ROOT_DIR/scripts/clean_disk.sh" --help

echo "gif clean disk test ok"

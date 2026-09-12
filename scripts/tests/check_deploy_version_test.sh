#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
TEST_DIR="$(mktemp -d)"
trap 'rm -rf "$TEST_DIR"' EXIT

APP_DIR="${TEST_DIR}/app"
mkdir -p "$APP_DIR"
git init -q -b main "$APP_DIR"
git -C "$APP_DIR" config user.name "GIF Test"
git -C "$APP_DIR" config user.email "gif-test@example.com"
git -C "$APP_DIR" config core.autocrlf false
printf '%s\n' 'module gif' >"${APP_DIR}/go.mod"
git -C "$APP_DIR" add go.mod
git -C "$APP_DIR" commit -qm "first release"
first_commit="$(git -C "$APP_DIR" rev-parse HEAD)"

STATE_FILE="${APP_DIR}/.last_deployed_commit"
SCRIPT="${ROOT_DIR}/scripts/check_deploy_version.sh"

commit_file() {
  printf '%s\n' "$1" >"${APP_DIR}/$1"
  git -C "$APP_DIR" add "$1"
  git -C "$APP_DIR" commit -qm "$1"
  git -C "$APP_DIR" rev-parse HEAD
}

write_state() {
  printf '%s\n' "$1" >"$STATE_FILE"
}

run_check() {
  /usr/bin/bash "$SCRIPT" "$@" "$APP_DIR"
}

expect_quiet() {
  local expected_code="$1"
  local expected_status="$2"
  shift 2
  local code=0
  local status=""
  status="$(run_check --quiet "$@")" || code=$?
  if [ "$code" -ne "$expected_code" ] || [ "$status" != "$expected_status" ]; then
    echo "expected ${expected_status}(${expected_code}) but got ${status:-<empty>}(${code})" >&2
    exit 1
  fi
}

# 1) 状态文件与 origin/main 一致 -> 0 up-to-date
printf '#!/usr/bin/env bash\n' >"${APP_DIR}/gif-server"
chmod 755 "${APP_DIR}/gif-server"
write_state "$first_commit"
git -C "$APP_DIR" update-ref refs/remotes/origin/main "$first_commit"
expect_quiet 0 up-to-date

up_to_date_output="$(run_check)"
grep -qE '^application: +/' <<<"$up_to_date_output"
grep -qE '^branch: +main$' <<<"$up_to_date_output"
grep -qF "state file:" <<<"$up_to_date_output"
grep -qF '.last_deployed_commit' <<<"$up_to_date_output"
grep -qF 'first release' <<<"$up_to_date_output"
grep -qF 'gif-server=present, gif-server.previous=missing' <<<"$up_to_date_output"
grep -qE '^local HEAD: +' <<<"$up_to_date_output"
grep -qE '^origin/main: +' <<<"$up_to_date_output"
grep -qE '^status: +up-to-date$' <<<"$up_to_date_output"

# 2) HEAD 已等于 origin/main，但状态文件仍是旧提交 -> 4 stuck
second_commit="$(commit_file second.txt)"
git -C "$APP_DIR" update-ref refs/remotes/origin/main "$second_commit"
expect_quiet 4 stuck

stuck_output="$(run_check || true)"
grep -qF 'stuck' <<<"$stuck_output"
grep -qF '提示' <<<"$stuck_output"
grep -qF 'clean_disk.sh' <<<"$stuck_output"
grep -qF 'auto_deploy.sh' <<<"$stuck_output"

# 3) 重新部署成功后恢复 up-to-date
write_state "$second_commit"
expect_quiet 0 up-to-date

# 4) origin/main 领先而 HEAD 落后 -> 3 newer-commit-available
third_commit="$(commit_file third.txt)"
git -C "$APP_DIR" update-ref refs/remotes/origin/main "$third_commit"
git -C "$APP_DIR" reset -q --hard "$second_commit"
write_state "$second_commit"
expect_quiet 3 newer-commit-available

newer_output="$(run_check || true)"
grep -qF 'newer-commit-available' <<<"$newer_output"
grep -qF '提示' <<<"$newer_output"
grep -qF '5 分钟' <<<"$newer_output"
grep -qF 'auto_deploy.sh' <<<"$newer_output"

# 5) 状态文件缺失 -> 1 unknown
rm -f "$STATE_FILE"
expect_quiet 1 unknown
missing_output="$(run_check || true)"
grep -qF 'last_deployed_commit' <<<"$missing_output"
grep -qF '提示' <<<"$missing_output"

# 6) 状态文件内容无法解析为本仓库提交 -> 1 unknown
write_state "not-a-commit"
expect_quiet 1 unknown
grep -qF 'not-a-commit' <<<"$(run_check)"

# 7) 缺少 origin 跟踪引用 -> 1 unknown（不能误判为落后）
write_state "$second_commit"
git -C "$APP_DIR" update-ref -d refs/remotes/origin/main
expect_quiet 1 unknown
grep -qF -- '--fetch' <<<"$(run_check)"

# 8) 不是 git 仓库 -> 1 unknown
not_repo="${TEST_DIR}/not-repo"
mkdir -p "$not_repo"
code=0
status="$(/usr/bin/bash "$SCRIPT" --quiet "$not_repo")" || code=$?
[ "$code" -eq 1 ]
[ "$status" = "unknown" ]

# 9) --branch 与 BRANCH 环境变量
git -C "$APP_DIR" update-ref refs/remotes/origin/main "$third_commit"
git -C "$APP_DIR" update-ref refs/remotes/origin/dev "$second_commit"
write_state "$second_commit"
expect_quiet 0 up-to-date --branch dev
branch_output="$(run_check --branch dev)"
grep -qE '^branch: +dev$' <<<"$branch_output"
grep -qE '^origin/dev: +' <<<"$branch_output"
code=0
status="$(BRANCH=dev /usr/bin/bash "$SCRIPT" --quiet "$APP_DIR")" || code=$?
[ "$code" -eq 0 ]
[ "$status" = "up-to-date" ]

# 10) DEPLOY_STATE_FILE 指向其他状态文件
custom_state="${TEST_DIR}/custom-state"
printf '%s\n' "$third_commit" >"$custom_state"
code=0
status="$(DEPLOY_STATE_FILE="$custom_state" /usr/bin/bash "$SCRIPT" --quiet "$APP_DIR")" || code=$?
[ "$code" -eq 0 ]
[ "$status" = "up-to-date" ]
grep -qF "$custom_state" <<<"$(DEPLOY_STATE_FILE="$custom_state" /usr/bin/bash "$SCRIPT" "$APP_DIR")"

# 11) 运行二进制状态：gif-server.previous 存在时也要显示
printf '#!/usr/bin/env bash\n' >"${APP_DIR}/gif-server.previous"
chmod 755 "${APP_DIR}/gif-server.previous"
grep -qF 'gif-server=present, gif-server.previous=present' <<<"$(run_check)"
rm -f "${APP_DIR}/gif-server"

# 12) --fetch 失败时明确报失败，不误判为最新
git -C "$APP_DIR" remote add origin "${TEST_DIR}/no-such-remote.git"
expect_quiet 1 unknown --fetch 2>/dev/null

# 13) 非法参数与用法错误
if run_check --bogus >/dev/null 2>&1; then
  echo "deploy version check accepted an unknown option" >&2
  exit 1
fi
code=0
/usr/bin/bash "$SCRIPT" --branch >/dev/null 2>&1 || code=$?
[ "$code" -eq 64 ]
code=0
/usr/bin/bash "$SCRIPT" --help >/dev/null 2>&1 || code=$?
[ "$code" -eq 0 ]

echo "gif deploy version check test ok"

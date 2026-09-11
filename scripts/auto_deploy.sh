#!/usr/bin/env bash
set -euo pipefail
BRANCH="${BRANCH:-main}"
DEPLOY_STATE_FILE="${DEPLOY_STATE_FILE:-.last_deployed_commit}"
LOG_DIR="${LOG_DIR:-logs}"
cd "$(dirname "$0")/.."
mkdir -p "$LOG_DIR"
# 日志按天写入logs/auto_deploy-日期.log，只保留最近2天（今天与昨天）。
exec >>"${LOG_DIR}/auto_deploy-$(TZ=Asia/Shanghai date '+%Y-%m-%d').log" 2>&1
find "$LOG_DIR" -maxdepth 1 -name 'auto_deploy-*.log' -mtime +1 -delete
exec 8> .auto-deploy.lock
flock -n 8 || exit 0
log(){ printf '[%s] %s\n' "$(TZ=Asia/Shanghai date '+%Y-%m-%d %H:%M:%S %Z')" "$*"; }
log "auto deploy started: branch=${BRANCH}"
git fetch origin "$BRANCH"
LOCAL="$(git rev-parse HEAD)"
REMOTE="$(git rev-parse "origin/${BRANCH}")"
DEPLOYED=""
test ! -f "$DEPLOY_STATE_FILE" || DEPLOYED="$(cat "$DEPLOY_STATE_FILE")"
if test "$LOCAL" != "$REMOTE"; then
  git pull --ff-only origin "$BRANCH"
  LOCAL="$(git rev-parse HEAD)"
fi
if test "$LOCAL" != "$DEPLOYED"; then
  log "deploy started: commit=${LOCAL}"
  PORT="${PORT:-8096}" ./scripts/deploy.sh
  printf '%s\n' "$LOCAL" > "$DEPLOY_STATE_FILE"
  log "deploy finished: commit=${LOCAL}"
else
  log 'no deploy needed'
fi

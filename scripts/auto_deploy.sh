#!/usr/bin/env bash
set -euo pipefail
BRANCH="${BRANCH:-main}"
DEPLOY_STATE_FILE="${DEPLOY_STATE_FILE:-.last_deployed_commit}"
cd "$(dirname "$0")/.."
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

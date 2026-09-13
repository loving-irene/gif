#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
usage: check_deploy_version.sh [--branch <name>] [--fetch] [--quiet] [application-directory]

判断线上是否已经运行跟踪分支的最新提交。gif-server 不支持 version 子命令，
"已部署提交"只能取自 scripts/auto_deploy.sh 部署成功后写入的状态文件
（默认 <application-directory>/.last_deployed_commit，可用 DEPLOY_STATE_FILE 覆盖，
相对路径按应用目录解析），再与 origin/<branch> 比较。

选项：

  --branch <name>  跟踪的分支，默认 main（也可用环境变量 BRANCH）
  --fetch          先执行 git fetch origin <branch>，确保比较的是远端最新提交
  --quiet          只输出状态关键字，便于 cron 或监控脚本判断

状态与退出码：

  0   up-to-date                 状态文件记录的提交 == origin/<branch>
  3   newer-commit-available     origin/<branch> 有尚未上线的提交
  4   stuck                      HEAD 已等于 origin/<branch>，但状态文件仍是旧提交
  1   unknown                    状态文件缺失或无法解析、无法解析 origin 引用、不是 git 仓库
  64  用法错误

非 up-to-date 时（交互模式）会询问是否回滚一个提交并重新部署，
确认后才执行 git reset --hard HEAD~1 与 scripts/auto_deploy.sh；
--quiet 或空输入/非 y 视为取消，不执行回滚。
EOF
}

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BRANCH="${BRANCH:-main}"
DO_FETCH=0
QUIET=0
APP_DIR="${APP_DIR:-}"

while [ "$#" -gt 0 ]; do
  case "$1" in
    -h|--help)
      usage
      exit 0
      ;;
    --branch)
      if [ "$#" -lt 2 ]; then
        echo "--branch requires a branch name" >&2
        usage
        exit 64
      fi
      BRANCH="$2"
      shift
      ;;
    --fetch)
      DO_FETCH=1
      ;;
    --quiet)
      QUIET=1
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

if [ -z "$BRANCH" ]; then
  echo "branch name must not be empty" >&2
  exit 64
fi

APP_DIR="${APP_DIR:-/var/www/gif}"

say() {
  if [ "$QUIET" -eq 0 ]; then
    printf '%s\n' "$*"
  fi
}

say_field() {
  local label="$1"
  shift
  say "$(printf '%-17s %s' "$label" "$*")"
}

IS_REPO=0
if [ -d "$APP_DIR" ] && git -C "$APP_DIR" rev-parse --git-dir >/dev/null 2>&1; then
  IS_REPO=1
  APP_DIR="$(cd "$APP_DIR" && pwd)"
fi

DEPLOY_STATE_FILE="${DEPLOY_STATE_FILE:-.last_deployed_commit}"
case "$DEPLOY_STATE_FILE" in
  /*) STATE_FILE="$DEPLOY_STATE_FILE" ;;
  *) STATE_FILE="${APP_DIR}/${DEPLOY_STATE_FILE}" ;;
esac

HEAD_COMMIT=""
REMOTE_COMMIT=""
DEPLOYED_COMMIT=""
DEPLOYED_RAW=""
STATUS=""
EXIT_CODE=0
REASON=""

if [ "$IS_REPO" -eq 0 ]; then
  STATUS="unknown"
  EXIT_CODE=1
  REASON="not-a-git-repository"
fi

if [ -z "$STATUS" ] && [ "$DO_FETCH" -eq 1 ]; then
  if ! git -C "$APP_DIR" fetch --quiet origin "$BRANCH"; then
    echo "git fetch failed for origin/${BRANCH}" >&2
    STATUS="unknown"
    EXIT_CODE=1
    REASON="fetch-failed"
  fi
fi

if [ -z "$STATUS" ]; then
  HEAD_COMMIT="$(git -C "$APP_DIR" rev-parse HEAD 2>/dev/null || true)"
  REMOTE_COMMIT="$(git -C "$APP_DIR" rev-parse --verify --quiet "refs/remotes/origin/${BRANCH}" 2>/dev/null || true)"
  if [ -z "$REMOTE_COMMIT" ]; then
    STATUS="unknown"
    EXIT_CODE=1
    REASON="no-origin-ref"
  fi
fi

if [ -z "$STATUS" ]; then
  if [ ! -f "$STATE_FILE" ]; then
    STATUS="unknown"
    EXIT_CODE=1
    REASON="missing-state-file"
  else
    DEPLOYED_RAW="$(tr -d ' \t\r\n' <"$STATE_FILE" || true)"
    if [ -n "$DEPLOYED_RAW" ]; then
      DEPLOYED_COMMIT="$(git -C "$APP_DIR" rev-parse --verify --quiet "${DEPLOYED_RAW}^{commit}" 2>/dev/null || true)"
    fi
    if [ -z "$DEPLOYED_COMMIT" ]; then
      STATUS="unknown"
      EXIT_CODE=1
      REASON="unresolvable-state-commit"
    fi
  fi
fi

if [ -z "$STATUS" ]; then
  if [ "$DEPLOYED_COMMIT" = "$REMOTE_COMMIT" ]; then
    STATUS="up-to-date"
    EXIT_CODE=0
  elif [ "$HEAD_COMMIT" = "$REMOTE_COMMIT" ]; then
    STATUS="stuck"
    EXIT_CODE=4
  else
    STATUS="newer-commit-available"
    EXIT_CODE=3
  fi
fi

summarize() {
  git -C "$APP_DIR" log -1 --format='%h %s' "$1" 2>/dev/null || true
}

DEPLOYED_DISPLAY="unknown"
if [ -n "$DEPLOYED_COMMIT" ]; then
  DEPLOYED_DISPLAY="$(summarize "$DEPLOYED_COMMIT")"
  [ -n "$DEPLOYED_DISPLAY" ] || DEPLOYED_DISPLAY="unknown"
fi

HEAD_DISPLAY="unknown"
if [ -n "$HEAD_COMMIT" ]; then
  HEAD_DISPLAY="$(summarize "$HEAD_COMMIT")"
fi

REMOTE_DISPLAY="unknown"
if [ -n "$REMOTE_COMMIT" ]; then
  REMOTE_DISPLAY="$(summarize "$REMOTE_COMMIT")"
fi

SERVER_STATE="missing"
if [ -f "${APP_DIR}/gif-server" ]; then
  SERVER_STATE="present"
fi
PREVIOUS_STATE="missing"
if [ -f "${APP_DIR}/gif-server.previous" ]; then
  PREVIOUS_STATE="present"
fi

say_field "application:" "${APP_DIR}"
say_field "branch:" "${BRANCH}"
say_field "state file:" "${STATE_FILE}"
say_field "deployed commit:" "${DEPLOYED_DISPLAY}"
say_field "local HEAD:" "${HEAD_DISPLAY}"
say_field "origin/${BRANCH}:" "${REMOTE_DISPLAY}"
say_field "running binary:" "gif-server=${SERVER_STATE}, gif-server.previous=${PREVIOUS_STATE}"
say_field "status:" "${STATUS}"

case "$STATUS" in
  newer-commit-available)
    say ""
    say "提示：origin/${BRANCH} 上有尚未上线的提交，可确认后回滚一个提交并重新部署。"
    ;;
  stuck)
    say ""
    say "提示：HEAD 已经等于 origin/${BRANCH}，但状态文件记录的仍然是旧提交，"
    say "      说明上一次部署没有成功，可确认后回滚一个提交并重新部署。"
    say "      若因磁盘空间不足导致构建失败，请先释放磁盘空间："
    say "      /usr/bin/bash ${SCRIPT_DIR}/clean_disk.sh"
    ;;
  unknown)
    say ""
    case "$REASON" in
      not-a-git-repository)
        say "提示：${APP_DIR} 不是可用的 git 仓库，请检查位置参数或 APP_DIR 环境变量。"
        ;;
      fetch-failed)
        say "提示：git fetch origin/${BRANCH} 失败（网络或权限问题），请检查远端后重试。"
        ;;
      no-origin-ref)
        say "提示：无法解析 origin/${BRANCH}；远端引用可能尚未 fetch，"
        say "      请加 --fetch 重试，或检查远端与分支名。"
        ;;
      missing-state-file)
        say "提示：状态文件 ${STATE_FILE} 不存在；部署成功前不会生成。"
        say "      请执行：/usr/bin/bash ${SCRIPT_DIR}/auto_deploy.sh"
        ;;
      unresolvable-state-commit)
        say "提示：状态文件内容无法解析为本仓库中的提交：${DEPLOYED_RAW:-<空>}"
        say "      该文件可能被手工改坏，或仓库历史被重写；确认后重跑："
        say "      /usr/bin/bash ${SCRIPT_DIR}/auto_deploy.sh"
        ;;
    esac
    ;;
esac

# confirm_rollback 交互确认是否回滚并重新部署；空输入或非 y/yes 视为取消。
confirm_rollback() {
  printf '检测到状态 %s：是否回滚一个提交并重新部署？[y/N] ' "$STATUS" >&2
  local answer=""
  read -r answer || answer=""
  case "$answer" in
    [yY]|[yY][eE][sS]) return 0 ;;
    *) return 1 ;;
  esac
}

# 非 up-to-date 时先交互确认，确认后才回滚一个提交并重新部署；所有路径均使用绝对路径。
if [ "$STATUS" != "up-to-date" ] && [ "$IS_REPO" -eq 1 ] && [ -n "$HEAD_COMMIT" ]; then
  say ""
  if [ "$QUIET" -eq 1 ]; then
    : # 静默模式：不执行回滚与重新部署，仅输出状态。
  elif confirm_rollback; then
    say "已确认，开始回滚并重新部署。"
    say "执行: git -C ${APP_DIR} reset --hard HEAD~1"
    if ! git -C "$APP_DIR" reset --hard HEAD~1; then
      echo "回滚失败：git -C ${APP_DIR} reset --hard HEAD~1" >&2
      exit "$EXIT_CODE"
    fi
    say "已回滚到: $(git -C "$APP_DIR" rev-parse --short HEAD 2>/dev/null || true)"
    say "执行: /usr/bin/bash ${SCRIPT_DIR}/auto_deploy.sh"
    if BRANCH="$BRANCH" DEPLOY_STATE_FILE="$STATE_FILE" /usr/bin/bash "${SCRIPT_DIR}/auto_deploy.sh"; then
      say "自动部署完成，状态已恢复为 up-to-date。"
      EXIT_CODE=0
      STATUS="up-to-date"
    else
      echo "自动部署失败（auto_deploy.sh 返回非零）" >&2
    fi
  else
    say "未确认，跳过回滚与重新部署。"
  fi
fi

if [ "$QUIET" -eq 1 ]; then
  printf '%s\n' "$STATUS"
fi
exit "$EXIT_CODE"

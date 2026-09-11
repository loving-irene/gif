#!/usr/bin/env bash
set -euo pipefail

SCHEDULE="${SCHEDULE:-*/5 * * * *}"
APP_DIR="$(cd "$(dirname "$0")/.." && pwd)"
LINE="${SCHEDULE} cd ${APP_DIR} && ./scripts/auto_deploy.sh >> ${APP_DIR}/auto_deploy.log 2>&1"
BEGIN_MARKER="# BEGIN GIF AUTO DEPLOY"
END_MARKER="# END GIF AUTO DEPLOY"

current_crontab="$(crontab -l 2>/dev/null || true)"
if ! cleaned_crontab="$(printf '%s\n' "$current_crontab" | awk \
  -v begin="$BEGIN_MARKER" -v end="$END_MARKER" -v app_dir="$APP_DIR" '
  $0 == begin {
    if (inside || seen_begin) exit 2
    inside = 1
    seen_begin = 1
    next
  }
  $0 == end {
    if (!inside || seen_end) exit 2
    inside = 0
    seen_end = 1
    next
  }
  !inside && index($0, "cd " app_dir " &&") && index($0, "./scripts/auto_deploy.sh") { next }
  !inside { print }
  END { if (inside || seen_begin != seen_end) exit 2 }
')"; then
  echo "invalid GIF AUTO DEPLOY marker block; crontab was not changed" >&2
  exit 1
fi

{
  if [ -n "$cleaned_crontab" ]; then
    printf '%s\n' "$cleaned_crontab"
  fi
  printf '%s\n%s\n%s\n' "$BEGIN_MARKER" "$LINE" "$END_MARKER"
} | crontab -
echo "installed: $LINE"

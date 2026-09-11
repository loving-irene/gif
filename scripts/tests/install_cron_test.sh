#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
TEST_DIR="$(mktemp -d)"
trap 'rm -rf "$TEST_DIR"' EXIT
STATE_FILE="${TEST_DIR}/crontab.txt"

cat >"${TEST_DIR}/crontab" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [ "${1:-}" = "-l" ]; then
  cat "$FAKE_CRONTAB_STATE"
else
  cat >"$FAKE_CRONTAB_STATE"
fi
EOF
chmod 755 "${TEST_DIR}/crontab"

cat >"$STATE_FILE" <<EOF
# keep this comment
*/5 * * * * cd ${ROOT_DIR} && ./scripts/auto_deploy.sh >> ${ROOT_DIR}/old.log 2>&1
*/5 * * * * cd /var/www/message && ./scripts/auto_deploy.sh
EOF

run_installer() {
  PATH="${TEST_DIR}:${PATH}" FAKE_CRONTAB_STATE="$STATE_FILE" \
    /usr/bin/bash "$ROOT_DIR/scripts/install_cron.sh" >/dev/null
}

run_installer
run_installer

[ "$(grep -c '^# BEGIN GIF AUTO DEPLOY$' "$STATE_FILE")" -eq 1 ]
[ "$(grep -c '^# END GIF AUTO DEPLOY$' "$STATE_FILE")" -eq 1 ]
[ "$(grep -cF "cd ${ROOT_DIR} &&" "$STATE_FILE")" -eq 1 ]
grep -qF "cd /var/www/message && ./scripts/auto_deploy.sh" "$STATE_FILE"
grep -qF "# keep this comment" "$STATE_FILE"

printf '%s\n' '# END GIF AUTO DEPLOY' >"$STATE_FILE"
cp "$STATE_FILE" "${STATE_FILE}.before"
if run_installer 2>/dev/null; then
  echo "installer accepted an incomplete marker block" >&2
  exit 1
fi
cmp -s "$STATE_FILE" "${STATE_FILE}.before"

echo "gif cron installer test ok"

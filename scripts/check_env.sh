#!/usr/bin/env bash
set -euo pipefail
for command in go git curl ss openssl flock systemctl nginx crontab certbot; do
 command -v "$command" >/dev/null || { echo "missing dependency: $command" >&2; exit 1; }
done
go version
echo 'dependencies found. Configure DNS, .env, and repository access before deployment.'

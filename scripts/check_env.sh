#!/usr/bin/env bash
set -euo pipefail
for command in go git curl ss openssl flock systemctl nginx crontab certbot python3; do
 command -v "$command" >/dev/null || { echo "missing dependency: $command" >&2; exit 1; }
done
go version
python3 -c 'import sqlite3; assert hasattr(sqlite3.Connection, "backup"), "Python 3.7+ with SQLite backup support is required"'
echo 'dependencies found. Configure DNS, .env, and repository access before deployment.'

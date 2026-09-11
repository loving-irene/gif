#!/usr/bin/env bash
set -euo pipefail
PORT="${PORT:-8096}"
APP_DIR="${APP_DIR:-/var/www/gif}"
DATA_DIR="${DATA_DIR:-/var/lib/gif}"
SERVICE_NAME="${SERVICE_NAME:-gif}"
GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
BACKUP_DIR="${BACKUP_DIR:-/var/backups/gif}"
[[ "$PORT" =~ ^[0-9]+$ && "$SERVICE_NAME" =~ ^[a-zA-Z0-9_-]+$ ]] || exit 1
[[ "$APP_DIR" =~ ^/[a-zA-Z0-9/_-]+$ && "$DATA_DIR" =~ ^/[a-zA-Z0-9/_-]+$ ]] || exit 1
cd "$APP_DIR"
exec 9> .deploy.lock
flock -n 9 || { echo 'another deployment is running'; exit 1; }
test -f .env || { echo 'run scripts/init_env.sh first'; exit 1; }
current_pid="$(systemctl show -p MainPID --value "$SERVICE_NAME" 2>/dev/null || true)"
command -v ss >/dev/null || { echo 'ss is required for port checks'; exit 1; }
for pid in $(ss -ltnp "sport = :${PORT}" | sed -n 's/.*pid=\([0-9]\+\).*/\1/p' | sort -u); do
  test "$pid" = "$current_pid" || { echo "port $PORT is occupied; aborting"; exit 1; }
done
GOPROXY="$GOPROXY" go test ./...
GOPROXY="$GOPROXY" go build -trimpath -buildvcs=false -o gif-server.next ./cmd/server
# 数据库备份成功后，才允许替换二进制、重启和运行新版本迁移。
sudo python3 "$APP_DIR/scripts/backup_database.py" --app-dir "$APP_DIR" --config "$APP_DIR/.env" --backup-dir "$BACKUP_DIR"
sudo install -d -m 750 -o www-data -g www-data "$DATA_DIR"
sudo chown root:www-data .env
sudo chmod 640 .env
chmod 755 gif-server.next
test ! -f gif-server || cp -p gif-server gif-server.previous
mv gif-server.next gif-server
sudo tee "/etc/systemd/system/${SERVICE_NAME}.service" >/dev/null <<SERVICE
[Unit]
Description=Shiguang GIF Studio
After=network.target
[Service]
User=www-data
Group=www-data
WorkingDirectory=${APP_DIR}
ExecStart=${APP_DIR}/gif-server -config ${APP_DIR}/.env -port ${PORT}
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=${DATA_DIR}
UMask=0077
MemoryMax=768M
MemorySwapMax=0
LimitCORE=0
LimitNOFILE=4096
TasksMax=128
[Install]
WantedBy=multi-user.target
SERVICE
sudo systemctl daemon-reload
sudo systemctl enable "$SERVICE_NAME"
sudo systemctl restart "$SERVICE_NAME"
for attempt in {1..20}; do
  if curl -fsS --max-time 2 "http://127.0.0.1:${PORT}/healthz" | grep -qx ok; then
    echo "deployment healthy: $(git rev-parse --short HEAD)"
    exit 0
  fi
  sleep 1
done
echo 'health check failed; restoring previous binary' >&2
if test -f gif-server.previous; then
  mv gif-server.previous gif-server
  sudo systemctl restart "$SERVICE_NAME"
fi
exit 1

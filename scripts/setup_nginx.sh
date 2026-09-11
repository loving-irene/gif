#!/usr/bin/env bash
set -euo pipefail
DOMAIN="${DOMAIN:-gif.jcc666.top}"
PORT="${PORT:-8096}"
SERVICE_NAME="${SERVICE_NAME:-gif}"
[[ "$DOMAIN" =~ ^[a-zA-Z0-9.-]+$ && "$PORT" =~ ^[0-9]+$ && "$SERVICE_NAME" =~ ^[a-zA-Z0-9_-]+$ ]] || exit 1
AVAILABLE="/etc/nginx/sites-available/${SERVICE_NAME}"
ENABLED="/etc/nginx/sites-enabled/${SERVICE_NAME}"
if test -f "$AVAILABLE" && test "${FORCE:-false}" != true; then
 echo "$AVAILABLE exists; HTTPS configuration preserved"
else
 sudo tee "$AVAILABLE" >/dev/null <<NGINX
server {
 listen 80;
 server_name ${DOMAIN};
 client_max_body_size 15m;
 client_body_timeout 30s;
 location / {
  proxy_pass http://127.0.0.1:${PORT};
  proxy_set_header Host \$host;
  proxy_set_header X-Real-IP \$remote_addr;
  proxy_set_header X-Forwarded-Proto \$scheme;
  proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
  proxy_request_buffering off;
  proxy_buffering off;
  proxy_max_temp_file_size 0;
  proxy_http_version 1.1;
  proxy_read_timeout 70s;
  proxy_send_timeout 45s;
 }
 location ~ /(?:\.env|\.git|\.db|scripts)/? {
  return 404;
 }
}
NGINX
fi
sudo ln -sfn "$AVAILABLE" "$ENABLED"
sudo nginx -t
sudo systemctl reload nginx

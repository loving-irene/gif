#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
if test -e .env; then echo '.env exists; left unchanged'; exit 0; fi
command -v openssl >/dev/null
umask 077
secret="$(openssl rand -hex 32)"
admin_password="$(openssl rand -hex 24)"
cat > .env <<ENV
GIF_BASE_URL=https://gif.jcc666.top
GIF_DATABASE_PATH=/var/lib/gif/gif.db
GIF_SECRET=${secret}
GIF_COOKIE_SECURE=true
GIF_TRUST_PROXY=true
GIF_DEBUG=false
GIF_ADMIN_PASSWORD=${admin_password}
GEEKAI_API_KEY=
# Aliyun Direct Mail, China (Hangzhou). These can also be set in /who.
ALIYUN_DM_SENDER=
ALIYUN_DM_SMTP_PASSWORD=
ENV
echo '.env created with unique secrets. Read GIF_ADMIN_PASSWORD locally to sign in; do not paste secrets into logs or commit this file.'

#!/bin/zsh
set -euo pipefail

SCRIPT_DIR="${0:A:h}"
SERVER_DIR="${SCRIPT_DIR:h}"
PROJECT_DIR="${SERVER_DIR:h}"
ENV_FILE="${CODEX_MOBILE_ENV_FILE:-$SERVER_DIR/.env}"

if [[ -f "$ENV_FILE" ]]; then
  set -a
  source "$ENV_FILE"
  set +a
fi

export PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:${PATH:-}"
export CODEX_BINARY="${CODEX_BINARY:-/Applications/Codex.app/Contents/Resources/codex}"
export CODEX_MOBILE_HOST="${CODEX_MOBILE_HOST:-127.0.0.1}"
export CODEX_MOBILE_PORT="${CODEX_MOBILE_PORT:-8000}"
export CODEX_APP_SERVER_HOST="${CODEX_APP_SERVER_HOST:-127.0.0.1}"
export CODEX_APP_SERVER_PORT="${CODEX_APP_SERVER_PORT:-39000}"
export CODEX_MOBILE_DEFAULT_CWD="${CODEX_MOBILE_DEFAULT_CWD:-$PROJECT_DIR}"
export CODEX_MOBILE_CLIENT_PING_INTERVAL_MS="${CODEX_MOBILE_CLIENT_PING_INTERVAL_MS:-15000}"
export CODEX_MOBILE_CLIENT_IDLE_TIMEOUT_MS="${CODEX_MOBILE_CLIENT_IDLE_TIMEOUT_MS:-45000}"

: "${CODEX_MOBILE_TOKEN:?CODEX_MOBILE_TOKEN 未设置}"

NODE_BINARY="${NODE_BINARY:-/opt/homebrew/bin/node}"
if [[ ! -x "$NODE_BINARY" ]]; then
  NODE_BINARY="$(command -v node || true)"
fi
if [[ -z "$NODE_BINARY" || ! -x "$NODE_BINARY" ]]; then
  echo "找不到可执行的 node" >&2
  exit 1
fi

cd "$SERVER_DIR"
exec "$NODE_BINARY" dist/index.js

#!/bin/zsh
set -euo pipefail

LABEL="work.codex.mobile.gateway"
SCRIPT_DIR="${0:A:h}"
SERVER_DIR="${SCRIPT_DIR:h}"
PROJECT_DIR="${SERVER_DIR:h}"
SUPPORT_DIR="$HOME/Library/Application Support/CodexMobileGateway"
RUNTIME_DIR="$SUPPORT_DIR/runtime"
START_SCRIPT="$SUPPORT_DIR/start-gateway.sh"
ENV_FILE="${CODEX_MOBILE_ENV_FILE:-$SERVER_DIR/.env}"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
LOG_DIR="$HOME/Library/Logs/CodexMobileGateway"

mkdir -p "$HOME/Library/LaunchAgents" "$LOG_DIR" "$SUPPORT_DIR"

if [[ ! -f "$ENV_FILE" ]]; then
  if [[ -z "${CODEX_MOBILE_TOKEN:-}" ]]; then
    echo "缺少 $ENV_FILE，且当前环境没有 CODEX_MOBILE_TOKEN" >&2
    exit 1
  fi

  umask 077
  {
    print -r -- "CODEX_MOBILE_TOKEN=${CODEX_MOBILE_TOKEN}"
    if [[ -n "${CODEX_THREAD_ID:-}" ]]; then
      print -r -- "CODEX_THREAD_ID=${CODEX_THREAD_ID}"
    fi
    print -r -- "CODEX_MOBILE_DEFAULT_CWD=${CODEX_MOBILE_DEFAULT_CWD:-$PROJECT_DIR}"
    print -r -- "CODEX_BINARY=${CODEX_BINARY:-/Applications/Codex.app/Contents/Resources/codex}"
    print -r -- "CODEX_MOBILE_CLIENT_PING_INTERVAL_MS=${CODEX_MOBILE_CLIENT_PING_INTERVAL_MS:-15000}"
    print -r -- "CODEX_MOBILE_CLIENT_IDLE_TIMEOUT_MS=${CODEX_MOBILE_CLIENT_IDLE_TIMEOUT_MS:-45000}"
  } > "$ENV_FILE"
fi

set -a
source "$ENV_FILE"
set +a

if [[ -z "${CODEX_MOBILE_TOKEN:-}" || ${#CODEX_MOBILE_TOKEN} -lt 16 ]]; then
  echo "CODEX_MOBILE_TOKEN 必须设置，且长度至少 16 位" >&2
  exit 1
fi
rm -rf "$RUNTIME_DIR"
mkdir -p "$RUNTIME_DIR"
cp -R "$SERVER_DIR/dist" "$RUNTIME_DIR/dist"
cp -R "$SERVER_DIR/node_modules" "$RUNTIME_DIR/node_modules"
cp "$SERVER_DIR/package.json" "$SERVER_DIR/package-lock.json" "$RUNTIME_DIR/"
cp "$ENV_FILE" "$RUNTIME_DIR/.env"
chmod 600 "$RUNTIME_DIR/.env"
if ! grep -q '^CODEX_MOBILE_DEFAULT_CWD=.' "$RUNTIME_DIR/.env"; then
  {
    print -r -- ""
    print -r -- "CODEX_MOBILE_DEFAULT_CWD=$PROJECT_DIR"
  } >> "$RUNTIME_DIR/.env"
fi

cat > "$START_SCRIPT" <<'EOF'
#!/bin/zsh
set -euo pipefail

SUPPORT_DIR="${CODEX_MOBILE_SUPPORT_DIR:-$HOME/Library/Application Support/CodexMobileGateway}"
RUNTIME_DIR="$SUPPORT_DIR/runtime"
ENV_FILE="${CODEX_MOBILE_ENV_FILE:-$RUNTIME_DIR/.env}"

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
export CODEX_MOBILE_DEFAULT_CWD="${CODEX_MOBILE_DEFAULT_CWD:-$RUNTIME_DIR}"
export CODEX_MOBILE_CLIENT_PING_INTERVAL_MS="${CODEX_MOBILE_CLIENT_PING_INTERVAL_MS:-15000}"
export CODEX_MOBILE_CLIENT_IDLE_TIMEOUT_MS="${CODEX_MOBILE_CLIENT_IDLE_TIMEOUT_MS:-45000}"

: "${CODEX_MOBILE_TOKEN:?CODEX_MOBILE_TOKEN 未设置}"

APP_SERVER_PIDS=("${(@f)$(lsof -tiTCP:"$CODEX_APP_SERVER_PORT" -sTCP:LISTEN 2>/dev/null || true)}")
if (( ${#APP_SERVER_PIDS[@]} > 0 )); then
  kill -TERM "${APP_SERVER_PIDS[@]}" 2>/dev/null || true
  sleep 0.5
fi

NODE_BINARY="${NODE_BINARY:-/opt/homebrew/bin/node}"
if [[ ! -x "$NODE_BINARY" ]]; then
  NODE_BINARY="$(command -v node || true)"
fi
if [[ -z "$NODE_BINARY" || ! -x "$NODE_BINARY" ]]; then
  echo "找不到可执行的 node" >&2
  exit 1
fi

cd "$RUNTIME_DIR"
exec "$NODE_BINARY" dist/index.js
EOF
chmod +x "$START_SCRIPT"

cat > "$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>$LABEL</string>
  <key>ProgramArguments</key>
  <array>
    <string>$START_SCRIPT</string>
  </array>
  <key>WorkingDirectory</key>
  <string>$SERVER_DIR</string>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ThrottleInterval</key>
  <integer>5</integer>
  <key>StandardOutPath</key>
  <string>$LOG_DIR/gateway.log</string>
  <key>StandardErrorPath</key>
  <string>$LOG_DIR/gateway.err.log</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
  </dict>
</dict>
</plist>
EOF

launchctl bootout "gui/$UID" "$PLIST" >/dev/null 2>&1 || true
launchctl bootstrap "gui/$UID" "$PLIST"
launchctl enable "gui/$UID/$LABEL"
launchctl kickstart -k "gui/$UID/$LABEL"

echo "已安装并启动 launchd 服务: $LABEL"
echo "配置文件: $ENV_FILE"
echo "运行配置: $RUNTIME_DIR/.env"
echo "运行目录: $RUNTIME_DIR"
echo "日志目录: $LOG_DIR"

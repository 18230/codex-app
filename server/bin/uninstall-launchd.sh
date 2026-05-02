#!/bin/zsh
set -euo pipefail

LABEL="work.codex.mobile.gateway"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
SUPPORT_DIR="$HOME/Library/Application Support/CodexMobileGateway"

launchctl bootout "gui/$UID" "$PLIST" >/dev/null 2>&1 || true
rm -f "$PLIST"
rm -rf "$SUPPORT_DIR/runtime" "$SUPPORT_DIR/start-gateway.sh"

echo "已卸载 launchd 服务: $LABEL"

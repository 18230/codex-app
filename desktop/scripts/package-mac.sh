#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
WAILS_BIN="${WAILS_BIN:-$(go env GOPATH)/bin/wails}"

cd "$PROJECT_DIR"
"$WAILS_BIN" build -platform darwin/arm64 -clean -o CodexMobileGateway || true

if [[ ! -d build/bin/CodexMobileGateway.app ]]; then
  echo "CodexMobileGateway.app 未生成" >&2
  exit 1
fi

TMP_DIR="$(mktemp -d /tmp/codex-mobile-gateway.XXXXXX)"
trap 'rm -rf "$TMP_DIR"' EXIT

cp -R build/bin/CodexMobileGateway.app "$TMP_DIR/"
xattr -cr "$TMP_DIR/CodexMobileGateway.app"
codesign --force --deep --sign - "$TMP_DIR/CodexMobileGateway.app"
codesign --verify --deep --strict "$TMP_DIR/CodexMobileGateway.app"
rm -f build/bin/CodexMobileGateway.dmg
hdiutil create -volname CodexMobileGateway -srcfolder "$TMP_DIR/CodexMobileGateway.app" -ov -format UDZO build/bin/CodexMobileGateway.dmg
hdiutil verify build/bin/CodexMobileGateway.dmg
xattr -cr build/bin/CodexMobileGateway.dmg

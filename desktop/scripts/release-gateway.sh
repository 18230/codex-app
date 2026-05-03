#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
用法：
  ./scripts/release-gateway.sh [tag]

说明：
  只发布 macOS 桌面网关包，不构建或上传 Android APK。
  tag 为空时默认使用 GitHub latest release。

示例：
  ./scripts/release-gateway.sh v0.2.4
  ./scripts/release-gateway.sh
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
REPO_DIR="$(cd "$PROJECT_DIR/.." && pwd)"
TAG="${1:-}"

require_command() {
  local name="$1"
  if ! command -v "$name" >/dev/null 2>&1; then
    echo "缺少命令：$name" >&2
    exit 1
  fi
}

require_command gh
require_command go
require_command npm

WAILS_BIN="${WAILS_BIN:-$(go env GOPATH)/bin/wails3}"
if [[ ! -x "$WAILS_BIN" ]]; then
  echo "找不到 wails3：$WAILS_BIN" >&2
  echo "可通过 WAILS_BIN=/path/to/wails3 指定。" >&2
  exit 1
fi

cd "$REPO_DIR"
gh auth status >/dev/null

if [[ -z "$TAG" ]]; then
  TAG="$(gh release list --limit 1 --json tagName --jq '.[0].tagName')"
fi
if [[ -z "$TAG" ]]; then
  echo "未找到可发布的 GitHub release tag" >&2
  exit 1
fi
if ! gh release view "$TAG" >/dev/null; then
  echo "GitHub release 不存在：$TAG" >&2
  exit 1
fi

VERSION="${TAG#v}"
if [[ -z "$VERSION" || "$VERSION" == "$TAG" ]]; then
  echo "release tag 需要使用 vX.Y.Z 格式：$TAG" >&2
  exit 1
fi

cd "$PROJECT_DIR"
PATH="$(dirname "$WAILS_BIN"):$PATH" "$SCRIPT_DIR/package-mac.sh"

DMG_SOURCE="$PROJECT_DIR/bin/CodexMobileGateway.dmg"
if [[ ! -f "$DMG_SOURCE" ]]; then
  echo "DMG 未生成：$DMG_SOURCE" >&2
  exit 1
fi

TMP_DIR="$(mktemp -d /tmp/codex-gateway-release.XXXXXX)"
trap 'rm -rf "$TMP_DIR"' EXIT

DMG_ASSET="$TMP_DIR/CodexMobileGateway-${VERSION}-darwin-arm64.dmg"
cp "$DMG_SOURCE" "$DMG_ASSET"

cd "$REPO_DIR"
gh release upload "$TAG" "$DMG_ASSET" --clobber

echo "已发布桌面网关包到 GitHub Release：$TAG"
echo "  $(basename "$DMG_ASSET")"

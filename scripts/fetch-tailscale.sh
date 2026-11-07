#!/bin/bash
# 取官方 tailscale 静态程序（BSD-3）：scripts/fetch-tailscale.sh <amd64|arm64> <输出目录>
# 版本固定在 TAILSCALE_VERSION；tarball 缓存在 .cache/（MEOWX_CACHE 可改），校验官方发布的 sha256。
set -euo pipefail
GOARCH="${1:?amd64 | arm64}"
OUT="${2:?输出目录}"
TAILSCALE_VERSION="${TAILSCALE_VERSION:-1.104.5}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
NAME="tailscale_${TAILSCALE_VERSION}_${GOARCH}.tgz"
CACHE_DIR="${MEOWX_CACHE:-$ROOT/.cache}"
CACHE="$CACHE_DIR/$NAME"
mkdir -p "$CACHE_DIR" "$OUT"
if [ ! -f "$CACHE" ]; then
    curl -fsSL -o "$CACHE.tmp" "https://pkgs.tailscale.com/stable/$NAME"
    WANT="$(curl -fsSL "https://pkgs.tailscale.com/stable/$NAME.sha256")"
    GOT="$(shasum -a 256 "$CACHE.tmp" 2>/dev/null | cut -d' ' -f1 || sha256sum "$CACHE.tmp" | cut -d' ' -f1)"
    [ "$WANT" = "$GOT" ] || { echo "sha256 不符：$NAME" >&2; rm -f "$CACHE.tmp"; exit 1; }
    mv "$CACHE.tmp" "$CACHE"
fi
tar -xzf "$CACHE" -C "$OUT" --strip-components=1 "tailscale_${TAILSCALE_VERSION}_${GOARCH}/tailscale" "tailscale_${TAILSCALE_VERSION}_${GOARCH}/tailscaled"
echo "$OUT/tailscale{,d} ($TAILSCALE_VERSION)"

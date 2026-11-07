#!/bin/bash
# 编内核：scripts/build-core.sh <amd64|arm64> <输出路径>
# meowx-core 按 CORE_REF 拉源码（缓存在 .cache/meowx-core，MEOWX_CACHE 可改；开发时可用 MEOWX_CORE_SRC 指向本地检出），打上 patches/core/*.patch（-p3，
# 与 meowx-openwrt 同一份补丁），tag 同 OpenWrt 版：喵窝用官方 tailscaled，内核不带 tailscale。
set -euo pipefail
GOARCH="${1:?amd64 | arm64}"
OUT="${2:?输出路径}"
CORE_REPO="https://github.com/mmwx-group/meowx-core.git"
CORE_REF="${CORE_REF:-0909705}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="${MEOWX_CACHE:-$ROOT/.cache}/meowx-core"

if [ -n "${MEOWX_CORE_SRC:-}" ]; then
    rm -rf "$SRC"; mkdir -p "$(dirname "$SRC")"
    git -C "$MEOWX_CORE_SRC" worktree prune
    cp -R "$MEOWX_CORE_SRC" "$SRC"
    git -C "$SRC" checkout -q --force "$CORE_REF"
else
    [ -d "$SRC/.git" ] || git clone -q "$CORE_REPO" "$SRC"
    git -C "$SRC" fetch -q origin
    git -C "$SRC" checkout -q --force "$CORE_REF"
fi
git -C "$SRC" clean -qfd
for p in "$ROOT"/patches/core/*.patch; do git -C "$SRC" apply -p3 "$p"; done

VERSION="meowx-fnos-$(git -C "$SRC" rev-parse --short HEAD)"
(cd "$SRC" && CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" go build -trimpath \
    -tags with_gvisor,no_easytier,no_tailscale,no_zerotier \
    -ldflags "-s -w -X github.com/metacubex/mihomo/constant.Version=$VERSION" \
    -o "$OUT" .)
echo "$OUT ($VERSION)"

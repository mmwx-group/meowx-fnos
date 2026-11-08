#!/bin/bash
# 编内核：scripts/build-core.sh <amd64|arm64> <输出路径>
# meowx-core 按 CORE_REF 拉源码（缓存在 .cache/meowx-core，MEOWX_CACHE 可改；开发时可用 MEOWX_CORE_SRC 指向本地检出），有补丁时打上
# patches/core/*.patch（-p3），tag 同 OpenWrt 版：喵窝用官方 tailscaled，内核不带 tailscale。
# 以前的补丁 003–006（喵窝入网、GGY 设置存文件、账户订阅挂在订阅域名上）都已并进 meowx-core，现在没有补丁。
# CORE_REF = meowx-core v0.4.9（带 AkileCDN 白名单子命令）。Actions 从 GitHub 拉源码，要指向已经推上去的提交。
set -euo pipefail
GOARCH="${1:?amd64 | arm64}"
OUT="${2:?输出路径}"
CORE_REPO="https://github.com/mmwx-group/meowx-core.git"
CORE_REF="${CORE_REF:-bca53bd}"
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
for p in "$ROOT"/patches/core/*.patch; do [ -e "$p" ] || continue; git -C "$SRC" apply -p3 "$p"; done

VERSION="meowx-fnos-$(git -C "$SRC" rev-parse --short HEAD)"
(cd "$SRC" && CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" go build -trimpath \
    -tags with_gvisor,no_easytier,no_tailscale,no_zerotier \
    -ldflags "-s -w -X github.com/metacubex/mihomo/constant.Version=$VERSION" \
    -o "$OUT" .)
echo "$OUT ($VERSION)"

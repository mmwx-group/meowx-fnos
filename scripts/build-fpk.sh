#!/bin/bash
# 打飞牛安装包：scripts/build-fpk.sh <版本> [x86|arm]
#
# 产物 dist/meowx-fnos-<版本>-<平台>.fpk = tar.gz：
#   app.tgz（应用目录：docker/{docker-compose.yaml,Dockerfile,rootfs/}）、manifest、cmd/、config/、ui/、图标、meowx.sc
# 镜像由飞牛起容器时按 Dockerfile 就地构建（FROM scratch + rootfs 里的静态程序），不连镜像仓库。
set -euo pipefail

VERSION="${1:?用法：$0 <版本> [x86|arm]}"
PLATFORM="${2:-x86}"
case "$PLATFORM" in
x86) GOARCH=amd64 ;;
arm) GOARCH=arm64 ;;
*) echo "平台只能是 x86 或 arm" >&2; exit 1 ;;
esac

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIST="$ROOT/dist"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
ROOTFS="$WORK/target/docker/rootfs"
mkdir -p "$DIST" "$ROOTFS/usr/bin" "$ROOTFS/data" "$ROOTFS/tmp" "$WORK/pkg"

# 1. 镜像里的程序：控制服务、内核（CORE_BIN 可指向已编好的，省得每次重编）、官方 tailscale
(cd "$ROOT/agent" && CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$ROOTFS/usr/bin/meowx-agent" .)
if [ -n "${CORE_BIN:-}" ]; then
    cp "$CORE_BIN" "$ROOTFS/usr/bin/meowx-core"
else
    "$ROOT/scripts/build-core.sh" "$GOARCH" "$ROOTFS/usr/bin/meowx-core"
fi
"$ROOT/scripts/fetch-tailscale.sh" "$GOARCH" "$ROOTFS/usr/bin" >/dev/null
"$ROOT/scripts/fetch-geo.sh" "$ROOTFS/usr/share/meowx/geo" >/dev/null

# 2. 应用目录
sed "s/__VERSION__/$VERSION/g" "$ROOT/app/docker/docker-compose.yaml" > "$WORK/target/docker/docker-compose.yaml"
cp "$ROOT/app/docker/Dockerfile" "$WORK/target/docker/"
export COPYFILE_DISABLE=1   # macOS 的 tar 别夹带 ._ 文件与扩展属性
# 属主一律 root：GNU tar（CI 的 Linux）与 bsdtar（macOS）写法不同
if tar --version 2>/dev/null | grep -q GNU; then
    TAR_FLAGS=(--owner=0 --group=0 --numeric-owner --no-xattrs)
else
    TAR_FLAGS=(--uid 0 --gid 0)
    tar --no-mac-metadata -cf /dev/null /dev/null 2>/dev/null && TAR_FLAGS+=(--no-xattrs --no-mac-metadata)
fi
tar -C "$WORK/target" "${TAR_FLAGS[@]}" -czf "$WORK/pkg/app.tgz" docker

# 3. 安装包
cp -R "$ROOT/fnos/cmd" "$ROOT/fnos/config" "$ROOT/fnos/ui" "$WORK/pkg/"
cp "$ROOT/fnos/meowx.sc" "$ROOT/fnos/ICON.PNG" "$ROOT/fnos/ICON_256.PNG" "$WORK/pkg/"
if [ -n "$(ls -A "$ROOT/fnos/wizard" 2>/dev/null)" ]; then cp -R "$ROOT/fnos/wizard" "$WORK/pkg/"; fi
CHECKSUM="$(md5 -q "$WORK/pkg/app.tgz" 2>/dev/null || md5sum "$WORK/pkg/app.tgz" | cut -d' ' -f1)"
sed -e "s/^version .*/version         = $VERSION/" \
    -e "s/^platform .*/platform        = $PLATFORM/" \
    -e "s/^checksum .*/checksum        = $CHECKSUM/" \
    "$ROOT/fnos/manifest" > "$WORK/pkg/manifest"

OUT="$DIST/meowx-fnos-${VERSION}-${PLATFORM}.fpk"
(cd "$WORK/pkg" && tar "${TAR_FLAGS[@]}" -czf "$OUT" *)
echo "$OUT ($(du -h "$OUT" | cut -f1))"

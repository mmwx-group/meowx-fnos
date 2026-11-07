#!/bin/bash
# 开发用：把新编的控制服务（与可选的内核）同步到 NAS 上已装好的应用目录，重建容器。不经应用中心，正式发版走 build-fpk.sh。
# 用法：NAS=user@nas.lan NAS_PORT=22 scripts/dev-deploy.sh [版本]
set -euo pipefail
NAS="${NAS:?NAS=user@host}"
NAS_PORT="${NAS_PORT:-22}"
VERSION="${1:-dev}"
GOARCH="${GOARCH:-amd64}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
APPDIR="${APPDIR:-/vol1/@appcenter/meowx}"
PKGVAR="${PKGVAR:-/vol1/@appdata/meowx}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

mkdir -p "$WORK/usr/bin"
(cd "$ROOT/agent" && CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$WORK/usr/bin/meowx-agent" .)
[ -n "${CORE_BIN:-}" ] && cp "$CORE_BIN" "$WORK/usr/bin/meowx-core"
sed "s/__VERSION__/$VERSION/g" "$ROOT/app/docker/docker-compose.yaml" > "$WORK/docker-compose.yaml"

SSH=(ssh -p "$NAS_PORT" -o BatchMode=yes "$NAS")
rsync -a -e "ssh -p $NAS_PORT" --rsync-path="sudo -n rsync" "$WORK/usr/bin/" "$NAS:$APPDIR/docker/rootfs/usr/bin/"
rsync -a -e "ssh -p $NAS_PORT" --rsync-path="sudo -n rsync" "$WORK/docker-compose.yaml" "$ROOT/app/docker/Dockerfile" "$NAS:$APPDIR/docker/"
"${SSH[@]}" "cd $APPDIR/docker && sudo -n env TRIM_PKGVAR=$PKGVAR docker compose -p meowx up -d --build --force-recreate 2>&1 | tail -3"
echo "deployed $VERSION"

#!/bin/bash
# 打通用 Docker 版镜像：scripts/build-docker.sh <版本> [选项]
#
#   --arch amd64,arm64   要哪些架构（缺省两个都要）
#   --tar                每个架构导出一个离线镜像 dist/meowx-docker-<版本>-<架构>.tar（缺省就是这个；
#                        群晖「Container Manager → 映像 → 导入」、威联通 / 绿联的「导入镜像」、docker load 都认）
#   --load               装进本机的 docker（只能一个架构）
#   --push <镜像名>      推到镜像仓库（多架构清单）。第一阶段不用：发布到哪里、叫什么还没定
#   --context-only       只备好构建目录（dist/docker-context），不调 docker
#
# 先按架构把程序备好（同飞牛版：控制服务现编，内核走 build-core.sh（CORE_BIN_<架构> 可指向编好的），官方 tailscale、geo 数据），
# 放进 dist/docker-context/rootfs-<架构>/，再用 docker buildx 按 docker/Dockerfile 出镜像。
# 镜像名 meowx-docker:<版本>，同时打 meowx-docker:latest。
set -euo pipefail

VERSION="${1:?用法：$0 <版本> [--arch amd64,arm64] [--tar|--load|--push 镜像名|--context-only]}"
shift
ARCHES="amd64,arm64"
MODE=tar
PUSH_IMAGE=""
while [ $# -gt 0 ]; do
    case "$1" in
    --arch) ARCHES="${2:?}"; shift ;;
    --tar) MODE=tar ;;
    --load) MODE=load ;;
    --push) MODE=push; PUSH_IMAGE="${2:?--push 要给镜像名}"; shift ;;
    --context-only) MODE=context ;;
    *) echo "不认识的选项 $1" >&2; exit 1 ;;
    esac
    shift
done

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIST="$ROOT/dist"
CTX="$DIST/docker-context"
rm -rf "$CTX"
mkdir -p "$CTX"
cp "$ROOT/docker/Dockerfile" "$CTX/"

IFS=, read -r -a ARCH_LIST <<< "$ARCHES"
for arch in "${ARCH_LIST[@]}"; do
    case "$arch" in amd64 | arm64) ;; *) echo "架构只能是 amd64 / arm64" >&2; exit 1 ;; esac
    RFS="$CTX/rootfs-$arch"
    mkdir -p "$RFS/usr/bin"
    (cd "$ROOT/agent" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" \
        go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$RFS/usr/bin/meowx-agent" .)
    core_var="CORE_BIN_$arch"
    if [ -n "${!core_var:-}" ]; then
        cp "${!core_var}" "$RFS/usr/bin/meowx-core"
    else
        "$ROOT/scripts/build-core.sh" "$arch" "$RFS/usr/bin/meowx-core"
    fi
    "$ROOT/scripts/fetch-tailscale.sh" "$arch" "$RFS/usr/bin" >/dev/null
    "$ROOT/scripts/fetch-geo.sh" "$RFS/usr/share/meowx/geo" >/dev/null
    echo "rootfs-$arch 备好"
done
[ "$MODE" = context ] && { echo "$CTX"; exit 0; }

docker buildx version >/dev/null 2>&1 || { echo "要 docker buildx（多架构构建）" >&2; exit 1; }
TAGS=(-t "meowx-docker:$VERSION" -t "meowx-docker:latest")
case "$MODE" in
tar)
    # buildx 的 docker 驱动（Docker 缺省的那个，没开 containerd 镜像存储时）不能直接导出 tar：先装进本机再 docker save
    DRIVER="$(docker buildx inspect 2>/dev/null | sed -n 's/^Driver: *//p' | head -1)"
    for arch in "${ARCH_LIST[@]}"; do
        OUT="$DIST/meowx-docker-$VERSION-$arch.tar"
        if [ "$DRIVER" = docker ]; then
            docker buildx build --platform "linux/$arch" --load "${TAGS[@]}" "$CTX"
            docker save -o "$OUT" "meowx-docker:$VERSION" "meowx-docker:latest"
        else
            docker buildx build --platform "linux/$arch" \
                -o "type=docker,dest=$OUT,\"name=meowx-docker:$VERSION,meowx-docker:latest\"" "$CTX"
        fi
        echo "$OUT ($(du -h "$OUT" | cut -f1))"
    done
    ;;
load)
    [ "${#ARCH_LIST[@]}" = 1 ] || { echo "--load 只能一个架构" >&2; exit 1; }
    docker buildx build --platform "linux/${ARCH_LIST[0]}" --load "${TAGS[@]}" "$CTX"
    ;;
push)
    PLATFORMS=""
    for arch in "${ARCH_LIST[@]}"; do PLATFORMS="${PLATFORMS:+$PLATFORMS,}linux/$arch"; done
    docker buildx build --platform "$PLATFORMS" --push -t "$PUSH_IMAGE:$VERSION" -t "$PUSH_IMAGE:latest" "$CTX"
    ;;
esac

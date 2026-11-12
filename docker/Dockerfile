# syntax=docker/dockerfile:1
# MeowX 通用 Docker 版（群晖 / 威联通 / 绿联等 NAS）。构建：scripts/build-docker.sh（会先把程序放进 rootfs-<架构>/）。
#
# 和飞牛版（app/docker/Dockerfile，FROM scratch）的区别：群晖这类内核没有 nf_tables，透明代理与喵窝要走 iptables 后端，
# 镜像里得有 iptables 程序（iptables-legacy；它是动态链接的，带着 musl 与几个库）——所以底子换成 Alpine 的几个包：
# busybox（只为能 docker exec 进去看看）、iptables + iptables-legacy、CA 证书、时区数据。
# 这些包在构建机自己的架构上用 apk --root --arch 装进目标架构的目录（不跑目标架构的程序，出 arm64 镜像不用 qemu）。
ARG ALPINE=alpine:3.24.4
ARG BUILDPLATFORM
FROM --platform=$BUILDPLATFORM ${ALPINE} AS base
ARG TARGETARCH
RUN set -eu; \
    case "$TARGETARCH" in amd64) A=x86_64 ;; arm64) A=aarch64 ;; *) echo "不支持的架构 $TARGETARCH" >&2; exit 1 ;; esac; \
    mkdir -p /out/etc/apk/keys; \
    cp /etc/apk/repositories /out/etc/apk/; \
    cp /usr/share/apk/keys/$A/* /out/etc/apk/keys/; \
    apk add --root /out --arch "$A" --initdb --no-scripts --no-cache \
        busybox iptables iptables-legacy ca-certificates-bundle tzdata; \
    # 没有 nf_tables 的内核上，内核的 auto-redirect 会去调 PATH 里的 iptables：指向 legacy
    cd /out/usr/sbin; \
    for n in iptables iptables-save iptables-restore ip6tables ip6tables-save ip6tables-restore; do ln -sf xtables-legacy-multi "$n"; done; \
    # --no-scripts 不会建 busybox 的小命令：补几个排查时常用的
    for n in ls cat ps ip ping wget top df du mkdir rm cp mv grep sed tail head vi env; do ln -sf /bin/busybox "/out/bin/$n"; done; \
    # /run：iptables-legacy 的锁文件 /run/xtables.lock 放这里（没有这个目录 legacy 直接报错）
    mkdir -p /out/data /out/tmp /out/run; chmod 1777 /out/tmp

FROM scratch
COPY --from=base /out/ /
ARG TARGETARCH
COPY rootfs-${TARGETARCH}/ /
ENV PATH=/usr/sbin:/usr/bin:/sbin:/bin \
    MEOWX_PLATFORM=docker \
    MEOWX_UI_PORT=7899 \
    TZ=Asia/Shanghai
VOLUME /data
# 网页 7899、混合代理端口 7890（容器用宿主网络，EXPOSE 只是说明）
EXPOSE 7899 7890
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/bin/meowx-agent"]

# 飞牛的应用脚本以应用自己的账号运行、碰不到 Docker，容器由飞牛直接按 compose 起：
# 所以镜像就地构建——只有静态程序、FROM scratch，不连任何镜像仓库。
FROM scratch
COPY rootfs/ /
ENV PATH=/usr/bin SSL_CERT_DIR=/etc/ssl/certs
ENTRYPOINT ["/usr/bin/meowx-agent"]

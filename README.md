# MeowX for 飞牛 NAS（fnOS）

在飞牛 NAS 上跑 MeowX：NAS 本机、NAS 上的其它容器、局域网设备（旁路由）都能透明代理，局域网里的设备也可以直接用 NAS 的代理端口；
还能加入主控的喵窝组网，在外面访问家里的 NAS 和局域网。全部在浏览器里管理。

- 订阅：粘贴订阅链接，或登录主控账户后从账户导入；自动更新、流量 / 到期显示
- 节点：分组、测速、切换；规则 / 全局 / 直连三种出站模式；连接与日志
- 透明代理：NAS 本机与其它容器走内核的 TUN，旁路由走 TPROXY；打开或改动后有试用期，到点没确认自动关掉，不怕把 NAS 的网络弄断
- 代理端口：HTTP / SOCKS5 混合端口（缺省 7890），局域网免密、外网连进来要密码
- 喵窝：用官方 tailscaled 入网，可以开放 NAS 所在的局域网网段
- po0 / GGY 加白

## 安装

1. 到 [Releases](https://github.com/mmwx-group/meowx-fnos/releases) 下载安装包：x86 机器选 `meowx-fnos-<版本>-x86.fpk`，ARM 机器选 `meowx-fnos-<版本>-arm.fpk`。
2. 飞牛「应用中心」→ 侧栏底部「手动安装」→ 上传 fpk。
3. 装好后在桌面打开 MeowX，或用浏览器访问 `http://<NAS 的地址>:7899`。

要求：fnOS 带 Docker（应用以 Docker 项目运行，镜像在 NAS 上就地构建，不用连镜像仓库）。

**升级**：0.1.1 起在「设置」里检查更新，有新版时点「立即更新」：MeowX 自己下载新版安装包（校验 sha256），
交给飞牛应用中心原地升级（同网页「手动安装」，设置、订阅、喵窝的设备身份都保留），一两分钟后自动重启。
0.1.0 没有这个功能，要先手动装一次 0.1.1：下载新版 fpk 再「手动安装」；提示已安装时先在应用中心卸载再装——
设置、订阅、喵窝的设备身份都存在 `/vol1/@appdata/meowx`，卸载不会删掉。

**网页密码**：管理页面缺省不要密码，NAS 的 7899 端口会开放到外网时请在「设置」里设一个。

## 从源码打包

```sh
scripts/build-fpk.sh <版本> [x86|arm]   # 产物在 dist/meowx-fnos-<版本>-<平台>.fpk
```

要 Go 1.27、git、curl。内核从 [meowx-core](https://github.com/mmwx-group/meowx-core) 按 `scripts/build-core.sh` 里固定的提交编译，
有补丁时打上 `patches/core/` 的补丁；喵窝用官方 tailscale 的静态程序（`scripts/fetch-tailscale.sh`，校验 sha256）。
推 main 由 Actions 打包，打 `v*` tag 发布（tag 要与 `fnos/manifest` 的 version 一致）。

设计见 [docs/design.md](docs/design.md)，控制服务的 API 见 [docs/api.md](docs/api.md)。

## 致谢与许可

内核是 MeowX 的 mihomo 分支 [meowx-core](https://github.com/mmwx-group/meowx-core)；后台做法来自
[MeowX OpenWrt 版](https://github.com/mmwx-group/meowx-openwrt)（基于 [OpenWrt-nikki](https://github.com/nikkinikki-org/OpenWrt-nikki)）。
本项目以 [GPL-3.0](LICENSE) 开源。

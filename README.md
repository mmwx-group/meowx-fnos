# MeowX for 飞牛 NAS（fnOS）

在飞牛 NAS 上跑 MeowX 内核：NAS 本机与局域网透明代理、旁路由、喵窝组网，用浏览器管理。设计见 [docs/design.md](docs/design.md)。

内核是 MeowX 自己的 mihomo 分支 [meowx-core](https://github.com/mmwx-group/meowx-core)；后台做法来自
[MeowX OpenWrt 版](https://github.com/mmwx-group/meowx-openwrt)（它又基于 [OpenWrt-nikki](https://github.com/nikkinikki-org/OpenWrt-nikki)）。
本项目以 [GPL-3.0](LICENSE) 开源。

## 打包

```sh
scripts/build-fpk.sh <版本> [x86|arm]   # 产物在 dist/
```

安装（NAS 上，root）：`appcenter-cli install-fpk meowx_<版本>_x86.fpk -v 1`

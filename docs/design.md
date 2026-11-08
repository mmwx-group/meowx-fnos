# MeowX 飞牛 NAS 版设计

> 2026-11-06 定：以 MeowX OpenWrt 版（meowx-openwrt）的后台做法 + meowx-core 独立程序为基础；不上飞牛应用中心，用户手动装 fpk；
> 第一版一次做齐：NAS 本机透明代理、局域网代理端口、旁路由（给整个局域网做透明代理）、喵窝、网页管理。

## 1. 为什么这么做

- **不用 meowC Linux 版**：那是 Flutter 桌面程序，要有桌面环境；NAS 没有屏幕、用浏览器管理。
- **以 OpenWrt 版为基础**：同类设备（无屏、浏览器管理、给局域网代理、用官方 tailscale 把局域网开放进喵窝），
  内核同一份 meowx-core（独立程序模式自己起监听端口与 TUN，`meowx-core panel …` 子命令管主控账户 / 账户订阅 / po0 / GGY / AkileCDN / 喵窝）。
  要重写的是飞牛这边的外壳：LuCI → 自己的网页界面，procd / UCI / ucode → 一个 Go 控制服务，opkg → fpk。

## 2. 飞牛的约束（2026-11-07 在测试机 fnOS 1.2.1000 上实测）

- **只有官方合作企业开发者能发 root 权限应用**；手动安装的 fpk 也照社群做法用 `run-as: package`。
- **生命周期脚本（cmd/*）以应用自己的账号（meowx）运行，碰不到 Docker**（`docker load` 被拒）。
- **容器由飞牛按 `config/resource` 的 `docker-project` 起停**，时机在 `install_init` 之后、`install_callback` 之前；
  compose 里能用 `${TRIM_PKGVAR}` 等环境变量。
- **网络权限靠容器拿**：`network_mode: host` + `cap_add: NET_ADMIN, NET_RAW` + `/dev/net/tun`（社群 Tailscale fpk 同样做法）。
  实测容器里 uid 0、看得到宿主网卡、能建 TUN、能开 netfilter netlink。
- **镜像就地构建**：compose 带 `build:`（`FROM scratch` + `rootfs/` 里的静态程序，`pull_policy: build`），
  不连任何镜像仓库（飞牛配了 docker.fnnas.com 等加速，但我们用不着）。实测构建出的镜像 6.6MB。
- fpk = tar.gz：`app.tgz`（应用目录，装到 `/vol<N>/@appcenter/meowx`，即 `TRIM_APPDEST`）+ `manifest` + `cmd/` + `config/` + `ui/` + 图标 + 端口说明 `meowx.sc`。
  数据目录 `TRIM_PKGVAR` = `/vol<N>/@appdata/meowx`，挂进容器 `/data`。安装：`appcenter-cli install-fpk <fpk> -v <卷号>`（root）。
- 平台 `platform = x86 | arm`，不能写多个值：x86 与 arm 各出一个包。
- **一键更新**（0.1.1 起）：appcenter-cli 没有保留数据的升级（`install-fpk` 拒绝已装的应用，`install-local` 是先卸载再装）。
  应用中心守护程序的 HTTP 接口（`/run/com.trim.app.center.sock`，属主 root、ACL 只给 `OfficialAppUsers` 组，`/rpc/v1` 下不要会话 token）
  有正经的升级：`download/task` 暂存安装包（按宿主路径读）→ `update/info` 取所在卷 → `update/task`，保留 `@appdata`、失败回滚。
  compose 把这个 socket 挂进容器（容器里 uid 0 能连），并用 `MEOWX_HOST_DATA` 告诉控制服务数据目录在宿主上的路径。
  2026-11-08 在测试机上实测 0.1.1 → 0.1.2：约 21 秒换好重启，数据目录里的文件原样保留。接口与字段对照社群商店 conversun/fnos-store 的实测记录。
  守护程序暂存的解包留在 `/vol1/appcenter-downloads/<应用>-<版本>-tpk`（网页手动安装同样留），容器里碰不到、不清。

## 3. 组成

```
fpk
├── app.tgz → docker/{docker-compose.yaml, Dockerfile, rootfs/}
│     rootfs/usr/bin/meowx-agent     控制服务（本仓库 agent/，Go）：网页界面 + API、生成运行配置、看管内核与 tailscaled、透明代理规则
│     rootfs/usr/bin/meowx-core      内核（meowx-core 独立程序，tag 同 OpenWrt：with_gvisor,no_easytier,no_tailscale,no_zerotier）
│     rootfs/usr/bin/tailscaled      喵窝（官方 tailscale 静态程序，BSD-3）
│     rootfs/usr/bin/tailscale
├── cmd/      只记日志、回报状态（起停交给飞牛）
├── config/   privilege（package）、resource（docker-project + 端口）
└── ui/       桌面图标：点开 http://<NAS>:7899/
```

容器里只有一个进程树：`meowx-agent`（PID 1）拉起并看管 `meowx-core` 与 `tailscaled`，退出时收掉自己加的路由与防火墙规则。

## 4. 功能与对应 OpenWrt 版的来源

| 功能 | OpenWrt 版 | NAS 版 |
|---|---|---|
| 设置存储 | UCI `/etc/config/meowx` | `/data/settings.json`（agent 管，字段照 `meowx.conf` 精简） |
| 订阅（导入 / 更新 / 切换） | `meowx.init` + `profile.js` | agent：下载到 `/data/profiles/`，按间隔自动更新 |
| 主控账户 / 账户订阅 / po0 / GGY / AkileCDN | `meowx-core panel …` + `app.js` / `extras.js` | 同样调 `meowx-core panel …`（状态文件放 `/data/panel`） |
| 运行配置（混入端口、DNS、TUN、嗅探、外部控制） | `mixin.uc` / `meowx.init` | agent 生成 `/data/run/config.yaml` |
| 透明代理（NAS 本机 + 旁路由） | `hijack.ut`（nftables + 策略路由） | 见 §5 |
| 喵窝 | `mesh_cli.uc` + 官方 tailscale | agent 移植 `mesh_cli.uc`，tailscaled 在容器里用内核网卡 |
| 节点切换 / 连接 / 日志 | LuCI + zashboard | 自己的网页界面经 agent 转发内核的 REST API |

## 5. 透明代理（最大的风险点）

测试机是用户在用的 NAS（十几个容器、SMB、Gitea、Nginx Proxy Manager 等，SSH 在 222 / 2233，外网经路由器端口转发进来）。

- **方案 A（先试）**：内核自己的 TUN `auto-route` + `auto-redirect`（sing-tun，全走 netlink，不需要 nft 程序），
  `route-exclude-address` 排除局域网、Docker 网桥（172.17–172.31）、组网段；旁路由 = 局域网设备把网关指向 NAS，转发流量同样进 TUN。
  飞牛自带的策略路由 `from <NAS 地址> lookup route_<网卡>`（优先级 10）让「NAS 回外网访问者的包」走原路，不会被 TUN 吸走——要实测。
- **方案 B（A 不行时）**：照 `hijack.ut` 用 nftables（TPROXY / redirect + 策略路由 + fwmark 放行内核自己），
  用 Go 的 netlink 库（google/nftables，Apache-2.0）下发；内核的出站打 `routing-mark` 放行（OpenWrt 版用 cgroup，容器里不好用）。
- **安全阀**：开透明代理时 agent 先记好要撤的东西，规则生效后 N 分钟内没在网页上点「保留」就自动撤掉（防止把 SSH / 网页都断了）；
  agent 退出（容器停、卸载）时一律撤掉。

## 6. 喵窝

- 照 OpenWrt 版：主控说可用 → 申报要开放的网段（`panel mesh sites --router <标识>`）→ 没登录就要一次性 key（`panel mesh enroll`）→ `tailscale up --login-server … --advertise-routes …`。
- `meowx-core panel mesh …` 目前只在 meowx-openwrt 的补丁 003 里，NAS 版编内核时同样打上（以后并进 meowx-core）。
- tailscaled 在容器里用内核网卡（`tailscale0`），状态目录 `/data/tailscale`；局域网转发、映射段的 DNAT、masquerade 同 OpenWrt 版。
- 网关如果是已在喵窝开放同一网段的 OpenWrt 路由器：NAS 再申报同一段会被主控判冲突、改成映射段——测试时先只开放 NAS 本机。

## 7. 网页界面

- 嵌在 agent 里（`go:embed`），端口 7899，飞牛桌面图标新标签页打开。
- 页面：概览（运行状态、出口、流量、开关）/ 订阅与主控账户 / 节点（代理组切换、测速）/ 透明代理与旁路由 / 喵窝 / 连接与日志 / 设置。
- 观感同 MeowX 品牌（奶白底、白卡、玫红 / 樱粉强调，跟随系统深浅色）。不引网页框架的话就用 Preact + htm（MIT，约 10KB，随包带，不连 CDN）。
- 网页没有登录：飞牛自己的会话管不到我们的端口。第一版只听局域网，设置里可设访问密码（OpenWrt 版的 `authentication` 同理）。

## 8. 分期

1. ✅ 骨架：fpk + 就地构建镜像 + 网页探测（2026-11-07 测试机通过）。
2. 内核与订阅：设置、订阅、主控账户、运行配置、看管内核、代理端口；网页基本页。
3. 透明代理：NAS 本机 → 旁路由，带安全阀。
4. 喵窝。
5. 界面打磨、arm 包、CI 出包、发到 dl.miaomiaowux.com。

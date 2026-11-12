# MeowX 通用 Docker 版（群晖 / 威联通 / 绿联等 NAS）

> 第一阶段（2026-11-10）：代码与镜像能在本地构建、在飞牛测试机上用 Docker 实测通过；**还没有发布**，镜像要自己构建（见文末）。
> 镜像发布到哪个仓库、叫什么名字还没定，下面的镜像名 `meowx-docker` 是本地构建出来的名字。

和飞牛版是同一套程序（控制服务 `meowx-agent` + 内核 `meowx-core` + 官方 `tailscaled`），功能一样：订阅、节点、代理端口、
网络测速、透明代理（NAS 本机 / 其它容器 / 旁路由）、喵窝、po0 / GGY / AkileCDN 加白。区别：

| | 飞牛版（fpk） | Docker 版 |
|---|---|---|
| 安装 | 飞牛应用中心「手动安装」 | 导入镜像 + 用 compose 建容器 |
| 镜像 | NAS 上就地构建，`FROM scratch`，只有静态程序 | Alpine 的几个包 + 同样的程序（要带 iptables 程序，见下） |
| 升级 | 网页里一键更新 | 网页只检查更新，换镜像要自己做 |
| 防火墙 | nftables | 自动选：有 nf_tables 用 nftables，没有（群晖）用 iptables |

## 要求

- x86_64 或 arm64 的 NAS，装了 Docker（群晖 Container Manager、威联通 Container Station、绿联 Docker）。
- 容器要宿主网络、`NET_ADMIN` / `NET_RAW` 权限、`/dev/net/tun`（同飞牛版、同官方 Tailscale 容器的要求）。
- 透明代理与喵窝改的是宿主的防火墙与路由：容器停掉（或关掉功能）时自己加的规则全部撤掉。

## 端口

容器用宿主网络，下面的端口直接占在 NAS 上，别和 NAS 上别的服务撞：

| 端口 | 用途 |
|---|---|
| 7899/tcp | 网页管理（`MEOWX_UI_PORT` 可改） |
| 7890/tcp | HTTP / SOCKS5 混合代理端口（网页「混入设置」里可改） |
| 7891/tcp | redir 端口（透明代理，iptables 后端用） |
| 7892/tcp+udp | tproxy 端口（透明代理，nftables 后端的旁路由用） |
| 1053/tcp+udp | 内核的 DNS（透明代理劫持 DNS 时用） |
| 127.0.0.1:9090 | 内核的控制接口（只听本机） |
| 41641/udp | 喵窝（tailscaled） |

## 数据目录

容器里的 `/data`，compose 示例挂的是 compose 文件旁边的 `./data`。设置、订阅、内核的 geo 数据与缓存、主控账户、
喵窝的设备身份（`/data/mesh`）都在这里。升级换镜像时保留它；删掉它等于重装（喵窝里会变成一台新设备）。

## 安装

### 1. 拿到镜像

现在只能自己构建（见「从源码构建」）：得到 `meowx-docker-<版本>-amd64.tar`（x86）或 `…-arm64.tar`（ARM）。
国内的 NAS 拉 Docker Hub / GHCR 经常不通，所以先做成离线镜像文件，在 NAS 的 Docker 界面里「导入」。

### 2. 群晖（DSM 7）

1. **加载 TUN 模块**（群晖默认没有 `/dev/net/tun`，没有的话容器起不来）。用 SSH 登录 NAS：
   ```sh
   sudo insmod /lib/modules/tun.ko
   ls -l /dev/net/tun          # 有这个文件就行
   ```
   **开机自动加载**：控制面板 → 任务计划 → 新增 → 触发的任务 → 用户定义的脚本；用户选 `root`、事件选「开机」，
   「任务设置」里的脚本填：
   ```sh
   [ -e /dev/net/tun ] || insmod /lib/modules/tun.ko
   ```
2. Container Manager → 映像 → 新增 → 从文件添加 → 选 `meowx-docker-<版本>-<架构>.tar`。
3. File Station 里建一个目录，如 `/volume1/docker/meowx`，把 `docker/docker-compose.yml` 放进去，
   把 `MEOWX_PLATFORM=docker` 改成 `MEOWX_PLATFORM=synology`（只影响显示名与报给主控的系统名）。
4. Container Manager → 项目 → 新增：路径选上面那个目录、来源选「使用现有的 docker-compose.yml」，建好就会启动。
5. 浏览器打开 `http://<NAS 的地址>:7899`。

群晖的内核（DSM 7，4.4 内核）**没有 nf_tables、没有 TPROXY、没有 NETMAP**，MeowX 自动改用 iptables（legacy）后端，见「防火墙后端」。

### 3. 威联通（QTS / QuTS hero）

1. 确认有 TUN：SSH 里 `ls -l /dev/net/tun`；没有的话 `sudo modprobe tun`（或 `insmod` 系统里的 `tun.ko`），
   开机自动加载要靠 `autorun.sh`（控制台 → 硬件 → 常规设置里先允许运行用户自定义的进程）。
2. Container Station → 映像 → 导入 → 选离线镜像文件。
3. Container Station → 应用程序 → 创建 → 粘贴 `docker/docker-compose.yml` 的内容（`./data` 改成绝对路径，如
   `/share/Container/meowx/data`；`MEOWX_PLATFORM` 写 `qnap`）。
4. 浏览器打开 `http://<NAS 的地址>:7899`。

### 4. 绿联（UGOS Pro）

1. UGOS Pro 基于较新的 Debian 内核，一般自带 TUN 与 nf_tables（MeowX 会用 nftables 后端，同飞牛版）。
   可以在 SSH 里 `ls -l /dev/net/tun` 确认。
2. Docker 应用 → 镜像 → 本地镜像 → 导入。
3. Docker 应用 → 项目 → 创建：粘贴 compose 内容，数据目录改成共享文件夹里的路径，`MEOWX_PLATFORM` 写 `ugreen`。
4. 浏览器打开 `http://<NAS 的地址>:7899`。

### 其它装了 Docker 的 Linux

```sh
docker load -i meowx-docker-<版本>-amd64.tar
mkdir -p ~/meowx && cp docker/docker-compose.yml ~/meowx/ && cd ~/meowx
docker compose up -d
```

## 环境变量

| 变量 | 缺省 | 说明 |
|---|---|---|
| `MEOWX_PLATFORM` | 镜像里是 `docker` | `docker` / `synology` / `qnap` / `ugreen`：网页上的版本名、喵窝入网时报给主控的系统名、订阅 UA 里的系统名。`fnos` = 飞牛版行为 |
| `MEOWX_UI_PORT` | 7899 | 网页端口 |
| `TZ` | Asia/Shanghai | 时区 |
| `MEOWX_FIREWALL` | auto | `nftables` / `iptables`：强制防火墙后端 |
| `MEOWX_IPTABLES` | auto | iptables 后端用 `legacy` 还是 `nft`：auto 时跟着宿主上已有规则多的那套（Docker 的 DOCKER-USER 在哪套就用哪套） |
| `MEOWX_POLICY_ROUTING` | auto | `off` = 当作没有策略路由：透明代理不用 TUN，全部退化成 REDIRECT（见下） |

## 防火墙后端

启动时探测一次（网页「透明代理」页与日志里有一行「防火墙：…」）：

- **nftables**（内核有 nf_tables、有策略路由，如绿联、飞牛）：与飞牛版完全一样——NAS 本机 / 容器走内核的 TUN，
  旁路由走 TPROXY，规则在 `inet meowx`、`inet meowx_host`、`inet meowx_mesh` 三张表里。
- **iptables**（群晖这类没有 nf_tables 的内核）：调镜像里的 `iptables-legacy`（或 `iptables-nft`），规则都在自己的链
  `MEOWX_*` 里，内置链里只有跳转，关掉或容器退出时按 `iptables-save` 找出来全部删掉：
  - NAS 本机与其它容器：照样走内核的 TUN（auto-route 靠策略路由，不碰防火墙）；本机 DNS 重定向到内核的 DNS 放在 nat 表的 `MEOWX_HOST`。
  - 旁路由（没有 TPROXY）：nat 表 `MEOWX_LAN` 把局域网设备的 DNS 重定向给内核的 DNS、其余 TCP 用 REDIRECT 交给 redir 端口 7891；
    UDP 在 mangle 表 `MEOWX_LAN` 打标记 0x81，策略路由（`fwmark 0x81/0xff lookup 81`，优先级 1025，表 81 = `default dev meowx`）
    把它们送进内核的 TUN。Docker 把 FORWARD 默认设成丢弃，局域网 ↔ TUN 的转发要放行：filter 表 `MEOWX_LAN_FWD` 挂在
    `DOCKER-USER` 最前面（没有 Docker 时挂 FORWARD）。内核崩溃重启后 TUN 网卡是新建的，表 81 的路由每分钟检查一次、没了就补上。
  - 喵窝：mangle FORWARD 给从 `tailscale0` 进来的包打标记（`0x20260000/0xffff0000`），nat POSTROUTING 按标记做 MASQUERADE；
    映射网段没有 NETMAP，**逐个地址 DNAT，一个映射段最多 /24（256 个地址）**，再大就在日志里报错、跳过那一段；
    不让喵窝里的设备访问 NAS 本身时，INPUT 最前面挂一条链（已建立的放回，其余从 `tailscale0` 进来的丢弃）；
    `DOCKER-USER` 里放行 `tailscale0` 进出。
- **没有策略路由**（`ip rule` 用不了，或 `MEOWX_POLICY_ROUTING=off`）：内核的 TUN 起不来，透明代理退化成 REDIRECT——
  NAS 本机（nat OUTPUT）、其它容器（按 Docker 网桥，nat PREROUTING）、旁路由的 TCP 都交给 redir 端口，DNS 照样劫持，
  **UDP 不代理**（QUIC 会回落到 TCP；游戏等纯 UDP 应用直连或不通）。

为什么镜像不再是 `FROM scratch`：iptables 后端要调 `iptables-legacy-restore` 等程序，它们是动态链接的（musl + libxtables 等），
自己静态编一份不值得；Alpine 的这几个包装进镜像约 9MB。飞牛版的 fpk 不需要 iptables，仍用 `FROM scratch` 就地构建，两边不共用镜像。

## 升级

网页「设置」里会检查新版本（发布清单 `latest.json` 的 `docker` 一段，见下），但**不能一键更新**：容器换不了自己的镜像。
有新版时：

- 离线镜像：下载新的 tar，在 Docker 界面里导入，然后在「项目」里重新构建 / 重启（compose 用的是 `meowx-docker:latest`，导入新版后指向新镜像）。
- 以后发布到镜像仓库时：`docker compose pull && docker compose up -d`。

数据目录不动，设置、订阅、喵窝的设备身份都保留。

发布清单的 docker 一段（约定，发布时写）：

```json
"docker": {
  "version": "0.2.0",
  "releasedAt": "…",
  "image": "<仓库>/meowx:0.2.0",
  "files": [
    {"kind": "image-tar", "arch": "x86_64", "name": "meowx-docker-0.2.0-amd64.tar", "url": "…", "size": 0, "sha256": "…"},
    {"kind": "image-tar", "arch": "arm64",  "name": "meowx-docker-0.2.0-arm64.tar", "url": "…", "size": 0, "sha256": "…"}
  ]
}
```

## 已知限制

- **IPv6 不代理**（同飞牛版第一版）：透明代理开着时内核的 DNS 不回 AAAA。
- iptables 后端 + 没有策略路由：UDP 不代理（见上）。
- 旁路由的 UDP 进 TUN 后，回包从 TUN 进来、源地址是外网：宿主的反向路径检查（`rp_filter`）是严格模式（1）时会被丢。
  MeowX 会试着把 TUN 网卡改成宽松（2），但 Docker 里 `/proc/sys` 多半只读、写不进；`net.ipv4.conf.all.rp_filter` 是 1 时
  请在宿主上 `sysctl -w net.ipv4.conf.all.rp_filter=2`（群晖可写进开机任务）。
- iptables（legacy）是「整张表读出来改完再写回」：和 Docker 自己改规则同时发生时可能互相覆盖。容器里的 iptables
  用的是容器自己的 `/run/xtables.lock`，和宿主的锁不是一把；只在开关透明代理 / 喵窝的那一下有窗口，影响很小。
  要彻底避免：宿主上确认有 `/run/xtables.lock` 这个**文件**后，compose 里加一行 `- /run/xtables.lock:/run/xtables.lock`
  （文件不存在时 Docker 会在宿主上建一个同名目录，反而让宿主的 iptables 报错，所以示例里没加）。
- iptables-nft 撤规则后会留下空的 `ip mangle` 等表 / legacy 会留下已加载的 `iptable_nat` 等模块：里面没有规则，不影响网络。
- 群晖 4.4 内核比较老：内核 TUN 的 auto-route 里有几条带端口的 `ip rule`（DNS 相关）老内核可能不认。
  本机透明代理不正常时先试 `MEOWX_POLICY_ROUTING=off`。
- 喵窝的映射网段最多 /24（iptables 后端）。

## 群晖上要先跑一遍的检查

装之前在群晖的 SSH 里跑一下，把结果发回来，可以确认走哪条路：

```sh
uname -a
ls -l /dev/net/tun; lsmod | grep -w tun
ip rule show                      # 能列出来 = 有策略路由
sudo ip rule add fwmark 0x99 lookup 99 pref 30000 && sudo ip rule del pref 30000 && echo 策略路由能加
sudo ip route add default dev lo table 99 && sudo ip route del default dev lo table 99 && echo 自定义路由表能用
cat /proc/sys/net/ipv4/conf/all/rp_filter /proc/sys/net/ipv4/ip_forward
sudo iptables -V; sudo iptables -S FORWARD | head; sudo iptables -S DOCKER-USER
ls /lib/modules | grep -E 'xt_mark|xt_iprange|xt_addrtype|xt_REDIRECT|xt_conntrack|ipt_MASQUERADE|iptable_mangle|iptable_nat|nf_tables|xt_TPROXY|xt_NETMAP'
ls -l /run/xtables.lock
docker version --format '{{.Server.Version}}'
```

## 从源码构建

```sh
scripts/build-docker.sh <版本>                      # 两个架构的离线镜像：dist/meowx-docker-<版本>-{amd64,arm64}.tar
scripts/build-docker.sh <版本> --arch amd64 --load  # 只出 x86、装进本机 docker
scripts/build-docker.sh <版本> --context-only       # 只备好构建目录 dist/docker-context
```

要 Go 1.27、git、curl、`docker buildx`。内核同飞牛版按 `scripts/build-core.sh` 编（`CORE_BIN_amd64` / `CORE_BIN_arm64` 可指向编好的）。
Alpine 的包在构建机自己的架构上装进目标架构的目录，出 arm64 镜像不需要 qemu。
buildx 用 Docker 缺省的 docker 驱动时导不出 tar，脚本会先 `--load` 再 `docker save`（本机会留下 `meowx-docker:<版本>` / `latest` 两个标签）。

GitHub Actions 的草稿在 `.github/workflow-drafts/docker.yml`（没放进 `workflows/`，不会跑）：只打离线镜像传到 Actions 产物，推镜像仓库的步骤注释掉了。

## 实测记录

2026-11-10 在飞牛测试机（fnOS，内核 6.18，x86_64，Docker 用 iptables-nft）上：停掉飞牛版，用 `MEOWX_PLATFORM=synology` 起 Docker 版
（另起容器、同样的端口），订阅 / 节点测速 / 混合端口都正常；强制 iptables 后端分别用 nft 与 legacy 两套、以及 `MEOWX_POLICY_ROUTING=off`
打开 NAS 本机（+ 其它容器）透明代理，出口与 fake-ip 都对；用一个临时的网络命名空间 + veth 假装局域网设备测旁路由：TCP（REDIRECT）、
DNS 劫持、UDP 经 TUN（9.9.9.9:9953 走了节点）都通，内核被杀掉重启后一分钟内补回了表 81 的路由；喵窝的 iptables 规则（legacy / nft）
真机下发再撤（`MEOWX_LIVE_IPTABLES`）。每次关掉 / 容器停止后 iptables、ip rule、路由表都没有残留。
自动探测在这台机器上选 nftables，旁路由走 TPROXY，同飞牛版。arm64 镜像在 x86 机器上构建成功（不用 qemu）。
没在真的群晖 / 威联通 / 绿联上跑过。

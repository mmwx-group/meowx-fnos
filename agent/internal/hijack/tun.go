// Package hijack：透明代理（NAS 本机 / 其它容器 / 旁路由）。
//
// 用内核自己的 TUN：auto-route（策略路由把流量送进 TUN）+ 可选 auto-redirect（TCP 走 nftables 重定向，比进 TUN 省）。
// 全走 netlink，不需要 nft / ip 程序（镜像里只有静态程序）。设计见 docs/design.md §5。
//
// 三类流量按「从哪个网卡进来」分：NAS 本机发起的（iif lo）、Docker 网桥进来的（其它容器）、物理网卡进来的（局域网设备把网关指向 NAS = 旁路由）。
// 外面连进 NAS 的服务时，NAS 回的包源地址是 NAS 自己的地址：飞牛给每个地址都配了「from <地址> lookup <网卡表>」（优先级 10，
// 在内核的规则 9000 之前），所以这些回包照原路走、不会被吸进 TUN。Docker 网桥默认整个排除：容器回外面访问者的包源地址是
// 容器地址，进了 TUN 会把发布出去的端口（Nginx Proxy Manager、Gitea…）弄断。
package hijack

import (
	"fmt"
	"net"
	"strings"

	"github.com/mmwx-group/meowx-fnos/agent/internal/store"
)

// Host：宿主机的网卡（容器是宿主网络，看到的就是宿主的）
type Host struct {
	Physical []Iface // 物理网卡（有 IPv4 的），局域网入口
	Bridges  []Iface // Docker 网桥（docker0、br-*）
}

type Iface struct {
	Name    string   `json:"name"`
	Subnets []string `json:"subnets"`
	Up      bool     `json:"up"`
}

// 不是局域网入口的虚拟网卡
var virtualPrefixes = []string{"lo", "docker", "br-", "veth", "tun", "tap", "tailscale", "meowx", "virbr", "vnet", "wg", "dummy", "ovs-", "kube", "cni", "flannel"}

func Discover() Host {
	var h Host
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		up := i.Flags&net.FlagUp != 0
		var subnets []string
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
				_, n, _ := net.ParseCIDR(ipn.String())
				subnets = append(subnets, n.String())
			}
		}
		iface := Iface{Name: i.Name, Subnets: subnets, Up: up}
		switch {
		case i.Name == "docker0" || strings.HasPrefix(i.Name, "br-"):
			h.Bridges = append(h.Bridges, iface)
		case isVirtual(i.Name):
		case len(subnets) > 0:
			h.Physical = append(h.Physical, iface)
		}
	}
	return h
}

func isVirtual(name string) bool {
	for _, p := range virtualPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// TUN 生成内核的 tun 配置。nil = 透明代理关着。
func TUN(s store.Settings, h Host) map[string]any {
	p := s.Proxy
	// 局域网设备（旁路由）走 TPROXY（见 lan.go），TUN 只管 NAS 本机与其它容器
	if !p.Enabled || (!p.HostProxy && !p.ContainerProxy) {
		return nil
	}
	t := map[string]any{
		"enable":                true,
		"device":                orDefault(s.Mixin.TUN.Device, "meowx"),
		"stack":                 orDefault(s.Mixin.TUN.Stack, "mixed"),
		"auto-route":            true,
		"auto-detect-interface": true,
		"auto-redirect":         p.TCPMode == "redirect",
		"strict-route":          false,
		"iproute2-table-index":  TUNTable,
	}
	if s.Mixin.TUN.MTU > 0 {
		t["mtu"] = s.Mixin.TUN.MTU
	}
	if p.DNSHijack {
		hijack := s.Mixin.TUN.DNSHijack
		if len(hijack) == 0 {
			hijack = []string{"tcp://any:53", "udp://any:53"}
		}
		t["dns-hijack"] = toAny(hijack)
	}

	// 不进 TUN 的目的地：保留地址（局域网、Docker、喵窝的 100.64/10…）；不代理 IPv6 时整个 v6 都不进
	exclude := append([]string{}, p.ReservedIP...)
	if p.IPv6 {
		exclude = append(exclude, p.ReservedIP6...)
	} else {
		exclude = append(exclude, "::/0")
	}
	// 本机所在网段一律直达（自定义了保留地址也不会把自己局域网吸进去）
	for _, i := range h.Physical {
		exclude = append(exclude, i.Subnets...)
	}
	t["route-exclude-address"] = toAny(dedupe(exclude))

	// 按入口网卡分：本机发起的（iif lo）、Docker 网桥进来的（其它容器）；物理网卡进来的一律不进 TUN（旁路由另走 TPROXY）
	var bridges []string
	for _, b := range h.Bridges {
		bridges = append(bridges, b.Name)
	}
	if !p.HostProxy {
		// 只收容器的：本机发起的不在 include 里就不进
		t["include-interface"] = toAny(dedupe(bridges))
	} else {
		// 物理网卡进来的一律不进 TUN（不论选没选作旁路由入口）
		excl := LANInterfaces(p.LANInterfaces, h)
		for _, i := range h.Physical {
			excl = append(excl, i.Name)
		}
		if !p.ContainerProxy {
			excl = append(excl, bridges...)
		}
		t["exclude-interface"] = toAny(dedupe(excl))
	}
	return t
}

// LANInterfaces：设置里选的局域网入口；没选 = 全部物理网卡
func LANInterfaces(chosen []string, h Host) []string {
	if len(chosen) > 0 {
		return chosen
	}
	var out []string
	for _, i := range h.Physical {
		out = append(out, i.Name)
	}
	return out
}

// HostRules：NAS 本机 / 容器透明代理开着时，另外要下发的 DNS 重定向与 fake-ip 策略路由；nil = 不需要
func HostRules(s store.Settings) *HostPlan {
	p := s.Proxy
	if TUN(s, Host{}) == nil {
		return nil
	}
	plan := &HostPlan{CoreMark: CoreMark, TUNTable: TUNTable}
	if p.HostProxy && p.DNSHijack && s.Mixin.DNS.Enabled {
		plan.DNSPort = listenPort(s.Mixin.DNS.Listen)
	}
	if s.Mixin.DNS.Mode == "fake-ip" {
		plan.FakeIPRange = s.Mixin.DNS.FakeIPRange
		if _, n, err := net.ParseCIDR(plan.FakeIPRange); err == nil {
			plan.FakeIPRange = n.String()
		}
	}
	return plan
}

// LAN 生成旁路由的规则计划；nil = 不开旁路由
func LAN(s store.Settings, h Host) *LANPlan {
	p := s.Proxy
	if !p.Enabled || !p.LANProxy || s.Mixin.TProxyPort == 0 {
		return nil
	}
	reserved := append([]string{}, p.ReservedIP...)
	for _, i := range h.Physical {
		reserved = append(reserved, i.Subnets...)
	}
	plan := &LANPlan{Interfaces: LANInterfaces(p.LANInterfaces, h), Reserved: reserved, TProxyPort: uint16(s.Mixin.TProxyPort)}
	if p.DNSHijack && s.Mixin.DNS.Enabled {
		plan.DNSPort = listenPort(s.Mixin.DNS.Listen)
	}
	return plan
}

// listenPort：「[::]:1053」「0.0.0.0:53」→ 端口
func listenPort(listen string) uint16 {
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return 0
	}
	var n int
	_, _ = fmt.Sscanf(port, "%d", &n)
	return uint16(n)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func toAny(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

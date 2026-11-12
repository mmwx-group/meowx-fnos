package hijack

import (
	"encoding/binary"
	"fmt"
	"net"
)

// 内核的出站连接（含它自己的 DNS 查询）打的 routing-mark：NAS 本机 DNS 重定向靠它放行内核自己
const CoreMark = 0x2026

// TUN 的策略路由表（显式写进内核配置，fake-ip 那条规则要指向它）
const TUNTable = 2022

// HostPlan：NAS 本机透明代理之外要下发的（host_linux.go）
type HostPlan struct {
	DNSPort     uint16 // 0 = 不劫持本机 DNS
	CoreMark    uint32
	FakeIPRange string // 发往这里的一律进 TUN
	TUNTable    int

	// 没有策略路由时（只有 iptables 后端）：内核的 TUN 起不来，退化成 REDIRECT——TCP 交给内核的 redir 端口，UDP 不代理
	RedirPort     uint16   // 0 = 不退化
	RedirectHost  bool     // NAS 本机发起的 TCP（nat OUTPUT）
	Bridges       []string // 其它容器：从这些 Docker 网桥进来的 TCP / DNS（nat PREROUTING）
	BridgeDNSPort uint16   // 容器的 DNS 重定向到这里；0 = 不劫持
	Reserved      []string // 不代理的目的地址
}

// LANPlan：旁路由要下发的规则（lan.go，只在 Linux 上真的下发）
type LANPlan struct {
	Interfaces []string // 局域网入口网卡
	Reserved   []string // 不代理的目的地址（IPv4 网段）
	TProxyPort uint16
	DNSPort    uint16 // 内核 DNS 的监听端口；0 = 不劫持 DNS

	// iptables 后端（没有 TPROXY）：TCP 用 REDIRECT 交给内核的 redir 端口；UDP 打标记经策略路由送进内核的 TUN（没有策略路由时 UDP 不代理）
	RedirPort uint16
	TUNDevice string // 空 = UDP 不代理
}

// mergeV4：只取 IPv4 网段，排序合并重叠的（nftables 区间集合不许重叠）
func mergeV4(cidrs []string) [][2]uint32 {
	var spans [][2]uint32
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil || n.IP.To4() == nil {
			continue
		}
		start := binary.BigEndian.Uint32(n.IP.To4())
		ones, _ := n.Mask.Size()
		end := start | (0xffffffff >> ones)
		if ones == 0 {
			end = 0xffffffff
		}
		spans = append(spans, [2]uint32{start, end})
	}
	for i := 1; i < len(spans); i++ {
		for j := i; j > 0 && spans[j][0] < spans[j-1][0]; j-- {
			spans[j], spans[j-1] = spans[j-1], spans[j]
		}
	}
	var merged [][2]uint32
	for _, s := range spans {
		if n := len(merged); n > 0 && s[0] <= merged[n-1][1]+1 && merged[n-1][1] != 0xffffffff {
			if s[1] > merged[n-1][1] {
				merged[n-1][1] = s[1]
			}
			continue
		}
		merged = append(merged, s)
	}
	return merged
}

// v4Ranges：合并后的 IPv4 网段写成 iptables iprange 的「起-止」
func v4Ranges(cidrs []string) []string {
	var out []string
	for _, s := range mergeV4(cidrs) {
		out = append(out, fmt.Sprintf("%s-%s", u32ip(s[0]), u32ip(s[1])))
	}
	return out
}

func u32ip(v uint32) string {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return net.IP(b).String()
}

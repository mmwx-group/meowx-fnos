package hijack

import (
	"fmt"

	"github.com/mmwx-group/meowx-fnos/agent/internal/fw"
)

// iptables 后端（群晖这类没有 nf_tables / TPROXY 的内核，见 internal/fw）：规则都在自己的链里，内置链里只有跳转。
//
//	本机（HostPlan）：nat 表 MEOWX_HOST（挂在 OUTPUT）——本机 DNS 重定向到内核的 DNS（内核自己的查询带 routing-mark，放行）；
//	    没有策略路由时再把本机发起的 TCP 重定向到内核的 redir 端口；MEOWX_HOST_PRE（挂在 PREROUTING，按 Docker 网桥）同样处理其它容器。
//	旁路由（LANPlan）：nat 表 MEOWX_LAN（按局域网入口网卡挂在 PREROUTING）——DNS 重定向，其余 TCP 重定向到 redir 端口；
//	    有 TUN 时 mangle 表 MEOWX_LAN 给 UDP 打标记 0x81，策略路由（表 81，优先级 1025）把它们送进 TUN；
//	    Docker 把 FORWARD 设成丢弃，filter 表 MEOWX_LAN_FWD（挂在 DOCKER-USER，没有就挂 FORWARD）放行「局域网 ↔ TUN」的转发。
// 撤的时候按前缀（MEOWX_HOST / MEOWX_LAN）从 iptables-save 里找出来删掉（fw.IPT.Cleanup）。

const (
	hostChain    = "MEOWX_HOST"
	hostPreChain = "MEOWX_HOST_PRE"
	lanChain     = "MEOWX_LAN"
	lanFwdChain  = "MEOWX_LAN_FWD"

	// 旁路由的 UDP 进 TUN：标记、路由表、优先级（与 nftables 后端的 0x80 / 80 / 1024 分开，两套撤的时候互不影响）
	lanUDPMark    = 0x81
	lanUDPRoute   = 81
	lanUDPRulePri = 1025
)

// hostScript：本机 / 容器的规则；空脚本 = 不用下发
func hostScript(p HostPlan) *fw.Script {
	s := &fw.Script{}
	if p.DNSPort != 0 || p.RedirectHost {
		s.Chain("nat", hostChain)
		add := func(rule string) { s.Add("nat", "-A "+hostChain+" "+rule) }
		add(fmt.Sprintf("-m mark --mark 0x%x -j RETURN", p.CoreMark)) // 内核自己的连接（routing-mark）放行
		add("-o lo -j RETURN")
		if p.DNSPort != 0 {
			add(fmt.Sprintf("-p udp --dport 53 -j REDIRECT --to-ports %d", p.DNSPort))
			add(fmt.Sprintf("-p tcp --dport 53 -j REDIRECT --to-ports %d", p.DNSPort))
		}
		if p.RedirectHost && p.RedirPort != 0 {
			for _, r := range redirectBody(p.Reserved, p.RedirPort) {
				add(r)
			}
		}
		s.Add("nat", "-A OUTPUT -j "+hostChain)
	}
	if len(p.Bridges) > 0 && p.RedirPort != 0 {
		s.Chain("nat", hostPreChain)
		add := func(rule string) { s.Add("nat", "-A "+hostPreChain+" "+rule) }
		if p.BridgeDNSPort != 0 {
			add(fmt.Sprintf("-p udp --dport 53 -j REDIRECT --to-ports %d", p.BridgeDNSPort))
			add(fmt.Sprintf("-p tcp --dport 53 -j REDIRECT --to-ports %d", p.BridgeDNSPort))
		}
		for _, r := range redirectBody(p.Reserved, p.RedirPort) {
			add(r)
		}
		for _, b := range p.Bridges {
			s.Add("nat", fmt.Sprintf("-A PREROUTING -i %s -j %s", b, hostPreChain))
		}
	}
	return s
}

// redirectBody：到本机的、保留地址不管，其余 TCP 交给 redir 端口
func redirectBody(reserved []string, port uint16) []string {
	out := []string{"-m addrtype --dst-type LOCAL -j RETURN"}
	for _, r := range v4Ranges(reserved) {
		out = append(out, "-m iprange --dst-range "+r+" -j RETURN")
	}
	return append(out, fmt.Sprintf("-p tcp -j REDIRECT --to-ports %d", port))
}

// lanScript：旁路由的规则。dockerUser = filter 表里有 DOCKER-USER（放行规则挂在那里，没有就挂 FORWARD 最前面）
func lanScript(p LANPlan, dockerUser bool) *fw.Script {
	s := &fw.Script{}
	s.Chain("nat", lanChain)
	nat := func(rule string) { s.Add("nat", "-A "+lanChain+" "+rule) }
	if p.DNSPort != 0 {
		// DNS 不论发给谁（包括 NAS 自己）都交给内核的 DNS
		nat(fmt.Sprintf("-p udp --dport 53 -j REDIRECT --to-ports %d", p.DNSPort))
		nat(fmt.Sprintf("-p tcp --dport 53 -j REDIRECT --to-ports %d", p.DNSPort))
	}
	for _, r := range redirectBody(p.Reserved, p.RedirPort) {
		nat(r)
	}
	for _, i := range p.Interfaces {
		s.Add("nat", fmt.Sprintf("-A PREROUTING -i %s -j %s", i, lanChain))
	}
	if p.TUNDevice == "" {
		return s
	}
	s.Chain("mangle", lanChain)
	mangle := func(rule string) { s.Add("mangle", "-A "+lanChain+" "+rule) }
	mangle("-m addrtype --dst-type LOCAL -j RETURN")
	for _, r := range v4Ranges(p.Reserved) {
		mangle("-m iprange --dst-range " + r + " -j RETURN")
	}
	if p.DNSPort != 0 {
		mangle("-p udp --dport 53 -j RETURN") // 已经重定向给内核的 DNS
	}
	mangle(fmt.Sprintf("-p udp -j MARK --set-xmark 0x%x/0xff", lanUDPMark))
	for _, i := range p.Interfaces {
		s.Add("mangle", fmt.Sprintf("-A PREROUTING -i %s -j %s", i, lanChain))
	}
	s.Chain("filter", lanFwdChain)
	for _, i := range p.Interfaces {
		s.Add("filter", fmt.Sprintf("-A %s -i %s -o %s -j ACCEPT", lanFwdChain, i, p.TUNDevice))
		s.Add("filter", fmt.Sprintf("-A %s -i %s -o %s -j ACCEPT", lanFwdChain, p.TUNDevice, i))
	}
	if dockerUser {
		s.Add("filter", "-I DOCKER-USER 1 -j "+lanFwdChain)
	} else {
		s.Add("filter", "-I FORWARD 1 -j "+lanFwdChain)
	}
	return s
}

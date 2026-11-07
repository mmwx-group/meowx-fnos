//go:build linux

package hijack

import (
	"encoding/binary"
	"fmt"
	"net"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// 旁路由（局域网设备把网关指向 NAS）：照 OpenWrt 版 hijack.ut 的局域网部分，用 TPROXY 把这些设备的 TCP / UDP 交给内核的
// tproxy 端口、DNS 重定向到内核的 DNS——包走本机收包（INPUT）路径。不走 TUN 转发：Docker 把 IPv4 的 FORWARD 默认设成丢弃，
// 局域网进来、往 TUN 出去的转发包会被它挡掉，而我们不去改 Docker 自己的防火墙表。
//
// 全部在自己的 nftables 表 inet meowx 里 + 一条 fwmark 策略路由（表 80，优先级 1024），关掉时整个删掉。

const (
	lanTable   = "meowx"
	lanMark    = 0x80
	lanMask    = 0xff
	lanRoute   = 80
	lanRulePri = 1024
)

// ApplyLAN 下发旁路由规则（先清掉旧的）
func ApplyLAN(p LANPlan) error {
	CleanupLAN()
	if len(p.Interfaces) == 0 || p.TProxyPort == 0 {
		return fmt.Errorf("旁路由要至少一个局域网入口网卡和 tproxy 端口")
	}
	c, err := nftables.New()
	if err != nil {
		return err
	}
	t := c.AddTable(&nftables.Table{Family: nftables.TableFamilyINet, Name: lanTable})

	reserved := &nftables.Set{Table: t, Name: "reserved4", KeyType: nftables.TypeIPAddr, Interval: true}
	elems, err := intervalElements(p.Reserved)
	if err != nil {
		return err
	}
	if err := c.AddSet(reserved, elems); err != nil {
		return err
	}

	// 代理链：到本机的、保留地址、DNS（交给下面的 nat 链）都不管；其余 TCP / UDP 交 tproxy 并打标记
	proxy := c.AddChain(&nftables.Chain{Name: "lan_proxy", Table: t})
	ret := func(exprs ...expr.Any) {
		c.AddRule(&nftables.Rule{Table: t, Chain: proxy, Exprs: append(exprs, &expr.Verdict{Kind: expr.VerdictReturn})})
	}
	ret(nfproto(unix.NFPROTO_IPV6)...) // IPv6 不代理（第一版）
	ret(&expr.Fib{Register: 1, FlagDADDR: true, ResultADDRTYPE: true},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.NativeEndian.PutUint32(unix.RTN_LOCAL)})
	ret(append(nfproto(unix.NFPROTO_IPV4),
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 16, Len: 4},
		&expr.Lookup{SourceRegister: 1, SetName: reserved.Name, SetID: reserved.ID})...)
	if p.DNSPort != 0 {
		ret(dport(53)...)
	}
	for _, proto := range []byte{unix.IPPROTO_TCP, unix.IPPROTO_UDP} {
		c.AddRule(&nftables.Rule{Table: t, Chain: proxy, Exprs: append(append(nfproto(unix.NFPROTO_IPV4), l4proto(proto)...),
			&expr.Immediate{Register: 1, Data: binaryutil.BigEndian.PutUint16(p.TProxyPort)},
			&expr.TProxy{Family: byte(nftables.TableFamilyIPv4), TableFamily: byte(nftables.TableFamilyINet), RegPort: 1},
			&expr.Immediate{Register: 1, Data: binaryutil.NativeEndian.PutUint32(lanMark)},
			&expr.Meta{Key: expr.MetaKeyMARK, SourceRegister: true, Register: 1},
			&expr.Verdict{Kind: expr.VerdictAccept},
		)})
	}

	pre := c.AddChain(&nftables.Chain{Name: "prerouting", Table: t, Type: nftables.ChainTypeFilter,
		Hooknum: nftables.ChainHookPrerouting, Priority: nftables.ChainPriorityMangle})
	var dns *nftables.Chain
	if p.DNSPort != 0 {
		dns = c.AddChain(&nftables.Chain{Name: "dns", Table: t, Type: nftables.ChainTypeNAT,
			Hooknum: nftables.ChainHookPrerouting, Priority: nftables.ChainPriorityNATDest})
	}
	for _, name := range p.Interfaces {
		c.AddRule(&nftables.Rule{Table: t, Chain: pre, Exprs: append(iifname(name), &expr.Verdict{Kind: expr.VerdictJump, Chain: proxy.Name})})
		if dns == nil {
			continue
		}
		for _, proto := range []byte{unix.IPPROTO_UDP, unix.IPPROTO_TCP} {
			c.AddRule(&nftables.Rule{Table: t, Chain: dns, Exprs: append(append(append(append(iifname(name), nfproto(unix.NFPROTO_IPV4)...), l4proto(proto)...), dport(53)...),
				&expr.Immediate{Register: 1, Data: binaryutil.BigEndian.PutUint16(p.DNSPort)},
				&expr.Redir{RegisterProtoMin: 1},
			)})
		}
	}
	if err := c.Flush(); err != nil {
		return fmt.Errorf("下发 nftables 规则：%w", err)
	}

	// 打了标记的包按本机收：fwmark → 表 80 → local 0.0.0.0/0 dev lo
	lo, err := netlink.LinkByName("lo")
	if err != nil {
		return err
	}
	_, all, _ := net.ParseCIDR("0.0.0.0/0")
	if err := netlink.RouteReplace(&netlink.Route{LinkIndex: lo.Attrs().Index, Dst: all, Table: lanRoute, Type: unix.RTN_LOCAL, Scope: netlink.SCOPE_HOST}); err != nil {
		CleanupLAN()
		return fmt.Errorf("加本机路由：%w", err)
	}
	rule := netlink.NewRule()
	rule.Mark, rule.Mask, rule.Table, rule.Priority, rule.Family = lanMark, ptr(uint32(lanMask)), lanRoute, lanRulePri, unix.AF_INET
	if err := netlink.RuleAdd(rule); err != nil {
		CleanupLAN()
		return fmt.Errorf("加策略路由：%w", err)
	}
	return nil
}

// CleanupLAN 撤掉旁路由（没有就什么都不做；容器起来时也调一次，清掉上次崩溃留下的）
func CleanupLAN() {
	if c, err := nftables.New(); err == nil {
		if tables, err := c.ListTablesOfFamily(nftables.TableFamilyINet); err == nil {
			for _, t := range tables {
				if t.Name == lanTable {
					c.DelTable(t)
					_ = c.Flush()
				}
			}
		}
	}
	rule := netlink.NewRule()
	rule.Mark, rule.Mask, rule.Table, rule.Priority, rule.Family = lanMark, ptr(uint32(lanMask)), lanRoute, lanRulePri, unix.AF_INET
	for netlink.RuleDel(rule) == nil {
	}
	if lo, err := netlink.LinkByName("lo"); err == nil {
		_, all, _ := net.ParseCIDR("0.0.0.0/0")
		_ = netlink.RouteDel(&netlink.Route{LinkIndex: lo.Attrs().Index, Dst: all, Table: lanRoute, Type: unix.RTN_LOCAL, Scope: netlink.SCOPE_HOST})
	}
}

func ptr[T any](v T) *T { return &v }

func nfproto(p byte) []expr.Any {
	return []expr.Any{&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{p}}}
}

func l4proto(p byte) []expr.Any {
	return []expr.Any{&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{p}}}
}

// th dport：TCP / UDP 头的目的端口都在偏移 2
func dport(port uint16) []expr.Any {
	return []expr.Any{
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.BigEndian.PutUint16(port)},
	}
}

func iifname(name string) []expr.Any {
	b := make([]byte, 16)
	copy(b, name)
	return []expr.Any{&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: b}}
}

// intervalElements：IPv4 网段 → nftables 区间集合的元素（起点 + 「终点的下一个」标 IntervalEnd）
func intervalElements(cidrs []string) ([]nftables.SetElement, error) {
	var out []nftables.SetElement
	for _, s := range mergeV4(cidrs) {
		start := make([]byte, 4)
		binary.BigEndian.PutUint32(start, s[0])
		out = append(out, nftables.SetElement{Key: start})
		if s[1] == 0xffffffff {
			continue // 到头了，不用结束元素
		}
		end := make([]byte, 4)
		binary.BigEndian.PutUint32(end, s[1]+1)
		out = append(out, nftables.SetElement{Key: end, IntervalEnd: true})
	}
	return out, nil
}

//go:build linux

package hijack

import (
	"fmt"
	"net"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// NAS 本机透明代理（TUN）之外的两件事：
//   - NAS 自己的 DNS 查询（IPv4 与 IPv6 的上游都算）一律重定向到内核的 DNS：不然它直接问路由器、拿到真实地址（含 AAAA），
//     IPv6 就绕过了代理。内核自己发的查询打了 routing-mark，放行。
//   - 发往 fake-ip 网段的一律进 TUN（策略路由优先级 8999，在内核的规则前面）：容器的 DNS 由 dockerd 代查，也会拿到 fake-ip，
//     而 Docker 网桥默认整个排除在 TUN 外（免得弄断发布出去的端口），没有这条容器就上不了网。
// 都在自己的表 inet meowx_host + 一条策略路由里，关掉时整个删掉。

const (
	hostTable   = "meowx_host"
	fakeRulePri = 8999
)

func ApplyHost(p HostPlan) error {
	CleanupHost()
	c, err := nftables.New()
	if err != nil {
		return err
	}
	if p.DNSPort != 0 {
		t := c.AddTable(&nftables.Table{Family: nftables.TableFamilyINet, Name: hostTable})
		out := c.AddChain(&nftables.Chain{Name: "dns", Table: t, Type: nftables.ChainTypeNAT,
			Hooknum: nftables.ChainHookOutput, Priority: nftables.ChainPriorityNATDest})
		for _, proto := range []byte{unix.IPPROTO_UDP, unix.IPPROTO_TCP} {
			c.AddRule(&nftables.Rule{Table: t, Chain: out, Exprs: append(append(append([]expr.Any{
				// 内核自己的查询（routing-mark）放行
				&expr.Meta{Key: expr.MetaKeyMARK, Register: 1},
				&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: binaryutil.NativeEndian.PutUint32(p.CoreMark)},
				// 发往本机回环的不动
				&expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 1},
				&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: ifname16("lo")},
			}, l4proto(proto)...), dport(53)...),
				&expr.Immediate{Register: 1, Data: binaryutil.BigEndian.PutUint16(p.DNSPort)},
				&expr.Redir{RegisterProtoMin: 1},
			)})
		}
		if err := c.Flush(); err != nil {
			return fmt.Errorf("下发 DNS 重定向：%w", err)
		}
	}
	if p.FakeIPRange != "" && p.TUNTable != 0 {
		_, dst, err := net.ParseCIDR(p.FakeIPRange)
		if err != nil {
			return fmt.Errorf("fake-ip 网段写错了：%w", err)
		}
		rule := netlink.NewRule()
		rule.Dst, rule.Table, rule.Priority, rule.Family = dst, p.TUNTable, fakeRulePri, unix.AF_INET
		if err := netlink.RuleAdd(rule); err != nil {
			CleanupHost()
			return fmt.Errorf("加 fake-ip 策略路由：%w", err)
		}
	}
	return nil
}

func CleanupHost() {
	if c, err := nftables.New(); err == nil {
		if tables, err := c.ListTablesOfFamily(nftables.TableFamilyINet); err == nil {
			for _, t := range tables {
				if t.Name == hostTable {
					c.DelTable(t)
					_ = c.Flush()
				}
			}
		}
	}
	if rules, err := netlink.RuleList(unix.AF_INET); err == nil {
		for _, r := range rules {
			if r.Priority == fakeRulePri {
				r := r
				_ = netlink.RuleDel(&r)
			}
		}
	}
}

func ifname16(name string) []byte {
	b := make([]byte, 16)
	copy(b, name)
	return b
}

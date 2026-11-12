package hijack

import (
	"strings"
	"testing"

	"github.com/mmwx-group/meowx-fnos/agent/internal/fw"
	"github.com/mmwx-group/meowx-fnos/agent/internal/store"
)

var synoHost = Host{
	Physical: []Iface{{Name: "eth0", Subnets: []string{"192.168.1.0/24"}}},
	Bridges:  []Iface{{Name: "docker0"}},
	Firewall: fw.Env{Backend: fw.IPTables, IPTables: "legacy"},
}

func lines(s *fw.Script) string { return s.String() }

func TestLANScriptIPTables(t *testing.T) {
	s := store.Defaults()
	s.Proxy.Enabled, s.Proxy.HostProxy, s.Proxy.LANProxy = true, false, true
	s.Proxy.ReservedIP = []string{"10.0.0.0/8", "192.168.0.0/16"}
	plan := LAN(s, synoHost)
	if plan == nil || plan.RedirPort != 7891 || plan.TUNDevice != "meowx" || plan.DNSPort != 1053 {
		t.Fatalf("旁路由计划：%+v", plan)
	}
	want := `*nat
:MEOWX_LAN - [0:0]
-A MEOWX_LAN -p udp --dport 53 -j REDIRECT --to-ports 1053
-A MEOWX_LAN -p tcp --dport 53 -j REDIRECT --to-ports 1053
-A MEOWX_LAN -m addrtype --dst-type LOCAL -j RETURN
-A MEOWX_LAN -m iprange --dst-range 10.0.0.0-10.255.255.255 -j RETURN
-A MEOWX_LAN -m iprange --dst-range 192.168.0.0-192.168.255.255 -j RETURN
-A MEOWX_LAN -p tcp -j REDIRECT --to-ports 7891
-A PREROUTING -i eth0 -j MEOWX_LAN
COMMIT
*mangle
:MEOWX_LAN - [0:0]
-A MEOWX_LAN -m addrtype --dst-type LOCAL -j RETURN
-A MEOWX_LAN -m iprange --dst-range 10.0.0.0-10.255.255.255 -j RETURN
-A MEOWX_LAN -m iprange --dst-range 192.168.0.0-192.168.255.255 -j RETURN
-A MEOWX_LAN -p udp --dport 53 -j RETURN
-A MEOWX_LAN -p udp -j MARK --set-xmark 0x81/0xff
-A PREROUTING -i eth0 -j MEOWX_LAN
COMMIT
*filter
:MEOWX_LAN_FWD - [0:0]
-A MEOWX_LAN_FWD -i eth0 -o meowx -j ACCEPT
-A MEOWX_LAN_FWD -i meowx -o eth0 -j ACCEPT
-I DOCKER-USER 1 -j MEOWX_LAN_FWD
COMMIT
`
	if got := lines(lanScript(*plan, true)); got != want {
		t.Fatalf("得到：\n%s\n应当：\n%s", got, want)
	}
	if got := lines(lanScript(*plan, false)); !strings.Contains(got, "-I FORWARD 1 -j MEOWX_LAN_FWD") {
		t.Fatalf("没有 DOCKER-USER 时挂 FORWARD：\n%s", got)
	}

	// 只开旁路由：起一块不接管路由的 TUN，给 UDP 用
	tun := TUN(s, synoHost)
	if tun == nil || tun["auto-route"] != false || tun["device"] != "meowx" || tun["exclude-interface"] != nil {
		t.Fatalf("旁路由用的 TUN：%v", tun)
	}

	// 没有策略路由：UDP 不代理，只剩 nat 表
	noPR := synoHost
	noPR.Firewall.NoPolicyRouting = true
	plan = LAN(s, noPR)
	if plan.TUNDevice != "" || TUN(s, noPR) != nil {
		t.Fatalf("没有策略路由不该用 TUN：%+v", plan)
	}
	if got := lines(lanScript(*plan, true)); strings.Contains(got, "*mangle") || strings.Contains(got, "*filter") {
		t.Fatalf("没有策略路由时只有 nat 表：\n%s", got)
	}

	// redir 端口空着：旁路由不开
	s.Mixin.RedirPort = 0
	if LAN(s, synoHost) != nil {
		t.Fatal("iptables 后端没有 redir 端口时不开旁路由")
	}
}

func TestHostIPTables(t *testing.T) {
	s := store.Defaults()
	s.Proxy.Enabled, s.Proxy.HostProxy, s.Proxy.ContainerProxy = true, true, true
	s.Proxy.ReservedIP = []string{"10.0.0.0/8"}

	// 有策略路由：本机与容器照旧走 TUN，只有 DNS 重定向换成 iptables
	plan := HostRules(s, synoHost)
	if plan == nil || plan.RedirPort != 0 || plan.DNSPort != 1053 || plan.FakeIPRange == "" {
		t.Fatalf("%+v", plan)
	}
	want := `*nat
:MEOWX_HOST - [0:0]
-A MEOWX_HOST -m mark --mark 0x2026 -j RETURN
-A MEOWX_HOST -o lo -j RETURN
-A MEOWX_HOST -p udp --dport 53 -j REDIRECT --to-ports 1053
-A MEOWX_HOST -p tcp --dport 53 -j REDIRECT --to-ports 1053
-A OUTPUT -j MEOWX_HOST
COMMIT
`
	if got := lines(hostScript(*plan)); got != want {
		t.Fatalf("得到：\n%s", got)
	}
	if tun := TUN(s, synoHost); tun == nil || tun["auto-route"] != true {
		t.Fatalf("有策略路由时本机走 TUN：%v", tun)
	}

	// 没有策略路由：不起 TUN，TCP 经 REDIRECT
	noPR := synoHost
	noPR.Firewall.NoPolicyRouting = true
	if TUN(s, noPR) != nil {
		t.Fatal("没有策略路由不起 TUN")
	}
	plan = HostRules(s, noPR)
	if plan.RedirPort != 7891 || !plan.RedirectHost || plan.FakeIPRange != "" || plan.BridgeDNSPort != 1053 {
		t.Fatalf("%+v", plan)
	}
	want = `*nat
:MEOWX_HOST - [0:0]
-A MEOWX_HOST -m mark --mark 0x2026 -j RETURN
-A MEOWX_HOST -o lo -j RETURN
-A MEOWX_HOST -p udp --dport 53 -j REDIRECT --to-ports 1053
-A MEOWX_HOST -p tcp --dport 53 -j REDIRECT --to-ports 1053
-A MEOWX_HOST -m addrtype --dst-type LOCAL -j RETURN
-A MEOWX_HOST -m iprange --dst-range 10.0.0.0-10.255.255.255 -j RETURN
-A MEOWX_HOST -m iprange --dst-range 192.168.1.0-192.168.1.255 -j RETURN
-A MEOWX_HOST -p tcp -j REDIRECT --to-ports 7891
-A OUTPUT -j MEOWX_HOST
:MEOWX_HOST_PRE - [0:0]
-A MEOWX_HOST_PRE -p udp --dport 53 -j REDIRECT --to-ports 1053
-A MEOWX_HOST_PRE -p tcp --dport 53 -j REDIRECT --to-ports 1053
-A MEOWX_HOST_PRE -m addrtype --dst-type LOCAL -j RETURN
-A MEOWX_HOST_PRE -m iprange --dst-range 10.0.0.0-10.255.255.255 -j RETURN
-A MEOWX_HOST_PRE -m iprange --dst-range 192.168.1.0-192.168.1.255 -j RETURN
-A MEOWX_HOST_PRE -p tcp -j REDIRECT --to-ports 7891
-A PREROUTING -i docker0 -j MEOWX_HOST_PRE
COMMIT
`
	if got := lines(hostScript(*plan)); got != want {
		t.Fatalf("得到：\n%s", got)
	}
}

// 飞牛版（零值 Host = nftables + 有策略路由）：计划与以前一样
func TestNFTablesDefaultsUnchanged(t *testing.T) {
	s := store.Defaults()
	s.Proxy.Enabled, s.Proxy.LANProxy = true, true
	h := Host{Physical: []Iface{{Name: "enp4s0", Subnets: []string{"10.10.1.0/24"}}}}
	plan := LAN(s, h)
	if plan.RedirPort != 0 || plan.TUNDevice != "" || plan.TProxyPort != 7892 {
		t.Fatalf("%+v", plan)
	}
	hp := HostRules(s, h)
	if hp == nil || hp.RedirPort != 0 || hp.Bridges != nil || hp.FakeIPRange == "" {
		t.Fatalf("%+v", hp)
	}
	s.Proxy.HostProxy, s.Proxy.ContainerProxy = false, false
	if HostRules(s, h) != nil || TUN(s, h) != nil {
		t.Fatal("只开旁路由时 nftables 后端不起 TUN、不下发本机规则")
	}
}

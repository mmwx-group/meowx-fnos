package hijack

import (
	"testing"

	"github.com/mmwx-group/meowx-fnos/agent/internal/store"
)

func TestMergeV4(t *testing.T) {
	got := mergeV4([]string{"10.0.0.0/8", "10.1.0.0/16", "192.168.1.0/24", "192.168.0.0/24", "::1/128", "bad", "0.0.0.0/8"})
	want := [][2]uint32{{0x00000000, 0x00ffffff}, {0x0a000000, 0x0affffff}, {0xc0a80000, 0xc0a801ff}}
	if len(got) != len(want) {
		t.Fatalf("%x", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 段 %x，应当是 %x", i, got[i], want[i])
		}
	}
}

func TestTUNExcludesLANAndBridges(t *testing.T) {
	s := store.Defaults()
	s.Proxy.Enabled = true
	h := Host{
		Physical: []Iface{{Name: "enp4s0", Subnets: []string{"10.10.1.0/24"}}},
		Bridges:  []Iface{{Name: "docker0"}, {Name: "br-x"}},
	}
	tun := TUN(s, h)
	excl := tun["exclude-interface"].([]any)
	if len(excl) != 3 || excl[0] != "enp4s0" {
		t.Fatalf("应当排除物理网卡与网桥：%v", excl)
	}
	s.Proxy.ContainerProxy = true
	if excl := TUN(s, h)["exclude-interface"].([]any); len(excl) != 1 {
		t.Fatalf("代理容器时不排除网桥：%v", excl)
	}
	s.Proxy.HostProxy, s.Proxy.ContainerProxy = false, false
	if TUN(s, h) != nil {
		t.Fatal("只开旁路由时不起 TUN")
	}
	s.Proxy.LANProxy = true
	plan := LAN(s, h)
	if plan == nil || plan.Interfaces[0] != "enp4s0" || plan.TProxyPort != 7892 || plan.DNSPort != 1053 {
		t.Fatalf("旁路由计划不对：%+v", plan)
	}
}

// 绕过节点服务器地址在本机 TUN 这条路上：只有开着本机 / 容器透明代理、有策略路由时才加；不代理 IPv6 时不加 v6
func TestServerRoutes(t *testing.T) {
	s := store.Defaults()
	s.Proxy.Enabled = true
	ip4, ip6 := []string{"1.2.3.4"}, []string{"2001:db8::1"}
	v4, v6 := ServerRoutes(s, Host{}, ip4, ip6)
	if len(v4) != 1 || v6 != nil {
		t.Fatalf("v6 off: %v %v", v4, v6)
	}
	s.Proxy.IPv6 = true
	if v4, v6 = ServerRoutes(s, Host{}, ip4, ip6); len(v4) != 1 || len(v6) != 1 {
		t.Fatalf("v6 on: %v %v", v4, v6)
	}
	off := s
	off.Proxy.BypassProxyServer = false
	if v4, v6 = ServerRoutes(off, Host{}, ip4, ip6); v4 != nil || v6 != nil {
		t.Fatal("bypass off")
	}
	lanOnly := s
	lanOnly.Proxy.HostProxy, lanOnly.Proxy.ContainerProxy, lanOnly.Proxy.LANProxy = false, false, true
	if v4, _ = ServerRoutes(lanOnly, Host{}, ip4, ip6); v4 != nil {
		t.Fatal("no host TUN")
	}
	noPR := Host{}
	noPR.Firewall.NoPolicyRouting = true
	if v4, _ = ServerRoutes(s, noPR, ip4, ip6); v4 != nil {
		t.Fatal("no policy routing")
	}
}

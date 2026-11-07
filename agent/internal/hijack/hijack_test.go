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

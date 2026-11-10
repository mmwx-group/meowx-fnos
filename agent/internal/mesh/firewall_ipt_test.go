package mesh

import (
	"strings"
	"testing"

	"github.com/mmwx-group/meowx-fnos/agent/internal/store"
)

func TestMeshScript(t *testing.T) {
	sites := []Site{
		{Original: "192.168.1.0/24", Advertised: "192.168.1.0/24"},
		{Original: "10.0.0.0/30", Advertised: "100.96.0.4/30", Mapped: true},
	}
	sc, errs := meshScript(store.Mesh{}, sites, true)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	want := `*mangle
:MEOWX_MESH_MARK - [0:0]
-A MEOWX_MESH_MARK -i tailscale0 -j MARK --set-xmark 0x20260000/0xffff0000
-A FORWARD -j MEOWX_MESH_MARK
COMMIT
*nat
:MEOWX_MESH_POST - [0:0]
-A MEOWX_MESH_POST -m mark --mark 0x20260000/0xffff0000 ! -o tailscale0 -j MASQUERADE
-A POSTROUTING -j MEOWX_MESH_POST
:MEOWX_MESH_PRE - [0:0]
-A MEOWX_MESH_PRE -d 100.96.0.4/32 -j DNAT --to-destination 10.0.0.0
-A MEOWX_MESH_PRE -d 100.96.0.5/32 -j DNAT --to-destination 10.0.0.1
-A MEOWX_MESH_PRE -d 100.96.0.6/32 -j DNAT --to-destination 10.0.0.2
-A MEOWX_MESH_PRE -d 100.96.0.7/32 -j DNAT --to-destination 10.0.0.3
-A PREROUTING -i tailscale0 -j MEOWX_MESH_PRE
COMMIT
*filter
:MEOWX_MESH_IN - [0:0]
-A MEOWX_MESH_IN -m conntrack --ctstate RELATED,ESTABLISHED -j RETURN
-A MEOWX_MESH_IN -i tailscale0 -j DROP
-I INPUT 1 -j MEOWX_MESH_IN
:MEOWX_MESH_FWD - [0:0]
-A MEOWX_MESH_FWD -i tailscale0 -j ACCEPT
-A MEOWX_MESH_FWD -o tailscale0 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
-I DOCKER-USER 1 -j MEOWX_MESH_FWD
COMMIT
`
	if got := sc.String(); got != want {
		t.Fatalf("得到：\n%s", got)
	}

	// 能访问 NAS 本身、没有 Docker：没有 INPUT / DOCKER-USER 的规则，也没有映射
	sc, _ = meshScript(store.Mesh{HostAccess: true}, sites[:1], false)
	if got := sc.String(); strings.Contains(got, "*filter") || strings.Contains(got, meshPre) {
		t.Fatalf("%s", got)
	}
}

func TestMappedLimit(t *testing.T) {
	rules, err := mappedDNAT(Site{Original: "192.168.0.0/24", Advertised: "100.100.5.0/24", Mapped: true})
	if err != nil || len(rules) != 256 || rules[255] != "-d 100.100.5.255/32 -j DNAT --to-destination 192.168.0.255" {
		t.Fatalf("/24：%v %d", err, len(rules))
	}
	sc, errs := meshScript(store.Mesh{HostAccess: true}, []Site{
		{Original: "192.168.0.0/23", Advertised: "100.100.4.0/23", Mapped: true},
		{Original: "192.168.9.0/31", Advertised: "100.100.9.0/31", Mapped: true},
	}, false)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "最多 /24") {
		t.Fatalf("超过 /24 要报错：%v", errs)
	}
	if got := sc.String(); !strings.Contains(got, "-d 100.100.9.1/32 -j DNAT --to-destination 192.168.9.1") || strings.Contains(got, "100.100.4.") {
		t.Fatalf("太大的那段跳过、别的照下：\n%s", got)
	}
	if _, err := mappedDNAT(Site{Original: "192.168.0.0/24", Advertised: "100.100.5.0/25", Mapped: true}); err == nil {
		t.Fatal("大小不一样要报错")
	}
}

package mesh

import (
	"os"
	"strings"
	"testing"

	"github.com/mmwx-group/meowx-fnos/agent/internal/fw"
	"github.com/mmwx-group/meowx-fnos/agent/internal/store"
)

// 在真机上用真的 iptables 下发、再撤掉喵窝的规则（tailscale0 不在时这些规则不起作用，不影响宿主）。
// 要 root 与镜像里的 iptables 程序：MEOWX_LIVE_IPTABLES=legacy|nft go test -run Live ./internal/mesh/
// （交叉编译成测试程序放进容器里跑：GOOS=linux go test -c -o mesh.test ./internal/mesh/）
func TestLiveMeshIPTables(t *testing.T) {
	variant := os.Getenv("MEOWX_LIVE_IPTABLES")
	if variant == "" {
		t.Skip("没设 MEOWX_LIVE_IPTABLES")
	}
	ipt := fw.IPT{Variant: variant, Run: fw.Exec}
	sites := []Site{
		{Original: "192.168.50.0/24", Advertised: "100.100.50.0/24", Mapped: true},
		{Original: "10.66.0.0/16", Advertised: "100.101.0.0/16", Mapped: true}, // 太大，跳过
	}
	sc, errs := meshScript(store.Mesh{}, sites, ipt.HasChain("filter", "DOCKER-USER"))
	if len(errs) != 1 {
		t.Fatalf("应当只有 /16 那段报错：%v", errs)
	}
	if err := ipt.Restore(sc); err != nil {
		t.Fatal(err)
	}
	save, _ := ipt.Save()
	for _, want := range []string{
		"-A FORWARD -j MEOWX_MESH_MARK",
		"-A POSTROUTING -j MEOWX_MESH_POST",
		"-A MEOWX_MESH_POST ! -o tailscale0 -m mark --mark 0x20260000/0xffff0000 -j MASQUERADE", // iptables-save 的写法（选项顺序与下发时不同）
		"-A PREROUTING -i tailscale0 -j MEOWX_MESH_PRE",
		"-A MEOWX_MESH_PRE -d 100.100.50.255/32 -j DNAT --to-destination 192.168.50.255",
		"-A INPUT -j MEOWX_MESH_IN",
	} {
		if !strings.Contains(save, want) {
			t.Errorf("下发后没找到：%s", want)
		}
	}
	if n := strings.Count(save, "-j DNAT"); n < 256 {
		t.Errorf("DNAT 只有 %d 条", n)
	}
	if err := ipt.Cleanup("MEOWX_MESH"); err != nil {
		t.Fatal(err)
	}
	if save, _ := ipt.Save(); strings.Contains(save, "MEOWX_MESH") {
		t.Fatalf("撤完还有残留：\n%s", save)
	}
}

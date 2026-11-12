package mesh

import (
	"encoding/binary"
	"fmt"
	"net"

	"github.com/mmwx-group/meowx-fnos/agent/internal/fw"
	"github.com/mmwx-group/meowx-fnos/agent/internal/store"
)

// 喵窝防火墙的 iptables 后端（群晖等没有 nf_tables 的内核，见 internal/fw），做的事同 firewall_linux.go 的 nftables 版：
//   - 喵窝进来去局域网的做源地址转换：iptables 的 POSTROUTING 认不出入口网卡，先在 mangle FORWARD 给从 tailscale0 进来的包
//     打标记（高 16 位 0x2026，不碰低位，别的程序的标记不受影响），nat POSTROUTING 按标记 MASQUERADE；
//   - 映射段改回原网段：没有 NETMAP，逐个地址 DNAT，一个网段最多 256 个地址（/24），再大就报错、跳过那一段；
//   - 喵窝里的设备不能访问 NAS 本身时，filter INPUT 最前面挂一条链：已建立的放回（RETURN，不替别的规则做主）、其余从 tailscale0 进来的丢弃；
//   - Docker 把 FORWARD 设成丢弃：DOCKER-USER 里挂一条链放行 tailscale0 进出。
// 全部在 MEOWX_MESH* 链里，撤的时候按前缀删。

const (
	meshPost = "MEOWX_MESH_POST"
	meshMark = "MEOWX_MESH_MARK"
	meshPre  = "MEOWX_MESH_PRE"
	meshIn   = "MEOWX_MESH_IN"
	meshFwd  = "MEOWX_MESH_FWD"

	meshMarkValue = "0x20260000/0xffff0000"
	maxMapped     = 256
)

// meshScript：iptables 规则；errs 是没法下发的映射段（太大 / 写错）
func meshScript(s store.Mesh, sites []Site, dockerUser bool) (*fw.Script, []error) {
	sc := &fw.Script{}
	var errs []error

	sc.Chain("mangle", meshMark)
	sc.Add("mangle", fmt.Sprintf("-A %s -i %s -j MARK --set-xmark %s", meshMark, device, meshMarkValue))
	sc.Add("mangle", "-A FORWARD -j "+meshMark)

	sc.Chain("nat", meshPost)
	sc.Add("nat", fmt.Sprintf("-A %s -m mark --mark %s ! -o %s -j MASQUERADE", meshPost, meshMarkValue, device))
	sc.Add("nat", "-A POSTROUTING -j "+meshPost)

	var dnat []string
	for _, site := range sites {
		if !site.Mapped {
			continue
		}
		rules, err := mappedDNAT(site)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		dnat = append(dnat, rules...)
	}
	if len(dnat) > 0 {
		sc.Chain("nat", meshPre)
		for _, r := range dnat {
			sc.Add("nat", "-A "+meshPre+" "+r)
		}
		sc.Add("nat", fmt.Sprintf("-A PREROUTING -i %s -j %s", device, meshPre))
	}

	if !s.HostAccess {
		sc.Chain("filter", meshIn)
		sc.Add("filter", "-A "+meshIn+" -m conntrack --ctstate RELATED,ESTABLISHED -j RETURN")
		sc.Add("filter", fmt.Sprintf("-A %s -i %s -j DROP", meshIn, device))
		sc.Add("filter", "-I INPUT 1 -j "+meshIn)
	}
	if dockerUser {
		sc.Chain("filter", meshFwd)
		sc.Add("filter", fmt.Sprintf("-A %s -i %s -j ACCEPT", meshFwd, device))
		sc.Add("filter", fmt.Sprintf("-A %s -o %s -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT", meshFwd, device))
		sc.Add("filter", "-I DOCKER-USER 1 -j "+meshFwd)
	}
	return sc, errs
}

// mappedDNAT：映射段 → 原网段，主机号不变，一个地址一条
func mappedDNAT(site Site) ([]string, error) {
	_, adv, err1 := net.ParseCIDR(site.Advertised)
	_, orig, err2 := net.ParseCIDR(site.Original)
	if err1 != nil || err2 != nil || adv.IP.To4() == nil || orig.IP.To4() == nil {
		return nil, fmt.Errorf("映射段 %s → %s 写错了", site.Advertised, site.Original)
	}
	ao, _ := adv.Mask.Size()
	oo, _ := orig.Mask.Size()
	if ao != oo {
		return nil, fmt.Errorf("映射段 %s 与原网段 %s 大小不一样", site.Advertised, site.Original)
	}
	n := uint64(1) << (32 - ao)
	if n > maxMapped {
		return nil, fmt.Errorf("映射段 %s 太大：这台 NAS 的内核没有 NETMAP，只能逐个地址转换，最多 /24（256 个地址）", site.Advertised)
	}
	a := binary.BigEndian.Uint32(adv.IP.To4())
	o := binary.BigEndian.Uint32(orig.IP.To4())
	out := make([]string, 0, n)
	for i := uint32(0); i < uint32(n); i++ {
		out = append(out, fmt.Sprintf("-d %s/32 -j DNAT --to-destination %s", ipv4(a+i), ipv4(o+i)))
	}
	return out, nil
}

func ipv4(v uint32) string {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return net.IP(b).String()
}

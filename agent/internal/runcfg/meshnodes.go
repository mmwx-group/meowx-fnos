package runcfg

import "strings"

// 喵窝白名单直连（规格 miaomiaowuC/Design/meshnet-whitelist-direct.md §B3，同 OpenWrt 版 mesh_direct.uc 的 mesh_nodes）：
// 主控给喵窝名单里的账号下发「喵窝版」节点（`_mmwx-mesh: true`，server 是白名单服务器的喵窝地址 100.64.x.y）。
// NAS 上喵窝是官方 tailscaled、内核网卡 tailscale0（飞牛版与 Docker 版都是：宿主网络 + /dev/net/tun），内核连 100.64.x.y
// 照路由表（tailscale 的策略路由 5270 → 表 52）走 tailscale0，所以不加 dialer-proxy（内核编译时去掉了 tailscale 出站）。
// 但透明代理开着时 TUN 打开了 auto-detect-interface：内核的出站连接一律绑到默认网卡（SO_BINDTODEVICE eth0 之类），
// 100.64.x.y 也会被绑出去、连不上——所以给这些节点写 interface-name: tailscale0（按节点写的优先于自动检测与全局设置）。

// MeshInterface：喵窝的内核网卡（mesh.device）
const MeshInterface = "tailscale0"

const meshField = "_mmwx-mesh"

// applyMeshNodes：on = 白名单直连生效（保留喵窝版节点，去掉 _mmwx-mesh，没写 interface-name 的写成 iface）；
// off = 拿掉这些节点，并从各代理组的 proxies 里摘掉，组因此变空、又没有别的成员来源（use / include-all…）时补 DIRECT；
// rules 与 sub-rules 里目标是这些节点的规则整行删掉（含 AND / OR / NOT 逻辑规则；删，不改成 DIRECT——内核遇到指向不存在的节点的
// 规则会拒载整份配置）。
// 写了 _mmwx-mesh 但不是 true 的节点照常保留，只去掉这个字段。没有带这个字段的节点时什么都不改
func applyMeshNodes(cfg map[string]any, on bool, iface string) {
	proxies, ok := cfg["proxies"].([]any)
	if !ok {
		return
	}
	found := false
	for _, it := range proxies {
		if m, ok := it.(map[string]any); ok {
			if _, has := m[meshField]; has {
				found = true
				break
			}
		}
	}
	if !found {
		return
	}
	removed := map[string]bool{}
	out := make([]any, 0, len(proxies))
	for _, it := range proxies {
		m, ok := it.(map[string]any)
		if !ok {
			out = append(out, it)
			continue
		}
		flag, has := m[meshField]
		if !has {
			out = append(out, it)
			continue
		}
		mesh := flag == true
		if mesh && !on {
			if name, ok := m["name"].(string); ok {
				removed[name] = true
			}
			continue
		}
		delete(m, meshField)
		if mesh && iface != "" {
			if v, _ := m["interface-name"].(string); v == "" {
				m["interface-name"] = iface
			}
		}
		out = append(out, m)
	}
	cfg["proxies"] = out
	if len(removed) == 0 {
		return
	}
	if rules, ok := cfg["rules"].([]any); ok {
		cfg["rules"] = dropRules(rules, removed)
	}
	if subs, ok := cfg["sub-rules"].(map[string]any); ok {
		for name, list := range subs {
			if rules, ok := list.([]any); ok {
				subs[name] = dropRules(rules, removed)
			}
		}
	}
	groups, _ := cfg["proxy-groups"].([]any)
	for _, it := range groups {
		g, ok := it.(map[string]any)
		if !ok {
			continue
		}
		members, ok := g["proxies"].([]any)
		if !ok {
			continue
		}
		kept := make([]any, 0, len(members))
		for _, mm := range members {
			if name, ok := mm.(string); ok && removed[name] {
				continue
			}
			kept = append(kept, mm)
		}
		if len(kept) == len(members) {
			continue
		}
		if len(kept) == 0 && !hasOtherSource(g) {
			kept = []any{"DIRECT"}
		}
		g["proxies"] = kept
	}
}

// 组里除了 proxies 还有别的成员来源：proxies 摘空了也不用补 DIRECT
func hasOtherSource(g map[string]any) bool {
	if use, ok := g["use"].([]any); ok && len(use) > 0 {
		return true
	}
	for _, k := range []string{"include-all", "include-all-proxies", "include-all-providers"} {
		if v, _ := g[k].(bool); v {
			return true
		}
	}
	return false
}

// ruleTarget：规则的目标（出站）。按顶层逗号切开（括号里的逗号不算，逻辑规则的条件写在括号里），MATCH / FINAL 取第二段、
// 其余取第三段；认不出回空
func ruleTarget(rule string) string {
	var fields []string
	depth, start := 0, 0
	for i := 0; i < len(rule); i++ {
		switch rule[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				fields = append(fields, strings.TrimSpace(rule[start:i]))
				start = i + 1
			}
		}
	}
	fields = append(fields, strings.TrimSpace(rule[start:]))
	switch strings.ToUpper(fields[0]) {
	case "MATCH", "FINAL":
		if len(fields) >= 2 {
			return fields[1]
		}
		return ""
	}
	if len(fields) >= 3 {
		return fields[2]
	}
	return ""
}

// dropRules：删掉目标是 removed 里节点的规则
func dropRules(rules []any, removed map[string]bool) []any {
	kept := make([]any, 0, len(rules))
	for _, r := range rules {
		if s, ok := r.(string); ok && removed[ruleTarget(s)] {
			continue
		}
		kept = append(kept, r)
	}
	return kept
}

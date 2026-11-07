// Package runcfg：把订阅档 + 设置合成内核的运行配置（同 OpenWrt 版的 mixin.uc + meowx.init 的 mixin_profile）。
//
// 顺序：订阅原文 → 盖上设置组成的混入（空值不盖）→ 规则前面插 阻止 QUIC / po0 直连 / 自定义规则 → 盖上「额外混入」YAML
// （其中 meowx-proxies / meowx-proxy-groups / meowx-rules 三个键插到对应列表最前，同 OpenWrt 版）→ 控制服务自己要的项（外部控制、TUN）。
package runcfg

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/mmwx-group/meowx-fnos/agent/internal/store"
	"gopkg.in/yaml.v3"
)

// Inputs：除了设置以外生成运行配置要的东西
type Inputs struct {
	Settings          store.Settings
	Po0ReportURLs     []string // 开着 po0 加白时各台 po0 的上报地址（每台 po0 直连）
	APIListen         string   // 内核的外部控制（只给控制服务用，监听 127.0.0.1）
	APISecret         string
	UIPath            string         // 面板（zashboard）静态文件目录
	TUN               map[string]any // 透明代理要的 TUN 设置（nil = 不开 TUN）
	Listeners         []map[string]any
	ExtraFakeIPFilter []string // 喵窝：主控 / 中继的域名（fake-ip 下要拿真实地址）
	RoutingMark       int
	NoAAAA            bool // DNS 不回 IPv6 地址
}

// Build 生成运行配置
func Build(profile []byte, in Inputs) ([]byte, error) {
	cfg := map[string]any{}
	if err := yaml.Unmarshal(profile, &cfg); err != nil {
		return nil, fmt.Errorf("订阅不是合法的 YAML：%w", err)
	}
	if cfg == nil {
		cfg = map[string]any{}
	}

	// NAS 上开哪些端口只看设置：订阅自带的入站端口一律不用（机场订阅常写 port: 7890 / socks-port: 7891，
	// 与混合端口、redir 端口撞了会有一个起不来，7890 就只剩 HTTP、不收 SOCKS5）
	for _, k := range []string{"port", "socks-port", "mixed-port", "redir-port", "tproxy-port"} {
		delete(cfg, k)
	}
	mixin := buildMixin(in)
	merge(cfg, mixin)

	// 规则前面插：阻止 QUIC → po0 直连 → 自定义（同 mixin.uc 的 meowx-rules 顺序）
	var prefix []any
	if in.Settings.Mixin.BlockQUIC {
		prefix = append(prefix, "AND,((NETWORK,UDP),(DST-PORT,443)),REJECT")
	}
	for _, r := range po0Rules(in.Po0ReportURLs) {
		prefix = append(prefix, r)
	}
	for _, r := range in.Settings.Mixin.Rules {
		if r = strings.TrimSpace(r); r != "" {
			prefix = append(prefix, r)
		}
	}
	if len(prefix) > 0 {
		rules, _ := cfg["rules"].([]any)
		cfg["rules"] = append(prefix, rules...)
	}

	if extra := strings.TrimSpace(in.Settings.Mixin.Extra); extra != "" {
		ext := map[string]any{}
		if err := yaml.Unmarshal([]byte(extra), &ext); err != nil {
			return nil, fmt.Errorf("额外混入的 YAML 写错了：%w", err)
		}
		for key, target := range map[string]string{"meowx-proxies": "proxies", "meowx-proxy-groups": "proxy-groups", "meowx-rules": "rules"} {
			if list, ok := ext[key].([]any); ok {
				old, _ := cfg[target].([]any)
				cfg[target] = append(list, old...)
			}
			delete(ext, key)
		}
		merge(cfg, ext)
	}

	// 控制服务自己要的，最后写、谁也盖不掉
	cfg["external-controller"] = in.APIListen
	cfg["secret"] = in.APISecret
	delete(cfg, "external-controller-tls")
	delete(cfg, "external-controller-unix")
	if in.UIPath != "" {
		cfg["external-ui"] = in.UIPath
		delete(cfg, "external-ui-url")
		delete(cfg, "external-ui-name")
	}
	if in.TUN != nil {
		cfg["tun"] = in.TUN
	} else {
		cfg["tun"] = map[string]any{"enable": false}
	}
	if len(in.Listeners) > 0 {
		old, _ := cfg["listeners"].([]any)
		for _, l := range in.Listeners {
			old = append(old, l)
		}
		cfg["listeners"] = old
	}
	if in.RoutingMark != 0 {
		cfg["routing-mark"] = in.RoutingMark
	}
	if in.NoAAAA {
		dns, _ := cfg["dns"].(map[string]any)
		if dns == nil {
			dns = map[string]any{}
			cfg["dns"] = dns
		}
		dns["ipv6"] = false
	}
	if len(in.ExtraFakeIPFilter) > 0 {
		dns, _ := cfg["dns"].(map[string]any)
		if dns == nil {
			dns = map[string]any{}
			cfg["dns"] = dns
		}
		list, _ := dns["fake-ip-filter"].([]any)
		for _, h := range in.ExtraFakeIPFilter {
			list = append(list, h)
		}
		dns["fake-ip-filter"] = list
	}
	return yaml.Marshal(cfg)
}

func buildMixin(in Inputs) map[string]any {
	m := in.Settings.Mixin
	c := map[string]any{
		"log-level":      m.LogLevel,
		"mode":           m.Mode,
		"ipv6":           m.IPv6,
		"unified-delay":  m.UnifiedDelay,
		"tcp-concurrent": m.TCPConcurrent,
		"allow-lan":      m.AllowLAN,
		"port":           m.HTTPPort,
		"socks-port":     m.SocksPort,
		"mixed-port":     m.MixedPort,
		"redir-port":     m.RedirPort,
		"tproxy-port":    m.TProxyPort,
		"profile": map[string]any{
			"store-selected": m.StoreSelected,
			"store-fake-ip":  m.StoreFakeIP,
		},
		"geo-auto-update":     m.GeoAutoUpdate,
		"geo-update-interval": m.GeoUpdateInterval,
	}
	if m.Auth {
		var auth []any
		for _, u := range m.Users {
			if u.Enabled && u.Username != "" {
				auth = append(auth, u.Username+":"+u.Password)
			}
		}
		// 开了认证却一个账号都没有时写空列表：内核视为不认证，与关着一样
		c["authentication"] = auth
		if m.SkipAuthLAN {
			c["skip-auth-prefixes"] = []any{"127.0.0.0/8", "::1/128", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "fc00::/7", "fe80::/10"}
		}
	}

	d := m.DNS
	dns := map[string]any{
		"enable":              d.Enabled,
		"listen":              d.Listen,
		"ipv6":                d.IPv6,
		"enhanced-mode":       d.Mode,
		"fake-ip-range":       d.FakeIPRange,
		"fake-ip-filter":      strList(d.FakeIPFilter),
		"fake-ip-filter-mode": d.FakeIPFilterMode,
		"respect-rules":       d.RespectRules,
	}
	if d.OverrideNameservers {
		dns["default-nameserver"] = strList(d.DefaultNameserver)
		dns["proxy-server-nameserver"] = strList(d.ProxyServerNameserver)
		dns["direct-nameserver"] = strList(d.DirectNameserver)
		dns["nameserver"] = strList(d.Nameserver)
		dns["fallback"] = strList(d.Fallback)
	}
	if d.OverridePolicy {
		policy := map[string]any{}
		for _, p := range d.NameserverPolicy {
			if p.Enabled && p.Matcher != "" && len(p.Nameserver) > 0 {
				policy[p.Matcher] = strList(p.Nameserver)
			}
		}
		dns["nameserver-policy"] = policy
	}
	c["dns"] = dns
	if len(d.Hosts) > 0 {
		hosts := map[string]any{}
		for _, h := range d.Hosts {
			if h.Enabled && h.Domain != "" {
				hosts[h.Domain] = strList(h.IP)
			}
		}
		c["hosts"] = hosts
	}
	c["sniffer"] = map[string]any{
		"enable":       m.Sniffer.Enabled,
		"force-domain": strList(m.Sniffer.ForceDomain),
		"skip-domain":  strList(m.Sniffer.SkipDomain),
	}
	return trim(c).(map[string]any)
}

// trim 去掉空值（同 include.uc 的 trim_all）：""、0、空列表、空映射不盖订阅里的。布尔照写（false 也是设置）。
// 入站端口例外：订阅里的在 Build 里先删掉了，这里的 0 就是不开。
func trim(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			t := trim(val)
			if isEmpty(t) {
				delete(x, k)
			} else {
				x[k] = t
			}
		}
		return x
	case []any:
		out := x[:0]
		for _, val := range x {
			if t := trim(val); !isEmpty(t) {
				out = append(out, t)
			}
		}
		return out
	}
	return v
}

func isEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case int:
		return x == 0
	case map[string]any:
		return len(x) == 0
	case []any:
		return len(x) == 0
	}
	return false
}

// merge：映射逐层合并，其余（标量、列表）整个替换（同 yq 的 `. * $item`）
func merge(dst, src map[string]any) {
	for k, v := range src {
		if sm, ok := v.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				merge(dm, sm)
				continue
			}
		}
		dst[k] = v
	}
}

func strList(in []string) []any {
	var out []any
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

var dottedQuad = regexp.MustCompile(`^[0-9.]+$`)

// po0Rules：每台 po0 直连（同客户端 Po0DirectRules / mixin.uc）。IP → IP-CIDR /32（v6 /128）no-resolve，域名 → DOMAIN
func po0Rules(urls []string) []string {
	var rules []string
	seen := map[string]bool{}
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" {
			continue
		}
		host := strings.ToLower(u.Hostname())
		var r string
		switch {
		case dottedQuad.MatchString(host):
			r = "IP-CIDR," + host + "/32,DIRECT,no-resolve"
		case strings.Contains(host, ":"):
			r = "IP-CIDR6," + host + "/128,DIRECT,no-resolve"
		default:
			r = "DOMAIN," + host + ",DIRECT"
		}
		if !seen[r] {
			seen[r] = true
			rules = append(rules, r)
		}
	}
	return rules
}

package runcfg

import (
	"strings"
	"testing"

	"github.com/mmwx-group/meowx-fnos/agent/internal/store"
	"gopkg.in/yaml.v3"
)

const sample = `
mixed-port: 1234
port: 7890
socks-port: 7891
mode: global
dns:
  enable: true
  nameserver: [1.1.1.1]
  enhanced-mode: redir-host
proxies:
  - &n {name: a, type: ss, server: 1.2.3.4, port: 8388, cipher: aes-128-gcm, password: x}
proxy-groups:
  - {name: PROXY, type: select, proxies: [a]}
rules:
  - MATCH,PROXY
tun: {enable: true, auto-route: true}
external-controller: 0.0.0.0:9090
`

func build(t *testing.T, mut func(*Inputs)) map[string]any {
	t.Helper()
	in := Inputs{Settings: store.Defaults(), APIListen: "127.0.0.1:9090", APISecret: "s"}
	if mut != nil {
		mut(&in)
	}
	out, err := Build([]byte(sample), in)
	if err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{}
	if err := yaml.Unmarshal(out, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestMixinOverridesAndKeeps(t *testing.T) {
	cfg := build(t, nil)
	if cfg["mixed-port"] != 7890 || cfg["mode"] != "rule" {
		t.Fatalf("设置应当盖住订阅：%v %v", cfg["mixed-port"], cfg["mode"])
	}
	dns := cfg["dns"].(map[string]any)
	// 没打开「覆盖上游」时用订阅自己的 nameserver
	if ns := dns["nameserver"].([]any); len(ns) != 1 || ns[0] != "1.1.1.1" {
		t.Fatalf("订阅的上游不该被盖：%v", ns)
	}
	if dns["enhanced-mode"] != "fake-ip" {
		t.Fatalf("DNS 模式应当来自设置：%v", dns["enhanced-mode"])
	}
	// 订阅里开的 TUN 一律关掉（透明代理由控制服务管），外部控制只听本机
	if cfg["tun"].(map[string]any)["enable"] != false || cfg["external-controller"] != "127.0.0.1:9090" {
		t.Fatalf("TUN / 外部控制没按控制服务的来：%v %v", cfg["tun"], cfg["external-controller"])
	}
	// 订阅自带的入站端口不用：不然 port 7890 / socks-port 7891 与混合端口、redir 端口撞
	if _, ok := cfg["port"]; ok {
		t.Fatalf("订阅的 HTTP 端口应当去掉：%v", cfg["port"])
	}
	if _, ok := cfg["socks-port"]; ok {
		t.Fatalf("订阅的 SOCKS 端口应当去掉：%v", cfg["socks-port"])
	}
	if cfg["redir-port"] != 7891 || cfg["tproxy-port"] != 7892 {
		t.Fatalf("设置里的端口没写上：%v %v", cfg["redir-port"], cfg["tproxy-port"])
	}
	// YAML 锚点展开后节点仍在
	if len(cfg["proxies"].([]any)) != 1 {
		t.Fatal("节点丢了")
	}
}

func TestRulePrefix(t *testing.T) {
	cfg := build(t, func(in *Inputs) {
		in.Settings.Mixin.BlockQUIC = true
		in.Settings.Mixin.Rules = []string{"DOMAIN,nas.lan,DIRECT"}
		in.Po0ReportURLs = []string{"https://5.6.7.8/api/firewall/x/add?slot=1", "https://po0.example.com:8443/a", "https://[2001:db8::1]/a", "https://5.6.7.8/api/firewall/y/add"}
	})
	rules := cfg["rules"].([]any)
	want := []string{
		"AND,((NETWORK,UDP),(DST-PORT,443)),REJECT",
		"IP-CIDR,5.6.7.8/32,DIRECT,no-resolve",
		"DOMAIN,po0.example.com,DIRECT",
		"IP-CIDR6,2001:db8::1/128,DIRECT,no-resolve",
		"DOMAIN,nas.lan,DIRECT",
		"MATCH,PROXY",
	}
	if len(rules) != len(want) {
		t.Fatalf("规则数不对：%v", rules)
	}
	for i, w := range want {
		if rules[i] != w {
			t.Fatalf("第 %d 条：%v，应当是 %v", i, rules[i], w)
		}
	}
}

func TestExtraMixin(t *testing.T) {
	cfg := build(t, func(in *Inputs) {
		in.Settings.Mixin.Extra = "meowx-rules: ['DOMAIN,x.com,REJECT']\nmeowx-proxies: [{name: b, type: direct}]\nsniffer: {enable: false}\n"
	})
	if r := cfg["rules"].([]any); r[0] != "DOMAIN,x.com,REJECT" {
		t.Fatalf("额外规则应当排最前：%v", r)
	}
	if p := cfg["proxies"].([]any); len(p) != 2 || p[0].(map[string]any)["name"] != "b" {
		t.Fatalf("额外节点应当排最前：%v", p)
	}
	if _, ok := cfg["meowx-rules"]; ok {
		t.Fatal("meowx-* 键不该留在配置里")
	}
	if cfg["sniffer"].(map[string]any)["enable"] != false {
		t.Fatal("额外混入应当最后盖")
	}
}

func TestBadExtra(t *testing.T) {
	in := Inputs{Settings: store.Defaults()}
	in.Settings.Mixin.Extra = "rules: [unclosed"
	if _, err := Build([]byte(sample), in); err == nil || !strings.Contains(err.Error(), "额外混入") {
		t.Fatalf("写错的额外混入应当报错：%v", err)
	}
}

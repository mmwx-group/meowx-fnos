package runcfg

import (
	"encoding/json"
	"testing"

	"github.com/mmwx-group/meowx-fnos/agent/internal/store"
	"gopkg.in/yaml.v3"
)

const meshSample = `
proxies:
  - {name: a, type: ss, server: 1.2.3.4}
  - {name: "po0 东京 · 喵窝", type: vless, server: 100.64.0.21, servername: tokyo.example.com, _mmwx-mesh: true}
  - {name: "b · 喵窝", type: trojan, server: 100.64.0.22, _mmwx-mesh: true, interface-name: eth9}
  - {name: c, type: ss, server: 5.6.7.8, _mmwx-mesh: false}
proxy-groups:
  - {name: 节点选择, type: select, proxies: [a, "po0 东京 · 喵窝", DIRECT]}
  - {name: 只有喵窝, type: url-test, proxies: ["po0 东京 · 喵窝", "b · 喵窝"]}
  - {name: 还有 provider, type: select, proxies: ["b · 喵窝"], use: [p1]}
  - {name: include-all, type: select, proxies: ["b · 喵窝"], include-all: true}
  - {name: 不相干, type: select, proxies: [a]}
`

func meshCfg(t *testing.T) map[string]any {
	t.Helper()
	cfg := map[string]any{}
	if err := yaml.Unmarshal([]byte(meshSample), &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestMeshNodesOn(t *testing.T) {
	cfg := meshCfg(t)
	groupsBefore := jsonOf(cfg["proxy-groups"])
	applyMeshNodes(cfg, true, "tailscale0")
	want := `[{"name":"a","server":"1.2.3.4","type":"ss"},` +
		`{"interface-name":"tailscale0","name":"po0 东京 · 喵窝","server":"100.64.0.21","servername":"tokyo.example.com","type":"vless"},` +
		`{"interface-name":"eth9","name":"b · 喵窝","server":"100.64.0.22","type":"trojan"},` +
		`{"name":"c","server":"5.6.7.8","type":"ss"}]`
	if got := jsonOf(cfg["proxies"]); got != want {
		t.Errorf("proxies =\n%s\nwant\n%s", got, want)
	}
	if got := jsonOf(cfg["proxy-groups"]); got != groupsBefore {
		t.Errorf("on 时组不该变：%s", got)
	}
}

func TestMeshNodesOff(t *testing.T) {
	cfg := meshCfg(t)
	applyMeshNodes(cfg, false, "tailscale0")
	if got, want := jsonOf(cfg["proxies"]), `[{"name":"a","server":"1.2.3.4","type":"ss"},{"name":"c","server":"5.6.7.8","type":"ss"}]`; got != want {
		t.Errorf("proxies = %s", got)
	}
	want := `[{"name":"节点选择","proxies":["a","DIRECT"],"type":"select"},` +
		`{"name":"只有喵窝","proxies":["DIRECT"],"type":"url-test"},` +
		`{"name":"还有 provider","proxies":[],"type":"select","use":["p1"]},` +
		`{"include-all":true,"name":"include-all","proxies":[],"type":"select"},` +
		`{"name":"不相干","proxies":["a"],"type":"select"}]`
	if got := jsonOf(cfg["proxy-groups"]); got != want {
		t.Errorf("groups =\n%s\nwant\n%s", got, want)
	}
}

func TestMeshNodesNone(t *testing.T) {
	cfg := map[string]any{"proxies": []any{map[string]any{"name": "a"}}, "proxy-groups": []any{}}
	before := jsonOf(cfg)
	applyMeshNodes(cfg, false, "tailscale0")
	if jsonOf(cfg) != before {
		t.Errorf("没有喵窝节点时不该改：%s", jsonOf(cfg))
	}
	applyMeshNodes(map[string]any{}, true, "tailscale0") // 没有 proxies 不出错
	cfg = map[string]any{"proxies": []any{map[string]any{"name": "x", "_mmwx-mesh": true}}}
	applyMeshNodes(cfg, false, "tailscale0")
	if jsonOf(cfg) != `{"proxies":[]}` {
		t.Errorf("没有组：%s", jsonOf(cfg))
	}
}

// 整条生成运行配置：喵窝版节点按 MeshDirect 处理，额外混入里引用它的组同样摘掉
func TestBuildMeshDirect(t *testing.T) {
	settings := store.Defaults()
	settings.Mixin.Extra = "meowx-proxy-groups:\n  - {name: 我的组, type: select, proxies: [\"po0 东京 · 喵窝\"]}\n"
	for _, on := range []bool{true, false} {
		out, err := Build([]byte(meshSample), Inputs{Settings: settings, APIListen: "127.0.0.1:9090", APISecret: "s", MeshDirect: on})
		if err != nil {
			t.Fatal(err)
		}
		cfg := map[string]any{}
		if err := yaml.Unmarshal(out, &cfg); err != nil {
			t.Fatal(err)
		}
		proxies := cfg["proxies"].([]any)
		groups := cfg["proxy-groups"].([]any)
		mine := groups[0].(map[string]any)
		if on {
			if len(proxies) != 4 || proxies[1].(map[string]any)["interface-name"] != "tailscale0" {
				t.Errorf("on: proxies = %v", proxies)
			}
			if _, has := proxies[1].(map[string]any)["_mmwx-mesh"]; has {
				t.Errorf("on: 私有字段没去掉")
			}
			if jsonOf(mine["proxies"]) != `["po0 东京 · 喵窝"]` {
				t.Errorf("on: 我的组 = %v", mine)
			}
		} else {
			if len(proxies) != 2 {
				t.Errorf("off: proxies = %v", proxies)
			}
			if jsonOf(mine["proxies"]) != `["DIRECT"]` {
				t.Errorf("off: 我的组 = %v", mine)
			}
		}
	}
}

func TestRuleTarget(t *testing.T) {
	cases := map[string]string{
		"DOMAIN-SUFFIX,example.com,节点选择":                 "节点选择",
		"IP-CIDR,1.2.3.0/24,a,no-resolve":                "a",
		"MATCH,节点选择":                                     "节点选择",
		"AND,((DOMAIN,a.com),(NETWORK,UDP)),po0 东京 · 喵窝": "po0 东京 · 喵窝",
		"NOT,((DOMAIN-KEYWORD,ads)),b · 喵窝":              "b · 喵窝",
		" DOMAIN , x.com , a ":                           "a",
		"DOMAIN,x.com":                                   "",
		"":                                               "",
	}
	for rule, want := range cases {
		if got := ruleTarget(rule); got != want {
			t.Errorf("ruleTarget(%q) = %q, want %q", rule, got, want)
		}
	}
}

const meshRulesSample = `
proxies:
  - {name: a, type: ss}
  - {name: "po0 东京 · 喵窝", type: vless, _mmwx-mesh: true}
proxy-groups:
  - {name: G, type: select, proxies: [a, "po0 东京 · 喵窝"]}
rules:
  - DOMAIN,direct.example.com,po0 东京 · 喵窝
  - AND,((DOMAIN,a.com),(NETWORK,TCP)),po0 东京 · 喵窝
  - OR,((DOMAIN,b.com),(DOMAIN,c.com)),G
  - SUB-RULE,(NETWORK,TCP),sub1
  - MATCH,G
sub-rules:
  sub1: ["DOMAIN,x.com,po0 东京 · 喵窝", "MATCH,a"]
  sub2: ["MATCH,G"]
`

// off 时删掉指向被拿掉节点的规则（rules 与 sub-rules，含逻辑规则），不改成 DIRECT；on 时规则不动
func TestMeshNodesRules(t *testing.T) {
	load := func() map[string]any {
		cfg := map[string]any{}
		if err := yaml.Unmarshal([]byte(meshRulesSample), &cfg); err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	cfg := load()
	applyMeshNodes(cfg, false, MeshInterface)
	if got, want := jsonOf(cfg["rules"]), `["OR,((DOMAIN,b.com),(DOMAIN,c.com)),G","SUB-RULE,(NETWORK,TCP),sub1","MATCH,G"]`; got != want {
		t.Errorf("rules = %s", got)
	}
	if got, want := jsonOf(cfg["sub-rules"]), `{"sub1":["MATCH,a"],"sub2":["MATCH,G"]}`; got != want {
		t.Errorf("sub-rules = %s", got)
	}
	cfg = load()
	before := jsonOf([]any{cfg["rules"], cfg["sub-rules"]})
	applyMeshNodes(cfg, true, MeshInterface)
	if got := jsonOf([]any{cfg["rules"], cfg["sub-rules"]}); got != before {
		t.Errorf("on 时规则不该变：%s", got)
	}
}

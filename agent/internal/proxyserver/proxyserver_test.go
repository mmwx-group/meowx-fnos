package proxyserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func eq(t *testing.T, name string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got  %#v\n want %#v", name, got, want)
	}
}

func TestKind(t *testing.T) {
	cases := map[string]string{
		"1.2.3.4": "v4", " 1.2.3.4 ": "v4", "2001:db8::1": "v6", "[2001:db8::1]": "v6",
		"hk-1.example.com": "domain", "a_b.example.com": "domain",
		"localhost": "", "": "", "a b.example.com": "", "x'.example.com": "",
	}
	for in, want := range cases {
		eq(t, in, Kind(in), want)
	}
}

func TestCollectAndClassify(t *testing.T) {
	home := t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(home, "providers"), 0o755))
	must(os.WriteFile(filepath.Join(home, "providers", "p1.yaml"), []byte("proxies:\n  - {name: h, server: us.example.org}\n"), 0o644))
	must(os.MkdirAll(filepath.Join(home, "proxies"), 0o755))
	// 没写 path：缓存在 <home>/proxies/<md5(url)>
	p2 := ProviderFile(home, map[string]any{"url": "https://sub.example.com/y"})
	must(os.WriteFile(p2, []byte("proxies:\n  - {name: i, server: 9.9.9.9}\n"), 0o644))
	must(os.WriteFile(filepath.Join(home, "b64.txt"), []byte("c3M6Ly8uLi4="), 0o644))

	cfg := map[string]any{
		"proxies": []any{
			map[string]any{"name": "a", "type": "ss", "server": "1.2.3.4"},
			map[string]any{"name": "b", "type": "vless", "server": "HK.Example.com"},
			map[string]any{"name": "c", "type": "trojan", "server": "1.2.3.4"},
			map[string]any{"name": "d", "type": "direct"},
			map[string]any{"name": "e", "type": "hysteria2", "server": "[2001:db8::10]"},
		},
		"proxy-providers": map[string]any{
			"a-inline": map[string]any{"type": "inline", "payload": []any{map[string]any{"name": "f", "server": "jp.example.net"}, map[string]any{"name": "g", "server": "5.6.7.8"}}},
			"b-file":   map[string]any{"type": "http", "url": "https://sub.example.com/x", "path": "./providers/p1.yaml"},
			"c-nopath": map[string]any{"type": "http", "url": "https://sub.example.com/y"},
			"d-b64":    map[string]any{"type": "file", "path": "b64.txt"},
			"e-gone":   map[string]any{"type": "file", "path": "/nonexistent/p.yaml"},
		},
	}
	eq(t, "only inline", Collect(cfg, ""), []string{"1.2.3.4", "HK.Example.com", "[2001:db8::10]", "jp.example.net", "5.6.7.8"})
	all := Collect(cfg, home)
	eq(t, "with files", all, []string{"1.2.3.4", "HK.Example.com", "[2001:db8::10]", "jp.example.net", "5.6.7.8", "us.example.org", "9.9.9.9"})
	ip4, ip6, domains := Classify(append(all, "2001:DB8:0:0::10", "hk.example.com", "localhost"))
	eq(t, "ip4", ip4, []string{"1.2.3.4", "5.6.7.8", "9.9.9.9"})
	eq(t, "ip6", ip6, []string{"2001:db8::10"})
	eq(t, "domains", domains, []string{"hk.example.com", "jp.example.net", "us.example.org"})
	eq(t, "empty", Collect(map[string]any{}, home), []string(nil))
}

func TestProviderFile(t *testing.T) {
	eq(t, "rel", ProviderFile("/run", map[string]any{"path": "./providers/proxy/a.yaml"}), "/run/providers/proxy/a.yaml")
	eq(t, "rel2", ProviderFile("/run", map[string]any{"path": "p/a.yaml"}), "/run/p/a.yaml")
	eq(t, "abs", ProviderFile("/run", map[string]any{"path": "/data/run/a.yaml"}), "/data/run/a.yaml")
	eq(t, "md5", ProviderFile("/run", map[string]any{"url": "https://x"}), "/run/proxies/7d0708a6f06c78b590ece7d8fc0fcc3f")
	eq(t, "none", ProviderFile("/run", map[string]any{}), "")
}

func TestPatchFakeIPFilter(t *testing.T) {
	cfg := func(dns map[string]any) map[string]any { return map[string]any{"dns": dns} }
	c := cfg(map[string]any{"enhanced-mode": "fake-ip", "fake-ip-filter": []any{"+.lan", "a.example.com"}})
	eq(t, "append", PatchFakeIPFilter(c, []string{"a.example.com", "b.example.com"}), true)
	eq(t, "list", c["dns"].(map[string]any)["fake-ip-filter"], []any{"+.lan", "a.example.com", "b.example.com"})
	c = cfg(map[string]any{"enhanced-mode": "fake-ip"})
	eq(t, "no list", PatchFakeIPFilter(c, []string{"b.example.com"}), true)
	eq(t, "no list value", c["dns"].(map[string]any)["fake-ip-filter"], []any{"b.example.com"})
	eq(t, "explicit blacklist", PatchFakeIPFilter(cfg(map[string]any{"enhanced-mode": "fake-ip", "fake-ip-filter-mode": "blacklist"}), []string{"b.example.com"}), true)
	eq(t, "whitelist", PatchFakeIPFilter(cfg(map[string]any{"enhanced-mode": "fake-ip", "fake-ip-filter-mode": "whitelist"}), []string{"b.example.com"}), false)
	eq(t, "rule", PatchFakeIPFilter(cfg(map[string]any{"enhanced-mode": "fake-ip", "fake-ip-filter-mode": "rule"}), []string{"b.example.com"}), false)
	eq(t, "redir-host", PatchFakeIPFilter(cfg(map[string]any{"enhanced-mode": "redir-host"}), []string{"b.example.com"}), false)
	eq(t, "no dns", PatchFakeIPFilter(map[string]any{}, []string{"b.example.com"}), false)
	eq(t, "already", PatchFakeIPFilter(cfg(map[string]any{"enhanced-mode": "fake-ip", "fake-ip-filter": []any{"b.example.com"}}), []string{"b.example.com"}), false)
}

func TestAnswerIPs(t *testing.T) {
	reply := []byte(`{"Answer":[{"type":5,"data":"cdn.example.com."},{"type":1,"data":"9.9.9.9"},{"type":28,"data":"2001:db8::9"}]}`)
	eq(t, "A", AnswerIPs(reply, "A"), []string{"9.9.9.9"})
	eq(t, "AAAA", AnswerIPs(reply, "AAAA"), []string{"2001:db8::9"})
	eq(t, "nx", AnswerIPs([]byte(`{"Status":3}`), "A"), []string(nil))
	eq(t, "junk", AnswerIPs([]byte(`404 page not found`), "A"), []string(nil))
}

func TestResolve(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer s3cret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/version":
			_, _ = w.Write([]byte(`{"version":"x"}`))
		case "/dns/query":
			name, qtype := r.URL.Query().Get("name"), r.URL.Query().Get("type")
			switch {
			case name == "hk.example.com" && qtype == "A":
				_, _ = w.Write([]byte(`{"Answer":[{"data":"203.0.113.1"},{"data":"203.0.113.2"}]}`))
			case name == "hk.example.com" && qtype == "AAAA":
				_, _ = w.Write([]byte(`{"Answer":[{"data":"2001:db8::1"}]}`))
			case name == "v6only.example.com" && qtype == "AAAA":
				_, _ = w.Write([]byte(`{"Answer":[{"data":"2001:db8::2"}]}`))
			default:
				_, _ = w.Write([]byte(`{"Status":3}`))
			}
		}
	}))
	defer srv.Close()
	api := API{Base: srv.URL, Secret: "s3cret"}
	if !api.WaitReady(context.Background(), time.Second) {
		t.Fatal("not ready")
	}
	if (API{Base: srv.URL, Secret: "wrong"}).WaitReady(context.Background(), 0) != true {
		t.Fatal("401 should count as ready")
	}
	ip4, ip6, unresolved := api.Resolve(context.Background(), []string{"hk.example.com", "v6only.example.com", "nx.example.com"})
	eq(t, "ip4", ip4, []string{"203.0.113.1", "203.0.113.2"})
	eq(t, "ip6", ip6, []string{"2001:db8::1", "2001:db8::2"})
	eq(t, "unresolved", unresolved, []string{"nx.example.com"})
	if (API{Base: "http://127.0.0.1:1"}).WaitReady(context.Background(), 0) {
		t.Fatal("dead port reported ready")
	}
}

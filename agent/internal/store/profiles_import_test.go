package store

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const clashYAML = "proxies:\n  - {name: a, type: ss, server: 1.2.3.4, port: 1, cipher: aes-128-gcm, password: x}\n"

var linkList = base64.StdEncoding.EncodeToString([]byte("ss://YWVzLTEyOC1nY206eA@1.2.3.4:1#a\nvmess://eyJhZGQiOiIxLjIuMy40In0=\n"))

// 机场只给 UA 里带 clash 的客户端 Clash 配置、别的回 base64 节点链接：缺省 UA 下自动换 clash.meta 再拉一次
func TestImportFallbackUserAgent(t *testing.T) {
	var uas []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua := r.Header.Get("User-Agent")
		uas = append(uas, ua)
		if strings.Contains(strings.ToLower(ua), "clash") {
			w.Header().Set("Subscription-Userinfo", "upload=1; download=2; total=10")
			_, _ = w.Write([]byte(clashYAML))
			return
		}
		_, _ = w.Write([]byte(linkList))
	}))
	defer srv.Close()
	s, err := OpenProfiles(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.Client = srv.Client()
	s.Version = "0.1.8"
	p, err := s.AddSubscription(context.Background(), srv.URL+"/sub", "", false)
	if err != nil {
		t.Fatalf("import: %v (UAs %q)", err, uas)
	}
	if len(uas) != 2 || !strings.HasPrefix(uas[0], "mihomo/1.19.0 MeowX/") || uas[1] != fallbackUserAgent {
		t.Fatalf("UAs: %q", uas)
	}
	if p.Total != 10 || p.Used != 3 {
		t.Fatalf("headers of the fallback response not used: %+v", p)
	}
}

// 一律回节点链接：报明确原因；自己填了 UA 的不替他换（只拉一次）
func TestImportLinkListError(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(linkList))
	}))
	defer srv.Close()
	s, err := OpenProfiles(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.Client = srv.Client()
	if _, err := s.AddSubscription(context.Background(), srv.URL+"/a", "", false); err == nil || !strings.Contains(err.Error(), "节点链接") {
		t.Fatalf("err: %v", err)
	}
	if hits != 2 {
		t.Fatalf("default UA should retry once with the fallback UA, hits %d", hits)
	}
	hits = 0
	if err := s.upsert(Profile{ID: "custom", URL: srv.URL + "/b", UserAgent: "MyClient/1.0"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(context.Background(), "custom"); err == nil || hits != 1 {
		t.Fatalf("custom UA: err %v hits %d", err, hits)
	}
}

func TestLooksLikeLinkList(t *testing.T) {
	cases := map[string]bool{
		linkList:                         true,
		"trojan://x@h:443#n\n":           true,
		clashYAML:                        false,
		"<html>blocked</html>":           false,
		"":                               false,
		strings.TrimRight(linkList, "="): true,
	}
	for in, want := range cases {
		if got := looksLikeLinkList([]byte(in)); got != want {
			t.Errorf("%q: got %v", in[:min(len(in), 30)], got)
		}
	}
}

package store

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMeshHeaderValue(t *testing.T) {
	panel := PanelHosts{Base: "https://panel.example.com", SubBase: "https://sub.example.com"}
	cases := []struct {
		name string
		p    Profile
		on   bool
		want string
	}{
		{"主控域名 on", Profile{URL: "https://panel.example.com/x/abc"}, true, "on"},
		{"订阅域名 off", Profile{URL: "https://SUB.example.com/api/clash/subscribe?token=1"}, false, "off"},
		{"实时通道是这个主控", Profile{URL: "https://cdn.example.net/x/abc", RealtimeURL: "https://panel.example.com"}, true, "on"},
		{"从主控账户导入的", Profile{URL: "https://cdn.example.net/x/abc", FromPanel: true}, true, "on"},
		{"别家机场", Profile{URL: "https://airport.example.org/sub"}, true, ""},
		{"别的妙妙屋主控", Profile{URL: "https://other.example.org/x/1", RealtimeURL: "https://other.example.org"}, true, ""},
		{"本地上传的", Profile{FromPanel: true}, true, ""},
	}
	for _, c := range cases {
		if got := MeshHeaderValue(c.p, panel, c.on); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	// 没登录：一律不带
	if got := MeshHeaderValue(Profile{URL: "https://panel.example.com/x/1", FromPanel: true}, PanelHosts{}, true); got != "" {
		t.Errorf("没登录: %q", got)
	}
	// 老内核没有 sub_base：订阅域名上的认不出（除非实时通道地址或 FromPanel 认得出）
	if got := MeshHeaderValue(Profile{URL: "https://sub.example.com/x/1"}, PanelHosts{Base: "https://panel.example.com"}, true); got != "" {
		t.Errorf("没有 sub_base: %q", got)
	}
}

// 下载时把头带上（官方 UA 那次与换 clash.meta 重拉那次都带），MeshHeader 回空时不带
func TestDownloadSendsMeshHeader(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("X-MeowX-Mesh"))
		_, _ = w.Write([]byte("proxies:\n  - {name: a, type: ss, server: 1.2.3.4, port: 1, cipher: aes-128-gcm, password: x}\n"))
	}))
	defer srv.Close()
	s, err := OpenProfiles(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.MeshHeader = func(p Profile) string { return "on" }
	if _, err := s.AddSubscription(context.Background(), srv.URL+"/x/abc", "t", true); err != nil {
		t.Fatal(err)
	}
	s.MeshHeader = func(p Profile) string { return "" }
	if _, err := s.AddSubscription(context.Background(), srv.URL+"/other", "u", false); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "on" || got[1] != "" {
		t.Fatalf("headers = %q", got)
	}
}

package app

import (
	"reflect"
	"testing"

	"github.com/mmwx-group/meowx-fnos/agent/internal/store"
)

func TestRealtimePlan(t *testing.T) {
	profiles := []store.Profile{
		{ID: "a", URL: "https://sub.panel-a.com/x/1", RealtimeURL: "https://Panel-A.com", Realtime: "rtA1", SubKey: "pkg:1", SubRev: "r1"},
		{ID: "b", URL: "https://panel-b.com/x/2", RealtimeURL: "https://panel-b.com", Realtime: "rtB1", SubKey: "file:b.yaml", SubRev: "r2"},
		{ID: "c", URL: "https://panel-b.com/x/3", RealtimeURL: "https://panel-b.com", Realtime: "rtB2", SubKey: "pkg:3", SubRev: "r3"},
		{ID: "d", URL: "https://airport.example/sub"}, // 别家机场：没有实时通道
		{ID: "e", URL: "https://panel-c.com/x/5", RealtimeURL: "https://panel-c.com", SubKey: "pkg:5"}, // 没有 rev、没有凭证
	}
	channels, subs := RealtimePlan("https://panel-a.com", "tok", profiles, "c")
	wantCh := []RealtimeChannel{
		{Base: "https://panel-a.com", Token: "tok"},
		{Base: "https://panel-b.com", RT: "rtB2"}, // 同一主控两份：在用的那份的凭证
	}
	if !reflect.DeepEqual(channels, wantCh) {
		t.Fatalf("channels: %+v", channels)
	}
	wantSubs := []realtimeSub{
		{Base: "https://panel-a.com", Key: "pkg:1", Rev: "r1"}, // 登录的主控：base 用登录态的写法
		{Base: "https://panel-b.com", Key: "file:b.yaml", Rev: "r2"},
		{Base: "https://panel-b.com", Key: "pkg:3", Rev: "r3"},
	}
	if !reflect.DeepEqual(subs, wantSubs) {
		t.Fatalf("subs: %+v", subs)
	}
	// 没登录：每个主控一条只读通道
	channels, _ = RealtimePlan("", "", profiles, "")
	if len(channels) != 2 || channels[0].RT != "rtA1" || channels[1].RT != "rtB1" {
		t.Fatalf("not logged in: %+v", channels)
	}
}

func TestProfilesToRefresh(t *testing.T) {
	profiles := []store.Profile{
		{ID: "a", URL: "https://panel-a.com/x/1", RealtimeURL: "https://panel-a.com", SubKey: "pkg:1", SubRev: "r1"},
		{ID: "b", URL: "https://panel-a.com/x/2", RealtimeURL: "https://panel-a.com", SubKey: "pkg:2", SubRev: "r2"},
		{ID: "c", URL: "https://sub.panel-a.com/x/3"}, // 旧主控：没有键，只在不带键的通知里重拉（按订阅地址的主机名认不出 → 不算）
		{ID: "d", URL: "https://panel-b.com/x/4", RealtimeURL: "https://panel-b.com", SubKey: "pkg:1", SubRev: "r9"},
		{ID: "e", RealtimeURL: "https://panel-a.com", SubKey: "pkg:1"}, // 本地上传的不能刷新
	}
	cases := []struct {
		base, key, rev string
		want           []string
	}{
		{"https://panel-a.com", "pkg:1", "new", []string{"a"}},
		{"https://panel-a.com", "pkg:1", "r1", nil}, // rev 没变
		{"https://PANEL-A.com:443", "pkg:2", "", []string{"b"}},
		{"https://panel-a.com", "", "", []string{"a", "b"}}, // 不带键 = 这个主控来的全部
		{"https://panel-b.com", "pkg:1", "x", []string{"d"}},
		{"", "pkg:1", "x", nil},
	}
	for _, c := range cases {
		if got := ProfilesToRefresh(profiles, c.base, c.key, c.rev); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %s %s: got %v want %v", c.base, c.key, c.rev, got, c.want)
		}
	}
}

func TestRealtimeHookToken(t *testing.T) {
	a := &App{}
	tok := a.realtimeHookToken()
	if len(tok) != 32 || !a.RealtimeHookTokenOK(tok) || a.RealtimeHookTokenOK("") || a.RealtimeHookTokenOK(tok+"x") {
		t.Fatalf("token check: %q", tok)
	}
}

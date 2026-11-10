package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeTestURL(t *testing.T) {
	ok := map[string]string{
		"":                                       "",
		"   ":                                    "",
		"https://www.gstatic.com/generate_204":   "https://www.gstatic.com/generate_204",
		" http://captive.apple.com\n":            "http://captive.apple.com",
		"HTTPS://cp.cloudflare.com/generate_204": "HTTPS://cp.cloudflare.com/generate_204",
		"http://connect.rom.miui.com/generate_204": "http://connect.rom.miui.com/generate_204",
		"http://[2001:db8::1]:8080/204?a=1":        "http://[2001:db8::1]:8080/204?a=1",
		"http://10.0.0.1/":                         "http://10.0.0.1/",
	}
	for in, want := range ok {
		got, err := NormalizeTestURL(in)
		if err != nil || got != want {
			t.Errorf("NormalizeTestURL(%q) = %q, %v；想要 %q", in, got, err, want)
		}
	}
	bad := []string{
		"www.gstatic.com/generate_204",
		"ftp://example.com/",
		"socks5://127.0.0.1:1080",
		"javascript:alert(1)",
		"http:example.com",
		"http://",
		"https:///path",
		"http://exa mple.com/",
		"http://%zz/",
		"https://example.com/" + strings.Repeat("a", testURLMax),
	}
	for _, in := range bad {
		if got, err := NormalizeTestURL(in); err == nil {
			t.Errorf("NormalizeTestURL(%q) = %q，应该报错", in, got)
		}
	}
}

// 老设置文件没有 test_url：读进来是空（跟随订阅），存了再读保持原样
func TestSettingsTestURLCompat(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"enabled":true,"auto_update_hours":12}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Get(); got.TestURL != "" || !got.Enabled || got.AutoUpdateHours != 12 {
		t.Fatalf("老文件：%+v", got)
	}
	if _, err := s.Update(func(cur *Settings) error {
		cur.TestURL = "https://cp.cloudflare.com/generate_204"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	again, err := OpenSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.Get().TestURL != "https://cp.cloudflare.com/generate_204" {
		t.Fatalf("存盘后读回：%q", again.Get().TestURL)
	}
}

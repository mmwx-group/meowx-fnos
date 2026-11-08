package app

import (
	"context"
	"os"
	"testing"

	"github.com/mmwx-group/meowx-fnos/agent/internal/store"
)

// AkileCDN 白名单：开着 + 有 Key + 有节点才算要上报；上一次的结果在控制服务启动时删掉（等同内存盘）；
// 「从订阅添加节点」内核没在跑时读选中的订阅
func TestAkile(t *testing.T) {
	t.Setenv("MEOWX_GEO_DIR", t.TempDir())
	dir := t.TempDir()
	a, err := New("test", dir, "/nonexistent/meowx-core", "/nonexistent", "")
	if err != nil {
		t.Fatal(err)
	}
	paths := a.Core.Paths
	if err := os.MkdirAll(paths.Panel, 0o700); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		state  string
		active bool
	}{
		{`{"enabled":true,"key":"ak_TESTkey0000000000AAAA","nodes":[{"name":"n","port":443,"rule_id":1,"label":""}]}`, true},
		{`{"enabled":false,"key":"ak_TESTkey0000000000AAAA","nodes":[{"name":"n","port":443,"rule_id":1,"label":""}]}`, false},
		{`{"enabled":true,"key":"","nodes":[{"name":"n","port":443,"rule_id":1,"label":""}]}`, false},
		{`{"enabled":true,"key":"ak_TESTkey0000000000AAAA","nodes":[]}`, false},
		{`not json`, false},
	}
	for _, c := range cases {
		if err := os.WriteFile(paths.AkileStatePath(), []byte(c.state), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := a.akileActive(); got != c.active {
			t.Errorf("akileActive(%s) = %v, want %v", c.state, got, c.active)
		}
	}

	// 结果文件：重新起控制服务时删掉
	if err := os.WriteFile(paths.AkileResultPath(), []byte(`{"1":{"ok":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New("test", dir, "/nonexistent/meowx-core", "/nonexistent", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.AkileResultPath()); !os.IsNotExist(err) {
		t.Errorf("result file kept across restart: %v", err)
	}

	// 内核没在跑：没选订阅时读运行配置，选了读那份订阅
	if got := a.AkileNodesConfig(); got != paths.ConfigPath() {
		t.Errorf("no profile: %s", got)
	}
	_, _ = a.Settings.Update(func(cur *store.Settings) error {
		cur.TestProfile = false
		return nil
	})
	p, err := a.Profiles.AddLocal(context.Background(), "test", []byte("proxies:\n  - {name: a, type: ss, server: 1.2.3.4, port: 443, cipher: aes-128-gcm, password: x}\n"))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = a.Settings.Update(func(cur *store.Settings) error {
		cur.Profile = p.ID
		return nil
	})
	if got := a.AkileNodesConfig(); got != a.Profiles.Path(p.ID) {
		t.Errorf("active profile: %s, want %s", got, a.Profiles.Path(p.ID))
	}
}

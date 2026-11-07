package app

import (
	"os"
	"testing"

	"github.com/mmwx-group/meowx-fnos/agent/internal/store"
)

// 停着时改了透明代理：真正套用（启动）前要补试用期；没开 MeowX 或没开透明代理时不进
func TestTrialIfUnconfirmed(t *testing.T) {
	t.Setenv("MEOWX_GEO_DIR", t.TempDir())
	a, err := New("test", t.TempDir(), "/nonexistent/meowx-core", "/nonexistent", "")
	if err != nil {
		t.Fatal(err)
	}
	set := func(enabled, proxy bool) {
		_, _ = a.Settings.Update(func(cur *store.Settings) error {
			cur.Enabled, cur.Proxy.Enabled, cur.Proxy.KeepMinutes = enabled, proxy, 5
			return nil
		})
	}

	// 没有标记：不进试用期
	set(true, true)
	a.TrialIfUnconfirmed()
	if a.Trial().Pending {
		t.Fatal("没有没套用过的改动，不该进试用期")
	}

	// 有标记但 MeowX 停着：还不进，标记留着
	a.MarkUnconfirmed()
	set(false, true)
	a.TrialIfUnconfirmed()
	if a.Trial().Pending {
		t.Fatal("MeowX 停着时不该进试用期")
	}
	if _, err := os.Stat(a.unconfirmedPath()); err != nil {
		t.Fatal("标记应当留到真正套用")
	}

	// 启动：进试用期，标记交给试用期
	set(true, true)
	a.TrialIfUnconfirmed()
	if !a.Trial().Pending {
		t.Fatal("停着时改过透明代理，启动时应当进试用期")
	}
	if _, err := os.Stat(a.unconfirmedPath()); err == nil {
		t.Fatal("进了试用期之后标记应当删掉")
	}

	// 保留：试用期与标记都清掉
	a.MarkUnconfirmed()
	a.Keep()
	if a.Trial().Pending {
		t.Fatal("保留后不该还在试用期")
	}
	if _, err := os.Stat(a.unconfirmedPath()); err == nil {
		t.Fatal("保留后标记应当删掉")
	}
}

package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/mmwx-group/meowx-fnos/agent/internal/store"
)

// 透明代理的安全阀：打开 / 改了透明代理之后，N 分钟内没在网页上点「保留」就自动关掉——规则要是把 NAS 的网络（连带 SSH、网页）
// 弄断了，等一会儿自己就恢复。试用状态落在 /data/proxy-trial.json：容器在试用期内重启（或 NAS 重启）时一律当没确认、先关掉。

type TrialStatus struct {
	Pending  bool  `json:"pending"`
	Deadline int64 `json:"deadline,omitempty"` // 秒
}

func (a *App) trialPath() string { return filepath.Join(a.DataDir, "proxy-trial.json") }

// 透明代理改过、但那次没有真正套用（MeowX 停着时保存 / 只存不套用）：标记一下，真正套用时（点「启动」、容器重启）补一个试用期。
// 以前停着时改了透明代理再点启动，规则直接生效、没有试用期，写错了就把 NAS 的网络弄断
func (a *App) unconfirmedPath() string { return filepath.Join(a.DataDir, "proxy-unconfirmed") }

// MarkUnconfirmed：透明代理设置改了，这次没套用
func (a *App) MarkUnconfirmed() {
	_ = store.WriteFileAtomic(a.unconfirmedPath(), []byte("1"), 0o600)
}

// TrialIfUnconfirmed：套用之前调：开着 MeowX 与透明代理、又有没套用过的透明代理改动 → 进试用期
func (a *App) TrialIfUnconfirmed() {
	s := a.Settings.Get()
	if !s.Enabled || !s.Proxy.Enabled {
		return
	}
	if _, err := os.Stat(a.unconfirmedPath()); err != nil {
		return
	}
	a.StartTrial(s.Proxy.KeepMinutes)
}

func (a *App) Trial() TrialStatus {
	data, err := os.ReadFile(a.trialPath())
	if err != nil {
		return TrialStatus{}
	}
	var st TrialStatus
	_ = json.Unmarshal(data, &st)
	st.Pending = st.Deadline > 0
	return st
}

// StartTrial：开始试用，到点没确认就关透明代理
func (a *App) StartTrial(minutes int) {
	if minutes <= 0 {
		a.Keep()
		return
	}
	deadline := time.Now().Add(time.Duration(minutes) * time.Minute)
	data, _ := json.Marshal(TrialStatus{Deadline: deadline.Unix()})
	_ = store.WriteFileAtomic(a.trialPath(), data, 0o600)
	_ = os.Remove(a.unconfirmedPath()) // 改动交给试用期管了
	a.Log.Add("agent", "透明代理已打开：请在网页上点「保留」，否则到点自动关掉")
	a.trialMu.Lock()
	if a.trialTimer != nil {
		a.trialTimer.Stop()
	}
	a.trialTimer = time.AfterFunc(time.Until(deadline), a.trialExpired)
	a.trialMu.Unlock()
}

// Keep：确认保留
func (a *App) Keep() {
	a.trialMu.Lock()
	if a.trialTimer != nil {
		a.trialTimer.Stop()
		a.trialTimer = nil
	}
	a.trialMu.Unlock()
	_ = os.Remove(a.trialPath())
	_ = os.Remove(a.unconfirmedPath())
}

func (a *App) trialExpired() {
	if !a.Trial().Pending {
		return
	}
	a.Log.Add("agent", "透明代理到点没确认，自动关掉")
	a.disableProxy()
}

func (a *App) disableProxy() {
	_ = os.Remove(a.trialPath())
	_ = os.Remove(a.unconfirmedPath())
	_, _ = a.Settings.Update(func(cur *store.Settings) error {
		cur.Proxy.Enabled = false
		return nil
	})
	_ = a.Apply(context.Background())
}

// RecoverTrial：启动时（第一次 Apply 之前）——上次试用没确认就重启了，先把透明代理关掉
func (a *App) RecoverTrial() {
	if a.Trial().Pending {
		a.Log.Add("agent", "上次打开透明代理后没确认就重启了，先关掉透明代理")
		_ = os.Remove(a.trialPath())
		_, _ = a.Settings.Update(func(cur *store.Settings) error {
			cur.Proxy.Enabled = false
			return nil
		})
	}
}

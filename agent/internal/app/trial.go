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

// Package app：把设置、订阅、内核串起来（同 OpenWrt 版 meowx.init 的 start / reload / 定时任务）。
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mmwx-group/meowx-fnos/agent/internal/core"
	"github.com/mmwx-group/meowx-fnos/agent/internal/hijack"
	"github.com/mmwx-group/meowx-fnos/agent/internal/runcfg"
	"github.com/mmwx-group/meowx-fnos/agent/internal/store"
)

type App struct {
	Version  string
	DataDir  string
	UIPath   string
	Settings *store.SettingsStore
	Profiles *store.ProfileStore
	Core     *core.Supervisor
	Log      *core.Ring

	applyMu   sync.Mutex
	lastError string

	trialMu    sync.Mutex
	trialTimer *time.Timer
}

const apiListen = "127.0.0.1:9090"

func New(version, dataDir, coreBinary, uiPath string) (*App, error) {
	log := core.NewRing(3000)
	settings, err := store.OpenSettings(dataDir)
	if err != nil {
		return nil, fmt.Errorf("读设置：%w", err)
	}
	profiles, err := store.OpenProfiles(filepath.Join(dataDir, "profiles"))
	if err != nil {
		return nil, fmt.Errorf("读订阅：%w", err)
	}
	profiles.Version = version
	a := &App{
		Version: version, DataDir: dataDir, UIPath: uiPath,
		Settings: settings, Profiles: profiles, Log: log,
		Core: &core.Supervisor{
			Paths: core.Paths{
				Binary:  coreBinary,
				HomeDir: filepath.Join(dataDir, "run"),
				Panel:   filepath.Join(dataDir, "panel"),
			},
			API:    apiListen,
			Secret: loadSecret(filepath.Join(dataDir, "api-secret")),
			Log:    log,
		},
	}
	_ = os.MkdirAll(a.Core.Paths.HomeDir, 0o755)
	// 新订阅换上去之前用设置混进去、让内核试读（同 OpenWrt 版：按真实设置混入出完整配置再测）
	profiles.Validate = func(ctx context.Context, data []byte) error {
		if !a.Settings.Get().TestProfile {
			return nil
		}
		cfg, err := a.buildConfig(data)
		if err != nil {
			return err
		}
		return a.Core.Test(ctx, cfg)
	}
	return a, nil
}

// 内核外部控制的密钥：第一次生成、存在数据卷里
func loadSecret(path string) string {
	if b, err := os.ReadFile(path); err == nil && len(b) >= 16 {
		return string(b)
	}
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	s := hex.EncodeToString(raw[:])
	_ = store.WriteFileAtomic(path, []byte(s), 0o600)
	return s
}

func (a *App) LastError() string {
	a.applyMu.Lock()
	defer a.applyMu.Unlock()
	return a.lastError
}

func (a *App) buildConfig(profile []byte) ([]byte, error) {
	s := a.Settings.Get()
	return runcfg.Build(profile, runcfg.Inputs{
		Settings:      s,
		Po0ReportURLs: a.po0ReportURLs(),
		APIListen:     apiListen,
		APISecret:     a.Core.Secret,
		UIPath:        a.UIPath,
		TUN:           hijack.TUN(s, hijack.Discover()),
	})
}

// po0 实际上报的地址（开着 po0 加白时）：每台 po0 直连
func (a *App) po0ReportURLs() []string {
	data, err := os.ReadFile(filepath.Join(a.Core.Paths.Panel, "po0.json"))
	if err != nil {
		return nil
	}
	var st struct {
		Enabled    bool     `json:"enabled"`
		ReportURLs []string `json:"report_urls"`
	}
	if json.Unmarshal(data, &st) != nil || !st.Enabled {
		return nil
	}
	return st.ReportURLs
}

// Apply 按当前设置起停内核：关着就停；开着就生成运行配置（失败时运行中的不动）再重启内核
func (a *App) Apply(ctx context.Context) error {
	a.applyMu.Lock()
	defer a.applyMu.Unlock()
	err := a.applyLocked(ctx)
	if err != nil {
		a.lastError = err.Error()
		a.Log.Add("agent", "应用设置失败："+err.Error())
	} else {
		a.lastError = ""
	}
	return err
}

func (a *App) applyLocked(ctx context.Context) error {
	s := a.Settings.Get()
	if !s.Enabled {
		hijack.CleanupLAN()
		a.Core.Stop()
		return nil
	}
	if s.Profile == "" {
		return errors.New("还没有选订阅")
	}
	if _, ok := a.Profiles.Get(s.Profile); !ok {
		return errors.New("选的订阅不存在了")
	}
	profile, err := os.ReadFile(a.Profiles.Path(s.Profile))
	if err != nil {
		return fmt.Errorf("读订阅内容：%w", err)
	}
	cfg, err := a.buildConfig(profile)
	if err != nil {
		return err
	}
	if s.TestProfile {
		if err := a.Core.Test(ctx, cfg); err != nil {
			return fmt.Errorf("新配置没通过内核检查，运行中的保持不变：%w", err)
		}
	}
	if err := store.WriteFileAtomic(a.Core.Paths.ConfigPath(), cfg, 0o600); err != nil {
		return err
	}
	a.Log.Add("agent", "启动内核")
	hijack.CleanupLAN()
	if err := a.Core.Start(ctx); err != nil {
		return err
	}
	// 旁路由：内核的 tproxy 端口起来以后再把局域网设备的流量交过去
	if plan := hijack.LAN(s, hijack.Discover()); plan != nil {
		if err := hijack.ApplyLAN(*plan); err != nil {
			return fmt.Errorf("旁路由规则没下发成功：%w", err)
		}
		a.Log.Add("agent", fmt.Sprintf("旁路由已开：%v → tproxy %d", plan.Interfaces, plan.TProxyPort))
	}
	return nil
}

// Shutdown：容器停止时（先撤旁路由，再停内核：内核退出时撤掉自己的 TUN 与路由）
func (a *App) Shutdown() {
	hijack.CleanupLAN()
	a.Core.Stop()
}

// Loop：定时任务（订阅自动更新、白名单上报），每分钟看一次
func (a *App) Loop(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	var lastReport time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			a.autoUpdate(ctx, now)
			if a.reportDue(now, lastReport) {
				lastReport = now
				a.panelReport(ctx)
			}
		}
	}
}

func (a *App) autoUpdate(ctx context.Context, now time.Time) {
	s := a.Settings.Get()
	for _, id := range a.Profiles.Due(s.AutoUpdateHours, now) {
		p, err := a.Profiles.Update(ctx, id)
		if err != nil {
			a.Log.Add("agent", fmt.Sprintf("自动更新订阅「%s」失败：%v", p.Name, err))
			continue
		}
		a.Log.Add("agent", fmt.Sprintf("自动更新订阅「%s」", p.Name))
		if s.Enabled && s.Profile == id {
			_ = a.Apply(ctx)
		}
	}
}

// 白名单上报间隔同客户端：有 po0 时每 10 分钟，只有主控账户时每小时；内核没在跑不报
func (a *App) reportDue(now, last time.Time) bool {
	if !a.Core.Status().Running {
		return false
	}
	interval := time.Hour
	if len(a.po0ReportURLs()) > 0 {
		interval = 10 * time.Minute
	} else if _, err := os.Stat(filepath.Join(a.Core.Paths.Panel, "panel.json")); err != nil {
		return false
	}
	return now.Sub(last) >= interval
}

func (a *App) panelReport(ctx context.Context) {
	before := fmt.Sprint(a.po0ReportURLs())
	if _, err := a.Core.Panel(ctx, "report"); err != nil {
		a.Log.Add("agent", "白名单上报失败："+err.Error())
	}
	// 主控下发的 po0 列表变了：重载让直连规则跟上
	if fmt.Sprint(a.po0ReportURLs()) != before {
		a.Log.Add("agent", "po0 列表变了，重载")
		_ = a.Apply(ctx)
	}
}

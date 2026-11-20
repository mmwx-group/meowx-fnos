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
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mmwx-group/meowx-fnos/agent/internal/core"
	"github.com/mmwx-group/meowx-fnos/agent/internal/hijack"
	"github.com/mmwx-group/meowx-fnos/agent/internal/mesh"
	"github.com/mmwx-group/meowx-fnos/agent/internal/proxyserver"
	"github.com/mmwx-group/meowx-fnos/agent/internal/runcfg"
	"github.com/mmwx-group/meowx-fnos/agent/internal/store"
	"github.com/mmwx-group/meowx-fnos/agent/internal/update"
	"gopkg.in/yaml.v3"
)

type App struct {
	Version  string
	DataDir  string
	UIPath   string
	Settings *store.SettingsStore
	Profiles *store.ProfileStore
	Core     *core.Supervisor
	Log      *core.Ring
	Mesh     *mesh.Manager
	Update   *update.Updater
	Speed    *store.SpeedHistory // 网页「网络测速」的记录
	Board    *store.SpeedBoard   // 网页「网络测速」上传到测速榜的本机设置（同意与否、install_id）

	applyMu   sync.Mutex
	lastError string

	trialMu    sync.Mutex
	trialTimer *time.Timer

	UIPort string        // 网页 / 接口端口（内核的订阅变更回调打到这里），空 = 7899
	rt     realtimeState // 主控实时通道（realtime.go）

	// 绕过节点服务器地址：上一轮经内核解析出来的节点地址（含配置里直接写的 IP），下发透明代理时一并排除
	psMu      sync.Mutex
	ps4, ps6  []string
	psAt      time.Time
	psRunning atomic.Bool
}

const apiListen = "127.0.0.1:9090"

func New(version, dataDir, coreBinary, tsBinDir, uiPath string) (*App, error) {
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
		Speed: store.OpenSpeedHistory(filepath.Join(dataDir, "speedtest-history.json")),
		Board: store.OpenSpeedBoard(filepath.Join(dataDir, "speedboard.json")),
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
	seedGeo(a.Core.Paths.HomeDir, log)
	// AkileCDN 白名单上一次的结果只留到控制服务重启（同 OpenWrt 版的内存盘）：重启后第一轮一定写
	_ = os.Remove(a.Core.Paths.AkileResultPath())
	a.Mesh = &mesh.Manager{
		Dir: filepath.Join(dataDir, "mesh"), Bin: tsBinDir, Core: a.Core, Log: log, Settings: settings,
		// 主控 / 中继域名变了：内核在跑就重载，把它们加进 fake-ip-filter（tailscaled 才拿得到真实地址）
		OnHostsChanged: func() {
			if a.Core.Status().Running {
				go func() { _ = a.Apply(context.Background()) }()
			}
		},
	}
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

// seedGeo：随包带的 geo 数据（/usr/share/meowx/geo）在内核工作目录里没有时复制过去——第一次启动不用连 GitHub；
// 已经有的（内核自动更新过的）不覆盖
func seedGeo(home string, log *core.Ring) {
	src := os.Getenv("MEOWX_GEO_DIR")
	if src == "" {
		src = "/usr/share/meowx/geo"
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return
	}
	for _, e := range entries {
		dst := filepath.Join(home, e.Name())
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err == nil && store.WriteFileAtomic(dst, data, 0o644) == nil {
			log.Add("agent", "放入随包的 "+e.Name())
		}
	}
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
		Settings:          s,
		Po0ReportURLs:     a.po0ReportURLs(),
		APIListen:         apiListen,
		APISecret:         a.Core.Secret,
		UIPath:            a.UIPath,
		TUN:               hijack.TUN(s, hijack.Discover()),
		ExtraFakeIPFilter: a.Mesh.Hosts(),
		RoutingMark:       hijack.CoreMark,
		ProxyServers:      a.proxyServersInput(s),
		// 透明代理开着、不代理 IPv6：内核的 DNS 不回 AAAA，应用都走 IPv4（进 TUN / TPROXY）
		NoAAAA: s.Proxy.Enabled && !s.Proxy.IPv6 && (s.Proxy.HostProxy || s.Proxy.LANProxy),
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
		hijack.CleanupHost()
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
	hijack.CleanupHost()
	if err := a.Core.Start(ctx); err != nil {
		return err
	}
	// 主控实时通道：内核每次起来都要重新交给它（内核里只有落盘的覆盖表与订阅 rev，通道配置不落盘）
	a.KickRealtime()
	if err := a.applyHijackLocked(s, hijack.Discover(), false); err != nil {
		return err
	}
	// 绕过节点服务器地址：等内核的控制接口通了解析节点域名，地址有变化再重新下发
	if proxyServersActive(s) {
		go a.refreshProxyServers(context.Background(), true)
	}
	return nil
}

// applyHijackLocked 下发透明代理（本机 DNS / fake-ip、本机 TUN 的节点绕过、旁路由）。quiet = 刷新节点地址时重新下发，不重复记日志
func (a *App) applyHijackLocked(s store.Settings, host hijack.Host, quiet bool) error {
	ip4, ip6 := a.knownProxyServers(s)
	if plan := hijack.HostRules(s, host); plan != nil {
		plan.Reserved = append(plan.Reserved, hostCIDRs(ip4)...)
		if err := hijack.ApplyHost(*plan); err != nil {
			return fmt.Errorf("本机 DNS / fake-ip 规则没下发成功：%w", err)
		}
		if plan.RedirPort != 0 && !quiet {
			a.Log.Add("agent", fmt.Sprintf("没有策略路由：NAS 本机 / 容器的 TCP 经 REDIRECT 交给 redir %d，UDP 不代理", plan.RedirPort))
		}
	}
	// ApplyHost 先清掉了旧的：节点绕过的策略路由排在它后面下发
	if v4, v6 := hijack.ServerRoutes(s, host, ip4, ip6); len(v4)+len(v6) > 0 {
		if err := hijack.ApplyServerRoutes(v4, v6); err != nil {
			return fmt.Errorf("绕过节点服务器地址的策略路由没下发成功：%w", err)
		}
	}
	// 旁路由：内核的 tproxy 端口起来以后再把局域网设备的流量交过去
	if plan := hijack.LAN(s, host); plan != nil {
		plan.Reserved = append(plan.Reserved, hostCIDRs(ip4)...)
		if err := hijack.ApplyLAN(*plan); err != nil {
			return fmt.Errorf("旁路由规则没下发成功：%w", err)
		}
		if !quiet {
			if plan.RedirPort != 0 {
				udp := "UDP 经 TUN " + plan.TUNDevice
				if plan.TUNDevice == "" {
					udp = "UDP 不代理（没有策略路由）"
				}
				a.Log.Add("agent", fmt.Sprintf("旁路由已开（iptables）：%v → TCP 经 redir %d，%s", plan.Interfaces, plan.RedirPort, udp))
			} else {
				a.Log.Add("agent", fmt.Sprintf("旁路由已开：%v → tproxy %d", plan.Interfaces, plan.TProxyPort))
			}
		}
	}
	return nil
}

// proxyServersActive：绕过节点服务器地址这次用不用得上（开着透明代理、有要代理的流量）
func proxyServersActive(s store.Settings) bool {
	p := s.Proxy
	return s.Enabled && p.Enabled && p.BypassProxyServer && (p.HostProxy || p.ContainerProxy || p.LANProxy)
}

// knownProxyServers：上一轮的节点地址（用不上时都是 nil）
func (a *App) knownProxyServers(s store.Settings) (ip4, ip6 []string) {
	if !proxyServersActive(s) {
		return nil, nil
	}
	a.psMu.Lock()
	defer a.psMu.Unlock()
	return a.ps4, a.ps6
}

func (a *App) proxyServersInput(s store.Settings) *runcfg.ProxyServers {
	if !proxyServersActive(s) {
		return nil
	}
	ip4, ip6 := a.knownProxyServers(s)
	return &runcfg.ProxyServers{Home: a.Core.Paths.HomeDir, Known: append(append([]string{}, ip4...), ip6...)}
}

func hostCIDRs(ips []string) []string {
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		out = append(out, ip+"/32")
	}
	return out
}

// refreshProxyServers：从运行配置里收齐节点的服务器地址，域名经内核的 /dns/query 解析（同 OpenWrt 版 proxy_server_cli.uc apply）；
// 和上一轮不一样就重新下发透明代理。afterStart = 内核刚起来，等它的控制接口最多 30 秒，结果总记一行日志
func (a *App) refreshProxyServers(ctx context.Context, afterStart bool) {
	if !a.psRunning.CompareAndSwap(false, true) {
		return
	}
	defer a.psRunning.Store(false)
	if !proxyServersActive(a.Settings.Get()) || !a.Core.Status().Running {
		return
	}
	data, err := os.ReadFile(a.Core.Paths.ConfigPath())
	if err != nil {
		return
	}
	cfg := map[string]any{}
	if yaml.Unmarshal(data, &cfg) != nil {
		return
	}
	servers := proxyserver.Collect(cfg, a.Core.Paths.HomeDir)
	ip4, ip6, domains := proxyserver.Classify(servers)
	note := ""
	if len(domains) > 0 {
		api := proxyserver.API{Base: "http://" + apiListen, Secret: a.Core.Secret}
		wait := time.Duration(0)
		if afterStart {
			wait = 30 * time.Second
		}
		if !api.WaitReady(ctx, wait) {
			// 解析不了就不动上一轮的（别把域名节点的地址丢掉）
			a.Log.Add("agent", fmt.Sprintf("绕过节点服务器地址：内核的控制接口不通，%d 个域名这次没解析", len(domains)))
			return
		}
		r4, r6, unresolved := api.Resolve(ctx, domains)
		ip4 = dedupeStrings(append(ip4, r4...))
		ip6 = dedupeStrings(append(ip6, r6...))
		note = fmt.Sprintf("，%d 个域名", len(domains))
		if len(unresolved) > 0 {
			note += "，解析不出：" + strings.Join(unresolved, " ")
		}
	}

	a.applyMu.Lock()
	defer a.applyMu.Unlock()
	s := a.Settings.Get()
	if !proxyServersActive(s) || !a.Core.Status().Running {
		return
	}
	a.psMu.Lock()
	changed := !slices.Equal(a.ps4, ip4) || !slices.Equal(a.ps6, ip6)
	a.ps4, a.ps6, a.psAt = ip4, ip6, time.Now()
	a.psMu.Unlock()
	if changed {
		if err := a.applyHijackLocked(s, hijack.Discover(), true); err != nil {
			a.Log.Add("agent", "绕过节点服务器地址：重新下发透明代理失败："+err.Error())
			return
		}
	}
	if changed || afterStart {
		a.Log.Add("agent", fmt.Sprintf("绕过节点服务器地址：%d 个 IPv4 / %d 个 IPv6（%d 个节点%s）", len(ip4), len(ip6), len(servers), note))
	}
}

// proxyServersDue：每 30 分钟重新解析一次节点域名（节点换了 IP、订阅更新过）
func (a *App) proxyServersDue(now time.Time) bool {
	if !proxyServersActive(a.Settings.Get()) || !a.Core.Status().Running {
		return false
	}
	a.psMu.Lock()
	defer a.psMu.Unlock()
	return now.Sub(a.psAt) >= 30*time.Minute
}

func dedupeStrings(list []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range list {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// Shutdown：容器停止时（先撤旁路由，再停内核：内核退出时撤掉自己的 TUN 与路由；喵窝收掉 tailscaled 与防火墙）
func (a *App) Shutdown() {
	hijack.CleanupLAN()
	hijack.CleanupHost()
	a.Core.Stop()
	a.Mesh.Shutdown()
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
			hijack.RefreshLAN() // iptables 后端：内核重启过的话补上 UDP 进 TUN 的路由
			a.autoUpdate(ctx, now)
			if a.proxyServersDue(now) {
				go a.refreshProxyServers(ctx, false)
			}
			if a.reportDue(now, lastReport) {
				lastReport = now
				a.panelReport(ctx, true)
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

// 白名单上报间隔同客户端：有 po0、GGY 或 AkileCDN 加白时每 10 分钟，只有主控账户时每小时；内核没在跑不报
func (a *App) reportDue(now, last time.Time) bool {
	if !a.Core.Status().Running {
		return false
	}
	interval := time.Hour
	if len(a.po0ReportURLs()) > 0 || a.ggyActive() || a.akileActive() {
		interval = 10 * time.Minute
	} else if _, err := os.Stat(filepath.Join(a.Core.Paths.Panel, "panel.json")); err != nil {
		return false
	}
	return now.Sub(last) >= interval
}

// GGY 加白开着且至少一个 token（与主控账户无关，没登录也报）
func (a *App) ggyActive() bool {
	data, err := os.ReadFile(filepath.Join(a.Core.Paths.Panel, "ggy.json"))
	if err != nil {
		return false
	}
	var st struct {
		Enabled bool     `json:"enabled"`
		Tokens  []string `json:"tokens"`
	}
	return json.Unmarshal(data, &st) == nil && st.Enabled && len(st.Tokens) > 0
}

// AkileCDN 白名单开着、有 API Key、至少一个节点（与主控账户无关，没登录也报）
func (a *App) akileActive() bool {
	data, err := os.ReadFile(a.Core.Paths.AkileStatePath())
	if err != nil {
		return false
	}
	var st struct {
		Enabled bool              `json:"enabled"`
		Key     string            `json:"key"`
		Nodes   []json.RawMessage `json:"nodes"`
	}
	return json.Unmarshal(data, &st) == nil && st.Enabled && st.Key != "" && len(st.Nodes) > 0
}

// AkileNodesConfig：「从订阅添加节点」读哪份配置——内核在跑就是运行配置（实际在用的），没在跑就是选中的订阅（下次启动用的）
func (a *App) AkileNodesConfig() string {
	run := a.Core.Paths.ConfigPath()
	if a.Core.Status().Running {
		if _, err := os.Stat(run); err == nil {
			return run
		}
	}
	if id := a.Settings.Get().Profile; id != "" {
		if _, ok := a.Profiles.Get(id); ok {
			return a.Profiles.Path(id)
		}
	}
	return run
}

// ReportSoon：打开 po0 加白 / 添加自定义 po0 之后在后台补报一次白名单（同 OpenWrt 版），界面过一会儿就能看到每台的结果；
// 同定时上报，内核没在跑不报
func (a *App) ReportSoon() {
	if !a.Core.Status().Running {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if _, err := a.Core.Panel(ctx, "report"); err != nil {
			a.Log.Add("agent", "白名单上报失败："+err.Error())
		}
	}()
}

// timer = 定时那一轮：AkileCDN 只在出口 IP 变了时才写（一条规则只放一个 IP，多台设备都开时别每 10 分钟互相覆盖）
func (a *App) panelReport(ctx context.Context, timer bool) {
	before := fmt.Sprint(a.po0ReportURLs())
	if _, err := a.Core.Panel(ctx, "report"); err != nil {
		a.Log.Add("agent", "白名单上报失败："+err.Error())
	}
	if a.akileActive() {
		args := []string{"akile", "report"}
		if timer {
			args = append(args, "--timer")
		}
		if _, err := a.Core.Panel(ctx, args...); err != nil {
			a.Log.Add("agent", "AkileCDN 白名单上报失败："+err.Error())
		}
	}
	// 主控下发的 po0 列表变了：重载让直连规则跟上
	if fmt.Sprint(a.po0ReportURLs()) != before {
		a.Log.Add("agent", "po0 列表变了，重载")
		_ = a.Apply(ctx)
	}
}

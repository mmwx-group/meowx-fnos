// Package mesh：喵窝（主控内置的组网，Tailscale 协议）。照 OpenWrt 版 mesh_cli.uc：
// 主控说可用 → 申报要开放的局域网网段 → 没登录就向主控要一次性 key → tailscale up（--netfilter-mode=off）→ 自己写防火墙。
//
// 和 OpenWrt 版不同：tailscaled 在容器里由控制服务看管（内核网卡 tailscale0，状态在 /data/mesh）；防火墙见 firewall_linux.go。
package mesh

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mmwx-group/meowx-fnos/agent/internal/core"
	"github.com/mmwx-group/meowx-fnos/agent/internal/store"
)

const device = "tailscale0"

// Site：一个开放出去的网段（原网段 → 在喵窝里用的地址；冲突时映射）
type Site struct {
	Original   string `json:"original"`
	Advertised string `json:"advertised"`
	Mapped     bool   `json:"mapped"`
}

// State：落盘的喵窝状态（/data/mesh/state.json）
type State struct {
	RouterID   string   `json:"router_id"`
	ControlURL string   `json:"control_url,omitempty"`
	MagicDNS   string   `json:"magic_dns,omitempty"`
	Sites      []Site   `json:"sites,omitempty"`
	Advertised []string `json:"advertised,omitempty"`
	Hosts      []string `json:"hosts,omitempty"` // 主控 / 中继的域名：fake-ip 下要拿真实地址
	Error      string   `json:"error,omitempty"`
	AppliedAt  int64    `json:"applied_at,omitempty"`
}

type Manager struct {
	Dir      string // /data/mesh
	Bin      string // tailscaled / tailscale 所在目录
	Core     *core.Supervisor
	Log      *core.Ring
	Settings *store.SettingsStore
	// 主控 / 中继域名变了（透明代理开着时要重载，把它们加进 fake-ip-filter）
	OnHostsChanged func()

	mu       sync.Mutex // 一次只跑一个 apply / leave
	daemonMu sync.Mutex
	daemon   *exec.Cmd
	done     chan struct{}
}

func (m *Manager) socket() string { return filepath.Join(m.Dir, "tailscaled.sock") }

func (m *Manager) LoadState() State {
	var st State
	if data, err := os.ReadFile(filepath.Join(m.Dir, "state.json")); err == nil {
		_ = json.Unmarshal(data, &st)
	}
	return st
}

func (m *Manager) saveState(st State) {
	data, _ := json.MarshalIndent(st, "", "  ")
	_ = store.WriteFileAtomic(filepath.Join(m.Dir, "state.json"), data, 0o600)
}

// Hosts：给运行配置的 fake-ip-filter
func (m *Manager) Hosts() []string {
	if !m.Settings.Get().Mesh.Enabled {
		return nil
	}
	return m.LoadState().Hosts
}

// ---------- tailscaled ----------

func (m *Manager) startDaemon() error {
	m.daemonMu.Lock()
	defer m.daemonMu.Unlock()
	if m.daemon != nil {
		return nil
	}
	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		return err
	}
	cmd := exec.Command(filepath.Join(m.Bin, "tailscaled"),
		"--state="+filepath.Join(m.Dir, "tailscaled.state"),
		"--statedir="+m.Dir,
		"--socket="+m.socket(),
		"--tun="+device,
		"--port=41641")
	cmd.Env = append(os.Environ(), "TS_NO_LOGS_NO_SUPPORT=true")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return err
	}
	m.daemon = cmd
	done := make(chan struct{})
	m.done = done
	go func() {
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			// tailscaled 很啰嗦：只留错误与几类关键事件
			if strings.Contains(line, "rror") || strings.Contains(line, "Switching ipn state") || strings.Contains(line, "control:") && strings.Contains(line, "fail") {
				m.Log.Add("mesh", redact(line))
			}
		}
	}()
	go func() {
		_ = cmd.Wait()
		m.daemonMu.Lock()
		if m.daemon == cmd {
			m.daemon = nil
		}
		m.daemonMu.Unlock()
		close(done)
	}()
	return nil
}

func (m *Manager) stopDaemon() {
	m.daemonMu.Lock()
	cmd, done := m.daemon, m.done
	m.daemonMu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}

func (m *Manager) DaemonRunning() bool {
	m.daemonMu.Lock()
	defer m.daemonMu.Unlock()
	return m.daemon != nil
}

// ts 调 tailscale 命令行（经我们的 socket）
func (m *Manager) ts(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, filepath.Join(m.Bin, "tailscale"), append([]string{"--socket=" + m.socket()}, args...)...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

type tsStatus struct {
	BackendState string
	Self         *tsPeer
	Peer         map[string]*tsPeer
}

type tsPeer struct {
	ID            string
	HostName      string
	DNSName       string
	OS            string
	TailscaleIPs  []string
	Online        bool
	Relay         string
	CurAddr       string
	PrimaryRoutes []string
}

func (m *Manager) status(ctx context.Context) *tsStatus {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := m.ts(ctx, "status", "--json")
	if err != nil && !strings.HasPrefix(strings.TrimSpace(out), "{") {
		return nil
	}
	var st tsStatus
	if json.Unmarshal([]byte(out), &st) != nil {
		return nil
	}
	return &st
}

type tsPrefs struct {
	ControlURL      string
	AdvertiseRoutes []string
}

func (m *Manager) prefs(ctx context.Context) *tsPrefs {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := m.ts(ctx, "debug", "prefs")
	if err != nil {
		return nil
	}
	var p tsPrefs
	if json.Unmarshal([]byte(out), &p) != nil {
		return nil
	}
	return &p
}

func (m *Manager) waitDaemon(ctx context.Context, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if st := m.status(ctx); st != nil && st.BackendState != "" {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// ---------- 命令 ----------

// Apply：按设置入网或退出
func (m *Manager) Apply(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Settings.Get().Mesh.Enabled {
		return m.up(ctx)
	}
	m.down(ctx)
	return nil
}

func (m *Manager) fail(st State, msg string) error {
	st.Error = msg
	st.AppliedAt = time.Now().Unix()
	m.saveState(st)
	m.Log.Add("mesh", "应用失败："+msg)
	return errors.New(msg)
}

func (m *Manager) routerID(st *State) string {
	if st.RouterID == "" {
		var b [8]byte
		_, _ = rand.Read(b[:])
		st.RouterID = "fnos-" + hex.EncodeToString(b[:])
	}
	return st.RouterID
}

func (m *Manager) panel(ctx context.Context, args ...string) (map[string]any, error) {
	out, err := m.Core.Panel(ctx, append([]string{"mesh"}, args...)...)
	if err != nil {
		return nil, err
	}
	var obj map[string]any
	if err := json.Unmarshal(out, &obj); err != nil {
		return nil, fmt.Errorf("应答无法解析：%v", err)
	}
	return obj, nil
}

func (m *Manager) up(ctx context.Context) error {
	s := m.Settings.Get().Mesh
	st := m.LoadState()
	hostsBefore := append([]string(nil), st.Hosts...)
	if _, err := os.Stat(filepath.Join(m.Core.Paths.Panel, "panel.json")); err != nil {
		return m.fail(st, "还没有登录主控账户（在「订阅」页用主控的登录链接登录）")
	}
	if err := m.startDaemon(); err != nil {
		return m.fail(st, "tailscaled 起不来："+err.Error())
	}
	if !m.waitDaemon(ctx, 20*time.Second) {
		return m.fail(st, "tailscaled 没有启动（看日志）")
	}

	ps, err := m.panel(ctx, "status")
	if err != nil {
		return m.fail(st, "连不上主控："+err.Error())
	}
	if avail, _ := ps["available"].(bool); !avail {
		msg, _ := ps["message"].(string)
		if msg == "" {
			msg = "主控没有开放喵窝（或登录的不是管理员账号）"
		}
		return m.fail(st, msg)
	}
	control := strings.TrimRight(fmt.Sprint(ps["control_url"]), "/")
	if control == "" || control == "<nil>" {
		return m.fail(st, "主控没有给出控制地址")
	}
	st.ControlURL = control
	st.MagicDNS, _ = ps["magic_dns"].(string)

	// 先把主控域名加进 fake-ip-filter，tailscaled 才连得上主控（中继地址入网后再补）
	controlHosts := domainHosts([]string{control})
	st.Hosts = union(hostsBefore, controlHosts)
	m.saveState(st)
	m.hostsChanged(hostsBefore, st.Hosts)

	// 申报要开放的网段（整份），拿到每个网段在喵窝里用的地址
	id := m.routerID(&st)
	args := []string{"sites", "--router", id}
	if s.ForceMap {
		args = append(args, "--force-map")
	}
	args = append(args, s.Networks...)
	sr, err := m.panel(ctx, args...)
	if err != nil {
		return m.fail(st, "申报网段失败："+err.Error())
	}
	st.Sites, st.Advertised = nil, nil
	if list, ok := sr["sites"].([]any); ok {
		for _, it := range list {
			x, _ := it.(map[string]any)
			if x == nil || fmt.Sprint(x["router"]) != id {
				continue
			}
			site := Site{Original: fmt.Sprint(x["original"]), Advertised: fmt.Sprint(x["advertised"])}
			site.Mapped, _ = x["mapped"].(bool)
			st.Sites = append(st.Sites, site)
			st.Advertised = append(st.Advertised, site.Advertised)
		}
	}

	// 没登录、或者登录的是别的控制服务器：向主控要一次性 key 重新入网
	tst := m.status(ctx)
	pr := m.prefs(ctx)
	name := Hostname(s.Hostname)
	otherControl := pr != nil && pr.ControlURL != "" && strings.TrimRight(pr.ControlURL, "/") != control
	needLogin := tst == nil || otherControl
	if tst != nil {
		switch tst.BackendState {
		case "NeedsLogin", "NoState", "NeedsMachineAuth":
			needLogin = true
		}
	}
	upArgs := []string{"up", "--reset", "--timeout=60s", "--login-server=" + control, "--hostname=" + name,
		"--advertise-routes=" + strings.Join(st.Advertised, ","), "--accept-dns=false", "--accept-routes=false", "--netfilter-mode=off"}
	if needLogin {
		prev := ""
		if tst != nil && tst.Self != nil && regexp.MustCompile(`^[0-9]+$`).MatchString(tst.Self.ID) {
			prev = tst.Self.ID
		}
		er, err := m.panel(ctx, "enroll", name, prev)
		if err != nil {
			return m.fail(st, "向主控要入网 key 失败："+err.Error())
		}
		key, _ := er["auth_key"].(string)
		if key == "" {
			return m.fail(st, "主控没有给入网 key")
		}
		upArgs = append(upArgs, "--authkey="+key)
		if otherControl {
			upArgs = append(upArgs, "--force-reauth")
		}
		m.Log.Add("mesh", fmt.Sprintf("入网：%s → %s", name, control))
	}
	upCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if out, err := m.ts(upCtx, upArgs...); err != nil {
		return m.fail(st, "tailscale up 失败："+redact(strings.TrimSpace(out)))
	}

	if err := firewallApply(s, st.Sites); err != nil {
		m.Log.Add("mesh", "防火墙规则没下发成功："+err.Error())
	}

	// 入网后按现在的主控 + 中继重算
	hostsPhase1 := st.Hosts
	st.Hosts = union(controlHosts, m.relayHosts(ctx))
	st.Error = ""
	st.AppliedAt = time.Now().Unix()
	m.saveState(st)
	m.hostsChanged(hostsPhase1, st.Hosts)
	adv := "无"
	if len(st.Advertised) > 0 {
		adv = strings.Join(st.Advertised, "，")
	}
	m.Log.Add("mesh", "已入网，开放网段："+adv)
	return nil
}

func (m *Manager) down(ctx context.Context) {
	st := m.LoadState()
	if m.DaemonRunning() {
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		_, _ = m.ts(c, "down")
		cancel()
	}
	m.stopDaemon()
	firewallRemove()
	before := st.Hosts
	st.Hosts, st.Error = nil, ""
	st.AppliedAt = time.Now().Unix()
	m.saveState(st)
	m.hostsChanged(before, nil)
}

// Leave：把这台 NAS 从喵窝里删掉（主控里删设备、撤掉申报的网段、tailscale logout），并关掉开关
func (m *Manager) Leave(ctx context.Context) (warning string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.LoadState()
	if _, err := os.Stat(filepath.Join(m.Core.Paths.Panel, "panel.json")); err == nil {
		if m.DaemonRunning() {
			if ts := m.status(ctx); ts != nil && ts.Self != nil && regexp.MustCompile(`^[0-9]+$`).MatchString(ts.Self.ID) {
				if _, err := m.panel(ctx, "delete", ts.Self.ID); err != nil {
					warning = "主控里删设备失败：" + err.Error()
				}
			}
		}
		if st.RouterID != "" {
			_, _ = m.panel(ctx, "sites", "--router", st.RouterID)
		}
	}
	if m.DaemonRunning() {
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		_, _ = m.ts(c, "logout")
		cancel()
	}
	_, _ = m.Settings.Update(func(cur *store.Settings) error {
		cur.Mesh.Enabled = false
		return nil
	})
	m.down(ctx)
	st = m.LoadState()
	st.Sites, st.Advertised = nil, nil
	m.saveState(st)
	m.Log.Add("mesh", "已退出喵窝")
	return warning
}

// Shutdown：容器停止时（不 tailscale down：下次起来直接接着用）
func (m *Manager) Shutdown() {
	m.stopDaemon()
	firewallRemove()
}

// ---------- 状态（网页用） ----------

type PeerView struct {
	Name    string   `json:"name"`
	DNSName string   `json:"dns_name"`
	IP      string   `json:"ip"`
	OS      string   `json:"os"`
	Online  bool     `json:"online"`
	Relay   string   `json:"relay"`
	Direct  bool     `json:"direct"`
	Routes  []string `json:"routes"`
}

type View struct {
	Settings      store.Mesh `json:"settings"`
	Hostname      string     `json:"hostname"`
	State         State      `json:"state"`
	PanelLoggedIn bool       `json:"panel_logged_in"`
	Daemon        bool       `json:"daemon"`
	Backend       string     `json:"backend,omitempty"`
	Self          *PeerView  `json:"self,omitempty"`
	Peers         []PeerView `json:"peers,omitempty"`
	ControlURL    string     `json:"control_url,omitempty"`
	Advertised    []string   `json:"advertised,omitempty"`
}

func (m *Manager) View(ctx context.Context) View {
	s := m.Settings.Get().Mesh
	v := View{Settings: s, Hostname: Hostname(s.Hostname), State: m.LoadState(), Daemon: m.DaemonRunning()}
	if _, err := os.Stat(filepath.Join(m.Core.Paths.Panel, "panel.json")); err == nil {
		v.PanelLoggedIn = true
	}
	if !v.Daemon {
		return v
	}
	if st := m.status(ctx); st != nil {
		v.Backend = st.BackendState
		if st.Self != nil {
			p := peerView(st.Self)
			v.Self = &p
		}
		for _, p := range st.Peer {
			v.Peers = append(v.Peers, peerView(p))
		}
		sort.Slice(v.Peers, func(i, j int) bool { return v.Peers[i].Name < v.Peers[j].Name })
	}
	if pr := m.prefs(ctx); pr != nil {
		v.ControlURL, v.Advertised = pr.ControlURL, pr.AdvertiseRoutes
	}
	return v
}

func peerView(p *tsPeer) PeerView {
	v := PeerView{Name: p.HostName, DNSName: strings.TrimSuffix(p.DNSName, "."), OS: p.OS, Online: p.Online, Relay: p.Relay, Direct: p.CurAddr != "", Routes: p.PrimaryRoutes}
	if len(p.TailscaleIPs) > 0 {
		v.IP = p.TailscaleIPs[0]
	}
	return v
}

// ---------- 小工具 ----------

func (m *Manager) hostsChanged(before, after []string) {
	if strings.Join(sorted(before), ",") != strings.Join(sorted(after), ",") && m.OnHostsChanged != nil {
		m.OnHostsChanged()
	}
}

// relayHosts：中继（DERP）的域名
func (m *Manager) relayHosts(ctx context.Context) []string {
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := m.ts(c, "debug", "derp-map")
	if err != nil {
		return nil
	}
	var dm struct {
		Regions map[string]struct {
			Nodes []struct{ HostName string }
		}
	}
	if json.Unmarshal([]byte(out), &dm) != nil {
		return nil
	}
	var hosts []string
	for _, r := range dm.Regions {
		for _, n := range r.Nodes {
			hosts = append(hosts, n.HostName)
		}
	}
	return domainHosts(hosts)
}

// domainHosts：地址里的主机名，去掉 IP
func domainHosts(in []string) []string {
	var out []string
	for _, s := range in {
		h := s
		if u, err := url.Parse(s); err == nil && u.Hostname() != "" {
			h = u.Hostname()
		}
		if h != "" && net.ParseIP(h) == nil {
			out = append(out, strings.ToLower(h))
		}
	}
	return union(nil, out)
}

func union(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range append(append([]string{}, a...), b...) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func sorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

var keyRe = regexp.MustCompile(`(hskey|tskey)-[A-Za-z0-9_-]+`)

// 别把入网 key 写进日志 / 应答
func redact(s string) string { return keyRe.ReplaceAllString(s, "***") }

// Hostname：喵窝里的设备名（设置里写了就用，否则 NAS 的主机名），规范化成小写字母数字与横线
func Hostname(custom string) string {
	raw := custom
	if raw == "" {
		raw, _ = os.Hostname()
	}
	var b strings.Builder
	for _, r := range strings.ToLower(raw) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	name := strings.Trim(regexp.MustCompile(`-+`).ReplaceAllString(b.String(), "-"), "-")
	if name == "" {
		name = "meowx-nas"
	}
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}

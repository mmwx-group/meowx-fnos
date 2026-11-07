// Package store：设置与订阅档的落盘（都在 /data 下，JSON / YAML 文件，原子替换写）。
package store

import (
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Settings 是 NAS 版的全部设置，字段与缺省值照 OpenWrt 版的 /etc/config/meowx（meowx.conf）精简而来。
type Settings struct {
	Enabled         bool   `json:"enabled"`           // 内核开不开
	Profile         string `json:"profile"`           // 当前订阅档 ID
	AutoUpdateHours int    `json:"auto_update_hours"` // 订阅自动更新间隔，0 = 不自动
	TestProfile     bool   `json:"test_profile"`      // 启用新配置前先让内核试读一遍（坏配置不换上去）

	Mixin Mixin `json:"mixin"`
	Proxy Proxy `json:"proxy"`
	Mesh  Mesh  `json:"mesh"`
	Web   Web   `json:"web"`
}

// Mixin：盖在订阅上的那部分配置（同 mixin.uc）。空值不覆盖订阅里的。
type Mixin struct {
	LogLevel      string `json:"log_level"`
	Mode          string `json:"mode"` // rule / global / direct
	BlockQUIC     bool   `json:"block_quic"`
	IPv6          bool   `json:"ipv6"`
	UnifiedDelay  bool   `json:"unified_delay"`
	TCPConcurrent bool   `json:"tcp_concurrent"`

	AllowLAN    bool       `json:"allow_lan"`
	MixedPort   int        `json:"mixed_port"`
	HTTPPort    int        `json:"http_port"`
	SocksPort   int        `json:"socks_port"`
	RedirPort   int        `json:"redir_port"`
	TProxyPort  int        `json:"tproxy_port"`
	Auth        bool       `json:"authentication"` // 代理端口要不要密码
	Users       []AuthUser `json:"users"`
	SkipAuthLAN bool       `json:"skip_auth_lan"` // 本机与局域网来的不要密码（只有外面来的要）

	TUN     TUN     `json:"tun"`
	DNS     DNS     `json:"dns"`
	Sniffer Sniffer `json:"sniffer"`

	StoreSelected bool `json:"store_selected"`
	StoreFakeIP   bool `json:"store_fake_ip"`

	Rules []string `json:"rules"` // 自定义规则，排在订阅规则前面
	Extra string   `json:"extra"` // 额外混入的 YAML（OpenWrt 版的 mixin 文件内容），最后合并

	GeoAutoUpdate     bool `json:"geo_auto_update"`
	GeoUpdateInterval int  `json:"geo_update_interval"`
}

type AuthUser struct {
	Enabled  bool   `json:"enabled"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type TUN struct {
	Device    string   `json:"device"`
	Stack     string   `json:"stack"` // system / gvisor / mixed
	MTU       int      `json:"mtu"`
	DNSHijack []string `json:"dns_hijack"`
}

type DNS struct {
	Enabled          bool     `json:"enabled"`
	Listen           string   `json:"listen"`
	IPv6             bool     `json:"ipv6"`
	Mode             string   `json:"mode"` // fake-ip / redir-host
	FakeIPRange      string   `json:"fake_ip_range"`
	FakeIPFilter     []string `json:"fake_ip_filter"`
	FakeIPFilterMode string   `json:"fake_ip_filter_mode"`
	RespectRules     bool     `json:"respect_rules"`

	OverrideNameservers   bool               `json:"override_nameservers"` // 关着 = 用订阅里的上游
	DefaultNameserver     []string           `json:"default_nameserver"`
	ProxyServerNameserver []string           `json:"proxy_server_nameserver"`
	DirectNameserver      []string           `json:"direct_nameserver"`
	Nameserver            []string           `json:"nameserver"`
	Fallback              []string           `json:"fallback"`
	OverridePolicy        bool               `json:"override_policy"`
	NameserverPolicy      []NameserverPolicy `json:"nameserver_policy"`
	Hosts                 []Host             `json:"hosts"`
}

type NameserverPolicy struct {
	Enabled    bool     `json:"enabled"`
	Matcher    string   `json:"matcher"`
	Nameserver []string `json:"nameserver"`
}

type Host struct {
	Enabled bool     `json:"enabled"`
	Domain  string   `json:"domain"`
	IP      []string `json:"ip"`
}

type Sniffer struct {
	Enabled     bool     `json:"enabled"`
	ForceDomain []string `json:"force_domain"`
	SkipDomain  []string `json:"skip_domain"`
}

// Proxy：透明代理（同 OpenWrt 版的 proxy 段）。NAS 版的「本机」= NAS 自己，「局域网」= 旁路由。
type Proxy struct {
	Enabled        bool     `json:"enabled"`
	TCPMode        string   `json:"tcp_mode"`        // tun / tproxy / redirect
	UDPMode        string   `json:"udp_mode"`        // tun / tproxy / ""
	HostProxy      bool     `json:"host_proxy"`      // NAS 本机的流量
	ContainerProxy bool     `json:"container_proxy"` // NAS 上其它容器的流量
	LANProxy       bool     `json:"lan_proxy"`       // 旁路由：网关指向 NAS 的局域网设备
	LANInterfaces  []string `json:"lan_interfaces"`
	DNSHijack      bool     `json:"dns_hijack"`
	IPv6           bool     `json:"ipv6"`
	BypassChinaIP  bool     `json:"bypass_china_ip"`
	ReservedIP     []string `json:"reserved_ip"`
	ReservedIP6    []string `json:"reserved_ip6"`
	ProxyTCPPorts  string   `json:"proxy_tcp_ports"`
	ProxyUDPPorts  string   `json:"proxy_udp_ports"`
	KeepMinutes    int      `json:"keep_minutes"` // 安全阀：开启后几分钟内没确认就自动撤掉
}

// Mesh：喵窝（同 OpenWrt 版的 mesh 段）
type Mesh struct {
	Enabled    bool     `json:"enabled"`
	ForceMap   bool     `json:"force_map"`
	HostAccess bool     `json:"host_access"` // 喵窝里的设备能不能访问 NAS 本身
	Networks   []string `json:"networks"`    // 要开放的局域网网段
	Hostname   string   `json:"hostname"`
}

// Web：管理界面
type Web struct {
	Password string `json:"password"` // 空 = 不要密码（只建议在可信局域网里）
}

// Defaults 照 meowx.conf 的缺省值
func Defaults() Settings {
	return Settings{
		AutoUpdateHours: 24,
		TestProfile:     true,
		Mixin: Mixin{
			LogLevel:      "warning",
			Mode:          "rule",
			IPv6:          true,
			AllowLAN:      true,
			MixedPort:     7890,
			RedirPort:     7891,
			TProxyPort:    7892,
			Auth:          true,
			Users:         []AuthUser{{Enabled: true, Username: "meowx"}},
			SkipAuthLAN:   true,
			StoreSelected: true,
			StoreFakeIP:   true,
			TUN: TUN{
				Device:    "meowx",
				Stack:     "mixed",
				DNSHijack: []string{"tcp://any:53", "udp://any:53"},
			},
			DNS: DNS{
				Enabled:           true,
				Listen:            "[::]:1053",
				IPv6:              true,
				Mode:              "fake-ip",
				FakeIPRange:       "198.18.0.1/16",
				FakeIPFilter:      []string{"+.lan", "+.local"},
				DefaultNameserver: []string{"223.5.5.5", "223.6.6.6"},
				Nameserver:        []string{"https://223.5.5.5/dns-query", "https://223.6.6.6/dns-query"},
				NameserverPolicy: []NameserverPolicy{
					{Enabled: true, Matcher: "geosite:private,cn", Nameserver: []string{"https://223.5.5.5/dns-query", "https://223.6.6.6/dns-query"}},
					{Enabled: true, Matcher: "geosite:geolocation-!cn", Nameserver: []string{"https://1.1.1.1/dns-query", "https://8.8.8.8/dns-query"}},
				},
			},
			Sniffer: Sniffer{Enabled: true},
		},
		Mesh: Mesh{HostAccess: true},
		Proxy: Proxy{
			TCPMode:   "tun",
			UDPMode:   "tun",
			HostProxy: true,
			DNSHijack: true,
			IPv6:      false,
			ReservedIP: []string{
				"0.0.0.0/8", "10.0.0.0/8", "127.0.0.0/8", "100.64.0.0/10", "169.254.0.0/16",
				"172.16.0.0/12", "192.168.0.0/16", "224.0.0.0/4", "240.0.0.0/4",
			},
			ReservedIP6: []string{
				"::/128", "::1/128", "::ffff:0:0/96", "100::/64", "64:ff9b::/96", "2001::/32", "2001:10::/28",
				"2001:20::/28", "2001:db8::/32", "2002::/16", "fc00::/7", "fe80::/10", "ff00::/8",
			},
			ProxyTCPPorts: "0-65535",
			ProxyUDPPorts: "0-65535",
			KeepMinutes:   5,
		},
	}
}

// SettingsStore：内存里一份、改了就原子写盘
type SettingsStore struct {
	mu   sync.Mutex
	path string
	cur  Settings
}

func OpenSettings(dir string) (*SettingsStore, error) {
	s := &SettingsStore{path: filepath.Join(dir, "settings.json"), cur: Defaults()}
	data, err := os.ReadFile(s.path)
	if err == nil {
		// 先铺缺省值再解：老文件里没有的字段保持缺省
		if err := json.Unmarshal(data, &s.cur); err != nil {
			return nil, err
		}
	} else if os.IsNotExist(err) {
		// 第一次：代理端口的缺省账号给一个随机密码（外面来的连接要它，局域网免密）
		_, err = s.Update(func(cur *Settings) error {
			cur.Mixin.Users[0].Password = randomPassword()
			return nil
		})
		if err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	return s, nil
}

func (s *SettingsStore) Get() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur
}

// Update 在锁里改一份副本、写盘成功才换上
func (s *SettingsStore) Update(fn func(*Settings) error) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.cur
	if err := fn(&next); err != nil {
		return s.cur, err
	}
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return s.cur, err
	}
	if err := WriteFileAtomic(s.path, data, 0o600); err != nil {
		return s.cur, err
	}
	s.cur = next
	return next, nil
}

func randomPassword() string {
	const chars = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	var b [12]byte
	_, _ = rand.Read(b[:])
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b[:])
}

// WriteFileAtomic：先写临时文件再改名，断电也不会留下半截文件
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

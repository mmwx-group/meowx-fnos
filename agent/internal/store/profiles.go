package store

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mmwx-group/meowx-fnos/agent/internal/platform"
	"gopkg.in/yaml.v3"
)

// Profile：一份订阅或本地上传的配置（同 OpenWrt 版的 subscription 段 + 上传的文件）
type Profile struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url,omitempty"`        // 空 = 本地上传，不能刷新
	InfoURL   string `json:"info_url,omitempty"`   // 单独取用量的地址（可选）
	UserAgent string `json:"user_agent,omitempty"` // 空 / "meowx" = 官方客户端 UA
	FromPanel bool   `json:"from_panel,omitempty"` // 从主控账户导入的（退出登录时删）

	UpdatedAt      int64  `json:"updated_at,omitempty"`
	UpdateInterval int    `json:"update_interval,omitempty"` // 订阅建议的刷新间隔（小时）
	Used           int64  `json:"used,omitempty"`
	Total          int64  `json:"total,omitempty"`
	Expire         int64  `json:"expire,omitempty"`
	Error          string `json:"error,omitempty"` // 上次更新失败的原因（旧副本保留）

	// 主控给官方客户端的订阅键、内容指纹、实时通道地址与只读凭证（应答头 mmwx-sub-key / mmwx-sub-rev / mmwx-realtime-url /
	// mmwx-realtime，规格 miaomiaowuC/Design/entry-realtime.md §2.3 / §3.2）。每次下载按应答头存，没有就清空（别家机场、旧主控）
	SubKey      string `json:"sub_key,omitempty"`
	SubRev      string `json:"sub_rev,omitempty"`
	RealtimeURL string `json:"realtime_url,omitempty"`
	Realtime    string `json:"realtime,omitempty"`
}

type pushRefreshKey struct{}

// WithPushRefresh：主控推订阅变更触发的重拉。下载时带请求头 mmwx-refresh: push，主控据此不发「订阅已拉取」通知、短链限速不计数
func WithPushRefresh(ctx context.Context) context.Context {
	return context.WithValue(ctx, pushRefreshKey{}, true)
}

// ProfileStore：/data/profiles/index.json + <id>.yaml
type ProfileStore struct {
	mu   sync.Mutex
	dir  string
	list []Profile
	// 同一份订阅同时只更新一次（自动更新与手动刷新不打架）
	updating sync.Map
	// 生成 UA 用
	Version string
	// 下载时用的 HTTP 客户端（直连，不走系统代理：本机透明代理开着时它自己的流量要绕开，见 api 里的设置）
	Client *http.Client
	// 新内容换上去之前的检查（内核试读），nil = 只做基本检查
	Validate func(ctx context.Context, yamlData []byte) error
	// 拉这份订阅时 X-MeowX-Mesh 头的值（"on" / "off"；空 = 不带，别家机场的订阅），见 MeshHeaderValue。nil = 一律不带
	MeshHeader func(p Profile) string
}

func OpenProfiles(dir string) (*ProfileStore, error) {
	s := &ProfileStore{dir: dir, Client: &http.Client{Timeout: 2 * time.Minute}}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err == nil {
		if err := json.Unmarshal(data, &s.list); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return s, nil
}

func (s *ProfileStore) List() []Profile {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Profile(nil), s.list...)
}

func (s *ProfileStore) Get(id string) (Profile, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.list {
		if p.ID == id {
			return p, true
		}
	}
	return Profile{}, false
}

// Path：订阅档内容的文件
func (s *ProfileStore) Path(id string) string { return filepath.Join(s.dir, id+".yaml") }

func (s *ProfileStore) saveLocked() error {
	data, err := json.MarshalIndent(s.list, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(filepath.Join(s.dir, "index.json"), data, 0o600)
}

func (s *ProfileStore) upsert(p Profile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.list {
		if s.list[i].ID == p.ID {
			s.list[i] = p
			return s.saveLocked()
		}
	}
	s.list = append(s.list, p)
	return s.saveLocked()
}

// Delete 删掉一份（内容文件一起删）
func (s *ProfileStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.list[:0]
	for _, p := range s.list {
		if p.ID != id {
			out = append(out, p)
		}
	}
	s.list = out
	_ = os.Remove(s.Path(id))
	return s.saveLocked()
}

// Rename 改名
func (s *ProfileStore) Edit(id string, fn func(*Profile)) (Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.list {
		if s.list[i].ID == id {
			fn(&s.list[i])
			return s.list[i], s.saveLocked()
		}
	}
	return Profile{}, errors.New("订阅不存在")
}

func newID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// AddSubscription 添加并立刻下载一次（下载失败就不留这一条）
func (s *ProfileStore) AddSubscription(ctx context.Context, url, name string, fromPanel bool) (Profile, error) {
	url = strings.TrimSpace(url)
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return Profile{}, errors.New("订阅地址要以 http:// 或 https:// 开头")
	}
	for _, p := range s.List() {
		if p.URL == url {
			// 同一个地址再加一次 = 刷新它
			return s.Update(ctx, p.ID)
		}
	}
	p := Profile{ID: newID(), Name: strings.TrimSpace(name), URL: url, FromPanel: fromPanel}
	if err := s.download(ctx, &p); err != nil {
		return Profile{}, err
	}
	if p.Name == "" {
		p.Name = defaultName(url)
	}
	return p, s.upsert(p)
}

// AddLocal 上传的配置文件
func (s *ProfileStore) AddLocal(ctx context.Context, name string, data []byte) (Profile, error) {
	if err := s.check(ctx, data); err != nil {
		return Profile{}, err
	}
	p := Profile{ID: newID(), Name: strings.TrimSpace(name), UpdatedAt: time.Now().Unix()}
	if p.Name == "" {
		p.Name = "本地配置"
	}
	if err := WriteFileAtomic(s.Path(p.ID), data, 0o600); err != nil {
		return Profile{}, err
	}
	return p, s.upsert(p)
}

// Update 重新下载。失败时旧副本与用量保留，原因记在 Error。
func (s *ProfileStore) Update(ctx context.Context, id string) (Profile, error) {
	if _, busy := s.updating.LoadOrStore(id, true); busy {
		return Profile{}, errors.New("这份订阅正在更新")
	}
	defer s.updating.Delete(id)
	p, ok := s.Get(id)
	if !ok {
		return Profile{}, errors.New("订阅不存在")
	}
	if p.URL == "" {
		return p, errors.New("本地配置不能刷新")
	}
	err := s.download(ctx, &p)
	if err != nil {
		p.Error = err.Error()
	}
	if e := s.upsert(p); e != nil && err == nil {
		err = e
	}
	return p, err
}

func (s *ProfileStore) userAgent(p Profile) string {
	if p.UserAgent != "" && p.UserAgent != "meowx" {
		return p.UserAgent
	}
	// 同客户端：mihomo/1.19.0 打头（主控与机场按它给完整的 Clash YAML），MeowX/<版本> (<系统>) 是官方客户端标记
	return fmt.Sprintf("mihomo/1.19.0 MeowX/%s (%s)", s.Version, platform.Current().UAOS) // 飞牛版 (fnOS)
}

var userinfoRe = regexp.MustCompile(`(?i)(upload|download|total|expire)=(\d+)`)

func (s *ProfileStore) fetch(ctx context.Context, url, ua, mesh string) ([]byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", ua)
	if push, _ := ctx.Value(pushRefreshKey{}).(bool); push {
		req.Header.Set("mmwx-refresh", "push")
	}
	if mesh != "" {
		req.Header.Set("X-MeowX-Mesh", mesh)
	}
	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, resp.Header, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	return body, resp.Header, err
}

// fallbackUserAgent：订阅用官方 UA 拿到的不是 Clash 配置时再试一次的 UA。不少机场面板只认 UA 里带 clash 的客户端，
// 认不出就回节点链接列表（V2Ray / Shadowrocket 格式）；FlClash 等客户端的 UA 里带 clash，所以同一个订阅在它们那里能导入
const fallbackUserAgent = "clash.meta"

func (s *ProfileStore) download(ctx context.Context, p *Profile) error {
	ua := s.userAgent(*p)
	// 登录的主控（含它的订阅域名）的订阅：告诉主控白名单直连生效没有，on 时主控给喵窝版节点（规格 meshnet-whitelist-direct.md §B2）
	mesh := ""
	if s.MeshHeader != nil {
		mesh = s.MeshHeader(*p)
	}
	body, header, err := s.fetch(ctx, p.URL, ua, mesh)
	if err != nil {
		return fmt.Errorf("下载失败：%w", err)
	}
	// 用的是缺省（官方）UA、拿到的又不是 Clash 配置：换 clash.meta 再拉一次，拿到 Clash 配置就用它。
	// 主控订阅认得官方 UA，第一次就是 Clash 配置，不受影响；用户自己填了 UA 的照填的来，不替他换
	if clashShape(body) != nil && (p.UserAgent == "" || p.UserAgent == "meowx") {
		if b2, h2, err2 := s.fetch(ctx, p.URL, fallbackUserAgent, mesh); err2 == nil && clashShape(b2) == nil {
			body, header = b2, h2
		}
	}
	if err := s.check(ctx, body); err != nil {
		return err
	}
	info := header.Get("Subscription-Userinfo")
	if p.InfoURL != "" {
		if _, h, err := s.fetch(ctx, p.InfoURL, ua, ""); err == nil && h.Get("Subscription-Userinfo") != "" {
			info = h.Get("Subscription-Userinfo")
		}
	}
	if err := WriteFileAtomic(s.Path(p.ID), body, 0o600); err != nil {
		return err
	}
	p.Used, p.Total, p.Expire = 0, 0, 0
	var up, down int64
	for _, m := range userinfoRe.FindAllStringSubmatch(info, -1) {
		v, _ := strconv.ParseInt(m[2], 10, 64)
		switch strings.ToLower(m[1]) {
		case "upload":
			up = v
		case "download":
			down = v
		case "total":
			p.Total = v
		case "expire":
			p.Expire = v
		}
	}
	p.Used = up + down
	// 订阅名跟着主控 / 机场给的 profile-title 走（可能是 base64:…），同客户端
	if t := strings.TrimSpace(header.Get("Profile-Title")); t != "" {
		if strings.HasPrefix(t, "base64:") {
			if dec, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(t, "base64:")); err == nil {
				t = strings.TrimSpace(string(dec))
			}
		}
		if t != "" {
			p.Name = t
		}
	} else if p.Name == "" {
		p.Name = filenameFromDisposition(header.Get("Content-Disposition"))
	}
	if n, err := strconv.Atoi(strings.TrimSpace(header.Get("Profile-Update-Interval"))); err == nil {
		p.UpdateInterval = n
	}
	p.SubKey = strings.TrimSpace(header.Get("Mmwx-Sub-Key"))
	p.SubRev = strings.TrimSpace(header.Get("Mmwx-Sub-Rev"))
	p.RealtimeURL = strings.TrimSpace(header.Get("Mmwx-Realtime-Url"))
	p.Realtime = strings.TrimSpace(header.Get("Mmwx-Realtime"))
	p.UpdatedAt = time.Now().Unix()
	p.Error = ""
	return nil
}

// check：proxies 是非空列表，或 proxy-providers 是非空映射（同 OpenWrt 版）；再交给内核试读
// clashShape：proxies 是非空列表，或 proxy-providers 是非空映射（同 OpenWrt 版）
func clashShape(data []byte) error {
	var doc struct {
		Proxies   []any          `yaml:"proxies"`
		Providers map[string]any `yaml:"proxy-providers"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil || (len(doc.Proxies) == 0 && len(doc.Providers) == 0) {
		if looksLikeLinkList(data) {
			return errors.New("订阅返回的是节点链接（V2Ray / Shadowrocket 格式），不是 Clash 配置：机场没认出客户端，" +
				"可以在这个订阅的 User-Agent 里填机场支持的客户端（如 clash-verge、ClashMetaForAndroid）再试")
		}
		if err != nil {
			return errors.New("不是 Clash 的 YAML 配置")
		}
		return errors.New("配置里没有节点（proxies / proxy-providers 都是空的）")
	}
	return nil
}

var linkSchemes = []string{"vmess://", "vless://", "ss://", "ssr://", "trojan://", "hysteria2://", "hy2://", "hysteria://", "tuic://", "anytls://", "wireguard://", "socks://", "snell://"}

// looksLikeLinkList：内容是节点分享链接列表，或者整段 base64 解出来是（机场给不认识的客户端的通用格式）
func looksLikeLinkList(data []byte) bool {
	text := strings.TrimSpace(string(data))
	if dec, err := base64.StdEncoding.DecodeString(text); err == nil {
		text = string(dec)
	} else if dec, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(text, "=")); err == nil {
		text = string(dec)
	}
	for _, line := range strings.SplitN(text, "\n", 8) {
		line = strings.ToLower(strings.TrimSpace(line))
		for _, scheme := range linkSchemes {
			if strings.HasPrefix(line, scheme) {
				return true
			}
		}
	}
	return false
}

func (s *ProfileStore) check(ctx context.Context, data []byte) error {
	if err := clashShape(data); err != nil {
		return err
	}
	if s.Validate != nil {
		if err := s.Validate(ctx, data); err != nil {
			return fmt.Errorf("内核检查没通过：%w", err)
		}
	}
	return nil
}

var dispositionRe = regexp.MustCompile(`(?i)filename\*?=(?:UTF-8'')?"?([^";]+)"?`)

func filenameFromDisposition(v string) string {
	m := dispositionRe.FindStringSubmatch(v)
	if m == nil {
		return ""
	}
	name := m[1]
	if dec, err := urlUnescape(name); err == nil {
		name = dec
	}
	return strings.TrimSuffix(strings.TrimSuffix(name, ".yaml"), ".yml")
}

func urlUnescape(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			v, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
			if err != nil {
				return s, err
			}
			b.WriteByte(byte(v))
			i += 2
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String(), nil
}

func defaultName(url string) string {
	host := strings.TrimPrefix(strings.TrimPrefix(url, "https://"), "http://")
	if i := strings.IndexAny(host, "/?#"); i >= 0 {
		host = host[:i]
	}
	return host
}

// Due：该自动更新的（按设置的间隔；订阅给了更短的建议间隔就用它）
func (s *ProfileStore) Due(hours int, now time.Time) []string {
	var ids []string
	for _, p := range s.List() {
		if p.URL == "" {
			continue
		}
		h := hours
		if p.UpdateInterval > 0 && (h == 0 || p.UpdateInterval < h) {
			h = p.UpdateInterval
		}
		if h <= 0 {
			continue
		}
		if now.Sub(time.Unix(p.UpdatedAt, 0)) >= time.Duration(h)*time.Hour {
			ids = append(ids, p.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

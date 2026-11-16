// Package proxyserver：「绕过节点服务器地址」（透明代理 → 绕过，同 OpenWrt 版的 proxy_server.uc，照 OpenClash 的「绕过服务器地址」）。
// 当前配置里各节点的 server（proxies、proxy-providers 的缓存文件与 inline 里的）不进透明代理——局域网设备、NAS 本机或
// 别的容器自己也开着代理客户端时，它连节点的流量不会被 NAS 再代理一层。域名经内核的 /dns/query 解析（内核起来以后、每 30 分钟）。
package proxyserver

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

var domainPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)+$`)

func stripBrackets(s string) string {
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		return s[1 : len(s)-1]
	}
	return s
}

// Kind："v4" / "v6" / "domain"；认不出（空、单个标签的本机名、带非法字符）给 ""
func Kind(server string) string {
	s := stripBrackets(strings.TrimSpace(server))
	if s == "" {
		return ""
	}
	if ip := net.ParseIP(s); ip != nil {
		if ip.To4() != nil {
			return "v4"
		}
		return "v6"
	}
	if domainPattern.MatchString(s) {
		return "domain"
	}
	return ""
}

func serversOf(list any) []string {
	items, _ := list.([]any)
	var out []string
	for _, item := range items {
		p, _ := item.(map[string]any)
		if s, ok := p["server"].(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

// ProviderFile：proxy-provider 的缓存文件在哪。写了 path 按内核数据目录解（内核以 -d <home> 起）；
// 没写 path 的同内核的缺省位置 <home>/proxies/<url 的 md5>
func ProviderFile(home string, p map[string]any) string {
	if path, _ := p["path"].(string); path != "" {
		if filepath.IsAbs(path) {
			return path
		}
		return filepath.Join(home, strings.TrimPrefix(path, "./"))
	}
	if u, _ := p["url"].(string); u != "" {
		sum := md5.Sum([]byte(u))
		return filepath.Join(home, "proxies", hex.EncodeToString(sum[:]))
	}
	return ""
}

// Collect 收齐配置里各节点的 server（去重、保持顺序）。home = "" 时只收 proxies 与 inline provider
func Collect(cfg map[string]any, home string) []string {
	all := serversOf(cfg["proxies"])
	providers, _ := cfg["proxy-providers"].(map[string]any)
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p, _ := providers[name].(map[string]any)
		if p == nil {
			continue
		}
		if p["type"] == "inline" {
			all = append(all, serversOf(p["payload"])...)
		} else if home != "" {
			all = append(all, serversOf(readProvider(ProviderFile(home, p)))...)
		}
	}
	return dedupe(all)
}

// 缓存文件解成节点列表；文件不在、不是 YAML（如 base64 的节点链接列表）都给 nil
func readProvider(path string) any {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc map[string]any
	if yaml.Unmarshal(data, &doc) != nil {
		return nil
	}
	return doc["proxies"]
}

// Classify 分成 IPv4 / IPv6 / 要解析的域名（地址规整成标准写法，域名小写）
func Classify(servers []string) (ip4, ip6, domains []string) {
	for _, s := range servers {
		v := stripBrackets(strings.TrimSpace(s))
		switch Kind(s) {
		case "v4":
			ip4 = append(ip4, net.ParseIP(v).To4().String())
		case "v6":
			ip6 = append(ip6, net.ParseIP(v).String())
		case "domain":
			domains = append(domains, strings.ToLower(v))
		}
	}
	return dedupe(ip4), dedupe(ip6), dedupe(domains)
}

// PatchFakeIPFilter：fake-ip 模式、过滤名单是黑名单（缺省）时，把节点域名并进 dns.fake-ip-filter——局域网设备 / NAS 本机向内核
// 查节点域名时拿到真实地址，连过去才命中绕过（拿着假地址照样进透明代理）。改了回 true
func PatchFakeIPFilter(cfg map[string]any, domains []string) bool {
	dns, _ := cfg["dns"].(map[string]any)
	if dns == nil || dns["enhanced-mode"] != "fake-ip" || len(domains) == 0 {
		return false
	}
	if mode, _ := dns["fake-ip-filter-mode"].(string); mode != "" && mode != "blacklist" {
		return false
	}
	list, _ := dns["fake-ip-filter"].([]any)
	seen := map[string]bool{}
	for _, x := range list {
		if s, ok := x.(string); ok {
			seen[s] = true
		}
	}
	added := false
	for _, d := range domains {
		if !seen[d] {
			seen[d] = true
			list = append(list, d)
			added = true
		}
	}
	if added {
		dns["fake-ip-filter"] = list
	}
	return added
}

// AnswerIPs：内核 /dns/query 的应答里取地址：A 只要 IPv4、AAAA 只要 IPv6，CNAME 等跳过
func AnswerIPs(reply []byte, qtype string) []string {
	var r struct {
		Answer []struct {
			Data string `json:"data"`
		} `json:"Answer"`
	}
	if json.Unmarshal(reply, &r) != nil {
		return nil
	}
	var out []string
	for _, a := range r.Answer {
		ip := net.ParseIP(a.Data)
		if ip == nil {
			continue
		}
		if v4 := ip.To4(); v4 != nil && qtype == "A" {
			out = append(out, v4.String())
		} else if v4 == nil && qtype == "AAAA" {
			out = append(out, ip.String())
		}
	}
	return out
}

// API：内核的外部控制接口
type API struct {
	Base   string // http://127.0.0.1:9090
	Secret string
	Client *http.Client
}

func (a API) get(ctx context.Context, path string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.Base+path, nil)
	if err != nil {
		return 0, nil, err
	}
	if a.Secret != "" {
		req.Header.Set("Authorization", "Bearer "+a.Secret)
	}
	client := a.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, body, err
}

// WaitReady 等外部控制接口通（密钥不对回 401 也算通了），最多 wait
func (a API) WaitReady(ctx context.Context, wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	for {
		c, cancel := context.WithTimeout(ctx, 2*time.Second)
		code, _, err := a.get(c, "/version")
		cancel()
		if err == nil && (code == http.StatusOK || code == http.StatusUnauthorized) {
			return true
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Second):
		}
	}
}

// Resolve 域名逐个经内核解析（A + AAAA），至多 16 个同时。返回解析出来的地址与一个地址都没有的域名
func (a API) Resolve(ctx context.Context, domains []string) (ip4, ip6, unresolved []string) {
	type task struct{ domain, qtype string }
	var tasks []task
	for _, d := range domains {
		tasks = append(tasks, task{d, "A"}, task{d, "AAAA"})
	}
	results := make([][]string, len(tasks))
	sem := make(chan struct{}, 16)
	var wg sync.WaitGroup
	for i, t := range tasks {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, t task) {
			defer wg.Done()
			defer func() { <-sem }()
			c, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			code, body, err := a.get(c, "/dns/query?name="+url.QueryEscape(t.domain)+"&type="+t.qtype)
			if err == nil && code == http.StatusOK {
				results[i] = AnswerIPs(body, t.qtype)
			}
		}(i, t)
	}
	wg.Wait()
	resolved := map[string]bool{}
	for i, t := range tasks {
		if t.qtype == "A" {
			ip4 = append(ip4, results[i]...)
		} else {
			ip6 = append(ip6, results[i]...)
		}
		if len(results[i]) > 0 {
			resolved[t.domain] = true
		}
	}
	for _, d := range domains {
		if !resolved[d] {
			unresolved = append(unresolved, d)
		}
	}
	return dedupe(ip4), dedupe(ip6), unresolved
}

func dedupe(list []string) []string {
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

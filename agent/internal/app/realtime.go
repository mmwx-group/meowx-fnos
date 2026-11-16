package app

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/mmwx-group/meowx-fnos/agent/internal/store"
)

// 主控实时通道（入口即时切换 + 订阅自动更新，规格 miaomiaowuC/Design/entry-realtime.md §6 / §7）的控制服务一侧。
// 入口覆盖、加密通道、回执、落盘都在内核里（meowx-core component/panelrt）；这里把通道配置交给内核（PUT /meowx/realtime），
// 并接内核的订阅变更回调（POST /api/internal/subscription-changed/<hookToken>）重拉订阅。

// RealtimeChannel / realtimeSub：PUT /meowx/realtime 的正文
type RealtimeChannel struct {
	Base  string `json:"base"`
	Token string `json:"token,omitempty"`
	RT    string `json:"rt,omitempty"`
}

type realtimeSub struct {
	Base string `json:"base"`
	Key  string `json:"key"`
	Rev  string `json:"rev"`
}

type realtimeState struct {
	mu      sync.Mutex
	pending *time.Timer
	token   string // 内核回调地址里的随机凭证（控制服务每次启动换一个）
}

// hostOf：主控地址的主机名（小写）。登录的主控地址与订阅应答头给的 master_url 写法可能不同，一律按主机名比
func hostOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// RealtimePlan：通道计划。登录的主控用登录态；没登录的主控每个连一条只读通道（同一主控多份订阅时优先在用的那份的凭证）。
// subs 告诉内核本地手上每份订阅的 rev，免得它连上时把没变的也通知一遍
func RealtimePlan(loginBase, loginToken string, profiles []store.Profile, active string) ([]RealtimeChannel, []realtimeSub) {
	var channels []RealtimeChannel
	loginHost := ""
	if loginBase != "" && loginToken != "" {
		channels = append(channels, RealtimeChannel{Base: loginBase, Token: loginToken})
		loginHost = hostOf(loginBase)
	}
	rt := map[string]RealtimeChannel{}
	var order []string
	var subs []realtimeSub
	for _, p := range profiles {
		host := hostOf(p.RealtimeURL)
		if host == "" {
			continue
		}
		base := p.RealtimeURL
		if host == loginHost {
			base = loginBase
		}
		if p.SubKey != "" && p.SubRev != "" {
			subs = append(subs, realtimeSub{Base: base, Key: p.SubKey, Rev: p.SubRev})
		}
		if host == loginHost || p.Realtime == "" {
			continue
		}
		if _, seen := rt[host]; !seen {
			order = append(order, host)
			rt[host] = RealtimeChannel{Base: p.RealtimeURL, RT: p.Realtime}
		} else if p.ID == active {
			rt[host] = RealtimeChannel{Base: p.RealtimeURL, RT: p.Realtime}
		}
	}
	for _, host := range order {
		channels = append(channels, rt[host])
	}
	return channels, subs
}

// ProfilesToRefresh：内核通知「这个主控的这份订阅变了」后要重拉的订阅：同一主控来的（按实时通道地址或订阅地址的主机名认）、
// 键相符（键为空 = 这个主控来的全部）且 rev 与本地不同
func ProfilesToRefresh(profiles []store.Profile, base, key, rev string) []string {
	host := hostOf(base)
	if host == "" {
		return nil
	}
	var ids []string
	for _, p := range profiles {
		if p.URL == "" || (hostOf(p.RealtimeURL) != host && hostOf(p.URL) != host) {
			continue
		}
		if key != "" && (p.SubKey != key || (rev != "" && p.SubRev == rev)) {
			continue
		}
		ids = append(ids, p.ID)
	}
	return ids
}

func (a *App) realtimeHookToken() string {
	a.rt.mu.Lock()
	defer a.rt.mu.Unlock()
	if a.rt.token == "" {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		a.rt.token = hex.EncodeToString(b)
	}
	return a.rt.token
}

// RealtimeHookTokenOK：回调地址里的凭证对不对（常量时间比较）
func (a *App) RealtimeHookTokenOK(token string) bool {
	want := a.realtimeHookToken()
	return token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(want)) == 1
}

// KickRealtime：过 1 秒把通道配置交给内核（登录 / 退出、订阅增删改后都调，挤在一起的只做一次）
func (a *App) KickRealtime() {
	a.rt.mu.Lock()
	defer a.rt.mu.Unlock()
	if a.rt.pending != nil {
		a.rt.pending.Stop()
	}
	a.rt.pending = time.AfterFunc(time.Second, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		a.applyRealtime(ctx)
	})
}

// applyRealtime：内核在跑时把通道交给它；内核刚起来时等控制接口就绪（最多 30 秒）
func (a *App) applyRealtime(ctx context.Context) {
	if !a.Core.Status().Running {
		return
	}
	ready := false
	for i := 0; i < 30 && ctx.Err() == nil; i++ {
		if a.Core.Ping(ctx) == nil {
			ready = true
			break
		}
		time.Sleep(time.Second)
	}
	if !ready {
		a.Log.Add("agent", "主控实时通道：内核的控制接口不通，这次没交给内核")
		return
	}
	var login struct {
		Base  string `json:"base"`
		Token string `json:"token"`
	}
	if out, err := a.Core.Panel(ctx, "realtime-config"); err == nil {
		_ = json.Unmarshal(out, &login)
	}
	channels, subs := RealtimePlan(login.Base, login.Token, a.Profiles.List(), a.Settings.Get().Profile)
	if len(channels) == 0 {
		_, _ = a.Core.Call(ctx, http.MethodDelete, "/meowx/realtime", nil)
		return
	}
	port := a.UIPort
	if port == "" {
		port = "7899"
	}
	body := map[string]any{
		"channels": channels,
		"hook":     map[string]any{"url": fmt.Sprintf("http://127.0.0.1:%s/api/internal/subscription-changed/%s", port, a.realtimeHookToken())},
		"subs":     subs,
	}
	code, err := a.Core.Call(ctx, http.MethodPut, "/meowx/realtime", body)
	var names []string
	for _, c := range channels {
		kind := "只读"
		if c.Token != "" {
			kind = "账户"
		}
		names = append(names, hostOf(c.Base)+"（"+kind+"）")
	}
	switch {
	case err != nil:
		a.Log.Add("agent", "主控实时通道：交给内核失败："+err.Error())
	case code != http.StatusOK:
		a.Log.Add("agent", fmt.Sprintf("主控实时通道：内核不认（HTTP %d），内核版本可能过旧", code))
	default:
		a.Log.Add("agent", "主控实时通道："+strings.Join(names, "、"))
	}
}

// ClearRealtimeEntries：退出登录时清掉这个主控推来的入口覆盖
func (a *App) ClearRealtimeEntries(ctx context.Context, base string) {
	if base == "" || !a.Core.Status().Running {
		return
	}
	_, _ = a.Core.Call(ctx, http.MethodDelete, "/meowx/entries?source="+url.QueryEscape(base), nil)
}

// OnSubscriptionChanged：内核的订阅变更回调。逐个重拉相符的订阅（推送触发，带 mmwx-refresh: push），在用的那份变了就重新套用
func (a *App) OnSubscriptionChanged(base, key, rev string) {
	ids := ProfilesToRefresh(a.Profiles.List(), base, key, rev)
	if len(ids) == 0 {
		return
	}
	note := ""
	if key != "" {
		note = "（" + key + "）"
	}
	a.Log.Add("agent", fmt.Sprintf("主控 %s 的订阅变了%s，重拉 %d 份", hostOf(base), note, len(ids)))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	for _, id := range ids {
		p, err := a.Profiles.Update(store.WithPushRefresh(ctx), id)
		if err != nil {
			a.Log.Add("agent", fmt.Sprintf("重拉订阅「%s」失败：%v", p.Name, err))
			continue
		}
		s := a.Settings.Get()
		if s.Enabled && s.Profile == id {
			_ = a.Apply(ctx)
		}
	}
	a.KickRealtime()
}

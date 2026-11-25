package store

// 喵窝白名单直连（规格 miaomiaowuC/Design/meshnet-whitelist-direct.md §B2）：拉登录的主控的订阅时带 X-MeowX-Mesh: on|off，
// 主控据此给不给「喵窝版」节点。只发给登录的主控与它的订阅域名，别家机场（包括别的妙妙屋主控）的订阅不带。

import (
	"net/url"
	"strings"
)

// PanelHosts：登录的主控（panel.json 的 base = 主控地址、sub_base = 订阅域名；老内核没有 sub_base）
type PanelHosts struct {
	Base    string `json:"base"`
	SubBase string `json:"sub_base"`
}

func hostOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// IsPanelSubscription：这份订阅是不是登录的主控的——订阅地址的主机是主控域名或订阅域名，或者上次拉取时主控给的实时通道地址
// （mmwx-realtime-url，订阅域名与主控不同时也是主控地址）就是这个主控。从主控账户导入的（FromPanel）同样算
// （退出登录时会删掉，登录着时一定是这个主控的）
func IsPanelSubscription(p Profile, panel PanelHosts) bool {
	base := hostOf(panel.Base)
	if base == "" || p.URL == "" {
		return false
	}
	if p.FromPanel {
		return true
	}
	if h := hostOf(p.URL); h != "" && (h == base || h == hostOf(panel.SubBase)) {
		return true
	}
	return hostOf(p.RealtimeURL) == base
}

// MeshHeaderValue：拉这份订阅时 X-MeowX-Mesh 头的值；不是登录主控的订阅回空（不带这个头）
func MeshHeaderValue(p Profile, panel PanelHosts, on bool) string {
	if !IsPanelSubscription(p, panel) {
		return ""
	}
	if on {
		return "on"
	}
	return "off"
}

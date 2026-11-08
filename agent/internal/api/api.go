// Package api：网页界面用的 HTTP API（接口清单见 docs/api.md）。
package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/mmwx-group/meowx-fnos/agent/internal/app"
	"github.com/mmwx-group/meowx-fnos/agent/internal/core"
	"github.com/mmwx-group/meowx-fnos/agent/internal/hijack"
	"github.com/mmwx-group/meowx-fnos/agent/internal/store"
)

type Server struct {
	App *app.App
	Web fs.FS // 网页界面静态文件
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.putSettings)
	mux.HandleFunc("POST /api/service", s.service)
	mux.HandleFunc("GET /api/proxy", s.proxyStatus)
	mux.HandleFunc("POST /api/proxy/keep", s.proxyKeep)
	mux.HandleFunc("GET /api/host", s.host)
	mux.HandleFunc("GET /api/runconfig", s.runConfig)
	mux.HandleFunc("GET /api/update", s.updateCheck)
	mux.HandleFunc("POST /api/update", s.updateInstall)
	mux.HandleFunc("GET /api/logs", s.logs)
	mux.HandleFunc("DELETE /api/logs", s.clearLogs)

	mux.HandleFunc("GET /api/profiles", s.listProfiles)
	mux.HandleFunc("POST /api/profiles", s.addProfile)
	mux.HandleFunc("POST /api/profiles/upload", s.uploadProfile)
	mux.HandleFunc("PATCH /api/profiles/{id}", s.editProfile)
	mux.HandleFunc("DELETE /api/profiles/{id}", s.deleteProfile)
	mux.HandleFunc("POST /api/profiles/{id}/update", s.updateProfile)
	mux.HandleFunc("POST /api/profiles/{id}/use", s.useProfile)
	mux.HandleFunc("GET /api/profiles/{id}/content", s.profileContent)

	mux.HandleFunc("GET /api/panel", s.panelStatus)
	mux.HandleFunc("GET /api/panel/profile", s.panelProfile)
	mux.HandleFunc("POST /api/panel/login", s.panelLogin)
	mux.HandleFunc("POST /api/panel/logout", s.panelLogout)
	mux.HandleFunc("GET /api/panel/subscriptions", s.panelSubscriptions)
	mux.HandleFunc("POST /api/panel/import", s.panelImport)
	mux.HandleFunc("POST /api/panel/po0", s.panelPo0)
	mux.HandleFunc("POST /api/panel/ggy", s.panelGGY)
	mux.HandleFunc("POST /api/panel/akile", s.panelAkile)

	mux.HandleFunc("GET /api/mesh", s.meshView)
	mux.HandleFunc("PUT /api/mesh", s.meshSave)
	mux.HandleFunc("POST /api/mesh/leave", s.meshLeave)

	mux.Handle("/api/core/", s.coreProxy())
	mux.Handle("/", http.FileServerFS(s.Web))
	return s.auth(mux)
}

// ---------- 网页密码 ----------

const cookieName = "meowx_session"

// 会话令牌 = HMAC(密码)：改密码后旧会话自然失效，不用存会话表
func (s *Server) sessionToken(password string) string {
	m := hmac.New(sha256.New, []byte(s.App.Core.Secret))
	m.Write([]byte(password))
	return hex.EncodeToString(m.Sum(nil))
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pw := s.App.Settings.Get().Web.Password
		if pw == "" || !strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api/login" || r.URL.Path == "/api/status" {
			next.ServeHTTP(w, r)
			return
		}
		if c, err := r.Cookie(cookieName); err == nil && hmac.Equal([]byte(c.Value), []byte(s.sessionToken(pw))) {
			next.ServeHTTP(w, r)
			return
		}
		writeErr(w, http.StatusUnauthorized, errors.New("需要登录"))
	})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	pw := s.App.Settings.Get().Web.Password
	if pw == "" || !hmac.Equal([]byte(body.Password), []byte(pw)) {
		time.Sleep(time.Second)
		writeErr(w, http.StatusUnauthorized, errors.New("密码不对"))
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: s.sessionToken(pw), Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 30 * 86400})
	writeJSON(w, map[string]any{"ok": true})
}

// ---------- 状态 / 设置 / 服务 ----------

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	st := s.App.Settings.Get()
	needLogin := false
	if st.Web.Password != "" {
		c, err := r.Cookie(cookieName)
		needLogin = err != nil || !hmac.Equal([]byte(c.Value), []byte(s.sessionToken(st.Web.Password)))
	}
	out := map[string]any{"version": s.App.Version, "need_login": needLogin}
	if !needLogin {
		out["enabled"] = st.Enabled
		out["profile"] = st.Profile
		out["core"] = s.App.Core.Status()
		out["error"] = s.App.LastError()
		out["mixed_port"] = st.Mixin.MixedPort
	}
	writeJSON(w, out)
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.App.Settings.Get())
}

// PUT /api/settings：整份替换；?apply=0 只存不套用
func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var next store.Settings
	if !readJSON(w, r, &next) {
		return
	}
	prev := s.App.Settings.Get()
	saved, err := s.App.Settings.Update(func(cur *store.Settings) error {
		*cur = next
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	proxyChanged := saved.Proxy.Enabled && !reflect.DeepEqual(prev.Proxy, saved.Proxy)
	if r.URL.Query().Get("apply") != "0" {
		// 打开或改了透明代理：套用之前就进试用期——套用到一半失败（TUN 已经起来、旁路由规则没下发成功）时
		// NAS 的网络可能已经被改了，到点没确认照样自动关。MeowX 停着时这次不会套用，留到点「启动」时再进试用期
		switch {
		case proxyChanged && saved.Enabled:
			s.App.StartTrial(saved.Proxy.KeepMinutes)
		case proxyChanged:
			s.App.MarkUnconfirmed()
		case !saved.Proxy.Enabled:
			s.App.Keep()
		}
		if err := s.App.Apply(r.Context()); err != nil {
			writeJSON(w, map[string]any{"settings": s.App.Settings.Get(), "error": err.Error(), "trial": s.App.Trial()})
			return
		}
	} else if proxyChanged {
		s.App.MarkUnconfirmed()
	}
	writeJSON(w, map[string]any{"settings": s.App.Settings.Get(), "trial": s.App.Trial()})
}

// GET /api/proxy：透明代理的试用状态
func (s *Server) proxyStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.App.Trial())
}

// POST /api/proxy/keep：确认保留透明代理
func (s *Server) proxyKeep(w http.ResponseWriter, r *http.Request) {
	s.App.Keep()
	writeJSON(w, map[string]any{"ok": true})
}

// GET /api/host：宿主机的网卡（透明代理页选局域网入口用）
func (s *Server) host(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, hijack.Discover())
}

// POST /api/service {"action":"start|stop|restart"}：start / stop 会改「开关」并存盘
func (s *Server) service(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action string `json:"action"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	switch body.Action {
	case "start", "stop":
		if _, err := s.App.Settings.Update(func(cur *store.Settings) error {
			cur.Enabled = body.Action == "start"
			return nil
		}); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	case "restart":
	default:
		writeErr(w, http.StatusBadRequest, errors.New("action 只能是 start / stop / restart"))
		return
	}
	// 停着时改过透明代理：这次启动才真正套用，先进试用期
	s.App.TrialIfUnconfirmed()
	if err := s.App.Apply(r.Context()); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// 当前在跑的运行配置（编辑器只读看）
func (s *Server) runConfig(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, s.App.Core.Paths.ConfigPath())
}

// GET /api/logs?after=<序号>&source=core|agent
func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	lines := s.App.Log.Since(after, r.URL.Query().Get("source"))
	if lines == nil {
		lines = []core.Line{}
	}
	writeJSON(w, lines)
}

func (s *Server) clearLogs(w http.ResponseWriter, r *http.Request) {
	s.App.Log.Clear()
	writeJSON(w, map[string]any{"ok": true})
}

// ---------- 订阅 ----------

func (s *Server) listProfiles(w http.ResponseWriter, r *http.Request) {
	list := s.App.Profiles.List()
	if list == nil {
		list = []store.Profile{}
	}
	writeJSON(w, map[string]any{"profiles": list, "active": s.App.Settings.Get().Profile})
}

// POST /api/profiles {"url","name","use":true}
func (s *Server) addProfile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL  string `json:"url"`
		Name string `json:"name"`
		Use  bool   `json:"use"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	p, err := s.App.Profiles.AddSubscription(ctx, body.URL, body.Name, false)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	s.afterAdd(w, r, p, body.Use)
}

// 新加的订阅：要求「用它」或还没有在用的订阅时直接切过去（同客户端：导入即切过去）
func (s *Server) afterAdd(w http.ResponseWriter, r *http.Request, p store.Profile, use bool) {
	if use || s.App.Settings.Get().Profile == "" {
		if err := s.switchTo(r.Context(), p.ID); err != nil {
			writeJSON(w, map[string]any{"profile": p, "error": err.Error()})
			return
		}
	}
	writeJSON(w, map[string]any{"profile": p})
}

// POST /api/profiles/upload?name=…，正文是配置文件
func (s *Server) uploadProfile(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	p, err := s.App.Profiles.AddLocal(ctx, r.URL.Query().Get("name"), data)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.afterAdd(w, r, p, r.URL.Query().Get("use") == "1")
}

// PATCH /api/profiles/{id} {"name","url","info_url","user_agent"}（只改给了的字段）
func (s *Server) editProfile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name      *string `json:"name"`
		URL       *string `json:"url"`
		InfoURL   *string `json:"info_url"`
		UserAgent *string `json:"user_agent"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	p, err := s.App.Profiles.Edit(r.PathValue("id"), func(p *store.Profile) {
		if body.Name != nil {
			p.Name = strings.TrimSpace(*body.Name)
		}
		if body.URL != nil && p.URL != "" {
			p.URL = strings.TrimSpace(*body.URL)
		}
		if body.InfoURL != nil {
			p.InfoURL = strings.TrimSpace(*body.InfoURL)
		}
		if body.UserAgent != nil {
			p.UserAgent = strings.TrimSpace(*body.UserAgent)
		}
	})
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, map[string]any{"profile": p})
}

// 正在用的也能删：删掉后停内核（同退出登录删掉在用的主控订阅）。以前回 409、网页上也不给删除按钮，
// 只有一份订阅时就删不掉（#1718）
func (s *Server) deleteProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.App.Profiles.Delete(id); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if s.App.Settings.Get().Profile == id {
		_, _ = s.App.Settings.Update(func(cur *store.Settings) error {
			cur.Profile, cur.Enabled = "", false
			return nil
		})
		_ = s.App.Apply(r.Context())
	}
	writeJSON(w, map[string]any{"ok": true})
}

// 刷新：正是在用的且内核在跑时重载
func (s *Server) updateProfile(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	id := r.PathValue("id")
	p, err := s.App.Profiles.Update(ctx, id)
	if err != nil {
		writeJSON(w, map[string]any{"profile": p, "error": err.Error()})
		return
	}
	st := s.App.Settings.Get()
	if st.Enabled && st.Profile == id {
		if err := s.App.Apply(ctx); err != nil {
			writeJSON(w, map[string]any{"profile": p, "error": err.Error()})
			return
		}
	}
	writeJSON(w, map[string]any{"profile": p})
}

func (s *Server) useProfile(w http.ResponseWriter, r *http.Request) {
	if err := s.switchTo(r.Context(), r.PathValue("id")); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// 切换在用的订阅：没开的顺手开（同客户端「选了订阅就连上」）；新配置起不来就退回原来的选择
func (s *Server) switchTo(ctx context.Context, id string) error {
	if _, ok := s.App.Profiles.Get(id); !ok {
		return errors.New("订阅不存在")
	}
	prev := s.App.Settings.Get()
	if _, err := s.App.Settings.Update(func(cur *store.Settings) error {
		cur.Profile = id
		cur.Enabled = true
		return nil
	}); err != nil {
		return err
	}
	if err := s.App.Apply(ctx); err != nil {
		_, _ = s.App.Settings.Update(func(cur *store.Settings) error {
			cur.Profile, cur.Enabled = prev.Profile, prev.Enabled
			return nil
		})
		_ = s.App.Apply(ctx)
		return err
	}
	return nil
}

func (s *Server) profileContent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	http.ServeFile(w, r, s.App.Profiles.Path(r.PathValue("id")))
}

// ---------- 检查更新 / 一键更新 ----------

// GET /api/update[?refresh=1]：有没有新版本、能不能一键更新（结果缓存一小时）
func (s *Server) updateCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	writeJSON(w, s.App.Update.Check(ctx, r.URL.Query().Get("refresh") == "1"))
}

// POST /api/update：下载新版安装包、交给飞牛应用中心升级；回包之后应用中心会重启 MeowX。
// 关掉网页不打断下载（不跟请求的 context）
func (s *Server) updateInstall(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	v, err := s.App.Update.Install(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "version": v})
}

// ---------- 主控账户 ----------

func (s *Server) panelRun(w http.ResponseWriter, r *http.Request, args ...string) {
	out, err := s.App.Core.Panel(r.Context(), args...)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(out)
}

func (s *Server) panelStatus(w http.ResponseWriter, r *http.Request) { s.panelRun(w, r, "status") }

func (s *Server) panelSubscriptions(w http.ResponseWriter, r *http.Request) {
	s.panelRun(w, r, "subscriptions")
}

// POST /api/panel/import {"url","name"}：导入账户下的一份订阅（记成主控的，退出登录时删），导入即切过去
func (s *Server) panelImport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL  string `json:"url"`
		Name string `json:"name"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	p, err := s.App.Profiles.AddSubscription(ctx, body.URL, body.Name, true)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	s.afterAdd(w, r, p, true)
}

// POST /api/panel/login {"link":"miaomiaowu://login?..."}
func (s *Server) panelLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Link string `json:"link"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	out, err := s.App.Core.Panel(r.Context(), "login", strings.TrimSpace(body.Link))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	resp := map[string]any{}
	_ = json.Unmarshal(out, &resp)
	if p, err := s.autoImport(r.Context()); err != nil {
		resp["import_error"] = err.Error()
	} else if p != nil {
		resp["imported"] = p
	}
	// 登录后顺手拉主控下发的 po0 列表、补报一次白名单（同 OpenWrt 版）
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, _ = s.App.Core.Panel(ctx, "po0", "fetch")
		_, _ = s.App.Core.Panel(ctx, "report")
	}()
	writeJSON(w, resp)
}

// 登录那一刻自动导入（同客户端 autoImportCandidate）：当前没在用订阅、账户下恰好一份 → 导入并切过去；
// 多份不替用户选、已经在用别的订阅时不替用户换
func (s *Server) autoImport(ctx context.Context) (*store.Profile, error) {
	if s.App.Settings.Get().Profile != "" {
		return nil, nil
	}
	out, err := s.App.Core.Panel(ctx, "subscriptions")
	if err != nil {
		return nil, err
	}
	var subs struct {
		Subscriptions []struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"subscriptions"`
	}
	if json.Unmarshal(out, &subs) != nil || len(subs.Subscriptions) != 1 {
		return nil, nil
	}
	sub := subs.Subscriptions[0]
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	p, err := s.App.Profiles.AddSubscription(ctx, sub.URL, sub.Name, true)
	if err != nil {
		return nil, err
	}
	if err := s.switchTo(ctx, p.ID); err != nil {
		return &p, err
	}
	return &p, nil
}

// GET /api/panel/profile：向主控重取账户信息（昵称、头像），只在用户点「刷新」时调
func (s *Server) panelProfile(w http.ResponseWriter, r *http.Request) {
	s.panelRun(w, r, "profile")
}

// 退出登录：删掉从这个主控导入的订阅（同客户端；在用的那份删掉后停内核）
func (s *Server) panelLogout(w http.ResponseWriter, r *http.Request) {
	if _, err := s.App.Core.Panel(r.Context(), "logout"); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	active := s.App.Settings.Get().Profile
	removedActive := false
	for _, p := range s.App.Profiles.List() {
		if p.FromPanel {
			removedActive = removedActive || p.ID == active
			_ = s.App.Profiles.Delete(p.ID)
		}
	}
	if removedActive {
		_, _ = s.App.Settings.Update(func(cur *store.Settings) error {
			cur.Profile, cur.Enabled = "", false
			return nil
		})
		_ = s.App.Apply(r.Context())
	}
	writeJSON(w, map[string]any{"ok": true})
}

// POST /api/panel/po0 {"action":"status|enable|disable|fetch|add|remove","address","token","index"}
func (s *Server) panelPo0(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action  string `json:"action"`
		Address string `json:"address"`
		Token   string `json:"token"`
		Index   int    `json:"index"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	args := []string{"po0", body.Action}
	switch body.Action {
	case "status", "enable", "disable", "fetch":
	case "add":
		args = append(args, body.Address, body.Token)
	case "remove":
		args = append(args, strconv.Itoa(body.Index))
	default:
		writeErr(w, http.StatusBadRequest, errors.New("未知操作"))
		return
	}
	before := s.App.Settings.Get()
	out, err := s.App.Core.Panel(r.Context(), args...)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	// po0 列表变了：直连规则跟着变，内核在跑就重载
	if body.Action != "status" && before.Enabled {
		_ = s.App.Apply(r.Context())
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(out)
}

// POST /api/panel/ggy {"action":"status|enable|disable|add|remove|report","token","index"}
func (s *Server) panelGGY(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action string `json:"action"`
		Token  string `json:"token"`
		Index  int    `json:"index"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	args := []string{"ggy", body.Action}
	switch body.Action {
	case "status", "enable", "disable", "report":
	case "add":
		args = append(args, body.Token)
	case "remove":
		args = append(args, strconv.Itoa(body.Index))
	default:
		writeErr(w, http.StatusBadRequest, errors.New("未知操作"))
		return
	}
	s.panelRun(w, r, args...)
}

// POST /api/panel/akile {"action":"status|enable|disable|setkey|clearkey|nodes|rules|add|remove|report","key","port","rule_id","name","label","index"}
// AkileCDN 白名单：与主控账户无关、没登录也能用。打开开关、换 Key、开着时添加由 meowx-core 当场查出口 IP 并写一次再回状态；
// 回来的 Key 都是脱敏写法
func (s *Server) panelAkile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action string `json:"action"`
		Key    string `json:"key"`
		Port   int    `json:"port"`
		RuleID int64  `json:"rule_id"`
		Name   string `json:"name"`
		Label  string `json:"label"`
		Index  int    `json:"index"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	args := []string{"akile", body.Action}
	switch body.Action {
	case "status", "enable", "disable", "clearkey", "report":
	case "setkey":
		args = append(args, body.Key)
	case "nodes":
		args = append(args, s.App.AkileNodesConfig())
	case "rules":
		args = append(args, strconv.Itoa(body.Port))
	case "add":
		args = append(args, strconv.FormatInt(body.RuleID, 10), strconv.Itoa(body.Port), body.Name, body.Label)
	case "remove":
		args = append(args, strconv.Itoa(body.Index))
	default:
		writeErr(w, http.StatusBadRequest, errors.New("未知操作"))
		return
	}
	s.panelRun(w, r, args...)
}

// ---------- 喵窝 ----------

// GET /api/mesh：设置、状态、本机与其它设备，外加可开放的局域网网段候选
func (s *Server) meshView(w http.ResponseWriter, r *http.Request) {
	var lan []string
	for _, i := range hijack.Discover().Physical {
		lan = append(lan, i.Subnets...)
	}
	writeJSON(w, map[string]any{"mesh": s.App.Mesh.View(r.Context()), "lan": lan})
}

// PUT /api/mesh {设置}：存好就按设置入网 / 退出（入网最多一两分钟）
func (s *Server) meshSave(w http.ResponseWriter, r *http.Request) {
	var next store.Mesh
	if !readJSON(w, r, &next) {
		return
	}
	if _, err := s.App.Settings.Update(func(cur *store.Settings) error {
		cur.Mesh = next
		return nil
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := s.App.Mesh.Apply(ctx); err != nil {
		writeJSON(w, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// POST /api/mesh/leave：从主控删掉这台设备、撤掉网段、关开关
func (s *Server) meshLeave(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	writeJSON(w, map[string]any{"ok": true, "warning": s.App.Mesh.Leave(ctx)})
}

// ---------- 内核的 REST API（节点、连接、日志，WebSocket 也转） ----------

func (s *Server) coreProxy() http.Handler {
	target, _ := url.Parse("http://" + s.App.Core.API)
	rp := httputil.NewSingleHostReverseProxy(target)
	director := rp.Director
	rp.Director = func(r *http.Request) {
		director(r)
		// 保留编码后的路径：组名 / 节点名里带「/」（如 HK/JP）时，解码后的 Path 去掉前缀再转出去就变成两段路径，内核回 404
		escaped := strings.TrimPrefix(r.URL.EscapedPath(), "/api/core")
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api/core")
		r.URL.RawPath = escaped
		r.Header.Set("Authorization", "Bearer "+s.App.Core.Secret)
		r.Header.Del("Cookie")
	}
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		writeErr(w, http.StatusBadGateway, errors.New("内核没在运行"))
	}
	return rp
}

// ---------- 小工具 ----------

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("请求格式不对："+err.Error()))
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
}

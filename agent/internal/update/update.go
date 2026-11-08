// Package update：检查新版本（dl.miaomiaowux.com 的发布清单 latest.json 里飞牛一段），并交给飞牛应用中心原地升级。
//
// 飞牛没有给应用用的升级命令：appcenter-cli 的 install-fpk 遇到已装的应用直接拒绝，install-local 是「先卸载再装」，
// 装失败应用连数据一起没了。网页「手动安装」走的是应用中心守护程序的 HTTP 接口（unix socket，/rpc/v1 下不要会话
// token，只有 root 与官方应用组能连）：先暂存安装包，再提交 upgrade 任务——保留应用数据、失败自己回滚。
// 容器里是 root，docker-compose 把这个 socket 挂进来就能用（社群商店 conversun/fnos-store 同样走这条路，
// 接口与字段按它的实测记录对过）。升级时应用中心先停掉本应用，这个进程会被换掉，所以只等到任务提交成功为止。
package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultFeed = "https://dl.miaomiaowux.com/meowx/latest.json"
	// 容器里应用中心 socket 的位置（docker-compose 把宿主的 /run/com.trim.app.center.sock 挂到这里）
	DefaultSocket = "/run/fnos-appcenter.sock"
	appName       = "meowx"
	releasePage   = "https://github.com/mmwx-group/meowx-fnos/releases/tag/v"
	cacheFor      = time.Hour
)

type Updater struct {
	Version     string // 当前版本
	FeedURL     string
	Socket      string
	DataDir     string // 容器里的数据目录（/data），安装包下载到它下面的 update/
	HostDataDir string // 同一个目录在宿主上的路径：应用中心按宿主路径读安装包，没有就不能一键更新
	Client      *http.Client
	Log         func(string)

	mu        sync.Mutex
	cached    *Info
	file      *feedFile
	checkedAt time.Time
	busy      bool
	lastError string
}

// Info：给网页的检查结果
type Info struct {
	Current     string `json:"current"`
	Latest      string `json:"latest,omitempty"`
	HasUpdate   bool   `json:"has_update"`
	ReleasedAt  string `json:"released_at,omitempty"`
	NotesURL    string `json:"notes_url,omitempty"`
	DownloadURL string `json:"download_url,omitempty"`
	Size        int64  `json:"size,omitempty"`
	CanInstall  bool   `json:"can_install"`
	Reason      string `json:"reason,omitempty"` // 不能一键更新的原因
	Updating    bool   `json:"updating,omitempty"`
	LastError   string `json:"last_error,omitempty"` // 上一次一键更新失败的原因
	Error       string `json:"error,omitempty"`      // 这次没检查成
	CheckedAt   int64  `json:"checked_at"`
}

type feedFile struct {
	Kind   string `json:"kind"`
	Arch   string `json:"arch"`
	Name   string `json:"name"`
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func (u *Updater) logf(format string, args ...any) {
	if u.Log != nil {
		u.Log(fmt.Sprintf(format, args...))
	}
}

func (u *Updater) userAgent() string { return "MeowX/" + u.Version + " (fnOS)" }

// 发布清单里的架构名
func feedArch() string {
	if runtime.GOARCH == "arm64" {
		return "arm64"
	}
	return "x86_64"
}

// Cleanup：启动时删掉上次下载的安装包（应用中心暂存时已经拷走了）
func (u *Updater) Cleanup() {
	_ = os.RemoveAll(filepath.Join(u.DataDir, "update"))
}

// Check：缓存一小时，force 时重新取
func (u *Updater) Check(ctx context.Context, force bool) Info {
	u.mu.Lock()
	if !force && u.cached != nil && time.Since(u.checkedAt) < cacheFor {
		info := *u.cached
		info.Updating, info.LastError = u.busy, u.lastError
		u.mu.Unlock()
		return info
	}
	u.mu.Unlock()

	info := Info{Current: u.Version, CheckedAt: time.Now().Unix()}
	latest, released, file, err := u.fetchFeed(ctx)
	if err != nil {
		info.Error = err.Error()
	} else {
		info.Latest, info.ReleasedAt = latest, released
		info.NotesURL = releasePage + latest
		info.HasUpdate = Newer(latest, u.Version)
		if file != nil {
			info.DownloadURL, info.Size = file.URL, file.Size
		}
	}
	if info.HasUpdate {
		if file == nil {
			info.Reason = "新版本没有适合这台 NAS 的安装包"
		} else if reason := u.installBlocker(ctx); reason != "" {
			info.Reason = reason
		} else {
			info.CanInstall = true
		}
	}

	u.mu.Lock()
	defer u.mu.Unlock()
	if err == nil {
		u.cached, u.file, u.checkedAt = &info, file, time.Now()
	}
	info.Updating, info.LastError = u.busy, u.lastError
	return info
}

func (u *Updater) fetchFeed(ctx context.Context) (version, released string, file *feedFile, err error) {
	feed := u.FeedURL
	if feed == "" {
		feed = DefaultFeed
	}
	sep := "?"
	if strings.Contains(feed, "?") {
		sep = "&"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feed+sep+"t="+strconv.FormatInt(time.Now().Unix(), 10), nil)
	if err != nil {
		return "", "", nil, err
	}
	// Cloudflare 会拦没有像样 UA 的请求
	req.Header.Set("User-Agent", u.userAgent())
	resp, err := u.Client.Do(req)
	if err != nil {
		return "", "", nil, fmt.Errorf("取不到发布清单：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", "", nil, fmt.Errorf("取不到发布清单：HTTP %d", resp.StatusCode)
	}
	var doc struct {
		Platforms struct {
			Fnos *struct {
				Version    string     `json:"version"`
				ReleasedAt string     `json:"releasedAt"`
				Files      []feedFile `json:"files"`
			} `json:"fnos"`
		} `json:"platforms"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
		return "", "", nil, errors.New("发布清单格式不对")
	}
	f := doc.Platforms.Fnos
	if f == nil || f.Version == "" {
		return "", "", nil, errors.New("发布清单里没有飞牛版")
	}
	for i := range f.Files {
		if f.Files[i].Kind == "fpk" && f.Files[i].Arch == feedArch() && f.Files[i].URL != "" {
			file = &f.Files[i]
			break
		}
	}
	return f.Version, f.ReleasedAt, file, nil
}

// Newer：a 比 b 新（按点分的数字逐段比，段里数字后面的后缀不算；b 不是版本号时不算新）
func Newer(a, b string) bool {
	pa, okA := parseVersion(a)
	pb, okB := parseVersion(b)
	if !okA || !okB {
		return false
	}
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func parseVersion(v string) ([]int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" {
		return nil, false
	}
	var out []int
	for _, part := range strings.Split(v, ".") {
		n := 0
		digits := 0
		for _, c := range part {
			if c < '0' || c > '9' {
				break
			}
			n = n*10 + int(c-'0')
			digits++
		}
		if digits == 0 {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}

// installBlocker：不能一键更新的原因，空 = 能
func (u *Updater) installBlocker(ctx context.Context) string {
	const manual = "，请下载安装包后在飞牛「应用中心 → 手动安装」里更新（不用先卸载）"
	if u.HostDataDir == "" {
		return "这个安装包没有带一键更新需要的设置" + manual
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// 空请求：接口在的话回参数校验错误 10030；连不上或接口不在（回 404 文本）都算不能用
	err := u.call(ctx, "/rpc/v1/update/info", map[string]any{}, nil)
	var de *daemonError
	if errors.As(err, &de) && de.Code == 10030 {
		return ""
	}
	if err != nil {
		u.logf("一键更新不可用：%v", err)
	}
	return "连不上飞牛应用中心的升级接口" + manual
}

type daemonError struct {
	Path string
	Code int
	Msg  string
}

func (e *daemonError) Error() string {
	if e.Msg != "" {
		return fmt.Sprintf("应用中心回错误 %d（%s）：%s", e.Code, e.Path, e.Msg)
	}
	return fmt.Sprintf("应用中心回错误 %d（%s）", e.Code, e.Path)
}

// call：POST JSON 到应用中心；应答一律 HTTP 200 + {"code","msg","data"}，code 不是 0 就是失败
func (u *Updater) call(ctx context.Context, path string, body, out any) error {
	sock := u.Socket
	if sock == "" {
		sock = DefaultSocket
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	client := &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		}},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost"+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var env struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&env); err != nil {
		return fmt.Errorf("应用中心应答看不懂（%s，HTTP %d）", path, resp.StatusCode)
	}
	if env.Code != 0 {
		return &daemonError{Path: path, Code: env.Code, Msg: env.Msg}
	}
	if out != nil && len(env.Data) > 0 {
		return json.Unmarshal(env.Data, out)
	}
	return nil
}

// Install：下载新版安装包、校验，交给应用中心升级。任务提交成功就返回新版本号；
// 之后应用中心会停掉本应用、换上新版再启动（这个进程活不到那时）
func (u *Updater) Install(ctx context.Context) (string, error) {
	u.mu.Lock()
	if u.busy {
		u.mu.Unlock()
		return "", errors.New("正在更新")
	}
	u.busy, u.lastError = true, ""
	u.mu.Unlock()
	done := false
	defer func() {
		if !done {
			u.mu.Lock()
			u.busy = false
			u.mu.Unlock()
		}
	}()

	info := u.Check(ctx, true)
	if info.Error != "" {
		return "", errors.New(info.Error)
	}
	if !info.HasUpdate {
		return "", fmt.Errorf("已是最新版本（%s）", u.Version)
	}
	if !info.CanInstall {
		return "", errors.New(info.Reason)
	}
	u.mu.Lock()
	file := u.file
	u.mu.Unlock()

	name := filepath.Base(file.Name)
	if name == "." || name == "/" || !strings.HasSuffix(name, ".fpk") {
		name = appName + "-" + info.Latest + ".fpk"
	}
	if err := u.download(ctx, file, name); err != nil {
		u.fail(err)
		return "", err
	}
	u.logf("已下载 %s，交给飞牛应用中心升级", name)
	taskID, err := u.upgrade(ctx, filepath.Join(u.HostDataDir, "update", name), info.Latest)
	if err != nil {
		u.fail(err)
		return "", err
	}
	done = true
	u.logf("升级任务已提交（%s → %s），应用中心会重启 MeowX", u.Version, info.Latest)
	go u.watch(taskID)
	return info.Latest, nil
}

func (u *Updater) fail(err error) {
	u.logf("一键更新失败：%v", err)
	u.mu.Lock()
	u.lastError = err.Error()
	u.mu.Unlock()
}

func (u *Updater) download(ctx context.Context, f *feedFile, name string) error {
	if f.SHA256 == "" {
		return errors.New("发布清单里没有安装包的校验值")
	}
	if pu, err := url.Parse(f.URL); err != nil || (pu.Scheme != "https" && u.FeedURL == "") {
		return errors.New("安装包地址不对")
	}
	dir := filepath.Join(u.DataDir, "update")
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", u.userAgent())
	resp, err := u.Client.Do(req)
	if err != nil {
		return fmt.Errorf("下载失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("下载失败：HTTP %d", resp.StatusCode)
	}
	tmp := filepath.Join(dir, name+".part")
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(resp.Body, 512<<20))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("下载失败：%w", err)
	}
	if f.Size > 0 && n != f.Size {
		return fmt.Errorf("下载不完整（%d / %d 字节）", n, f.Size)
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), f.SHA256) {
		return errors.New("安装包校验不通过")
	}
	return os.Rename(tmp, filepath.Join(dir, name))
}

// 应用中心的任务状态：1 进行中、2 成功
const (
	statusRunning = 1
	statusSuccess = 2
)

func (u *Updater) upgrade(ctx context.Context, hostPath, version string) (string, error) {
	// 1. 暂存：应用中心解包、认出是哪个应用哪个版本
	var task struct {
		ID string `json:"downloadTaskId"`
	}
	if err := u.call(ctx, "/rpc/v1/download/task", map[string]any{"packageSourceType": "file", "path": hostPath}, &task); err != nil {
		return "", fmt.Errorf("应用中心暂存安装包失败：%w", err)
	}
	if task.ID == "" {
		return "", errors.New("应用中心暂存安装包失败：没有回任务号")
	}
	var staged struct {
		Status      int    `json:"status"`
		Message     string `json:"message"`
		PackageType string `json:"packageType"`
		AppName     string `json:"appName"`
		Version     string `json:"version"`
		Installed   bool   `json:"installed"`
	}
	deadline := time.Now().Add(3 * time.Minute)
	transient := 0
	for {
		if time.Now().After(deadline) {
			return "", errors.New("应用中心暂存安装包超时")
		}
		err := u.call(ctx, "/rpc/v1/download/status", map[string]any{"downloadTaskId": task.ID}, &staged)
		if err != nil {
			// 10050 / TRPC 超时是应用中心内部的瞬时错误，下一次通常就好
			var de *daemonError
			if (errors.As(err, &de) && de.Code == 10050) || strings.Contains(err.Error(), "timeout") {
				if transient++; transient < 5 {
					if err := sleep(ctx, time.Second); err != nil {
						return "", err
					}
					continue
				}
			}
			return "", fmt.Errorf("查询暂存状态失败：%w", err)
		}
		transient = 0
		if staged.Status == statusSuccess {
			break
		}
		if staged.Status != statusRunning {
			return "", fmt.Errorf("应用中心暂存安装包失败：%s", staged.Message)
		}
		if err := sleep(ctx, time.Second); err != nil {
			return "", err
		}
	}
	if staged.AppName != appName || staged.Version != version {
		return "", fmt.Errorf("安装包内容不对（%s %s）", staged.AppName, staged.Version)
	}
	if !staged.Installed {
		return "", errors.New("应用中心认为 MeowX 没有安装，不能升级")
	}

	// 2. 升级前检查：取应用所在的存储卷，升级钉在原来的卷上
	var info struct {
		WizardInfo struct {
			InstalledVolumeID int `json:"installedVolumeID"`
		} `json:"wizardInfo"`
	}
	base := map[string]any{"appName": appName, "updateVersion": version, "packageType": staged.PackageType, "language": "zh-CN"}
	if err := u.call(ctx, "/rpc/v1/update/info", base, &info); err != nil {
		return "", fmt.Errorf("升级前检查失败：%w", err)
	}
	vol := info.WizardInfo.InstalledVolumeID
	if vol <= 0 {
		return "", errors.New("查不到 MeowX 所在的存储卷，没有升级")
	}

	// 3. 提交升级任务（同网页手动安装：保留数据、失败回滚）
	body := map[string]any{
		"systemParameters": map[string]any{"agreedToProtocol": true, "installVolumeID": vol, "dataVolumeId": vol, "immediateStart": false},
		"customParameters": []any{},
	}
	for k, v := range base {
		body[k] = v
	}
	var up struct {
		ID string `json:"taskId"`
	}
	if err := u.call(ctx, "/rpc/v1/update/task", body, &up); err != nil {
		return "", fmt.Errorf("提交升级失败：%w", err)
	}
	return up.ID, nil
}

// watch：任务提交后还活着就跟一下结果（正常情况下应用中心很快会停掉本应用）
func (u *Updater) watch(taskID string) {
	defer func() {
		u.mu.Lock()
		u.busy = false
		u.mu.Unlock()
	}()
	if taskID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	for {
		if sleep(ctx, 3*time.Second) != nil {
			return
		}
		var st struct {
			Status  int    `json:"status"`
			Message string `json:"message"`
		}
		if err := u.call(ctx, "/rpc/v1/common/status", map[string]any{"taskId": taskID}, &st); err != nil {
			continue
		}
		switch st.Status {
		case statusRunning:
			continue
		case statusSuccess:
			return
		case 5: // 应用中心不认识这个任务（已回收或它重启过），说明不了成败
			return
		default:
			u.fail(fmt.Errorf("升级没有完成：%s", st.Message))
			return
		}
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

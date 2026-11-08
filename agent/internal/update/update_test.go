package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.1.1", "0.1.0", true},
		{"0.2.0", "0.1.9", true},
		{"0.1.0", "0.1.0", false},
		{"0.1.0", "0.1.1", false},
		{"1.0", "0.9.9", true},
		{"0.1.0.1", "0.1.0", true},
		{"v0.1.2", "0.1.1", true},
		{"0.1.2", "dev", false},
		{"", "0.1.0", false},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// 假应用中心：记下收到的请求，按真实接口的样子回
type fakeDaemon struct {
	mu        sync.Mutex
	calls     []string
	bodies    map[string]map[string]any
	installed bool
	version   string // 暂存后认出的版本
}

func (d *fakeDaemon) serve(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "mxsock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "ac.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	d.bodies = map[string]map[string]any{}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		d.mu.Lock()
		d.calls = append(d.calls, r.URL.Path)
		d.bodies[r.URL.Path] = body
		d.mu.Unlock()
		reply := func(data any) { _ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "", "data": data}) }
		if len(body) == 0 {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 10030, "msg": ""})
			return
		}
		switch r.URL.Path {
		case "/rpc/v1/download/task":
			reply(map[string]any{"downloadTaskId": "dl1"})
		case "/rpc/v1/download/status":
			reply(map[string]any{"status": 2, "appName": "meowx", "version": d.version, "packageType": "fpk", "installed": d.installed, "path": "/vol1/appcenter-downloads/meowx-" + d.version + "-tpk"})
		case "/rpc/v1/update/info":
			reply(map[string]any{"wizardInfo": map[string]any{"installedVolumeID": 1}})
		case "/rpc/v1/update/task":
			reply(map[string]any{"taskId": "up1"})
		case "/rpc/v1/common/status":
			reply(map[string]any{"status": 2})
		default:
			http.NotFound(w, r)
		}
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return sock
}

func feedServer(t *testing.T, version string, fpk []byte) *httptest.Server {
	t.Helper()
	sum := sha256.Sum256(fpk)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "MeowX/") {
			http.Error(w, "forbidden", 403)
			return
		}
		switch r.URL.Path {
		case "/latest.json":
			fmt.Fprintf(w, `{"platforms":{"ios":{"version":"9.9.9"},"fnos":{"version":%q,"releasedAt":"2026-11-08T00:00:00Z","files":[
				{"kind":"fpk","arch":"x86_64","name":"meowx-fnos-%[1]s-x86.fpk","url":"%[2]s/x86.fpk","size":%[3]d,"sha256":"%[4]s"},
				{"kind":"fpk","arch":"arm64","name":"meowx-fnos-%[1]s-arm.fpk","url":"%[2]s/arm.fpk","size":%[3]d,"sha256":"%[4]s"}]}}}`,
				version, srv.URL, len(fpk), hex.EncodeToString(sum[:]))
		case "/x86.fpk", "/arm.fpk":
			_, _ = w.Write(fpk)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCheckAndInstall(t *testing.T) {
	fpk := []byte("fake fpk payload")
	feed := feedServer(t, "0.1.2", fpk)
	d := &fakeDaemon{installed: true, version: "0.1.2"}
	sock := d.serve(t)
	data := t.TempDir()
	u := &Updater{Version: "0.1.1", FeedURL: feed.URL + "/latest.json", Socket: sock,
		DataDir: data, HostDataDir: "/vol1/@appdata/meowx/data", Client: feed.Client()}

	info := u.Check(context.Background(), false)
	if info.Error != "" || !info.HasUpdate || info.Latest != "0.1.2" || !info.CanInstall {
		t.Fatalf("check: %+v", info)
	}
	if info.NotesURL != releasePage+"0.1.2" || info.Size != int64(len(fpk)) {
		t.Fatalf("check details: %+v", info)
	}

	v, err := u.Install(context.Background())
	if err != nil || v != "0.1.2" {
		t.Fatalf("install: %v %v", v, err)
	}
	// 安装包落在数据目录的 update/ 下，交给应用中心的是宿主路径
	name := "meowx-fnos-0.1.2-" + map[string]string{"x86_64": "x86", "arm64": "arm"}[feedArch()] + ".fpk"
	if got, _ := os.ReadFile(filepath.Join(data, "update", name)); string(got) != string(fpk) {
		t.Fatalf("downloaded file: %q", got)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if p := d.bodies["/rpc/v1/download/task"]["path"]; p != "/vol1/@appdata/meowx/data/update/"+name {
		t.Fatalf("staged path: %v", p)
	}
	task := d.bodies["/rpc/v1/update/task"]
	if task["appName"] != "meowx" || task["updateVersion"] != "0.1.2" || task["packageType"] != "fpk" {
		t.Fatalf("update task: %v", task)
	}
	sys, _ := task["systemParameters"].(map[string]any)
	if sys["installVolumeID"] != float64(1) || sys["dataVolumeId"] != float64(1) || sys["agreedToProtocol"] != true {
		t.Fatalf("update task params: %v", sys)
	}
}

func TestNoUpdate(t *testing.T) {
	feed := feedServer(t, "0.1.1", []byte("x"))
	u := &Updater{Version: "0.1.1", FeedURL: feed.URL + "/latest.json", DataDir: t.TempDir(), Client: feed.Client()}
	info := u.Check(context.Background(), false)
	if info.Error != "" || info.HasUpdate || info.CanInstall {
		t.Fatalf("check: %+v", info)
	}
	if _, err := u.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "已是最新版本") {
		t.Fatalf("install: %v", err)
	}
}

// 没有应用中心接口（老安装包没挂 socket / 没有宿主路径）：能看到新版本，但只给下载地址
func TestCannotInstall(t *testing.T) {
	feed := feedServer(t, "0.1.2", []byte("x"))
	u := &Updater{Version: "0.1.1", FeedURL: feed.URL + "/latest.json", Socket: filepath.Join(t.TempDir(), "none.sock"),
		DataDir: t.TempDir(), HostDataDir: "/vol1/@appdata/meowx/data", Client: feed.Client()}
	info := u.Check(context.Background(), false)
	if !info.HasUpdate || info.CanInstall || !strings.Contains(info.Reason, "手动安装") || info.DownloadURL == "" {
		t.Fatalf("check: %+v", info)
	}
	u.HostDataDir = ""
	info = u.Check(context.Background(), true)
	if info.CanInstall || info.Reason == "" {
		t.Fatalf("check without host dir: %+v", info)
	}
}

// 校验值对不上：不交给应用中心
func TestBadChecksum(t *testing.T) {
	fpk := []byte("real")
	d := &fakeDaemon{installed: true, version: "0.1.2"}
	sock := d.serve(t)
	u := &Updater{Version: "0.1.1", Socket: sock, DataDir: t.TempDir(), HostDataDir: "/h", Client: http.DefaultClient}
	// 清单里的校验值是错的
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/latest.json" {
			fmt.Fprintf(w, `{"platforms":{"fnos":{"version":"0.1.2","files":[{"kind":"fpk","arch":%q,"name":"a.fpk","url":"%s/a.fpk","size":4,"sha256":"%s"}]}}}`,
				feedArch(), "http://"+r.Host, strings.Repeat("0", 64))
			return
		}
		_, _ = w.Write(fpk)
	}))
	defer bad.Close()
	u.FeedURL = bad.URL + "/latest.json"
	if _, err := u.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "校验") {
		t.Fatalf("install: %v", err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range d.calls {
		if c == "/rpc/v1/download/task" || c == "/rpc/v1/update/task" {
			t.Fatalf("daemon got %s despite bad checksum", c)
		}
	}
	if info := u.Check(context.Background(), false); info.LastError == "" {
		t.Fatalf("last error not kept: %+v", info)
	}
}

// 暂存出来的不是 MeowX 的这一版：不提交升级
func TestStagedMismatch(t *testing.T) {
	fpk := []byte("p")
	feed := feedServer(t, "0.1.2", fpk)
	d := &fakeDaemon{installed: true, version: "0.1.3"}
	sock := d.serve(t)
	u := &Updater{Version: "0.1.1", FeedURL: feed.URL + "/latest.json", Socket: sock,
		DataDir: t.TempDir(), HostDataDir: "/h", Client: feed.Client()}
	if _, err := u.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "内容不对") {
		t.Fatalf("install: %v", err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range d.calls {
		if c == "/rpc/v1/update/task" {
			t.Fatal("update task submitted for mismatched package")
		}
	}
}

func TestCleanup(t *testing.T) {
	data := t.TempDir()
	_ = os.MkdirAll(filepath.Join(data, "update"), 0o755)
	_ = os.WriteFile(filepath.Join(data, "update", "old.fpk"), []byte("x"), 0o644)
	(&Updater{DataDir: data}).Cleanup()
	if _, err := os.Stat(filepath.Join(data, "update")); !os.IsNotExist(err) {
		t.Fatalf("update dir still there: %v", err)
	}
}

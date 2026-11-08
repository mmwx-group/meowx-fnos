// Package core：看管 meowx-core 进程（起停、崩了重启、日志）、调它的 panel 子命令、试读配置。
package core

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Paths：内核相关的文件位置（都在 /data 下，挂出来的数据卷）
type Paths struct {
	Binary  string // /usr/bin/meowx-core
	HomeDir string // 内核工作目录：运行配置、geo 数据、缓存、规则集
	Panel   string // panel 子命令的状态目录
}

func (p Paths) ConfigPath() string { return filepath.Join(p.HomeDir, "config.yaml") }

// AkileResultPath：AkileCDN 白名单每个节点上一次的结果（OpenWrt 版放内存盘 /var/run/meowx/akile.json）。
// 控制服务启动时删掉，等同内存盘：重启后显示「还没更新过」、第一轮一定写
func (p Paths) AkileResultPath() string { return filepath.Join(p.Panel, "akile-results.json") }

// AkileStatePath：AkileCDN 白名单的设置（开关、API Key、节点列表；OpenWrt 版存 uci）
func (p Paths) AkileStatePath() string { return filepath.Join(p.Panel, "akile.json") }

// PanelEnv：panel 子命令的状态文件指到数据卷（OpenWrt 版在 /etc/meowx）
func (p Paths) PanelEnv() []string {
	return []string{
		"MEOWX_PANEL_STATE=" + filepath.Join(p.Panel, "panel.json"),
		"MEOWX_PO0_STATE=" + filepath.Join(p.Panel, "po0.json"),
		"MEOWX_GGY_RESULTS=" + filepath.Join(p.Panel, "ggy-results.json"),
		"MEOWX_APP_LOG=" + filepath.Join(p.Panel, "app.log"),
		"MEOWX_GGY_STATE=" + filepath.Join(p.Panel, "ggy.json"), // 没有 uci，GGY 设置存文件
		"MEOWX_AKILE_STATE=" + p.AkileStatePath(),               // AkileCDN 白名单同样存文件
		"MEOWX_AKILE_RESULT=" + p.AkileResultPath(),
		"MEOWX_MESH_OS=fnos", // 喵窝入网时报给主控的系统 / 客户端
		"MEOWX_MESH_CLIENT=meowx-fnos",
	}
}

// Supervisor：一个内核进程
type Supervisor struct {
	Paths  Paths
	API    string // 外部控制地址（127.0.0.1:端口）
	Secret string
	Log    *Ring

	mu        sync.Mutex
	cmd       *exec.Cmd
	want      bool // 用户要它在跑（崩了就重启）
	startedAt time.Time
	lastErr   string
	done      chan struct{}
	restarts  int
}

type Status struct {
	Running   bool   `json:"running"`
	StartedAt int64  `json:"started_at,omitempty"`
	Error     string `json:"error,omitempty"`
	Restarts  int    `json:"restarts"`
}

func (s *Supervisor) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{Running: s.cmd != nil, Error: s.lastErr, Restarts: s.restarts}
	if s.cmd != nil {
		st.StartedAt = s.startedAt.Unix()
	}
	return st
}

// Start 起内核并等它的外部控制能用（最多 30 秒）。已经在跑就先停。
func (s *Supervisor) Start(ctx context.Context) error {
	s.Stop()
	s.mu.Lock()
	s.want = true
	s.restarts = 0
	s.mu.Unlock()
	if err := s.spawn(); err != nil {
		return err
	}
	return s.waitReady(ctx, 30*time.Second)
}

func (s *Supervisor) spawn() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.want {
		return nil
	}
	cmd := exec.Command(s.Paths.Binary, "-d", s.Paths.HomeDir, "-f", s.Paths.ConfigPath())
	cmd.Env = append(os.Environ(), s.Paths.PanelEnv()...)
	cmd.Env = append(cmd.Env, "SAFE_PATHS="+s.Paths.HomeDir)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		s.lastErr = err.Error()
		return err
	}
	s.cmd, s.startedAt, s.lastErr = cmd, time.Now(), ""
	done := make(chan struct{})
	s.done = done
	go s.pump(out)
	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		if s.cmd == cmd {
			s.cmd = nil
		}
		want := s.want
		if want {
			msg := "内核退出"
			if err != nil {
				msg += "：" + err.Error()
			}
			s.lastErr = msg
			s.restarts++
		}
		restarts := s.restarts
		s.mu.Unlock()
		close(done)
		if want {
			s.Log.Add("agent", fmt.Sprintf("内核意外退出（第 %d 次），稍后重启", restarts))
			// 退避：2s、4s … 封顶 60s
			delay := time.Duration(1<<min(restarts, 5)) * time.Second
			if delay > time.Minute {
				delay = time.Minute
			}
			time.Sleep(delay)
			_ = s.spawn()
		}
	}()
	return nil
}

func (s *Supervisor) pump(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		s.Log.Add("core", sc.Text())
	}
}

// Stop 停内核（先 SIGTERM，5 秒不退再 SIGKILL）
func (s *Supervisor) Stop() {
	s.mu.Lock()
	s.want = false
	cmd, done := s.cmd, s.done
	s.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}

func (s *Supervisor) waitReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := s.Ping(ctx); err == nil {
			return nil
		}
		s.mu.Lock()
		running := s.cmd != nil
		lastErr := s.lastErr
		s.mu.Unlock()
		if !running && lastErr != "" {
			return errors.New(lastErr + "（看日志）")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	return errors.New("内核启动超时（看日志）")
}

// Ping：外部控制能不能用
func (s *Supervisor) Ping(ctx context.Context) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+s.API+"/version", nil)
	req.Header.Set("Authorization", "Bearer "+s.Secret)
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// Test：让内核试读一份完整配置（-t），不通过就返回内核说的原因
func (s *Supervisor) Test(ctx context.Context, config []byte) error {
	dir, err := os.MkdirTemp("", "meowx-test-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, config, 0o600); err != nil {
		return err
	}
	// geo 数据与规则集在工作目录里：试读也用它（只读）
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.Paths.Binary, "-d", s.Paths.HomeDir, "-f", path, "-t")
	cmd.Env = append(os.Environ(), "SAFE_PATHS="+s.Paths.HomeDir+":"+dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return errors.New(lastMeaningfulLine(string(out), err.Error()))
	}
	return nil
}

func lastMeaningfulLine(out, fallback string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l != "" && !strings.Contains(l, "test is successful") {
			if j := strings.Index(l, "msg="); j >= 0 {
				l = strings.Trim(l[j+4:], `"`)
			}
			return l
		}
	}
	return fallback
}

// Panel 调 `meowx-core panel …`，输出是 JSON（失败时 {"success":false,"error":…}）
func (s *Supervisor) Panel(ctx context.Context, args ...string) (json.RawMessage, error) {
	if err := os.MkdirAll(s.Paths.Panel, 0o700); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.Paths.Binary, append([]string{"panel"}, args...)...)
	cmd.Env = append(os.Environ(), s.Paths.PanelEnv()...)
	out, err := cmd.Output()
	var fail struct {
		Success *bool  `json:"success"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(out, &fail) == nil && fail.Success != nil && !*fail.Success {
		return nil, errors.New(fail.Error)
	}
	if err != nil && len(out) == 0 {
		return nil, err
	}
	return json.RawMessage(out), nil
}

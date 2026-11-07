// meowx-agent：飞牛 NAS 版的控制服务（网页界面 + 管理内核 / 透明代理 / 喵窝），容器里的 PID 1。
package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mmwx-group/meowx-fnos/agent/internal/api"
	"github.com/mmwx-group/meowx-fnos/agent/internal/app"
	"github.com/mmwx-group/meowx-fnos/agent/internal/hijack"
)

var version = "dev"

//go:embed web
var webFS embed.FS

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	a, err := app.New(version, envOr("MEOWX_DATA", "/data"), envOr("MEOWX_CORE", "/usr/bin/meowx-core"),
		envOr("MEOWX_TAILSCALE_DIR", "/usr/bin"), envOr("MEOWX_DASHBOARD", ""))
	if err != nil {
		log.Fatal(err)
	}
	a.Log.Add("agent", "MeowX "+version+" 启动")
	a.RecoverTrial()
	hijack.CleanupLAN() // 上次崩溃留下的旁路由规则

	web, _ := fs.Sub(webFS, "web")
	srv := &http.Server{
		Addr:              ":" + envOr("MEOWX_UI_PORT", "7899"),
		Handler:           (&api.Server{App: a, Web: web}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	go a.Loop(ctx)
	go func() {
		if a.Settings.Get().Enabled {
			_ = a.Apply(ctx)
		}
		if a.Settings.Get().Mesh.Enabled {
			_ = a.Mesh.Apply(ctx)
		}
	}()
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	<-ctx.Done()
	// 容器停止 / 卸载：先停内核（它退出时撤掉自己加的路由），再关网页
	a.Shutdown()
	shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shut)
}

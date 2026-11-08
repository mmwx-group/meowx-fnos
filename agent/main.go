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
	"github.com/mmwx-group/meowx-fnos/agent/internal/update"
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
	// 检查更新 + 一键更新：MEOWX_HOST_DATA（数据目录在宿主上的路径）与应用中心的 socket 由 docker-compose 给
	a.Update = &update.Updater{
		Version: version, DataDir: a.DataDir,
		FeedURL:     os.Getenv("MEOWX_UPDATE_FEED"), // 只给测试用，缺省是 dl.miaomiaowux.com 的发布清单
		Socket:      envOr("MEOWX_APPCENTER_SOCK", update.DefaultSocket),
		HostDataDir: os.Getenv("MEOWX_HOST_DATA"),
		Client:      &http.Client{Timeout: 15 * time.Minute},
		Log:         func(s string) { a.Log.Add("agent", s) },
	}
	a.Update.Cleanup()
	a.RecoverTrial()
	a.TrialIfUnconfirmed() // 停着时改过透明代理、之后才开起来的（如改完就重启了容器）：这次启动先进试用期
	hijack.CleanupLAN()    // 上次崩溃留下的规则
	hijack.CleanupHost()

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

// meowx-agent：飞牛 NAS 版的控制服务（网页界面 + 管理内核 / 透明代理 / 喵窝）。
// 现在只是骨架：起网页、报告容器里拿到的能力（TUN、netfilter netlink、宿主网络），用来验证 fpk + Docker 这条路。
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

var version = "dev"

func main() {
	addr := ":" + envOr("MEOWX_UI_PORT", "7899")
	mux := http.NewServeMux()
	mux.HandleFunc("/api/probe", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(probe())
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		b, _ := json.MarshalIndent(probe(), "", "  ")
		fmt.Fprintf(w, "<!doctype html><meta charset=utf-8><title>MeowX</title><h1>MeowX %s</h1><pre>%s</pre>", version, b)
	})
	log.Printf("meowx-agent %s listening on %s", version, addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

type probeResult struct {
	Version    string            `json:"version"`
	Arch       string            `json:"arch"`
	UID        int               `json:"uid"`
	Interfaces []string          `json:"interfaces"`
	TUN        string            `json:"tun"`
	Netfilter  string            `json:"netfilter"`
	Env        map[string]string `json:"env"`
}

func probe() probeResult {
	p := probeResult{Version: version, Arch: runtime.GOARCH, UID: os.Getuid(), Env: map[string]string{}}
	if ifs, err := net.Interfaces(); err == nil {
		for _, i := range ifs {
			p.Interfaces = append(p.Interfaces, i.Name)
		}
	}
	p.TUN = probeTUN()
	p.Netfilter = probeNetfilter()
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "TRIM_") || strings.HasPrefix(kv, "MEOWX_") {
			k, v, _ := strings.Cut(kv, "=")
			p.Env[k] = v
		}
	}
	return p
}

// 建一个不持久的 TUN 设备再关掉（关闭 fd 设备就没了）
func probeTUN() string {
	fd, err := unix.Open("/dev/net/tun", unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return "open /dev/net/tun: " + err.Error()
	}
	defer unix.Close(fd)
	var req [unix.IFNAMSIZ + 64]byte
	copy(req[:], "meowx-probe")
	*(*uint16)(unsafe.Pointer(&req[unix.IFNAMSIZ])) = unix.IFF_TUN | unix.IFF_NO_PI
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(unix.TUNSETIFF), uintptr(unsafe.Pointer(&req[0]))); e != 0 {
		return "TUNSETIFF: " + e.Error()
	}
	return "ok"
}

func probeNetfilter() string {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_NETFILTER)
	if err != nil {
		return "socket: " + err.Error()
	}
	unix.Close(fd)
	return "ok"
}

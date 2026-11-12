// Package platform：控制服务跑在哪种外壳里（飞牛 fpk / 通用 Docker 版），由环境变量 MEOWX_PLATFORM 决定。
//
// 只影响显示名、报给主控的系统名 / 客户端名、订阅与检查更新的 UA、喵窝路由器标识的前缀、一键更新走不走飞牛应用中心。
// 缺省（没设或写错）= fnos，飞牛版的行为一个字节不变。
//
//	fnos      飞牛 NAS 版（fpk，经飞牛应用中心一键更新）
//	docker    通用 Docker 版
//	synology  群晖（Docker 版，只换显示名与报给主控的系统名）
//	qnap      威联通（同上）
//	ugreen    绿联（同上）
package platform

import (
	"os"
	"strings"
)

type Info struct {
	ID         string // fnos / docker / synology / qnap / ugreen
	Edition    string // 网页上的版本名，如「飞牛 NAS 版」
	MeshOS     string // 喵窝入网时报给主控的系统名
	MeshClient string // 喵窝入网时报给主控的客户端名
	UAOS       string // 订阅 / 检查更新 UA 括号里的系统名：MeowX/<版本> (<这里>)
	Docker     bool   // 通用 Docker 版（不经飞牛应用中心更新）
}

var table = map[string]Info{
	"fnos":     {ID: "fnos", Edition: "飞牛 NAS 版", MeshOS: "fnos", MeshClient: "meowx-fnos", UAOS: "fnOS"},
	"docker":   {ID: "docker", Edition: "Docker 版", MeshOS: "linux", MeshClient: "meowx-docker", UAOS: "Docker", Docker: true},
	"synology": {ID: "synology", Edition: "群晖 Docker 版", MeshOS: "synology", MeshClient: "meowx-docker", UAOS: "Synology", Docker: true},
	"qnap":     {ID: "qnap", Edition: "威联通 Docker 版", MeshOS: "qnap", MeshClient: "meowx-docker", UAOS: "QNAP", Docker: true},
	"ugreen":   {ID: "ugreen", Edition: "绿联 Docker 版", MeshOS: "ugreen", MeshClient: "meowx-docker", UAOS: "UGREEN", Docker: true},
}

// Parse：按名字取，认不出来的当飞牛版
func Parse(id string) Info {
	if p, ok := table[strings.ToLower(strings.TrimSpace(id))]; ok {
		return p
	}
	return table["fnos"]
}

// Current：按 MEOWX_PLATFORM 取
func Current() Info { return Parse(os.Getenv("MEOWX_PLATFORM")) }

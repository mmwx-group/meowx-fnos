//go:build !linux

package hijack

import "errors"

// 非 Linux（在 Mac 上本地跑控制服务看界面时）：没有旁路由

func ApplyLAN(LANPlan) error { return errors.New("旁路由只在 Linux 上可用") }

func CleanupLAN() {}

func ApplyHost(HostPlan) error { return errors.New("只在 Linux 上可用") }

func CleanupHost() {}

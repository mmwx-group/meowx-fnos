//go:build !linux

package mesh

import (
	"errors"

	"github.com/mmwx-group/meowx-fnos/agent/internal/store"
)

func firewallApply(store.Mesh, []Site) error {
	return errors.New("喵窝的防火墙只在 Linux 上可用")
}

func firewallRemove() {}

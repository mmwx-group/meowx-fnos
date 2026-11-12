//go:build linux

package fw

import (
	"github.com/google/nftables"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func probe() Probe {
	p := Probe{Legacy: countRules("legacy"), NFT: countRules("nft")}
	if c, err := nftables.New(); err == nil {
		if _, err := c.ListTablesOfFamily(nftables.TableFamilyINet); err == nil {
			p.NFTables = true
		}
	}
	if _, err := netlink.RuleList(unix.AF_INET); err == nil {
		p.PolicyRouting = true
	}
	return p
}

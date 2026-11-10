//go:build !linux

package fw

// 非 Linux（在 Mac 上本地跑控制服务看界面）：当作 nftables，什么都不下发
func probe() Probe { return Probe{NFTables: true, PolicyRouting: true, Legacy: -1, NFT: -1} }

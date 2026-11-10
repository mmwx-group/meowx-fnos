// Package fw：防火墙后端的选择与 iptables 的公共操作（透明代理 hijack 与喵窝 mesh 共用）。
//
// 两套后端并存，运行时选：
//   - nftables（缺省，飞牛等新内核）：经 netlink 直接下发（google/nftables），镜像里不需要任何程序；
//   - iptables（群晖 DSM 7 这类没有 nf_tables、也没有 TPROXY / NETMAP 的内核）：调镜像里的 iptables-legacy（或 iptables-nft）
//     程序，规则都放在自己的链（MEOWX_*）里，内置链里只有跳转，撤的时候按 iptables-save 找出来逐条删掉。
//
// 另外看有没有策略路由（ip rule）：没有的话透明代理只能退化成 REDIRECT（TCP + DNS），UDP 不代理。
//
// 环境变量（不设 = auto）：
//
//	MEOWX_FIREWALL=auto|nftables|iptables
//	MEOWX_IPTABLES=auto|legacy|nft         iptables 后端用哪一套（auto：哪套里已有规则多用哪套，Docker 的规则在哪套就跟哪套）
//	MEOWX_POLICY_ROUTING=auto|on|off       off = 当作没有策略路由（强制退化成 REDIRECT）
package fw

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

const (
	NFTables = "nftables"
	IPTables = "iptables"
)

// Env：选好的防火墙后端（零值 = nftables + 有策略路由，即飞牛版一直以来的做法）
type Env struct {
	Backend         string `json:"backend"`            // nftables / iptables
	IPTables        string `json:"iptables,omitempty"` // iptables 后端用的那一套：legacy / nft
	NoPolicyRouting bool   `json:"no_policy_routing,omitempty"`
	Note            string `json:"note,omitempty"` // 怎么选出来的（给网页 / 日志看）
}

func (e Env) IsIPTables() bool { return e.Backend == IPTables }

// Describe：一句话说明（写日志）
func (e Env) Describe() string {
	s := "nftables"
	if e.IsIPTables() {
		s = "iptables（" + e.IPTables + "）"
	}
	if e.NoPolicyRouting {
		s += "，没有策略路由（透明代理退化成 REDIRECT，UDP 不代理）"
	}
	if e.Note != "" {
		s += "；" + e.Note
	}
	return s
}

// Probe：探测到的系统情况（choose 的输入）
type Probe struct {
	NFTables      bool // 内核的 nf_tables 能用
	PolicyRouting bool // ip rule 能用
	Legacy        int  // iptables-legacy 里已有的规则数，-1 = 用不了（没有程序或内核不支持）
	NFT           int  // iptables-nft 里已有的规则数，-1 = 用不了
}

// Choose：按环境变量与探测结果选后端（纯函数，单测覆盖）
func Choose(firewall, iptVariant, policyRouting string, p Probe) Env {
	var e Env
	haveIPT := p.Legacy >= 0 || p.NFT >= 0
	switch strings.ToLower(strings.TrimSpace(firewall)) {
	case "nftables", "nft":
		e.Backend, e.Note = NFTables, "MEOWX_FIREWALL 指定"
	case "iptables":
		e.Backend, e.Note = IPTables, "MEOWX_FIREWALL 指定"
	default:
		switch {
		case p.NFTables && p.PolicyRouting:
			e.Backend = NFTables
		case haveIPT:
			e.Backend = IPTables
			if !p.NFTables {
				e.Note = "内核没有 nf_tables"
			} else {
				e.Note = "没有策略路由"
			}
		default:
			e.Backend = NFTables
		}
	}
	if e.Backend == IPTables {
		switch strings.ToLower(strings.TrimSpace(iptVariant)) {
		case "legacy":
			e.IPTables = "legacy"
		case "nft":
			e.IPTables = "nft"
		default:
			// 跟着宿主上已有的规则走（Docker 自己的 DOCKER-USER 在哪套里，放行规则就得写在哪套里）
			switch {
			case p.NFT < 0:
				e.IPTables = "legacy"
			case p.Legacy < 0:
				e.IPTables = "nft"
			case p.Legacy > p.NFT:
				e.IPTables = "legacy"
			case p.NFT > p.Legacy:
				e.IPTables = "nft"
			case p.NFTables:
				e.IPTables = "nft"
			default:
				e.IPTables = "legacy"
			}
		}
	}
	switch strings.ToLower(strings.TrimSpace(policyRouting)) {
	case "off", "0", "false", "no":
		e.NoPolicyRouting = true
	case "on", "1", "true", "yes":
	default:
		e.NoPolicyRouting = !p.PolicyRouting
	}
	return e
}

var (
	detectOnce sync.Once
	detected   Env
)

// Detect：第一次调用时探测，之后用缓存（容器跑起来以后内核能力不会变）
func Detect() Env {
	detectOnce.Do(func() {
		detected = Choose(os.Getenv("MEOWX_FIREWALL"), os.Getenv("MEOWX_IPTABLES"), os.Getenv("MEOWX_POLICY_ROUTING"), probe())
	})
	return detected
}

// ---------- iptables ----------

// Runner：跑一个程序（stdin 可空），回合并的输出。测试里换成假的
type Runner func(stdin, name string, args ...string) (string, error)

// IPT：一套 iptables 程序（iptables-legacy* 或 iptables-nft*）
type IPT struct {
	Variant string // legacy / nft
	Run     Runner
}

func (t IPT) bin(suffix string) string { return "iptables-" + t.Variant + suffix }

// Script：一次 iptables-restore --noflush 的内容，按表分段
type Script struct {
	order []string
	lines map[string][]string
}

// Add 往某张表里加一行（":链 - [0:0]"、"-A …"、"-I …"）
func (s *Script) Add(table string, line string) {
	if s.lines == nil {
		s.lines = map[string][]string{}
	}
	if _, ok := s.lines[table]; !ok {
		s.order = append(s.order, table)
	}
	s.lines[table] = append(s.lines[table], line)
}

// Chain 声明自己的链（restore --noflush 时：没有就建，有就清空）
func (s *Script) Chain(table, chain string) { s.Add(table, ":"+chain+" - [0:0]") }

func (s *Script) Empty() bool { return len(s.order) == 0 }

func (s *Script) String() string {
	var b strings.Builder
	for _, t := range s.order {
		b.WriteString("*" + t + "\n")
		for _, l := range s.lines[t] {
			b.WriteString(l + "\n")
		}
		b.WriteString("COMMIT\n")
	}
	return b.String()
}

// Restore：整份交给 iptables-restore --noflush（每张表原子生效）
func (t IPT) Restore(s *Script) error {
	if s.Empty() {
		return nil
	}
	if out, err := t.Run(s.String(), t.bin("-restore"), "--noflush", "-w", "5"); err != nil {
		return fmt.Errorf("iptables-%s-restore：%v %s", t.Variant, err, strings.TrimSpace(out))
	}
	return nil
}

// Save：iptables-save 的输出（只要这几张表）
func (t IPT) Save() (string, error) {
	var all strings.Builder
	for _, table := range []string{"nat", "mangle", "filter"} {
		out, err := t.Run("", t.bin("-save"), "-t", table)
		if err != nil {
			return all.String(), err
		}
		all.WriteString(out)
	}
	return all.String(), nil
}

// HasChain：某张表里有没有这条链（如 filter 表的 DOCKER-USER）
func (t IPT) HasChain(table, chain string) bool {
	out, err := t.Run("", t.bin("-save"), "-t", table)
	if err != nil {
		return false
	}
	return ChainIn(out, table, chain)
}

// ChainIn：在 iptables-save 的输出里找链
func ChainIn(save, table, chain string) bool {
	cur := ""
	for _, line := range strings.Split(save, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "*"):
			cur = line[1:]
		case cur == table && strings.HasPrefix(line, ":"+chain+" "):
			return true
		}
	}
	return false
}

// Cmd：一条 iptables 命令（Cleanup 的结果，按顺序跑）
type Cmd struct {
	Table string
	Args  []string
}

// CleanupCmds：从 iptables-save 的输出算出撤掉某个前缀的链要跑的命令：先删别的链里跳到它们的规则，再清空、删掉这些链
func CleanupCmds(save, prefix string) []Cmd {
	var dels, flush, drop []Cmd
	cur := ""
	for _, line := range strings.Split(save, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "*"):
			cur = line[1:]
		case strings.HasPrefix(line, ":"+prefix):
			name := strings.Fields(line[1:])[0]
			flush = append(flush, Cmd{cur, []string{"-F", name}})
			drop = append(drop, Cmd{cur, []string{"-X", name}})
		case strings.HasPrefix(line, "-A "):
			f := strings.Fields(line)
			if len(f) < 2 || strings.HasPrefix(f[1], prefix) {
				continue // 自己链里的规则跟着 -F 一起没
			}
			for i := 2; i+1 < len(f); i++ {
				if (f[i] == "-j" || f[i] == "-g") && strings.HasPrefix(f[i+1], prefix) {
					f[0] = "-D"
					dels = append(dels, Cmd{cur, f})
					break
				}
			}
		}
	}
	return append(append(dels, flush...), drop...)
}

// Cleanup：撤掉某个前缀的全部链（没有就什么都不做）。出错只回第一个
func (t IPT) Cleanup(prefix string) error {
	save, err := t.Save()
	if err != nil {
		return err
	}
	var first error
	for _, c := range CleanupCmds(save, prefix) {
		args := append([]string{"-w", "5", "-t", c.Table}, c.Args...)
		if out, err := t.Run("", t.bin(""), args...); err != nil && first == nil {
			first = fmt.Errorf("iptables %s：%v %s", strings.Join(args, " "), err, strings.TrimSpace(out))
		}
	}
	return first
}

// Variants：镜像里有的 iptables 程序（撤规则时两套都撤，免得换过一套后留下另一套里的）
func Variants(run Runner) []IPT {
	var out []IPT
	for _, v := range []string{"legacy", "nft"} {
		if haveBinary("iptables-" + v + "-save") {
			out = append(out, IPT{Variant: v, Run: run})
		}
	}
	return out
}

// Current：选好的那一套（后端不是 iptables 时 ok = false）
func Current() (IPT, bool) {
	e := Detect()
	if !e.IsIPTables() {
		return IPT{}, false
	}
	return IPT{Variant: e.IPTables, Run: Exec}, true
}

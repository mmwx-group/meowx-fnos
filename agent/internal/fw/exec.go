package fw

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"time"
)

// Exec：真的跑程序（30 秒超时：iptables 拿不到锁时别卡住控制服务）
func Exec(stdin, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

func haveBinary(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// countRules：一套 iptables 里已有的规则数，-1 = 用不了
func countRules(variant string) int {
	if !haveBinary("iptables-" + variant + "-save") {
		return -1
	}
	// 不带 -t（见 IPT.Save）
	out, err := Exec("", "iptables-"+variant+"-save")
	if err != nil {
		return -1
	}
	n := 0
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "-A ") {
			n++
		}
	}
	return n
}

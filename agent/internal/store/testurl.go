package store

import (
	"errors"
	"net/url"
	"strings"
)

const testURLMax = 2048

// NormalizeTestURL 校验设置里的节点延迟测试地址（`test_url`），回去掉首尾空白的写法。
// 空 = 跟随订阅：测某个组用这个组写的 url，没写的用内置默认（取哪个由网页的节点页定，同客户端的 LatencyTestURL）；
// 填了就一律用它。只接受带主机名的 http / https 地址（内核的延迟测试只会这两种）。
func NormalizeTestURL(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if len(s) > testURLMax {
		return "", errors.New("测速地址太长")
	}
	if strings.ContainsAny(s, " \t\r\n") {
		return "", errors.New("测速地址里不能有空格")
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", errors.New("测速地址不是合法的网址")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("测速地址只能是 http:// 或 https:// 开头的网址")
	}
	if u.Hostname() == "" {
		return "", errors.New("测速地址缺少主机名")
	}
	return s, nil
}

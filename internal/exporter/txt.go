package exporter

import (
	"strconv"
	"strings"

	"cloudtrace/internal/model"
)

// TXT 把结果集渲染成每行一个 ip:port 的纯文本。
//
// 这是给脚本和其他工具用的格式，因此不做任何筛选与排序：调用方给什么就
// 输出什么，顺序即结果页上看到的顺序。
func TXT(records []model.IPRecord) []byte {
	var b strings.Builder
	for _, rec := range records {
		b.WriteString(HostPort(rec.IP, rec.Port))
		b.WriteString("\n")
	}
	return []byte(b.String())
}

// HostPort 拼出「地址:端口」，IPv6 自动加方括号。
//
// 不加方括号的 2606:4700::1:443 无法被任何下游解析——冒号到底属于地址还是
// 分隔符是读不出来的，这也是 RFC 3986 规定加括号的原因。
func HostPort(ip string, port int) string {
	host := strings.TrimSpace(ip)
	if host == "" {
		return ""
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	if port <= 0 {
		return host
	}
	return host + ":" + strconv.Itoa(port)
}

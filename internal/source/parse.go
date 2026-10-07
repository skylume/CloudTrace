package source

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Kind 是来源条目的类型。
type Kind string

const (
	// KindCIDR 是一个网段，Value 是规范化后的 CIDR。
	KindCIDR Kind = "cidr"
	// KindIP 是单个 IP，Value 是规范化后的 IP 文本。
	KindIP Kind = "ip"
	// KindRange 是一段 IP 区间，Value 形如 "起始-结束"。
	KindRange Kind = "range"
	// KindHost 是一个域名，Value 是小写域名。
	KindHost Kind = "host"
)

// Entry 是一条解析出的候选来源。
type Entry struct {
	Kind  Kind
	Value string
	Port  int // 0 表示未指定，使用扫描的默认端口
}

// Parsed 是来源文本的解析结果。
//
// 各分类计数供输入框做实时预览，Entries 供后续流程消费。
type Parsed struct {
	Entries []Entry

	CIDRs   int // 网段数
	IPs     int // 节点数
	Ranges  int // 区间数
	Hosts   int // 域名数
	Ports   int // 带显式端口的条目数
	Ignored int // 被忽略的行数（注释、空行、无法识别的内容）
}

// Empty 报告是否一条都没解析出来。
func (p Parsed) Empty() bool { return len(p.Entries) == 0 }

// ParseSourceText 解析多形态来源文本。
//
// 支持一行一个条目，也支持用逗号、分号、竖线或空白分隔的多个条目；
// 以 # 开头的行、以及 # 前有空白的内容视为注释。
//
// 这是**纯函数**：只做文本解析，不查 DNS。域名到 IP 的解析是单独一步
// （ResolveHosts），否则解析器没法单测。
//
// 无法识别的行不会被丢弃，而是计入 Ignored，让界面能提示「有几行没看懂」。
// 输入非空但一条都没解析出来时返回错误——这是值得直接告诉用户的输入问题。
func ParseSourceText(text string) (Parsed, error) {
	var out Parsed
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(stripComment(raw))
		if line == "" {
			continue
		}
		for _, token := range splitTokens(line) {
			entry, ok := parseToken(token)
			if !ok {
				out.Ignored++
				continue
			}
			out.Entries = append(out.Entries, entry)
			switch entry.Kind {
			case KindCIDR:
				out.CIDRs++
			case KindIP:
				out.IPs++
			case KindRange:
				out.Ranges++
			case KindHost:
				out.Hosts++
			}
			if entry.Port > 0 {
				out.Ports++
			}
		}
	}
	if out.Empty() && out.Ignored > 0 {
		return out, fmt.Errorf("没有解析出任何可用的来源（忽略了 %d 行）", out.Ignored)
	}
	return out, nil
}

// stripComment 去掉行内注释。
//
// 只把「行首的 #」与「前面有空白的 #」当注释起点：URL 里也可能出现 #，
// 那种情况不该被截断。
func stripComment(line string) string {
	for i := 0; i < len(line); i++ {
		if line[i] != '#' {
			continue
		}
		if i == 0 || line[i-1] == ' ' || line[i-1] == '\t' {
			return line[:i]
		}
	}
	return line
}

// splitTokens 按逗号、分号、竖线与空白切分一行。
//
// 不能切冒号（端口）、斜杠（网段）、连字符（区间），所以只认这几种分隔符。
func splitTokens(line string) []string {
	return strings.FieldsFunc(line, func(r rune) bool {
		switch r {
		case ',', ';', '|':
			return true
		}
		return r == ' ' || r == '\t' || r == '\r'
	})
}

// parseToken 解析单个条目。
func parseToken(token string) (Entry, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Entry{}, false
	}
	if strings.Contains(token, "://") {
		return parseURLToken(token)
	}
	if strings.HasPrefix(token, "[") {
		return parseBracketedToken(token)
	}
	if strings.Contains(token, "/") {
		_, network, err := net.ParseCIDR(token)
		if err != nil {
			return Entry{}, false
		}
		return Entry{Kind: KindCIDR, Value: network.String()}, true
	}
	// 先试纯 IP：IPv6 自身带冒号，不能先按 host:port 拆。
	if ip := net.ParseIP(token); ip != nil {
		return Entry{Kind: KindIP, Value: ip.String()}, true
	}
	if start, end, ok := splitRange(token); ok {
		return Entry{Kind: KindRange, Value: start + "-" + end}, true
	}

	host, port := splitHostPortLoose(token)
	if ip := net.ParseIP(host); ip != nil {
		return Entry{Kind: KindIP, Value: ip.String(), Port: port}, true
	}
	if isHostname(host) {
		return Entry{Kind: KindHost, Value: strings.ToLower(host), Port: port}, true
	}
	return Entry{}, false
}

// parseURLToken 解析带协议的地址，只取主机名与显式端口。
func parseURLToken(token string) (Entry, bool) {
	u, err := url.Parse(token)
	if err != nil || u.Hostname() == "" {
		return Entry{}, false
	}
	host := u.Hostname()
	port := 0
	if p := u.Port(); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 && v <= 65535 {
			port = v
		}
	}
	if ip := net.ParseIP(host); ip != nil {
		return Entry{Kind: KindIP, Value: ip.String(), Port: port}, true
	}
	if !isHostname(host) {
		return Entry{}, false
	}
	return Entry{Kind: KindHost, Value: strings.ToLower(host), Port: port}, true
}

// parseBracketedToken 解析 [IPv6] 与 [IPv6]:port 两种写法。
func parseBracketedToken(token string) (Entry, bool) {
	end := strings.IndexByte(token, ']')
	if end < 0 {
		return Entry{}, false
	}
	ip := net.ParseIP(token[1:end])
	if ip == nil {
		return Entry{}, false
	}
	rest := token[end+1:]
	if rest == "" {
		return Entry{Kind: KindIP, Value: ip.String()}, true
	}
	if !strings.HasPrefix(rest, ":") {
		return Entry{}, false
	}
	port, err := strconv.Atoi(rest[1:])
	if err != nil || port <= 0 || port > 65535 {
		return Entry{}, false
	}
	return Entry{Kind: KindIP, Value: ip.String(), Port: port}, true
}

// splitRange 解析 "起始-结束" 形式，要求两端同族且起始不大于结束。
func splitRange(token string) (string, string, bool) {
	i := strings.IndexByte(token, '-')
	if i <= 0 || i == len(token)-1 {
		return "", "", false
	}
	start := net.ParseIP(token[:i])
	end := net.ParseIP(token[i+1:])
	if start == nil || end == nil {
		return "", "", false
	}
	if (start.To4() == nil) != (end.To4() == nil) {
		return "", "", false
	}
	if bytes.Compare(start, end) > 0 {
		return "", "", false
	}
	return start.String(), end.String(), true
}

// splitHostPortLoose 宽松地拆 host:port。
//
// 拆不出来（没有冒号、端口非法、冒号前为空）时把整串当主机名返回，
// 端口为 0。这样调用方不必先判断有没有端口。
func splitHostPortLoose(token string) (string, int) {
	i := strings.LastIndexByte(token, ':')
	if i <= 0 || i == len(token)-1 {
		return token, 0
	}
	port, err := strconv.Atoi(token[i+1:])
	if err != nil || port <= 0 || port > 65535 {
		return token, 0
	}
	return token[:i], port
}

// isHostname 判断是不是合法域名。
//
// 要求至少有一个点、每段非空且只含字母数字与连字符、段首尾不是连字符、
// 顶级段是纯字母且长度不少于 2。最后两条是为了把 "1.2.3.4.5" 这类
// 数字串挡在外面。
func isHostname(s string) bool {
	s = strings.Trim(s, ".")
	if s == "" || len(s) > 253 || !strings.Contains(s, ".") {
		return false
	}
	labels := strings.Split(s, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return false
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for i := 0; i < len(label); i++ {
			if b := label[i]; !isASCIIAlnum(b) && b != '-' {
				return false
			}
		}
	}
	tld := labels[len(labels)-1]
	if len(tld) < 2 {
		return false
	}
	for i := 0; i < len(tld); i++ {
		if !isASCIILetter(tld[i]) {
			return false
		}
	}
	return true
}

// Resolver 抽象 DNS 查询，便于测试注入假实现。
type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// DefaultResolver 返回系统 DNS 解析器。
func DefaultResolver() Resolver { return net.DefaultResolver }

// ResolveHosts 把域名解析成 IP。
//
// 与文本解析分开是刻意的：文本解析保持纯函数才能单测，DNS 这一步单独做。
// 单个域名解析失败不影响其他域名，失败清单由第二个返回值给出。
// 只有在解析器缺失或 context 取消时才返回错误。
func ResolveHosts(ctx context.Context, resolver Resolver, hosts []string) (ips []string, failed []string, err error) {
	if resolver == nil {
		return nil, nil, errors.New("DNS 解析器不能为空")
	}
	seen := make(map[string]bool, len(hosts))
	for _, host := range hosts {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ips, failed, ctxErr
		}
		addrs, lookupErr := resolver.LookupIPAddr(ctx, host)
		if lookupErr != nil {
			// 取消不是「这个域名解析不了」：记成失败会让界面把它显示成
			// 「域名有问题」，而用户只是点了停止。立刻返回，让调用方按
			// 中止处理。
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ips, failed, ctxErr
			}
			failed = append(failed, host)
			continue
		}
		if len(addrs) == 0 {
			failed = append(failed, host)
			continue
		}
		for _, addr := range addrs {
			ip := addr.IP.String()
			if seen[ip] {
				continue
			}
			seen[ip] = true
			ips = append(ips, ip)
		}
	}
	return ips, failed, nil
}

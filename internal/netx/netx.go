// Package netx 按配置构造网络原语。
//
// 这里只管一件事：**域名怎么解析**。
//
// 需要注意作用范围——探测目标（扫描、测速、CF 校验）全都是 IP，走的是
// 「直接拨这个地址」，不经过任何解析。真正需要 DNS 的是程序自己发起的那些
// HTTP 请求：远程源、归属地库、测速地址与出口探测。自定义 DNS 只作用于后者。
package netx

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"
)

// Dialer 的签名与 net.Dialer.DialContext 一致，可直接赋给 http.Transport。
type Dialer func(ctx context.Context, network, addr string) (net.Conn, error)

// NewDialer 按配置构造拨号函数。
//
// servers 为空时返回 nil——调用方据此走默认路径（系统解析）。返回 nil 而不是
// 一个「什么都不做」的包装，是为了让「没配自定义 DNS」这条路上不多一层函数调用，
// 也让调用方一眼能看出自己有没有被接管。
func NewDialer(servers []string, fallback bool) Dialer {
	resolver := newResolver(servers)
	if resolver == nil {
		return nil
	}
	// 返回方法值而不是结构体：Dialer 是函数类型，调用方直接把它交给
	// http.Transport.DialContext 即可。
	d := &dialer{
		resolver: resolver,
		fallback: fallback,
		base:     &net.Dialer{Timeout: DefaultTimeout},
	}
	return d.dialContext
}

// dialer 先解析再按解析结果拨号。
type dialer struct {
	resolver *net.Resolver
	fallback bool
	base     *net.Dialer
}

func (d *dialer) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		// 没有端口的部分（或格式怪异的地址）交给标准库去报错。
		return d.base.DialContext(ctx, network, addr)
	}
	// 目标本来就是 IP：不必也不能再解析。扫描与测速走的全是这条路。
	if net.ParseIP(host) != nil {
		return d.base.DialContext(ctx, network, addr)
	}

	ips, err := d.lookup(ctx, host)
	if err != nil {
		return nil, err
	}

	var lastErr error
	for _, ip := range ips {
		conn, dialErr := d.base.DialContext(ctx, network, net.JoinHostPort(ip, port))
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	if lastErr == nil {
		lastErr = &net.AddrError{Err: "解析结果为空", Addr: host}
	}
	return nil, lastErr
}

// lookup 先问自定义 DNS，按配置决定要不要回退系统解析。
func (d *dialer) lookup(ctx context.Context, host string) ([]string, error) {
	return pickLookup(ctx, host, d.resolver.LookupHost, net.DefaultResolver.LookupHost, d.fallback)
}

// pickLookup 是「先自定义、失败再回退」的决策本身。
//
// 两个解析函数作为参数传进来而不是直接调包级的：回退这条路径只有注入假实现
// 才测得到——回环地址走的是 hosts 文件、根本不会去问 DNS，拿真名字又必须联网。
type lookupFunc func(ctx context.Context, host string) ([]string, error)

func pickLookup(ctx context.Context, host string, custom, system lookupFunc, fallback bool) ([]string, error) {
	ips, err := custom(ctx, host)
	if err == nil && len(ips) > 0 {
		return ips, nil
	}
	if !fallback {
		if err == nil {
			err = &net.DNSError{Err: "解析结果为空", Name: host}
		}
		return nil, err
	}

	// 自定义 DNS 挂掉时回退系统解析：用户配它是为了拿到更准的结果，不是为了
	// 在它不可用时彻底上不了网。
	systemIPs, sysErr := system(ctx, host)
	if sysErr == nil && len(systemIPs) > 0 {
		return systemIPs, nil
	}
	// 两条路都失败时优先报自定义那条：那才是用户刚配的东西。
	if err != nil {
		return nil, err
	}
	if sysErr != nil {
		return nil, sysErr
	}
	return nil, &net.DNSError{Err: "解析结果为空", Name: host}
}

// newResolver 构造指向指定服务器的解析器；没有可用服务器时返回 nil。
func newResolver(servers []string) *net.Resolver {
	targets := normalizeServers(servers)
	if len(targets) == 0 {
		return nil
	}

	var mu sync.Mutex
	next := 0

	return &net.Resolver{
		// PreferGo 必须为真：用 cgo 解析器时 Dial 根本不会被调用，
		// 自定义 DNS 会静默失效——而界面上它显示为已生效。
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			mu.Lock()
			target := targets[next%len(targets)]
			next++
			mu.Unlock()

			// 给「连 DNS 服务器」本身也加个上限：不回包的服务器会让请求
			// 一直挂到上下文取消，而用户看到的是「点了没反应」。
			dialCtx, cancel := context.WithTimeout(ctx, DefaultTimeout)
			defer cancel()

			var d net.Dialer
			return d.DialContext(dialCtx, network, target)
		},
	}
}

// normalizeServers 补上端口并去掉空项。
//
// 用户填的多半是 `1.1.1.1` 这种裸地址，而 Dial 要的是 host:port。
// 支持带端口的写法（自建 DNS 常监听在 5353 之类）。
func normalizeServers(servers []string) []string {
	out := make([]string, 0, len(servers))
	for _, raw := range servers {
		item := strings.TrimSpace(raw)
		if item == "" {
			continue
		}
		if _, _, err := net.SplitHostPort(item); err != nil {
			item = net.JoinHostPort(item, "53")
		}
		out = append(out, item)
	}
	return out
}

// DefaultTimeout 是解析与拨号的默认超时。
//
// 没有它，一个不回包的 DNS 服务器会让请求挂到上下文取消为止——用户看到的是
// 「点了没反应」。
const DefaultTimeout = 5 * time.Second

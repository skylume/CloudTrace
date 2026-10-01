package probe

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ChromeUA 是探测请求使用的 User-Agent。
//
// 用真实的浏览器 UA：部分节点的边缘会按 UA 区分放行策略，
// 用 Go 默认 UA 拿到的响应与真实用户不一致。
const ChromeUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

// maxTraceBody 是读取 trace 响应体的上限，防止异常节点返回超大响应把内存吃满。
const maxTraceBody = 64 << 10

// ThresholdMultiplier 返回 HTTPing 延迟阈值相对 TCPing 的放大倍率。
//
// TLS 握手要比纯 TCP 多花 2~3 个往返，用同一把阈值会把所有 TLS 节点误杀，
// 所以按是否走 TLS 放大阈值。
func ThresholdMultiplier(useTLS bool) float64 {
	if useTLS {
		return 4.0
	}
	return 1.3
}

// DirectTransport 构建强制直连目标 IP 的 HTTP 传输层。
//
// 为什么必须替换 DialContext：请求的 URL 用的是测试域名，若交给默认传输层
// 解析，DNS 很可能把我们导到另一个 IP 上，测出来的延迟与目标节点无关。
// 这里把地址写死成 ip:port，域名只用于 Host 头与 TLS SNI。
//
// 代理一律禁用：用户环境里若配了系统代理，请求会走到代理上，
// 既测不出目标节点的延迟，也测不出它的带宽。
func DirectTransport(ip string, port int, host string, useTLS bool, timeout time.Duration) *http.Transport {
	address := net.JoinHostPort(ip, strconv.Itoa(port))
	dial := dialerFor(timeout)

	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dial(ctx, network, address)
		},
		// 关闭长连接复用：每一轮探测都要重新握手，否则从第二轮开始
		// 省掉了握手时间，同一节点会得到明显偏低且不一致的延迟。
		DisableKeepAlives:     true,
		ForceAttemptHTTP2:     false,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
	}
	if useTLS {
		transport.TLSClientConfig = &tls.Config{ServerName: stripPort(host)}
	}
	return transport
}

// stripPort 去掉主机名里的端口，用于 TLS SNI。
func stripPort(host string) string {
	if host == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

// HTTPing 通过 HTTP 请求测量到 ip:port 的首字节时间（TTFB），并顺带取回数据中心代码。
//
// 请求发往 host 的 trace 端点，但连接强制打到 ip:port 上。返回结果里的
// Colo 优先取响应体的 colo 字段，取不到再回退到 cf-ray 响应头。
//
// 参数与错误约定同 TCPing：只有参数非法才返回错误，全部请求失败
// 体现为 Loss = 1。
func HTTPing(ctx context.Context, ip string, port int, host string, useTLS bool, times int, timeout time.Duration) (Result, error) {
	if err := validateTarget(ip, port); err != nil {
		return Result{}, err
	}
	if host == "" {
		return Result{}, errors.New("测试域名不能为空")
	}
	if times < 1 {
		return Result{}, fmt.Errorf("探测次数 %d 必须至少为 1", times)
	}
	if timeout <= 0 {
		return Result{}, fmt.Errorf("超时 %v 必须为正", timeout)
	}

	transport := DirectTransport(ip, port, host, useTLS, timeout)
	defer transport.CloseIdleConnections()
	return httping(ctx, host, useTLS, times, transport)
}

// httping 是 HTTPing 的实现体，传输层由调用方注入以便测试。
func httping(ctx context.Context, host string, useTLS bool, times int, transport http.RoundTripper) (Result, error) {
	scheme := "http"
	if useTLS {
		scheme = "https"
	}
	endpoint := scheme + "://" + host + TracePath

	samples := make([]float64, 0, times)
	sent := 0
	colo := ""

	for i := 0; i < times; i++ {
		if ctx.Err() != nil {
			break
		}
		sent++

		req, err := newTraceRequest(ctx, endpoint, host)
		if err != nil {
			return Result{}, err
		}

		start := time.Now()
		resp, err := transport.RoundTrip(req)
		if err != nil {
			continue
		}
		// RoundTrip 返回时响应头已到齐，此刻即首字节时间。
		samples = append(samples, elapsedMS(start))

		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxTraceBody))
		_ = resp.Body.Close()

		if colo == "" {
			colo = extractColo(resp.Header, string(body))
		}
	}

	result := summarize(samples, sent)
	result.Colo = colo
	return result, nil
}

// FetchTrace 取回 ip:port 上 trace 端点的全部字段，用于节点明细采集。
//
// 与 HTTPing 的区别是它不关心耗时，与 VerifyCF 的区别是它不做判据判断。
// 调用方在关闭明细采集时不应调用它——那正是「开关关掉了请求照发」的
// 高发位置，判定开关必须在调用点之前。
func FetchTrace(ctx context.Context, ip string, port int, host string, useTLS bool, timeout time.Duration) (map[string]string, error) {
	if err := validateTarget(ip, port); err != nil {
		return nil, err
	}
	if host == "" {
		return nil, errors.New("测试域名不能为空")
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("超时 %v 必须为正", timeout)
	}

	transport := DirectTransport(ip, port, host, useTLS, timeout)
	defer transport.CloseIdleConnections()
	return fetchTrace(ctx, host, useTLS, transport)
}

// fetchTrace 是 FetchTrace 的实现体，传输层由调用方注入以便测试。
func fetchTrace(ctx context.Context, host string, useTLS bool, transport http.RoundTripper) (map[string]string, error) {
	scheme := "http"
	if useTLS {
		scheme = "https"
	}

	req, err := newTraceRequest(ctx, scheme+"://"+host+TracePath, host)
	if err != nil {
		return nil, err
	}
	resp, err := transport.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("取 trace 失败：%w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTraceBody))
	if err != nil {
		return nil, fmt.Errorf("读 trace 响应失败：%w", err)
	}
	return ParseTrace(string(body))
}

// newTraceRequest 构造 trace 请求：URL 用测试域名，Host 头显式指定。
func newTraceRequest(ctx context.Context, endpoint, host string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("构造请求失败：%w", err)
	}
	req.Host = host
	req.Header.Set("User-Agent", ChromeUA)
	return req, nil
}

// extractColo 按「响应体 colo 优先，cf-ray 头回退」的顺序取数据中心代码。
func extractColo(header http.Header, body string) string {
	if trace, err := ParseTrace(body); err == nil {
		if colo := ExtractColo(trace); colo != "" {
			return colo
		}
	}
	return coloFromCfRay(header.Get("Cf-Ray"))
}

// coloFromCfRay 从 cf-ray 响应头里取数据中心代码。
//
// cf-ray 形如 "7d3b3f0e2a1c4f5a-HKG"，取最后一段；也有节点只回三位代码。
func coloFromCfRay(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if i := strings.LastIndexByte(value, '-'); i >= 0 {
		value = value[i+1:]
	}
	value = strings.ToUpper(strings.TrimSpace(value))
	if len(value) != 3 {
		return ""
	}
	for _, r := range value {
		if r < 'A' || r > 'Z' {
			return ""
		}
	}
	return value
}

package geo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"cloudtrace/internal/probe"
)

// 出口地区探测的参数。
const (
	// traceURL 是 Cloudflare 的 trace 端点：它返回调用方自己的出口信息。
	//
	// 要的是**本机出口**而不是被测节点的归属——被测节点的归属由扫描阶段的
	// trace 采集负责，两者是两回事。
	traceURL = "https://www.cloudflare.com/cdn-cgi/trace"
	// traceRetries 是探测失败后的重试次数。
	//
	// 网络抖动一次就下结论会给出假警告，而假警告比不提示更费时间。
	traceRetries = 3
	// traceTimeout 是单次探测的超时。
	traceTimeout = 5 * time.Second
	// maxTraceBody 是响应体上限。正常响应只有几百字节。
	maxTraceBody = 16 << 10
)

// ShouldWarn 判断某个出口地区是否值得提示。
//
// 不提示的三种取值各有理由：
//   - 空：拿不到信息，无法判断，提示只会制造噪音；
//   - CN：国内出口，正常情况；
//   - XX：数据缺失，不代表异常；
//   - T1：Tor 出口的专用代码，属于已知的特殊情况。
//
// 其余一律提示。这一项是「你可能走了代理」的善意提醒，误报的代价只是一条
// 横幅，而漏报会让用户拿着失真的延迟数据去做决策。
func ShouldWarn(loc string) bool {
	switch strings.ToUpper(strings.TrimSpace(loc)) {
	case "", "CN", "XX", "T1":
		return false
	}
	return true
}

// DetectExitCountry 探测本机出口的国家码，失败时重试若干次。
//
// 全部失败后返回错误：调用方据此**不提示**。拿不到信息时静默比乱猜好。
func DetectExitCountry(ctx context.Context) (string, error) {
	return detectExitCountry(ctx, traceURL)
}

// detectExitCountry 是带地址注入的实现，便于用本机服务验证重试与解析。
func detectExitCountry(ctx context.Context, url string) (string, error) {
	var lastErr error
	for attempt := 0; attempt < traceRetries; attempt++ {
		loc, err := probeExitOnce(ctx, url)
		if err == nil {
			return loc, nil
		}
		lastErr = err
	}
	return "", fmt.Errorf("探测本机出口地区失败（已重试 %d 次）：%w", traceRetries, lastErr)
}

// probeExitOnce 发一次 trace 请求并取出 loc 字段。
func probeExitOnce(ctx context.Context, url string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, traceTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	// 不走代理：这里要判断的正是「出口是不是代理」，自己再套一层代理就
	// 永远只能看到代理的出口。
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()

	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("trace 返回状态码 %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTraceBody))
	if err != nil {
		return "", err
	}

	trace, err := probe.ParseTrace(string(body))
	if err != nil {
		return "", err
	}
	loc := strings.ToUpper(strings.TrimSpace(probe.ExtractLoc(trace)))
	if loc == "" {
		return "", errors.New("trace 响应里没有 loc 字段")
	}
	return loc, nil
}

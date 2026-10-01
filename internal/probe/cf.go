package probe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// minCFRounds 是节点校验的最少轮数：单轮结论太容易被一次网络抖动左右。
const minCFRounds = 3

// CFVerdict 是节点校验的三态结论。
//
// 为什么不是布尔值：「确认不是 Cloudflare」与「没能验证」必须分开。
// 前者要剔除节点，后者只说明这次网络不通，按前者处理会把好节点误杀。
type CFVerdict int

const (
	// CFUnknown 未能验证：所有轮次的请求都在传输层失败。
	CFUnknown CFVerdict = iota
	// CFValid 确认是 Cloudflare 节点。
	CFValid
	// CFInvalid 确认不是 Cloudflare 节点，或响应被中间设备劫持。
	CFInvalid
)

// Keep 报告该结论下节点是否应保留。未能验证时保留。
func (v CFVerdict) Keep() bool { return v != CFInvalid }

func (v CFVerdict) String() string {
	switch v {
	case CFValid:
		return "valid"
	case CFInvalid:
		return "invalid"
	default:
		return "unknown"
	}
}

// CFTarget 描述一个待校验的节点。
type CFTarget struct {
	IP     string
	Port   int
	Host   string // 用于 Host 头与 TLS SNI 的测试域名
	UseTLS bool
}

// CFRounds 返回实际使用的校验轮数，不足下限时抬到下限。
func CFRounds(requested int) int {
	if requested < minCFRounds {
		return minCFRounds
	}
	return requested
}

// CFResult 是节点校验的结论与探测统计。
//
// Stat 里对调用方有意义的是 LatencyMax 与 Jitter：判定节点质量时
// 取延迟最大值而不是平均值，抖动取样本标准差。
type CFResult struct {
	Verdict CFVerdict
	Rounds  int // 实际发起的轮数
	Stat    Result
}

// VerifyCF 校验 ip:port 是不是真正的 Cloudflare 节点。
//
// 三种判据满足其一即认定有效：
//   - 状态码 400 且 Server 头以 cloudflare 开头；
//   - 状态码 200 且响应体含 colo 字段；
//   - 状态码 200 且响应头含 cf-ray。
//
// 拿到过响应但都不满足 → CFInvalid（剔除）；一轮响应都没拿到 →
// CFUnknown（保留，只是标记为未验证）。
func VerifyCF(ctx context.Context, target CFTarget, rounds int, timeout time.Duration) (CFResult, error) {
	if err := validateTarget(target.IP, target.Port); err != nil {
		return CFResult{}, err
	}
	if target.Host == "" {
		return CFResult{}, errors.New("测试域名不能为空")
	}
	if timeout <= 0 {
		return CFResult{}, fmt.Errorf("超时 %v 必须为正", timeout)
	}

	transport := DirectTransport(target.IP, target.Port, target.Host, target.UseTLS, timeout)
	defer transport.CloseIdleConnections()
	return verifyCF(ctx, target, CFRounds(rounds), transport)
}

// verifyCF 是 VerifyCF 的实现体，传输层由调用方注入以便测试。
func verifyCF(ctx context.Context, target CFTarget, rounds int, transport http.RoundTripper) (CFResult, error) {
	scheme := "http"
	if target.UseTLS {
		scheme = "https"
	}
	endpoint := scheme + "://" + target.Host + TracePath

	samples := make([]float64, 0, rounds)
	sent := 0
	responded := 0

	for i := 0; i < rounds; i++ {
		if ctx.Err() != nil {
			break
		}
		sent++

		req, err := newTraceRequest(ctx, endpoint, target.Host)
		if err != nil {
			return CFResult{}, err
		}

		start := time.Now()
		resp, err := transport.RoundTrip(req)
		if err != nil {
			continue
		}
		samples = append(samples, elapsedMS(start))

		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxTraceBody))
		_ = resp.Body.Close()
		responded++

		if isCloudflareResponse(resp.StatusCode, resp.Header, string(body)) {
			return CFResult{
				Verdict: CFValid,
				Rounds:  i + 1,
				Stat:    summarize(samples, sent),
			}, nil
		}
	}

	verdict := CFInvalid
	if responded == 0 {
		verdict = CFUnknown
	}
	return CFResult{Verdict: verdict, Rounds: sent, Stat: summarize(samples, sent)}, nil
}

// isCloudflareResponse 判断单次响应是否满足三条判据中的任意一条。
func isCloudflareResponse(status int, header http.Header, body string) bool {
	if status == http.StatusBadRequest {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(header.Get("Server"))), "cloudflare") {
			return true
		}
	}
	if status == http.StatusOK {
		if trace, err := ParseTrace(body); err == nil && ExtractColo(trace) != "" {
			return true
		}
		if header.Get("Cf-Ray") != "" {
			return true
		}
	}
	return false
}

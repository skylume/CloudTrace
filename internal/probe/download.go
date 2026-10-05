package probe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// downloadSlices 是把测速时长切成的片数。
	downloadSlices = 100
	// downloadBufferSize 是单次读取的缓冲区大小。
	downloadBufferSize = 32 * 1024
	// downloadAlpha 是速率的指数加权移动平均系数，约等于「窗口 5 片」。
	// 取这个量级是为了在 10 秒 / 100 片的粒度下既能跟上带宽变化，
	// 又不会被单片抖动带偏。
	downloadAlpha = 1.0 / 3.0
)

// ErrRateLimited 表示下载源返回 429，调用方据此做熔断计数。
var ErrRateLimited = errors.New("下载源限流（HTTP 429）")

// ewma 是速率的指数加权移动平均。
//
// 逐片瞬时速率波动很大（受 TCP 拥塞窗口影响），直接取平均会被开头
// 慢启动的那几片严重拉低，所以用加权平均做平滑。
type ewma struct {
	alpha   float64
	value   float64
	samples int
}

func newEWMA(alpha float64) *ewma {
	return &ewma{alpha: alpha}
}

// add 加入一个新样本。首个样本直接作为初值：若从 0 起步，
// 前几片会被严重压低。
func (e *ewma) add(v float64) {
	if e.samples == 0 {
		e.value = v
	} else {
		e.value = e.alpha*v + (1-e.alpha)*e.value
	}
	e.samples++
}

func (e *ewma) count() int { return e.samples }

// valueMBps 返回平滑后的速率，单位 MB/s。
func (e *ewma) valueMBps() float64 { return e.value / 1024 / 1024 }

// DownloadOptions 是一次测速下载的参数。
type DownloadOptions struct {
	// Duration 是测速窗口。
	Duration time.Duration
	// MaxBytes 是本次下载的字节上限，0 表示不限。
	//
	// 到量即停。手机热点、按流量计费的宽带上，一次测速能吃掉几百 MB——用户
	// 设这个上限的意思是「别把我这个月的流量跑完」，此时停下比测完重要。
	// 上限是近似的：一次读取可能整块越过它。
	MaxBytes int64
	// UseTLS 决定 rawURL 不带协议时补 http 还是 https。
	UseTLS bool
}

// Download 测量到 ip:port 的真实下载带宽，单位 MB/s。
//
// 请求发往 rawURL，但连接强制打到 ip:port 上。窗口内按固定片数采样并做平滑。
//
// 返回错误的情形：参数非法、请求失败、状态码不是 200（429 返回
// ErrRateLimited）。窗口内正常结束、提前传完、到达字节上限都返回测得的速度——
// 这三种都是「测到了」，不是失败。
func Download(ctx context.Context, ip string, port int, rawURL string, opts DownloadOptions) (float64, error) {
	if ip == "" {
		return 0, errors.New("探测目标 IP 为空")
	}
	if rawURL == "" {
		return 0, errors.New("下载地址不能为空")
	}
	if opts.Duration <= 0 {
		return 0, fmt.Errorf("测速时长 %v 必须为正", opts.Duration)
	}

	target, err := normalizeDownloadURL(rawURL, opts.UseTLS)
	if err != nil {
		return 0, err
	}
	if port <= 0 || port > 65535 {
		port = defaultPortFor(target.Scheme)
	}

	transport := DirectTransport(ip, port, target.Hostname(), target.Scheme == "https", opts.Duration)
	defer transport.CloseIdleConnections()
	return download(ctx, target.String(), opts.Duration, opts.MaxBytes, transport)
}

// normalizeDownloadURL 补全协议并校验主机名。
func normalizeDownloadURL(rawURL string, useTLS bool) (*url.URL, error) {
	if !strings.Contains(rawURL, "://") {
		scheme := "http"
		if useTLS {
			scheme = "https"
		}
		rawURL = scheme + "://" + rawURL
	}
	target, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("下载地址无法解析：%w", err)
	}
	if target.Hostname() == "" {
		return nil, fmt.Errorf("下载地址缺少主机名：%s", rawURL)
	}
	return target, nil
}

// defaultPortFor 返回协议对应的默认端口。
func defaultPortFor(scheme string) int {
	if scheme == "https" {
		return 443
	}
	return 80
}

// download 是 Download 的实现体，传输层由调用方注入以便测试。
func download(ctx context.Context, rawURL string, duration time.Duration, maxBytes int64, transport http.RoundTripper) (float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, fmt.Errorf("构造下载请求失败：%w", err)
	}
	req.Header.Set("User-Agent", ChromeUA)

	resp, err := transport.RoundTrip(req)
	if err != nil {
		return 0, fmt.Errorf("下载请求失败：%w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return 0, ErrRateLimited
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("下载源返回 HTTP %d", resp.StatusCode)
	}

	// context 取消时关闭响应体：否则服务端不再发数据时 Read 会一直阻塞，
	// 「点停止立刻生效」就落空了。
	stopWatchdog := context.AfterFunc(ctx, func() { _ = resp.Body.Close() })
	defer stopWatchdog()

	// 标准库的传输层已经解好 chunked 编码，读到的是纯载荷；
	// 这里不需要也不应该再手工处理分块头，否则会把块头字节算进速度。
	return measure(ctx, resp.Body, duration, maxBytes, time.Now)
}

// measure 在时长窗口内按固定片数采样，返回平滑后的速率（MB/s）。
//
// maxBytes 为本次下载的字节上限，0 表示不限；到量即停，且停下时测得的速度
// 依然有效——它本来就是「这段时间里下得多快」。
//
// now 由调用方注入，便于在测试里模拟时钟不推进的环境。
func measure(
	ctx context.Context,
	body io.Reader,
	duration time.Duration,
	maxBytes int64,
	now func() time.Time,
) (float64, error) {
	slice := duration / downloadSlices
	if slice <= 0 {
		slice = time.Millisecond
	}

	buffer := make([]byte, downloadBufferSize)
	meter := newEWMA(downloadAlpha)

	start := now()
	deadline := start.Add(duration)
	nextBoundary := start.Add(slice)

	var total, settled int64
	settledAt := start

	// settle 把「上次结算之后新读到的字节」按实际耗时折算成瞬时速率。
	// 用实际耗时而不是固定的片长：收尾那一段往往不满一片，按片长折算
	// 会系统性低估。
	//
	// 实际耗时可能小到测不出来：虚拟机上时钟粒度能到毫秒级，一次几毫秒
	// 的传输会整个落在同一个刻度里，差值为零。这时退回按片长折算——宁可
	// 保守，也不能丢掉这一段字节。丢掉会让已经下到数据的目标得到 0 MB/s，
	// 而 0 的含义是「没测过」，等于把快节点挤出结果。
	settle := func(at time.Time) {
		if total <= settled {
			return
		}
		elapsed := at.Sub(settledAt).Seconds()
		if elapsed <= 0 {
			elapsed = slice.Seconds()
		}
		meter.add(float64(total-settled) / elapsed)
		settled = total
		settledAt = at
	}

	for {
		n, readErr := body.Read(buffer)
		total += int64(n)
		at := now()

		if at.After(nextBoundary) {
			settle(at)
			nextBoundary = at.Add(slice)
		}

		if readErr != nil {
			// 收尾：把最后一段结算进去，而不是把剩余时间按 0 计入——
			// 后者会把结果严重拉低。一个字节都没读到就结束（数据已传完）
			// 的情况不产生样本。
			settle(at)
			if ctxErr := ctx.Err(); ctxErr != nil {
				if meter.count() > 0 {
					break
				}
				return 0, ctxErr
			}
			if !errors.Is(readErr, io.EOF) && total == 0 {
				return 0, fmt.Errorf("下载中断：%w", readErr)
			}
			break
		}
		// 到量即停，且先于超时判断：两者同时满足时，到量是更要紧的那个原因。
		if maxBytes > 0 && total >= maxBytes {
			settle(at)
			break
		}
		if at.After(deadline) {
			break
		}
	}
	return meter.valueMBps(), nil
}

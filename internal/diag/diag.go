// Package diag 做一次「网络为什么不通」的自检。
//
// 与 health 分工明确：health 查的是**配置**对不对（端口占用、目录不可写、
// 并发填得离谱），这里查的是**网络**通不通。用户遇到「扫不出结果」时，先跑
// 这个就能分清是环境问题还是节点问题——而这两者的处理方式完全不同。
//
// 四项检查按「一层套一层」的顺序排：DNS 都解不出，后面的 TCP 与 trace 必然
// 也过不去。所以结论不是四项平权，而是**最早失败的那一层就是根因**。
package diag

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"cloudtrace/internal/geo"
)

// Status 是一项检查的结论。
type Status string

const (
	// StatusOK 表示这一层没问题。
	StatusOK Status = "ok"
	// StatusWarn 表示能通但不对劲（慢、出口看着像代理）。
	StatusWarn Status = "warn"
	// StatusBad 表示这一层不通。
	StatusBad Status = "bad"
)

// 检查项标识。前端据此选文案与图标。
const (
	KeyDNS    = "dns"
	KeyTCP    = "tcp"
	KeyTrace  = "trace"
	KeyEgress = "egress"
)

// Item 是一项检查的结果。
type Item struct {
	Key string `json:"key"`
	// Status 是结论。
	Status Status `json:"status"`
	// Summary 是「出了什么事」，一句话。
	Summary string `json:"summary"`
	// Detail 是原始数据（耗时、地址、trace 字段），贴进 issue 时有用。
	Detail string `json:"detail,omitempty"`
	// Advice 是「怎么办」。只在不是 ok 时给——全绿的时候没人想看建议。
	Advice string `json:"advice,omitempty"`
}

// Report 是一次诊断的完整结果。
type Report struct {
	CheckedAt time.Time `json:"checked_at"`
	// Status 是总体结论：取各项里最差的那个。
	Status Status `json:"status"`
	Items  []Item `json:"items"`
	// ElapsedMS 是这次诊断花掉的时间。它自己就是一个信号：超过十几秒说明
	// 网络已经很勉强了。
	ElapsedMS int64 `json:"elapsed_ms"`
}

// 检查用的目标与阈值。
//
// 目标写死而不是可配：这一页是「网络到底通不通」的基准测试，允许改目标就
// 等于允许把基准改成随便什么东西。要测自己关心的目标，用扫描页。
const (
	// dnsHost 取 Cloudflare 的通用域名——本项目测的就是它家的节点，
	// 解析不出这个域名，扫描必然没有结果。
	dnsHost = "cloudflare.com"
	// tcpAddr 取 1.1.1.1:443：既是 Anycast 边缘，也是本项目最常用的探测目标。
	tcpAddr = "1.1.1.1:443"
	// traceURL 返回调用方自己的出口信息。
	traceURL = "https://1.1.1.1/cdn-cgi/trace"

	// 单次检查的超时。四项加起来最坏约 12 秒，够短。
	dnsTimeout   = 5 * time.Second
	tcpTimeout   = 5 * time.Second
	traceTimeout = 6 * time.Second

	// 慢的判定线。比它们慢说明链路有问题，但还没到不通的程度。
	slowDNS   = 800 * time.Millisecond
	slowTCP   = 500 * time.Millisecond
	slowTrace = 3 * time.Second
)

// Options 是诊断需要的运行时输入。
//
// 与 health 一样**全部可注入**：真实环境跑真探测，测试里换成假实现，四项检查
// 才能各自被稳定触发——尤其是「全失败」这种在真机上不好复现的路径。
type Options struct {
	Resolve    func(ctx context.Context, host string) ([]string, error)
	Dial       func(ctx context.Context, addr string) error
	FetchTrace func(ctx context.Context) (string, error)
	// Now 取当前时间。
	Now func() time.Time
}

// DefaultOptions 返回走真实网络的实现。
func DefaultOptions() Options {
	return Options{
		Resolve: func(ctx context.Context, host string) ([]string, error) {
			addrs, err := net.DefaultResolver.LookupHost(ctx, host)
			return addrs, err
		},
		Dial: func(ctx context.Context, addr string) error {
			var d net.Dialer
			conn, err := d.DialContext(ctx, "tcp", addr)
			if err != nil {
				return err
			}
			return conn.Close()
		},
		FetchTrace: fetchTrace,
		Now:        time.Now,
	}
}

// fetchTrace 拉一次 trace 端点。
//
// 直连、不走代理：这一项要判断的正是「本机出去看到了什么」，自己再套一层
// 代理就永远只能看到代理的出口。http.DefaultTransport 默认读环境变量里的
// 代理设置，所以这里显式给一个不读环境变量的 Transport。
func fetchTrace(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, traceTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, traceURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "CloudTrace-diag")

	client := &http.Client{
		Transport: &http.Transport{Proxy: nil},
		Timeout:   traceTimeout,
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// 上限 64KB：正常响应只有几百字节，读多了只可能是被中间设备塞了东西。
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// Run 跑一遍四项检查。
//
// 即使前面失败了也继续跑后面的：用户想知道的是「哪些不通」，而不是「第一个
// 不通的是哪个」——一张全红的表比一条错误信息更能说明问题出在本地网络。
func Run(ctx context.Context, opts Options) Report {
	if opts.Now == nil {
		opts.Now = time.Now
	}

	start := opts.Now()
	items := []Item{
		checkDNS(ctx, opts),
		checkTCP(ctx, opts),
	}
	traceItem, loc := checkTrace(ctx, opts)
	items = append(items, traceItem, checkEgress(loc))

	return Report{
		CheckedAt: start,
		Status:    worst(items),
		Items:     items,
		ElapsedMS: opts.Now().Sub(start).Milliseconds(),
	}
}

// checkDNS 解析一个通用域名。
func checkDNS(ctx context.Context, opts Options) Item {
	if opts.Resolve == nil {
		return Item{Key: KeyDNS, Status: StatusBad, Summary: "解析器不可用", Advice: "这是程序内部的问题，请反馈"}
	}
	ctx, cancel := context.WithTimeout(ctx, dnsTimeout)
	defer cancel()

	begin := time.Now()
	addrs, err := opts.Resolve(ctx, dnsHost)
	elapsed := time.Since(begin)

	if err != nil {
		return Item{
			Key:     KeyDNS,
			Status:  StatusBad,
			Summary: fmt.Sprintf("解析不了 %s", dnsHost),
			Detail:  err.Error(),
			Advice:  "检查 DNS 设置；如果用了代理或 VPN，先关掉再试。也可以把 DNS 换成 1.1.1.1 或 8.8.8.8",
		}
	}
	if len(addrs) == 0 {
		return Item{
			Key:     KeyDNS,
			Status:  StatusBad,
			Summary: fmt.Sprintf("解析 %s 得到空结果", dnsHost),
			Advice:  "DNS 服务器返回了空应答，换一个 DNS 再试",
		}
	}

	item := Item{
		Key:     KeyDNS,
		Status:  StatusOK,
		Summary: fmt.Sprintf("解析正常（%s）", humanMS(elapsed)),
		Detail:  fmt.Sprintf("%s → %s", dnsHost, strings.Join(addrs, ", ")),
	}
	if elapsed > slowDNS {
		item.Status = StatusWarn
		item.Summary = fmt.Sprintf("解析很慢（%s）", humanMS(elapsed))
		item.Advice = "DNS 慢会让扫描的前几步都很慢，换一个更快的 DNS 会有明显改善"
	}
	return item
}

// checkTCP 连一次 Cloudflare 的边缘端口。
func checkTCP(ctx context.Context, opts Options) Item {
	if opts.Dial == nil {
		return Item{Key: KeyTCP, Status: StatusBad, Summary: "连接器不可用", Advice: "这是程序内部的问题，请反馈"}
	}
	ctx, cancel := context.WithTimeout(ctx, tcpTimeout)
	defer cancel()

	begin := time.Now()
	err := opts.Dial(ctx, tcpAddr)
	elapsed := time.Since(begin)

	if err != nil {
		return Item{
			Key:     KeyTCP,
			Status:  StatusBad,
			Summary: fmt.Sprintf("连不上 %s", tcpAddr),
			Detail:  err.Error(),
			Advice:  "本机到 Cloudflare 的 443 不通。检查防火墙、路由，或确认是否处在需要认证的网络里",
		}
	}

	item := Item{
		Key:     KeyTCP,
		Status:  StatusOK,
		Summary: fmt.Sprintf("连接正常（%s）", humanMS(elapsed)),
		Detail:  tcpAddr,
	}
	if elapsed > slowTCP {
		item.Status = StatusWarn
		item.Summary = fmt.Sprintf("连接偏慢（%s）", humanMS(elapsed))
		item.Advice = "单次连接就慢，扫描出的延迟也会整体偏高，结果参考价值有限"
	}
	return item
}

// checkTrace 拉一次 trace 并解析出出口信息，返回结果项与出口国家码。
//
// 出口国家码顺便交给 checkEgress：同一个响应里就有，没必要再发一次请求。
func checkTrace(ctx context.Context, opts Options) (Item, string) {
	if opts.FetchTrace == nil {
		return Item{Key: KeyTrace, Status: StatusBad, Summary: "trace 拉取器不可用"}, ""
	}

	begin := time.Now()
	body, err := opts.FetchTrace(ctx)
	elapsed := time.Since(begin)
	if err != nil {
		return Item{
			Key:     KeyTrace,
			Status:  StatusBad,
			Summary: "拉不到 cdn-cgi/trace",
			Detail:  err.Error(),
			Advice:  "TCP 能通但 trace 拿不到，多半是被中间设备拦了。换一个网络再试",
		}, ""
	}

	fields := parseTrace(body)
	loc := fields["loc"]
	colo := fields["colo"]

	detail := "loc=" + orDash(loc) + " colo=" + orDash(colo)
	if ip := fields["ip"]; ip != "" {
		detail += " ip=" + ip
	}

	item := Item{
		Key:     KeyTrace,
		Status:  StatusOK,
		Summary: fmt.Sprintf("trace 可达（%s）", humanMS(elapsed)),
		Detail:  detail,
	}
	if loc == "" {
		// 拿得到响应却没有 loc，说明回的不是 Cloudflare 的 trace——
		// 要么被劫持，要么走了一个会改写响应的中间设备。
		item.Status = StatusWarn
		item.Summary = "trace 有响应，但不是 Cloudflare 的格式"
		item.Advice = "响应里没有 loc 字段，可能被中间设备改写了。换个网络确认一下"
	} else if elapsed > slowTrace {
		item.Status = StatusWarn
		item.Summary = fmt.Sprintf("trace 很慢（%s）", humanMS(elapsed))
	}
	return item, loc
}

// checkEgress 判断出口是不是代理。
//
// 判定规则与扫描时那条横幅完全一致（geo.ShouldWarn 的那套），所以这里不重复
// 实现，只做「说人话」的部分——两处判定分叉的话，用户会看到一边提示一边不提示。
func checkEgress(loc string) Item {
	item := Item{Key: KeyEgress}

	switch {
	case loc == "":
		item.Status = StatusWarn
		item.Summary = "拿不到出口国家"
		item.Advice = "没有出口信息就无法判断是否走了代理，这一项不影响扫描"
	case !geo.ShouldWarn(loc):
		item.Status = StatusOK
		item.Summary = fmt.Sprintf("出口为 %s，正常", loc)
		item.Detail = "loc=" + loc
	default:
		item.Status = StatusWarn
		item.Summary = fmt.Sprintf("出口为 %s，看着像代理或 VPN", loc)
		item.Detail = "loc=" + loc
		item.Advice = "走代理时扫出来的延迟反映的是代理到节点的距离，不是你家到节点的距离。想测真实线路就先关掉代理"
	}
	return item
}

// parseTrace 把 trace 的 `key=value` 逐行解析成 map。
//
// 认不出的行直接跳过：trace 的字段集是 Cloudflare 定的，会变；为几个不认识的
// 字段把整次诊断判失败不值得。
func parseTrace(body string) map[string]string {
	out := make(map[string]string, 8)
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return out
}

// worst 取各项里最差的结论。
func worst(items []Item) Status {
	status := StatusOK
	for _, item := range items {
		switch item.Status {
		case StatusBad:
			return StatusBad
		case StatusWarn:
			status = StatusWarn
		}
	}
	return status
}

// humanMS 把耗时写成「12ms」或「1.4s」。
//
// 毫秒以上就不再给小数毫秒：`820.471ms` 那串数字对判断「慢不慢」没有任何帮助。
func humanMS(d time.Duration) string {
	if d < time.Second {
		return strconv.FormatInt(d.Milliseconds(), 10) + "ms"
	}
	return strconv.FormatFloat(d.Seconds(), 'f', 1, 64) + "s"
}

func orDash(value string) string {
	if value == "" {
		return "—"
	}
	return value
}

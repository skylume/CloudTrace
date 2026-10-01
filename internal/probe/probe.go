// Package probe 提供延迟探测、trace 解析、节点校验与下载测速。
//
// 设计约束：
//   - 本包只做算法，不做任务编排、不读配置、不写文件；
//   - 所有可能阻塞的调用都接受 context，取消必须立即生效；
//   - 网络原语（拨号函数、HTTP 传输层）都通过参数注入，便于用假实现
//     做单测，单元测试中不访问真实网络；
//   - 本包不得持有任何包级可变状态，同一组输入必须得到同一组结果。
//
// 探测失败不算错误：全部失败体现为 Loss = 1 且延迟字段为哨兵值，
// 调用方据此把记录排除出排序，而不是中断整个任务。
package probe

import (
	"context"
	"math"
	"net"
	"time"

	"cloudtrace/internal/model"
)

// Result 是一次探测的统计结果，延迟类字段单位为毫秒。
type Result struct {
	Latency    float64 // 最小延迟
	LatencyAvg float64
	LatencyMax float64
	Jitter     float64 // 样本标准差
	Loss       float64 // 丢包率，0..1
	Sent       int     // 发出的探测次数
	Recv       int     // 成功的探测次数
	Colo       string  // 数据中心代码，仅 HTTPing 会填充
}

// Ok 报告是否至少成功探测过一次。
func (r Result) Ok() bool { return r.Recv > 0 }

// dialContextFunc 与 net.Dialer.DialContext 同签名。
//
// 抽成类型是为了让探测函数接受注入的拨号实现，测试里用假拨号器
// 即可断言「连的是哪个地址」，无需真的建立连接。
type dialContextFunc func(ctx context.Context, network, address string) (net.Conn, error)

// dialerFor 返回带超时的标准拨号器。
//
// 必须用带超时的拨号：直接用 net.Dial 在目标不可达时会一直挂住，
// 整个 worker 就卡死了。
func dialerFor(timeout time.Duration) dialContextFunc {
	d := &net.Dialer{Timeout: timeout}
	return d.DialContext
}

// summarize 把若干次成功探测的耗时汇总成统计结果。
//
// sent 是实际发出的次数（可能大于 len(samples)，差额即失败次数）。
// samples 为空时返回「不可达」：延迟字段置哨兵值，Loss 为 1。
//
// 抖动用**样本标准差**（除以 n-1）而不是总体标准差（除以 n）：
// 探测次数通常只有 1~4 次，小样本下总体标准差会系统性低估波动。
func summarize(samples []float64, sent int) Result {
	if sent <= 0 {
		sent = len(samples)
	}
	r := Result{Sent: sent, Recv: len(samples)}
	if len(samples) == 0 {
		r.Latency = model.Unreachable
		r.LatencyAvg = model.Unreachable
		r.LatencyMax = model.Unreachable
		r.Loss = 1
		return r
	}

	min, max, sum := samples[0], samples[0], 0.0
	for _, v := range samples {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
		sum += v
	}
	avg := sum / float64(len(samples))

	r.Latency = min
	r.LatencyMax = max
	r.LatencyAvg = avg
	r.Jitter = sampleStdDev(samples, avg)
	r.Loss = float64(sent-len(samples)) / float64(sent)
	return r
}

// sampleStdDev 计算样本标准差；样本数不足 2 时返回 0。
func sampleStdDev(samples []float64, mean float64) float64 {
	if len(samples) < 2 {
		return 0
	}
	var acc float64
	for _, v := range samples {
		d := v - mean
		acc += d * d
	}
	return math.Sqrt(acc / float64(len(samples)-1))
}

// elapsedMS 返回自 start 起经过的毫秒数。
func elapsedMS(start time.Time) float64 {
	return float64(time.Since(start).Microseconds()) / 1000
}

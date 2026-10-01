package probe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"
)

// TCPing 测量 ip:port 的 TCP 连接延迟，并统计 min / avg / max / 抖动 / 丢包。
//
// timeout 同时作为单次拨号超时。times 为探测次数：两阶段扫描的粗扫阶段
// 传 1，此时 min = avg = max 且抖动为 0。
//
// 返回值的约定：
//   - 参数非法（IP 为空、端口越界、times < 1、timeout 非正）→ 零值 + 错误；
//   - 其余情况一律返回可用的统计结果 + nil。全部探测失败体现为
//     Loss = 1 且延迟字段为哨兵值；context 被取消体现为 Sent < times。
//     这两种情况都不是错误，调用方据此把记录排除出排序即可。
func TCPing(ctx context.Context, ip string, port, times int, timeout time.Duration) (Result, error) {
	return tcping(ctx, ip, port, times, timeout, dialerFor(timeout))
}

// tcping 是 TCPing 的实现体，拨号函数由调用方注入以便测试。
func tcping(ctx context.Context, ip string, port, times int, timeout time.Duration, dial dialContextFunc) (Result, error) {
	if err := validateTarget(ip, port); err != nil {
		return Result{}, err
	}
	if times < 1 {
		return Result{}, fmt.Errorf("探测次数 %d 必须至少为 1", times)
	}
	if timeout <= 0 {
		return Result{}, fmt.Errorf("超时 %v 必须为正", timeout)
	}

	address := net.JoinHostPort(ip, strconv.Itoa(port))
	samples := make([]float64, 0, times)
	sent := 0

	for i := 0; i < times; i++ {
		// 每次探测前检查取消：点了停止就该立刻停下，
		// 不能把剩余次数跑完。
		if ctx.Err() != nil {
			break
		}
		sent++

		start := time.Now()
		conn, err := dial(ctx, "tcp", address)
		if err != nil {
			continue
		}
		// 先记录耗时再关闭：关闭本身的耗时不属于连接延迟。
		samples = append(samples, elapsedMS(start))
		_ = conn.Close()
	}

	return summarize(samples, sent), nil
}

// validateTarget 校验探测目标的基本合法性。
func validateTarget(ip string, port int) error {
	if ip == "" {
		return errors.New("探测目标 IP 为空")
	}
	if port <= 0 || port > 65535 {
		return fmt.Errorf("端口 %d 超出 1..65535", port)
	}
	return nil
}

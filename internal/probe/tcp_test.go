package probe

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"cloudtrace/internal/model"
)

// startLocalListener 启动一个接受连接后立刻关闭的本地监听器，返回端口。
//
// 用回环地址而不是外网：单元测试不允许依赖真实网络。
func startLocalListener(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("启动本地监听失败：%v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port
}

// freePort 返回一个刚释放的本地端口，连接它通常会被立刻拒绝。
func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("获取空闲端口失败：%v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

func TestTCPingAgainstLocalListener(t *testing.T) {
	port := startLocalListener(t)
	const timeout = time.Second

	got, err := TCPing(context.Background(), "127.0.0.1", port, 3, timeout)
	if err != nil {
		t.Fatalf("TCPing 返回错误：%v", err)
	}
	if got.Sent != 3 || got.Recv != 3 {
		t.Fatalf("Sent/Recv = %d/%d，期望 3/3", got.Sent, got.Recv)
	}
	if !closeTo(got.Loss, 0) {
		t.Errorf("Loss = %v，期望 0", got.Loss)
	}
	if got.Latency < 0 || got.Latency > float64(timeout.Milliseconds()) {
		t.Errorf("Latency = %v，期望落在 [0, %d]", got.Latency, timeout.Milliseconds())
	}
	if got.Latency > got.LatencyAvg || got.LatencyAvg > got.LatencyMax {
		t.Errorf("min/avg/max 顺序错误：%v %v %v", got.Latency, got.LatencyAvg, got.LatencyMax)
	}
	if got.Jitter < 0 {
		t.Errorf("Jitter = %v，期望非负", got.Jitter)
	}
}

func TestTCPingSingleTimeHasNoJitter(t *testing.T) {
	port := startLocalListener(t)

	got, err := TCPing(context.Background(), "127.0.0.1", port, 1, time.Second)
	if err != nil {
		t.Fatalf("TCPing 返回错误：%v", err)
	}
	if got.Recv != 1 {
		t.Fatalf("Recv = %d，期望 1", got.Recv)
	}
	if !closeTo(got.Jitter, 0) {
		t.Errorf("单次探测 Jitter = %v，期望 0", got.Jitter)
	}
	if !closeTo(got.Latency, got.LatencyAvg) || !closeTo(got.LatencyAvg, got.LatencyMax) {
		t.Errorf("单次探测 min/avg/max 应当相等：%v %v %v", got.Latency, got.LatencyAvg, got.LatencyMax)
	}
}

func TestTCPingAllFail(t *testing.T) {
	port := freePort(t)

	got, err := TCPing(context.Background(), "127.0.0.1", port, 2, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("全部失败不应返回错误，实际：%v", err)
	}
	if got.Sent != 2 || got.Recv != 0 {
		t.Fatalf("Sent/Recv = %d/%d，期望 2/0", got.Sent, got.Recv)
	}
	if !closeTo(got.Loss, 1) {
		t.Errorf("Loss = %v，期望 1", got.Loss)
	}
	if !closeTo(got.Latency, model.Unreachable) || !closeTo(got.LatencyAvg, model.Unreachable) || !closeTo(got.LatencyMax, model.Unreachable) {
		t.Errorf("不可达时延迟应为哨兵值，实际 %v %v %v", got.Latency, got.LatencyAvg, got.LatencyMax)
	}
	if got.Ok() {
		t.Error("全部失败时 Ok() 应为 false")
	}
}

func TestTCPingInvalidParams(t *testing.T) {
	tests := []struct {
		name    string
		ip      string
		port    int
		times   int
		timeout time.Duration
	}{
		{name: "空 IP", ip: "", port: 443, times: 1, timeout: time.Second},
		{name: "端口越界", ip: "1.1.1.1", port: 70000, times: 1, timeout: time.Second},
		{name: "次数为零", ip: "1.1.1.1", port: 443, times: 0, timeout: time.Second},
		{name: "次数为负", ip: "1.1.1.1", port: 443, times: -1, timeout: time.Second},
		{name: "超时为零", ip: "1.1.1.1", port: 443, times: 1, timeout: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := TCPing(context.Background(), tt.ip, tt.port, tt.times, tt.timeout); err == nil {
				t.Error("期望返回错误，实际为 nil")
			}
		})
	}
}

func TestTCPingStopsWhenContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := TCPing(ctx, "127.0.0.1", freePort(t), 5, time.Second)
	if err != nil {
		t.Fatalf("取消不应返回错误，实际：%v", err)
	}
	if got.Sent != 0 {
		t.Errorf("Sent = %d，期望 0：取消后不应再发起探测", got.Sent)
	}
}

func TestTCPingUsesInjectedDialer(t *testing.T) {
	var (
		mu        sync.Mutex
		addresses []string
	)
	dial := func(_ context.Context, network, address string) (net.Conn, error) {
		mu.Lock()
		addresses = append(addresses, network+" "+address)
		mu.Unlock()
		client, server := net.Pipe()
		_ = server.Close()
		return client, nil
	}

	got, err := tcping(context.Background(), "1.1.1.1", 8443, 2, time.Second, dial)
	if err != nil {
		t.Fatalf("tcping 返回错误：%v", err)
	}
	if got.Recv != 2 {
		t.Errorf("Recv = %d，期望 2", got.Recv)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(addresses) != 2 {
		t.Fatalf("拨号次数 = %d，期望 2", len(addresses))
	}
	for _, address := range addresses {
		if address != "tcp 1.1.1.1:8443" {
			t.Errorf("拨号地址 = %q，期望 %q", address, "tcp 1.1.1.1:8443")
		}
	}
}

func TestTCPingFormatsIPv6Address(t *testing.T) {
	var captured string
	dial := func(_ context.Context, _, address string) (net.Conn, error) {
		captured = address
		return nil, errors.New("故意失败")
	}

	if _, err := tcping(context.Background(), "2606:4700::1", 443, 1, time.Second, dial); err != nil {
		t.Fatalf("tcping 返回错误：%v", err)
	}
	if captured != "[2606:4700::1]:443" {
		t.Errorf("IPv6 拨号地址 = %q，期望 %q", captured, "[2606:4700::1]:443")
	}
}

func TestTCPingDialFailureCountsAsLoss(t *testing.T) {
	dial := func(_ context.Context, _, _ string) (net.Conn, error) {
		return nil, errors.New("故意失败")
	}

	got, err := tcping(context.Background(), "1.1.1.1", 443, 3, time.Second, dial)
	if err != nil {
		t.Fatalf("tcping 返回错误：%v", err)
	}
	if got.Sent != 3 || got.Recv != 0 {
		t.Errorf("Sent/Recv = %d/%d，期望 3/0", got.Sent, got.Recv)
	}
	if !closeTo(got.Loss, 1) {
		t.Errorf("Loss = %v，期望 1", got.Loss)
	}
}

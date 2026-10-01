package probe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// pacedReader 按固定节奏产出数据，用于验证「数据传完就停」。
type pacedReader struct {
	remaining int
	chunk     int
	delay     time.Duration
}

func (r *pacedReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	time.Sleep(r.delay)
	n := min(r.chunk, r.remaining, len(p))
	r.remaining -= n
	return n, nil
}

// failingReader 立刻返回一个非 EOF 的错误。
type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func TestEWMAContinuity(t *testing.T) {
	meter := newEWMA(0.5)

	if meter.count() != 0 {
		t.Fatalf("初值样本数 = %d，期望 0", meter.count())
	}
	// 首个样本直接作为初值：若从 0 起步，前几片会被严重压低。
	meter.add(100)
	if !closeTo(meter.value, 100) {
		t.Fatalf("首个样本后 value = %v，期望 100", meter.value)
	}
	meter.add(200)
	if !closeTo(meter.value, 150) {
		t.Fatalf("第二个样本后 value = %v，期望 150", meter.value)
	}
	meter.add(200)
	if !closeTo(meter.value, 175) {
		t.Fatalf("第三个样本后 value = %v，期望 175", meter.value)
	}
	if meter.count() != 3 {
		t.Errorf("样本数 = %d，期望 3", meter.count())
	}
}

func TestEWMAConvergesToConstant(t *testing.T) {
	meter := newEWMA(downloadAlpha)
	for i := 0; i < 100; i++ {
		meter.add(1000)
	}
	if !closeTo(meter.value, 1000) {
		t.Errorf("恒定输入下应收敛到 1000，实际 %v", meter.value)
	}
}

func TestEWMASmoothsSpike(t *testing.T) {
	meter := newEWMA(0.5)
	for i := 0; i < 20; i++ {
		meter.add(100)
	}
	meter.add(10000)

	// 平滑后的值必须明显低于尖峰，否则单片抖动会直接污染结果。
	if meter.value >= 10000 {
		t.Errorf("尖峰未被平滑，value = %v", meter.value)
	}
	if meter.value <= 100 {
		t.Errorf("尖峰应抬高结果，value = %v", meter.value)
	}
}

func TestEWMAValueMBps(t *testing.T) {
	meter := newEWMA(0.5)
	meter.add(1024 * 1024 * 4) // 每秒 4MB
	if got := meter.valueMBps(); !closeTo(got, 4) {
		t.Errorf("valueMBps = %v，期望 4", got)
	}
}

func TestMeasureStopsAtEOF(t *testing.T) {
	// 窗口设成 5 秒，但数据只有 25ms 的量：必须立刻返回，
	// 而不是把剩余时间按 0 计入。
	const duration = 5 * time.Second
	reader := &pacedReader{remaining: 100 * 1024, chunk: 4096, delay: time.Millisecond}

	start := time.Now()
	speed, err := measure(context.Background(), reader, duration)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("measure 返回错误：%v", err)
	}
	if elapsed > duration/2 {
		t.Errorf("耗时 %v，说明没有在 EOF 处提前结束", elapsed)
	}
	if speed <= 0 {
		t.Errorf("speed = %v，期望为正", speed)
	}
}

func TestMeasureEmptyBodyReturnsZero(t *testing.T) {
	speed, err := measure(context.Background(), strings.NewReader(""), time.Second)
	if err != nil {
		t.Fatalf("measure 返回错误：%v", err)
	}
	if speed != 0 {
		t.Errorf("空响应体 speed = %v，期望 0", speed)
	}
}

func TestMeasureReturnsErrorOnImmediateFailure(t *testing.T) {
	speed, err := measure(context.Background(), failingReader{err: errors.New("连接被重置")}, time.Second)
	if err == nil {
		t.Fatal("一个字节都没读到就失败时应返回错误")
	}
	if speed != 0 {
		t.Errorf("speed = %v，期望 0", speed)
	}
}

func TestMeasureReturnsContextErrorWhenCancelledEarly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	speed, err := measure(ctx, failingReader{err: errors.New("读失败")}, time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v，期望 context.Canceled", err)
	}
	if speed != 0 {
		t.Errorf("speed = %v，期望 0", speed)
	}
}

// newDownloadServer 启动一个持续产出数据的本地服务端。
func newDownloadServer(t *testing.T, total int, chunked bool) (*httptest.Server, int) {
	t.Helper()
	const chunkSize = 32 * 1024

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		if !chunked {
			w.Header().Set("Content-Length", fmt.Sprint(total))
		}
		buffer := make([]byte, chunkSize)
		for written := 0; written < total; {
			n := min(chunkSize, total-written)
			if _, err := w.Write(buffer[:n]); err != nil {
				return
			}
			written += n
			if chunked {
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
			}
		}
	}))
	t.Cleanup(server.Close)
	return server, server.Listener.Addr().(*net.TCPAddr).Port
}

func TestDownloadMeasuresSpeed(t *testing.T) {
	server, port := newDownloadServer(t, 4<<20, false)

	speed, err := Download(context.Background(), "127.0.0.1", port, server.URL+"/__down", 5*time.Second, false)
	if err != nil {
		t.Fatalf("Download 返回错误：%v", err)
	}
	if speed <= 0 {
		t.Errorf("speed = %v，期望为正", speed)
	}
}

func TestDownloadChunkedBody(t *testing.T) {
	// 标准库的传输层会解好 chunked 编码，读到的必须是纯载荷：
	// 若把分块头算进字节数，速度会偏高。
	server, port := newDownloadServer(t, 2<<20, true)

	speed, err := Download(context.Background(), "127.0.0.1", port, server.URL+"/__down", 5*time.Second, false)
	if err != nil {
		t.Fatalf("Download 返回错误：%v", err)
	}
	if speed <= 0 {
		t.Errorf("speed = %v，期望为正", speed)
	}
	if speed > 4096 {
		t.Errorf("speed = %v MB/s，远超本机可能，疑似把分块头算进了字节数", speed)
	}
}

func TestDownloadSetsHostAndPath(t *testing.T) {
	var (
		mu       sync.Mutex
		gotHosts []string
		gotPaths []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotHosts = append(gotHosts, r.Host)
		gotPaths = append(gotPaths, r.URL.Path)
		mu.Unlock()
		_, _ = w.Write([]byte(strings.Repeat("x", 1024)))
	}))
	defer server.Close()

	port := server.Listener.Addr().(*net.TCPAddr).Port
	// 不带协议的地址：应当按 useTLS 补全，并保留主机名与路径。
	if _, err := Download(context.Background(), "127.0.0.1", port, "edge.example.com/__down", 2*time.Second, false); err != nil {
		t.Fatalf("Download 返回错误：%v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(gotHosts) != 1 || gotHosts[0] != "edge.example.com" {
		t.Errorf("Host 头 = %v，期望 [edge.example.com]", gotHosts)
	}
	if len(gotPaths) != 1 || gotPaths[0] != "/__down" {
		t.Errorf("请求路径 = %v，期望 [/__down]", gotPaths)
	}
}

func TestDownloadRateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	port := server.Listener.Addr().(*net.TCPAddr).Port
	_, err := Download(context.Background(), "127.0.0.1", port, server.URL+"/__down", time.Second, false)
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v，期望 ErrRateLimited", err)
	}
}

func TestDownloadNonOKStatus(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{name: "服务器错误", status: http.StatusInternalServerError},
		{name: "未找到", status: http.StatusNotFound},
		{name: "禁止访问", status: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
			}))
			defer server.Close()

			port := server.Listener.Addr().(*net.TCPAddr).Port
			if _, err := Download(context.Background(), "127.0.0.1", port, server.URL+"/__down", time.Second, false); err == nil {
				t.Error("非 200 状态码应返回错误")
			}
		})
	}
}

func TestDownloadInvalidParams(t *testing.T) {
	tests := []struct {
		name     string
		ip       string
		port     int
		rawURL   string
		duration time.Duration
	}{
		{name: "空 IP", ip: "", port: 443, rawURL: "https://example.com/a", duration: time.Second},
		{name: "空地址", ip: "1.1.1.1", port: 443, rawURL: "", duration: time.Second},
		{name: "时长为零", ip: "1.1.1.1", port: 443, rawURL: "https://example.com/a", duration: 0},
		{name: "时长为负", ip: "1.1.1.1", port: 443, rawURL: "https://example.com/a", duration: -time.Second},
		{name: "地址无法解析", ip: "1.1.1.1", port: 443, rawURL: "http://[::1", duration: time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Download(context.Background(), tt.ip, tt.port, tt.rawURL, tt.duration, false); err == nil {
				t.Error("期望返回错误，实际为 nil")
			}
		})
	}
}

func TestDownloadStopsWhenContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := Download(ctx, "127.0.0.1", 1, "https://example.com/a", time.Second, false); err == nil {
		t.Error("已取消的 context 上应返回错误")
	}
}

func TestNormalizeDownloadURL(t *testing.T) {
	tests := []struct {
		name    string
		rawURL  string
		useTLS  bool
		want    string
		wantErr bool
	}{
		{name: "已带协议", rawURL: "https://example.com/a", want: "https://example.com/a"},
		{name: "补 https", rawURL: "example.com/a", useTLS: true, want: "https://example.com/a"},
		{name: "补 http", rawURL: "example.com/a", want: "http://example.com/a"},
		{name: "缺主机名", rawURL: "https:///a", wantErr: true},
		{name: "无法解析", rawURL: "http://[::1", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeDownloadURL(tt.rawURL, tt.useTLS)
			if (err != nil) != tt.wantErr {
				t.Fatalf("错误 = %v，期望出错 = %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got.String() != tt.want {
				t.Errorf("URL = %q，期望 %q", got.String(), tt.want)
			}
		})
	}
}

func TestDefaultPortFor(t *testing.T) {
	if got := defaultPortFor("https"); got != 443 {
		t.Errorf("https 默认端口 = %d，期望 443", got)
	}
	if got := defaultPortFor("http"); got != 80 {
		t.Errorf("http 默认端口 = %d，期望 80", got)
	}
}

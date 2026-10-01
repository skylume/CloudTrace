package probe

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"cloudtrace/internal/model"
)

func TestThresholdMultiplier(t *testing.T) {
	// 这两个值是硬性约定：TLS 握手多花 2~3 个往返，不放大阈值会把
	// 所有 TLS 节点误杀。
	if got := ThresholdMultiplier(false); !closeTo(got, 1.3) {
		t.Errorf("无 TLS 倍率 = %v，期望 1.3", got)
	}
	if got := ThresholdMultiplier(true); !closeTo(got, 4.0) {
		t.Errorf("有 TLS 倍率 = %v，期望 4.0", got)
	}
}

func TestColoFromCfRay(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "标准格式取后缀", value: "7d3b3f0e2a1c4f5a-HKG", want: "HKG"},
		{name: "只有代码", value: "HKG", want: "HKG"},
		{name: "小写归一化", value: "7d3b-hkg", want: "HKG"},
		{name: "首尾空白", value: "  7d3b-NRT  ", want: "NRT"},
		{name: "空值", value: "", want: ""},
		{name: "后缀长度不对", value: "7d3b-AB", want: ""},
		{name: "后缀含数字", value: "7d3b-H1G", want: ""},
		{name: "整体长度不对", value: "ABCD", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := coloFromCfRay(tt.value); got != tt.want {
				t.Errorf("coloFromCfRay(%q) = %q，期望 %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestExtractColoPrefersBodyOverHeader(t *testing.T) {
	tests := []struct {
		name   string
		header http.Header
		body   string
		want   string
	}{
		{
			name:   "响应体优先",
			header: http.Header{"Cf-Ray": []string{"7d3b3f0e2a1c4f5a-NRT"}},
			body:   "colo=HKG\nloc=HK\n",
			want:   "HKG",
		},
		{
			name:   "响应体不含 colo 时回退响应头",
			header: http.Header{"Cf-Ray": []string{"7d3b3f0e2a1c4f5a-HKG"}},
			body:   "<html>hi</html>",
			want:   "HKG",
		},
		{
			name:   "两者都没有",
			header: http.Header{},
			body:   "<html>hi</html>",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractColo(tt.header, tt.body); got != tt.want {
				t.Errorf("extractColo = %q，期望 %q", got, tt.want)
			}
		})
	}
}

func TestDirectTransportConfig(t *testing.T) {
	plain := DirectTransport("1.1.1.1", 443, "example.com", false, time.Second)
	if plain.Proxy != nil {
		t.Error("必须禁用代理：用户环境有代理时结果全错")
	}
	if plain.TLSClientConfig != nil {
		t.Error("无 TLS 时不应配置 TLSClientConfig")
	}
	if !plain.DisableKeepAlives {
		t.Error("必须关闭长连接复用：否则第二轮起省掉握手时间，延迟偏低")
	}

	tlsTransport := DirectTransport("1.1.1.1", 443, "example.com:8443", true, time.Second)
	if tlsTransport.TLSClientConfig == nil {
		t.Fatal("有 TLS 时必须配置 TLSClientConfig")
	}
	if got := tlsTransport.TLSClientConfig.ServerName; got != "example.com" {
		t.Errorf("ServerName = %q，期望 %q（应去掉端口）", got, "example.com")
	}
}

// TestDirectTransportDialsFixedAddress 验证「强制直连目标 IP」这条硬性要求。
//
// URL 用的是不可解析的域名：如果传输层没有把地址写死，请求会直接失败。
func TestDirectTransportDialsFixedAddress(t *testing.T) {
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
		_, _ = w.Write([]byte("colo=HKG\n"))
	}))
	defer server.Close()

	port := server.Listener.Addr().(*net.TCPAddr).Port
	transport := DirectTransport("127.0.0.1", port, "probe.invalid", false, 2*time.Second)
	defer transport.CloseIdleConnections()

	req, err := http.NewRequest(http.MethodGet, "http://probe.invalid"+TracePath, nil)
	if err != nil {
		t.Fatalf("构造请求失败：%v", err)
	}
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("强制直连失败（说明走了 DNS 解析）：%v", err)
	}
	_ = resp.Body.Close()

	mu.Lock()
	defer mu.Unlock()
	if len(gotHosts) != 1 {
		t.Fatalf("服务端收到 %d 次请求，期望 1", len(gotHosts))
	}
	if gotHosts[0] != "probe.invalid" {
		t.Errorf("Host 头 = %q，期望 %q", gotHosts[0], "probe.invalid")
	}
	if gotPaths[0] != TracePath {
		t.Errorf("请求路径 = %q，期望 %q", gotPaths[0], TracePath)
	}
}

// TestHTTPingAgainstLocalServer 走完整的导出路径：URL 用测试域名，
// 连接被强制打到本地服务端。
func TestHTTPingAgainstLocalServer(t *testing.T) {
	var (
		mu       sync.Mutex
		gotHosts []string
		gotUA    []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotHosts = append(gotHosts, r.Host)
		gotUA = append(gotUA, r.Header.Get("User-Agent"))
		mu.Unlock()
		_, _ = w.Write([]byte("ip=1.1.1.1\ncolo=NRT\nloc=JP\n"))
	}))
	defer server.Close()

	port := server.Listener.Addr().(*net.TCPAddr).Port
	got, err := HTTPing(context.Background(), "127.0.0.1", port, "edge.example.com", false, 2, 2*time.Second)
	if err != nil {
		t.Fatalf("HTTPing 返回错误：%v", err)
	}
	if got.Sent != 2 || got.Recv != 2 {
		t.Fatalf("Sent/Recv = %d/%d，期望 2/2", got.Sent, got.Recv)
	}
	if got.Colo != "NRT" {
		t.Errorf("Colo = %q，期望 %q", got.Colo, "NRT")
	}
	if !closeTo(got.Loss, 0) {
		t.Errorf("Loss = %v，期望 0", got.Loss)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(gotHosts) != 2 {
		t.Fatalf("服务端收到 %d 次请求，期望 2", len(gotHosts))
	}
	for _, host := range gotHosts {
		if host != "edge.example.com" {
			t.Errorf("Host 头 = %q，期望 %q", host, "edge.example.com")
		}
	}
	for _, ua := range gotUA {
		if ua != ChromeUA {
			t.Errorf("User-Agent = %q，期望 %q", ua, ChromeUA)
		}
	}
}

func TestHTTPingOverTLS(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{
			name: "响应体里的 colo",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("colo=HKG\nloc=HK\n"))
			},
			want: "HKG",
		},
		{
			name: "响应头里的 cf-ray",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Cf-Ray", "7d3b3f0e2a1c4f5a-HKG")
				_, _ = w.Write([]byte("<html>not a trace</html>"))
			},
			want: "HKG",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewTLSServer(tt.handler)
			defer server.Close()

			// 用测试服务端自带的传输层：它信任测试证书，
			// 这样 TLS 分支能真正跑通而不必关掉证书校验。
			host := server.Listener.Addr().String()
			got, err := httping(context.Background(), host, true, 2, server.Client().Transport)
			if err != nil {
				t.Fatalf("httping 返回错误：%v", err)
			}
			if got.Recv != 2 {
				t.Fatalf("Recv = %d，期望 2", got.Recv)
			}
			if got.Colo != tt.want {
				t.Errorf("Colo = %q，期望 %q", got.Colo, tt.want)
			}
		})
	}
}

func TestHTTPingAllFail(t *testing.T) {
	got, err := HTTPing(context.Background(), "127.0.0.1", freePort(t), "edge.example.com", false, 2, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("全部失败不应返回错误，实际：%v", err)
	}
	if got.Sent != 2 || got.Recv != 0 {
		t.Errorf("Sent/Recv = %d/%d，期望 2/0", got.Sent, got.Recv)
	}
	if !closeTo(got.Loss, 1) {
		t.Errorf("Loss = %v，期望 1", got.Loss)
	}
	if !closeTo(got.Latency, model.Unreachable) {
		t.Errorf("Latency = %v，期望哨兵值", got.Latency)
	}
}

func TestHTTPingInvalidParams(t *testing.T) {
	tests := []struct {
		name    string
		ip      string
		port    int
		host    string
		times   int
		timeout time.Duration
	}{
		{name: "空 IP", ip: "", port: 443, host: "a.com", times: 1, timeout: time.Second},
		{name: "端口越界", ip: "1.1.1.1", port: 0, host: "a.com", times: 1, timeout: time.Second},
		{name: "空域名", ip: "1.1.1.1", port: 443, host: "", times: 1, timeout: time.Second},
		{name: "次数为零", ip: "1.1.1.1", port: 443, host: "a.com", times: 0, timeout: time.Second},
		{name: "超时为负", ip: "1.1.1.1", port: 443, host: "a.com", times: 1, timeout: -time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := HTTPing(context.Background(), tt.ip, tt.port, tt.host, false, tt.times, tt.timeout); err == nil {
				t.Error("期望返回错误，实际为 nil")
			}
		})
	}
}

func TestHTTPingStopsWhenContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := HTTPing(ctx, "127.0.0.1", freePort(t), "edge.example.com", false, 3, time.Second)
	if err != nil {
		t.Fatalf("取消不应返回错误，实际：%v", err)
	}
	if got.Sent != 0 {
		t.Errorf("Sent = %d，期望 0：取消后不应再发起请求", got.Sent)
	}
}

func TestNewTraceRequestSetsHostHeader(t *testing.T) {
	req, err := newTraceRequest(context.Background(), "https://example.com"+TracePath, "example.com")
	if err != nil {
		t.Fatalf("构造请求失败：%v", err)
	}
	if req.Host != "example.com" {
		t.Errorf("Host = %q，期望 %q", req.Host, "example.com")
	}
	if !strings.HasPrefix(req.Header.Get("User-Agent"), "Mozilla/5.0") {
		t.Errorf("User-Agent = %q，期望以 Mozilla/5.0 开头", req.Header.Get("User-Agent"))
	}
}

func TestStripPort(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "example.com", want: "example.com"},
		{in: "example.com:8443", want: "example.com"},
		{in: "2606:4700::1", want: "2606:4700::1"},
		{in: "[2606:4700::1]:8443", want: "2606:4700::1"},
		{in: "", want: ""},
	}
	for _, tt := range tests {
		if got := stripPort(tt.in); got != tt.want {
			t.Errorf("stripPort(%q) = %q，期望 %q", tt.in, got, tt.want)
		}
	}
}

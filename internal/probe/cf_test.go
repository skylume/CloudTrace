package probe

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// roundTripFunc 让测试可以直接注入一个传输层。
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// fakeResponse 构造一个一次性响应。
func fakeResponse(status int, header http.Header, body string) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestCFRounds(t *testing.T) {
	tests := []struct {
		requested int
		want      int
	}{
		{requested: 0, want: 3},
		{requested: 1, want: 3},
		{requested: 2, want: 3},
		{requested: 3, want: 3},
		{requested: 5, want: 5},
		{requested: -1, want: 3},
	}
	for _, tt := range tests {
		if got := CFRounds(tt.requested); got != tt.want {
			t.Errorf("CFRounds(%d) = %d，期望 %d", tt.requested, got, tt.want)
		}
	}
}

func TestCFVerdictKeep(t *testing.T) {
	// 关键：未能验证必须保留。网络抖动不等于「不是 Cloudflare」，
	// 按不保留处理会把好节点误杀。
	tests := []struct {
		verdict CFVerdict
		want    bool
	}{
		{verdict: CFValid, want: true},
		{verdict: CFUnknown, want: true},
		{verdict: CFInvalid, want: false},
	}
	for _, tt := range tests {
		if got := tt.verdict.Keep(); got != tt.want {
			t.Errorf("%v.Keep() = %v，期望 %v", tt.verdict, got, tt.want)
		}
	}
}

func TestCFVerdictString(t *testing.T) {
	tests := []struct {
		verdict CFVerdict
		want    string
	}{
		{verdict: CFValid, want: "valid"},
		{verdict: CFInvalid, want: "invalid"},
		{verdict: CFUnknown, want: "unknown"},
	}
	for _, tt := range tests {
		if got := tt.verdict.String(); got != tt.want {
			t.Errorf("String() = %q，期望 %q", got, tt.want)
		}
	}
}

func TestVerifyCFJudgements(t *testing.T) {
	tests := []struct {
		name   string
		status int
		header http.Header
		body   string
		want   CFVerdict
	}{
		{
			name:   "判据一：400 且 Server 以 cloudflare 开头",
			status: http.StatusBadRequest,
			header: http.Header{"Server": []string{"cloudflare"}},
			want:   CFValid,
		},
		{
			name:   "判据一：大小写不敏感",
			status: http.StatusBadRequest,
			header: http.Header{"Server": []string{"CloudFlare-nginx"}},
			want:   CFValid,
		},
		{
			name:   "判据二：200 且响应体含 colo",
			status: http.StatusOK,
			body:   "ip=1.1.1.1\ncolo=HKG\n",
			want:   CFValid,
		},
		{
			name:   "判据三：200 且响应头含 cf-ray",
			status: http.StatusOK,
			header: http.Header{"Cf-Ray": []string{"7d3b-HKG"}},
			body:   "<html>hi</html>",
			want:   CFValid,
		},
		{
			name:   "400 但 Server 不是 cloudflare",
			status: http.StatusBadRequest,
			header: http.Header{"Server": []string{"nginx"}},
			want:   CFInvalid,
		},
		{
			name:   "200 但既无 colo 也无 cf-ray",
			status: http.StatusOK,
			body:   "<html>welcome</html>",
			want:   CFInvalid,
		},
		{
			name:   "302 跳转",
			status: http.StatusFound,
			header: http.Header{"Location": []string{"https://example.com"}},
			want:   CFInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
				return fakeResponse(tt.status, tt.header, tt.body), nil
			})
			target := CFTarget{IP: "1.1.1.1", Port: 443, Host: "edge.example.com", UseTLS: true}

			got, err := verifyCF(context.Background(), target, 3, transport)
			if err != nil {
				t.Fatalf("verifyCF 返回错误：%v", err)
			}
			if got.Verdict != tt.want {
				t.Errorf("Verdict = %v，期望 %v", got.Verdict, tt.want)
			}
		})
	}
}

func TestVerifyCFUnknownWhenAllRequestsFail(t *testing.T) {
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("连接被拒绝")
	})
	target := CFTarget{IP: "1.1.1.1", Port: 443, Host: "edge.example.com"}

	got, err := verifyCF(context.Background(), target, 3, transport)
	if err != nil {
		t.Fatalf("verifyCF 返回错误：%v", err)
	}
	if got.Verdict != CFUnknown {
		t.Errorf("全部请求失败应降级为未验证，实际 %v", got.Verdict)
	}
	if !got.Verdict.Keep() {
		t.Error("未验证的节点必须保留")
	}
	if got.Rounds != 3 {
		t.Errorf("Rounds = %d，期望 3", got.Rounds)
	}
	if !closeTo(got.Stat.Loss, 1) {
		t.Errorf("Loss = %v，期望 1", got.Stat.Loss)
	}
}

func TestVerifyCFShortCircuitsOnFirstValidRound(t *testing.T) {
	var calls int32
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return fakeResponse(http.StatusOK, nil, "colo=HKG\n"), nil
	})
	target := CFTarget{IP: "1.1.1.1", Port: 443, Host: "edge.example.com"}

	got, err := verifyCF(context.Background(), target, 5, transport)
	if err != nil {
		t.Fatalf("verifyCF 返回错误：%v", err)
	}
	if got.Verdict != CFValid {
		t.Fatalf("Verdict = %v，期望 valid", got.Verdict)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("确认有效后应立刻返回，实际请求 %d 次", n)
	}
}

func TestVerifyCFAggregatesStat(t *testing.T) {
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return fakeResponse(http.StatusOK, nil, "<html>not cf</html>"), nil
	})
	target := CFTarget{IP: "1.1.1.1", Port: 443, Host: "edge.example.com"}

	got, err := verifyCF(context.Background(), target, 4, transport)
	if err != nil {
		t.Fatalf("verifyCF 返回错误：%v", err)
	}
	if got.Rounds != 4 {
		t.Errorf("Rounds = %d，期望 4", got.Rounds)
	}
	if got.Stat.Recv != 4 {
		t.Errorf("Recv = %d，期望 4", got.Stat.Recv)
	}
	if got.Stat.LatencyMax < got.Stat.Latency {
		t.Errorf("延迟最大值 %v 不应小于最小值 %v", got.Stat.LatencyMax, got.Stat.Latency)
	}
}

func TestVerifyCFStopsWhenContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var calls int32
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return fakeResponse(http.StatusOK, nil, "colo=HKG\n"), nil
	})
	target := CFTarget{IP: "1.1.1.1", Port: 443, Host: "edge.example.com"}

	got, err := verifyCF(ctx, target, 3, transport)
	if err != nil {
		t.Fatalf("verifyCF 返回错误：%v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Errorf("取消后不应再发起请求，实际 %d 次", n)
	}
	if got.Verdict != CFUnknown {
		t.Errorf("Verdict = %v，期望 unknown", got.Verdict)
	}
}

func TestVerifyCFAgainstLocalServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Server", "cloudflare")
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	port := server.Listener.Addr().(*net.TCPAddr).Port
	target := CFTarget{IP: "127.0.0.1", Port: port, Host: "edge.example.com"}

	got, err := VerifyCF(context.Background(), target, 0, 2*time.Second)
	if err != nil {
		t.Fatalf("VerifyCF 返回错误：%v", err)
	}
	if got.Verdict != CFValid {
		t.Errorf("Verdict = %v，期望 valid", got.Verdict)
	}
	if got.Rounds != 1 {
		t.Errorf("Rounds = %d，期望 1（首轮即确认）", got.Rounds)
	}
}

func TestVerifyCFInvalidParams(t *testing.T) {
	tests := []struct {
		name    string
		target  CFTarget
		timeout time.Duration
	}{
		{name: "空 IP", target: CFTarget{Port: 443, Host: "a.com"}, timeout: time.Second},
		{name: "端口越界", target: CFTarget{IP: "1.1.1.1", Port: 0, Host: "a.com"}, timeout: time.Second},
		{name: "空域名", target: CFTarget{IP: "1.1.1.1", Port: 443}, timeout: time.Second},
		{name: "超时为负", target: CFTarget{IP: "1.1.1.1", Port: 443, Host: "a.com"}, timeout: -time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := VerifyCF(context.Background(), tt.target, 3, tt.timeout); err == nil {
				t.Error("期望返回错误，实际为 nil")
			}
		})
	}
}

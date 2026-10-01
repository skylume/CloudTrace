package probe

import (
	"context"
	"math"
	"testing"
	"time"

	"cloudtrace/internal/model"
)

// closeTo 用于浮点断言。
func closeTo(got, want float64) bool {
	return math.Abs(got-want) < 1e-9
}

func TestSummarize(t *testing.T) {
	tests := []struct {
		name    string
		samples []float64
		sent    int
		want    Result
	}{
		{
			name:    "单次探测没有抖动",
			samples: []float64{12},
			sent:    1,
			want:    Result{Latency: 12, LatencyAvg: 12, LatencyMax: 12, Jitter: 0, Loss: 0, Sent: 1, Recv: 1},
		},
		{
			name:    "三次探测取极值与样本标准差",
			samples: []float64{10, 20, 30},
			sent:    3,
			want:    Result{Latency: 10, LatencyAvg: 20, LatencyMax: 30, Jitter: 10, Loss: 0, Sent: 3, Recv: 3},
		},
		{
			name:    "两次探测的样本标准差",
			samples: []float64{10, 20},
			sent:    2,
			// 样本标准差：(10-15)^2 + (20-15)^2 = 50，除以 n-1=1，开方 ≈ 7.0710678
			want: Result{Latency: 10, LatencyAvg: 15, LatencyMax: 20, Jitter: math.Sqrt(50), Loss: 0, Sent: 2, Recv: 2},
		},
		{
			name:    "全部失败返回哨兵值与满丢包",
			samples: nil,
			sent:    3,
			want: Result{
				Latency:    model.Unreachable,
				LatencyAvg: model.Unreachable,
				LatencyMax: model.Unreachable,
				Loss:       1,
				Sent:       3,
				Recv:       0,
			},
		},
		{
			name:    "部分丢包",
			samples: []float64{10, 10},
			sent:    4,
			want:    Result{Latency: 10, LatencyAvg: 10, LatencyMax: 10, Jitter: 0, Loss: 0.5, Sent: 4, Recv: 2},
		},
		{
			name:    "sent 为零时按样本数兜底",
			samples: []float64{5},
			sent:    0,
			want:    Result{Latency: 5, LatencyAvg: 5, LatencyMax: 5, Loss: 0, Sent: 1, Recv: 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summarize(tt.samples, tt.sent)
			assertResult(t, got, tt.want)
		})
	}
}

// assertResult 逐字段比对统计结果。
func assertResult(t *testing.T, got, want Result) {
	t.Helper()
	for _, field := range []struct {
		name     string
		got      float64
		want     float64
		tolerant bool
	}{
		{"Latency", got.Latency, want.Latency, true},
		{"LatencyAvg", got.LatencyAvg, want.LatencyAvg, true},
		{"LatencyMax", got.LatencyMax, want.LatencyMax, true},
		{"Jitter", got.Jitter, want.Jitter, true},
		{"Loss", got.Loss, want.Loss, true},
	} {
		if !closeTo(field.got, field.want) {
			t.Errorf("%s = %v，期望 %v", field.name, field.got, field.want)
		}
	}
	if got.Sent != want.Sent {
		t.Errorf("Sent = %d，期望 %d", got.Sent, want.Sent)
	}
	if got.Recv != want.Recv {
		t.Errorf("Recv = %d，期望 %d", got.Recv, want.Recv)
	}
}

func TestSampleStdDev(t *testing.T) {
	tests := []struct {
		name    string
		samples []float64
		mean    float64
		want    float64
	}{
		{name: "空样本", samples: nil, mean: 0, want: 0},
		{name: "单样本恒为 0", samples: []float64{7}, mean: 7, want: 0},
		{name: "全相同", samples: []float64{5, 5, 5}, mean: 5, want: 0},
		{name: "对称分布", samples: []float64{10, 20, 30}, mean: 20, want: 10},
		// 样本标准差分母是 n-1，用总体标准差会得到 5，这里必须区分开。
		{name: "样本与总体标准差的差异", samples: []float64{0, 10}, mean: 5, want: math.Sqrt(50)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sampleStdDev(tt.samples, tt.mean); !closeTo(got, tt.want) {
				t.Errorf("sampleStdDev = %v，期望 %v", got, tt.want)
			}
		})
	}
}

func TestResultOk(t *testing.T) {
	if (Result{}).Ok() {
		t.Error("零值结果不应被视为探测成功")
	}
	if !(Result{Recv: 1}).Ok() {
		t.Error("有成功次数时应视为探测成功")
	}
}

func TestValidateTarget(t *testing.T) {
	tests := []struct {
		name    string
		ip      string
		port    int
		wantErr bool
	}{
		{name: "正常", ip: "1.1.1.1", port: 443},
		{name: "空 IP", ip: "", port: 443, wantErr: true},
		{name: "端口为零", ip: "1.1.1.1", port: 0, wantErr: true},
		{name: "端口为负", ip: "1.1.1.1", port: -1, wantErr: true},
		{name: "端口越界", ip: "1.1.1.1", port: 65536, wantErr: true},
		{name: "IPv6", ip: "2606:4700::1", port: 443},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTarget(tt.ip, tt.port)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateTarget(%q, %d) 错误 = %v，期望出错 = %v", tt.ip, tt.port, err, tt.wantErr)
			}
		})
	}
}

func TestElapsedMS(t *testing.T) {
	start := time.Now().Add(-1500 * time.Microsecond)
	got := elapsedMS(start)
	// 下界确认换算成毫秒而不是秒；上界只需要拦住量级错误（例如误按微秒
	// 返回会得到 1500）。不收紧上界是因为时钟粒度可能有好几毫秒，两次
	// 取时之间正好跨过一格的话，读数会多出一格。
	if got < 1.4 || got > 100 {
		t.Errorf("elapsedMS = %v，期望落在 1.4..100 毫秒之间", got)
	}
}

func TestDialerForRespectsContext(t *testing.T) {
	dial := dialerFor(time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := dial(ctx, "tcp", "127.0.0.1:1"); err == nil {
		t.Error("已取消的 context 上拨号应当失败")
	}
}

package source

import (
	"math/rand"
	"net"
	"reflect"
	"strings"
	"testing"
)

// newTestRand 返回可复现的随机源。
func newTestRand(seed int64) *rand.Rand { return rand.New(rand.NewSource(seed)) }

func TestSampleIsReproducible(t *testing.T) {
	cidrs := []string{"104.16.0.0/22", "172.64.0.0/24"}

	first, err := Sample(cidrs, 2, 0, 42)
	if err != nil {
		t.Fatalf("Sample 返回错误：%v", err)
	}
	second, err := Sample(cidrs, 2, 0, 42)
	if err != nil {
		t.Fatalf("Sample 返回错误：%v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("同一种子应得到一致结果：\n%v\n%v", first, second)
	}
	if len(first) != 10 {
		t.Fatalf("采样 %d 个，期望 10（4 个 /24 × 2 + 1 个 /24 × 2）", len(first))
	}
}

func TestSampleSplitsBySlash24(t *testing.T) {
	ips, err := Sample([]string{"104.16.0.0/22"}, 1, 0, 7)
	if err != nil {
		t.Fatalf("Sample 返回错误：%v", err)
	}
	if len(ips) != 4 {
		t.Fatalf("采样 %d 个，期望 4（/22 含 4 个 /24，每个取 1 个）", len(ips))
	}

	seen := map[string]bool{}
	for _, ip := range ips {
		if !strings.HasPrefix(ip, "104.16.") {
			t.Fatalf("IP %q 不在 104.16.0.0/16 内", ip)
		}
		octets := strings.Split(ip, ".")
		seen[octets[2]] = true
	}
	if len(seen) != 4 {
		t.Errorf("第三段只出现 %d 种取值，期望 4 种（每个 /24 各一个）：%v", len(seen), ips)
	}
}

func TestSamplePerSlash24Count(t *testing.T) {
	tests := []struct {
		name       string
		perSlash24 int
		want       int
	}{
		{name: "取 1 个", perSlash24: 1, want: 4},
		{name: "取 3 个", perSlash24: 3, want: 12},
		{name: "零按 1 处理", perSlash24: 0, want: 4},
		{name: "负数按 1 处理", perSlash24: -5, want: 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ips, err := Sample([]string{"104.16.0.0/22"}, tt.perSlash24, 0, 7)
			if err != nil {
				t.Fatalf("Sample 返回错误：%v", err)
			}
			if len(ips) != tt.want {
				t.Errorf("采样 %d 个，期望 %d", len(ips), tt.want)
			}
		})
	}
}

func TestSampleAvoidsBroadcastOctet(t *testing.T) {
	ips, err := Sample([]string{"104.16.0.0/20"}, 20, 0, 3)
	if err != nil {
		t.Fatalf("Sample 返回错误：%v", err)
	}
	for _, ip := range ips {
		if strings.HasSuffix(ip, ".255") {
			t.Errorf("采样到广播地址 %q", ip)
		}
	}
}

func TestSampleDeduplicates(t *testing.T) {
	// 同一条来源写两遍不该产生两份结果。
	ips, err := Sample([]string{"1.1.1.1", "1.1.1.1"}, 1, 0, 1)
	if err != nil {
		t.Fatalf("Sample 返回错误：%v", err)
	}
	if want := []string{"1.1.1.1"}; !reflect.DeepEqual(ips, want) {
		t.Errorf("ips = %v，期望 %v", ips, want)
	}

	// 同一批结果里也不允许出现重复地址：每个 /24 随机取的地址可能撞车。
	more, err := Sample([]string{"104.16.0.0/20"}, 5, 0, 9)
	if err != nil {
		t.Fatalf("Sample 返回错误：%v", err)
	}
	seen := map[string]bool{}
	for _, ip := range more {
		if seen[ip] {
			t.Errorf("结果里出现重复 IP：%q", ip)
		}
		seen[ip] = true
	}
}

func TestSampleTruncatesToMax(t *testing.T) {
	ips, err := Sample([]string{"104.16.0.0/20"}, 10, 3, 11)
	if err != nil {
		t.Fatalf("Sample 返回错误：%v", err)
	}
	if len(ips) != 3 {
		t.Errorf("采样 %d 个，期望被截断到 3", len(ips))
	}
}

func TestSampleSingleAddress(t *testing.T) {
	ips, err := Sample([]string{"1.1.1.1"}, 1, 0, 1)
	if err != nil {
		t.Fatalf("Sample 返回错误：%v", err)
	}
	if want := []string{"1.1.1.1"}; !reflect.DeepEqual(ips, want) {
		t.Errorf("ips = %v，期望 %v", ips, want)
	}
}

func TestSampleEnumeratesSegmentLongerThanSlash24(t *testing.T) {
	ips, err := Sample([]string{"1.1.1.0/30"}, 1, 0, 1)
	if err != nil {
		t.Fatalf("Sample 返回错误：%v", err)
	}
	want := []string{"1.1.1.0", "1.1.1.1", "1.1.1.2", "1.1.1.3"}
	if !reflect.DeepEqual(ips, want) {
		t.Errorf("ips = %v，期望 %v（比 /24 更小的段直接全取）", ips, want)
	}
}

func TestSampleIPv6KeepsPrefix(t *testing.T) {
	tests := []struct {
		name    string
		cidr    string
		prefix  string
		wantLen int
	}{
		{name: "/64 只随机后 64 位", cidr: "2606:4700::/64", prefix: "2606:4700:", wantLen: 3},
		{name: "/32 保持前 32 位稳定", cidr: "2606:4700::/32", prefix: "2606:4700:", wantLen: 2},
		{name: "/120 只随机最后一段", cidr: "2606:4700::/120", prefix: "2606:4700:", wantLen: 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ips, err := Sample([]string{tt.cidr}, tt.wantLen, 0, 5)
			if err != nil {
				t.Fatalf("Sample 返回错误：%v", err)
			}
			if len(ips) != tt.wantLen {
				t.Fatalf("采样 %d 个，期望 %d：%v", len(ips), tt.wantLen, ips)
			}
			for _, ip := range ips {
				if !strings.HasPrefix(ip, tt.prefix) {
					t.Errorf("IP %q 未保持前缀 %q", ip, tt.prefix)
				}
				if net.ParseIP(ip) == nil {
					t.Errorf("采样结果 %q 不是合法 IP", ip)
				}
			}
		})
	}
}

func TestSampleIPv6SingleAddress(t *testing.T) {
	ips, err := Sample([]string{"2606:4700::1/128"}, 3, 0, 1)
	if err != nil {
		t.Fatalf("Sample 返回错误：%v", err)
	}
	if want := []string{"2606:4700::1"}; !reflect.DeepEqual(ips, want) {
		t.Errorf("ips = %v，期望 %v", ips, want)
	}
}

func TestSampleUnboundedHasSafetyCap(t *testing.T) {
	// 用户可能填一个极大的段，「不限制」不能真的无限展开。
	ips, err := Sample([]string{"0.0.0.0/0"}, 1, 0, 1)
	if err != nil {
		t.Fatalf("Sample 返回错误：%v", err)
	}
	if len(ips) != maxUnboundedCandidates {
		t.Errorf("采样 %d 个，期望被安全上限截断到 %d", len(ips), maxUnboundedCandidates)
	}
}

func TestSampleErrors(t *testing.T) {
	tests := []struct {
		name  string
		cidrs []string
		max   int
	}{
		{name: "上限为负", cidrs: []string{"1.1.1.0/24"}, max: -1},
		{name: "非法网段", cidrs: []string{"1.1.1.0/33"}, max: 0},
		{name: "空网段", cidrs: []string{"  "}, max: 0},
		{name: "不是网段", cidrs: []string{"example.com"}, max: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Sample(tt.cidrs, 1, tt.max, 1); err == nil {
				t.Error("期望返回错误，实际为 nil")
			}
		})
	}
}

func TestParseNetwork(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "IPv4 网段", in: "104.16.0.0/12", want: "104.16.0.0/12"},
		{name: "IPv4 单地址补 /32", in: "1.1.1.1", want: "1.1.1.1/32"},
		{name: "IPv6 单地址补 /128", in: "2606:4700::1", want: "2606:4700::1/128"},
		{name: "去掉首尾空白", in: "  1.1.1.1  ", want: "1.1.1.1/32"},
		{name: "空输入", in: "", wantErr: true},
		{name: "非法地址", in: "not-an-ip", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			network, err := parseNetwork(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("错误 = %v，期望出错 = %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got := network.String(); got != tt.want {
				t.Errorf("网段 = %q，期望 %q", got, tt.want)
			}
		})
	}
}

func TestPickIndices(t *testing.T) {
	rng := newTestRand(1)

	all := pickIndices(3, 5, rng)
	if want := []int{0, 1, 2}; !reflect.DeepEqual(all, want) {
		t.Errorf("需要数超过总数时应全取，实际 %v", all)
	}

	subset := pickIndices(100, 5, rng)
	if len(subset) != 5 {
		t.Fatalf("挑出 %d 个下标，期望 5", len(subset))
	}
	seen := map[int]bool{}
	for i, index := range subset {
		if index < 0 || index >= 100 {
			t.Fatalf("下标 %d 越界", index)
		}
		if seen[index] {
			t.Fatalf("下标 %d 重复", index)
		}
		seen[index] = true
		if i > 0 && subset[i-1] >= index {
			t.Fatalf("下标应升序返回，实际 %v", subset)
		}
	}
}

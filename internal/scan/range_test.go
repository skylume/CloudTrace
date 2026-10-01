package scan

import (
	"net/netip"
	"testing"
)

// expand 把网段列表展开成地址序列，用来验证拆分结果与区间完全一致。
//
// 这是「拆得对不对」唯一靠得住的判据：只数网段个数看不出漏地址，
// 只看首尾看不出中间重叠。
func expand(prefixes []netip.Prefix) []netip.Addr {
	var out []netip.Addr
	for _, prefix := range prefixes {
		for addr := prefix.Addr(); prefix.Contains(addr); addr = addr.Next() {
			out = append(out, addr)
		}
	}
	return out
}

// walkRange 独立地按步长走一遍区间，作为展开结果的对照。
func walkRange(t *testing.T, start, end netip.Addr) []netip.Addr {
	t.Helper()
	var out []netip.Addr
	for addr := start; addr.IsValid() && addr.Compare(end) <= 0; addr = addr.Next() {
		out = append(out, addr)
	}
	return out
}

func TestRangeToCIDRsCoversExactly(t *testing.T) {
	tests := []struct {
		name  string
		spec  string
		start string
		end   string
	}{
		{name: "跨对齐边界", spec: "1.1.1.5-1.1.1.100", start: "1.1.1.5", end: "1.1.1.100"},
		{name: "未对齐起点", spec: "192.168.1.13-192.168.1.42", start: "192.168.1.13", end: "192.168.1.42"},
		{name: "单个地址", spec: "1.1.1.1-1.1.1.1", start: "1.1.1.1", end: "1.1.1.1"},
		{name: "恰好一个 /24", spec: "10.0.0.0-10.0.0.255", start: "10.0.0.0", end: "10.0.0.255"},
		{name: "跨 /24 边界", spec: "10.0.0.200-10.0.1.20", start: "10.0.0.200", end: "10.0.1.20"},
		{name: "IPv6", spec: "2606:4700::1-2606:4700::5", start: "2606:4700::1", end: "2606:4700::5"},
		{name: "IPv6 跨边界", spec: "2606:4700::fe-2606:4700::102", start: "2606:4700::fe", end: "2606:4700::102"},
		// 终点是地址空间末尾：走完这一块之后没有下一个地址了。
		{name: "IPv4 末尾", spec: "255.255.255.252-255.255.255.255", start: "255.255.255.252", end: "255.255.255.255"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prefixes, ok := rangeToCIDRs(tt.spec)
			if !ok {
				t.Fatalf("rangeToCIDRs(%q) 返回失败", tt.spec)
			}
			start, err := netip.ParseAddr(tt.start)
			if err != nil {
				t.Fatalf("解析起点失败：%v", err)
			}
			end, err := netip.ParseAddr(tt.end)
			if err != nil {
				t.Fatalf("解析终点失败：%v", err)
			}

			got := expand(prefixes)
			want := walkRange(t, start, end)
			if len(got) != len(want) {
				t.Fatalf("展开出 %d 个地址，期望 %d 个", len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("第 %d 个地址 = %v，期望 %v（拆分出现漏地址或重叠）", i, got[i], want[i])
				}
			}
		})
	}
}

// TestRangeToCIDRsKeepsAlignedBlocksWhole 验证拆分不会把本来对齐的整块切碎。
//
// 切碎不影响正确性，但会让采样器多跑几轮、候选分布变差；一个恰好对齐的
// /24 必须只产出一个网段。
func TestRangeToCIDRsKeepsAlignedBlocksWhole(t *testing.T) {
	prefixes, ok := rangeToCIDRs("10.0.0.0-10.0.0.255")
	if !ok {
		t.Fatal("rangeToCIDRs 返回失败")
	}
	if len(prefixes) != 1 {
		t.Fatalf("拆出 %d 个网段，期望 1：%v", len(prefixes), prefixes)
	}
	if got := prefixes[0].String(); got != "10.0.0.0/24" {
		t.Errorf("网段 = %q，期望 %q", got, "10.0.0.0/24")
	}

	prefixes, ok = rangeToCIDRs("2606:4700::-2606:4700::ffff")
	if !ok {
		t.Fatal("rangeToCIDRs 返回失败")
	}
	if len(prefixes) != 1 {
		t.Fatalf("IPv6 拆出 %d 个网段，期望 1：%v", len(prefixes), prefixes)
	}
	if got := prefixes[0].String(); got != "2606:4700::/112" {
		t.Errorf("网段 = %q，期望 %q", got, "2606:4700::/112")
	}
}

func TestRangeToCIDRsInvalid(t *testing.T) {
	tests := []struct {
		name string
		spec string
	}{
		{name: "没有连字符", spec: "1.1.1.1"},
		{name: "起点大于终点", spec: "1.1.1.5-1.1.1.1"},
		{name: "两端不同族", spec: "1.1.1.1-2606:4700::1"},
		{name: "起点不是地址", spec: "abc-1.1.1.1"},
		{name: "终点不是地址", spec: "1.1.1.1-xyz"},
		{name: "终点为空", spec: "1.1.1.1-"},
		{name: "起点为空", spec: "-1.1.1.1"},
		{name: "空串", spec: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if prefixes, ok := rangeToCIDRs(tt.spec); ok {
				t.Errorf("rangeToCIDRs(%q) 应当失败，实际得到 %v", tt.spec, prefixes)
			}
		})
	}
}

func TestBlockEnd(t *testing.T) {
	tests := []struct {
		addr   string
		length int
		bits   int
		want   string
	}{
		{addr: "1.1.1.0", length: 24, bits: 32, want: "1.1.1.255"},
		{addr: "1.1.1.5", length: 32, bits: 32, want: "1.1.1.5"},
		{addr: "1.1.1.6", length: 31, bits: 32, want: "1.1.1.7"},
		{addr: "10.0.0.0", length: 0, bits: 32, want: "255.255.255.255"},
		{addr: "2606:4700::", length: 112, bits: 128, want: "2606:4700::ffff"},
		{addr: "2606:4700::1", length: 128, bits: 128, want: "2606:4700::1"},
		{addr: "::", length: 0, bits: 128, want: "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"},
	}

	for _, tt := range tests {
		t.Run(tt.addr+"/"+tt.want, func(t *testing.T) {
			addr, err := netip.ParseAddr(tt.addr)
			if err != nil {
				t.Fatalf("解析地址失败：%v", err)
			}
			got := blockEnd(addr, tt.length, tt.bits)
			if got.String() != tt.want {
				t.Errorf("blockEnd(%s, %d) = %s，期望 %s", tt.addr, tt.length, got, tt.want)
			}
		})
	}
}

func TestTrailingZeros(t *testing.T) {
	tests := []struct {
		addr string
		bits int
		want int
	}{
		{addr: "1.1.1.1", bits: 32, want: 0},
		{addr: "1.1.1.2", bits: 32, want: 1},
		// 10.0.0.0 = 5×2^25，最低置位在第 25 位，因此最大对齐块是 /7
		// （而不是想当然的 /8）。
		{addr: "10.0.0.0", bits: 32, want: 25},
		{addr: "0.0.0.0", bits: 32, want: 32},
		// 2606:4700:: 的第 3 字节是 0x47，低位有 1，故只数到 104。
		{addr: "2606:4700::", bits: 128, want: 104},
		{addr: "2606:4700::1", bits: 128, want: 0},
		{addr: "::", bits: 128, want: 128},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			addr, err := netip.ParseAddr(tt.addr)
			if err != nil {
				t.Fatalf("解析地址失败：%v", err)
			}
			if got := trailingZeros(addr, tt.bits); got != tt.want {
				t.Errorf("trailingZeros(%s) = %d，期望 %d", tt.addr, got, tt.want)
			}
		})
	}
}

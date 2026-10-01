package assets

import (
	"strings"
	"testing"
)

func TestOfficialRangesIPv4(t *testing.T) {
	ranges := OfficialRanges(4)
	if len(ranges) == 0 {
		t.Fatal("IPv4 内置网段为空")
	}
	for _, r := range ranges {
		if !strings.Contains(r, "/") || strings.Contains(r, ":") {
			t.Errorf("IPv4 内置网段 %q 不是 IPv4 CIDR", r)
		}
	}
}

func TestOfficialRangesIPv6(t *testing.T) {
	ranges := OfficialRanges(6)
	if len(ranges) == 0 {
		t.Fatal("IPv6 内置网段为空")
	}
	for _, r := range ranges {
		if !strings.Contains(r, "/") || !strings.Contains(r, ":") {
			t.Errorf("IPv6 内置网段 %q 不是 IPv6 CIDR", r)
		}
	}
}

func TestOfficialRangesUnknownVersion(t *testing.T) {
	// 不认识的协议族必须返回 nil：返回空切片会让调用方误以为
	// 「官方段就是空的」，从而静默扫不到任何东西。
	for _, v := range []int{0, 5, -1, 7} {
		if got := OfficialRanges(v); got != nil {
			t.Errorf("ipVersion=%d 时 = %v，期望 nil", v, got)
		}
	}
}

func TestOfficialRangesReturnsCopy(t *testing.T) {
	// 调用方会就地合并、去重，若共享同一底层数组会污染后续调用。
	first := OfficialRanges(4)
	if len(first) == 0 {
		t.Fatal("IPv4 内置网段为空")
	}
	original := first[0]
	first[0] = "篡改"

	if second := OfficialRanges(4); second[0] != original {
		t.Errorf("第二次调用读到 %q，期望 %q（说明返回了共享切片）", second[0], original)
	}
}

func TestSplitRanges(t *testing.T) {
	text := strings.Join([]string{
		"# 注释行",
		"104.16.0.0/13",
		"",
		"   ",
		"  172.64.0.0/13  ",
		"# 末尾注释",
	}, "\n")

	got := SplitRanges(text)
	want := []string{"104.16.0.0/13", "172.64.0.0/13"}
	if len(got) != len(want) {
		t.Fatalf("切分出 %d 条 %v，期望 %d 条 %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 条 = %q，期望 %q", i, got[i], want[i])
		}
	}
}

func TestSplitRangesEmpty(t *testing.T) {
	if got := SplitRanges(""); len(got) != 0 {
		t.Errorf("空文本切出 %v，期望空", got)
	}
}

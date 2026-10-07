package source

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"
)

func TestParseSourceTextForms(t *testing.T) {
	// 这 9 种形态是功能规格里明确要求支持的输入写法。
	tests := []struct {
		name  string
		input string
		want  Entry
	}{
		{name: "网段", input: "104.16.0.0/12", want: Entry{Kind: KindCIDR, Value: "104.16.0.0/12"}},
		{name: "网段按掩码规范化", input: "1.1.1.1/24", want: Entry{Kind: KindCIDR, Value: "1.1.1.0/24"}},
		{name: "单个 IP", input: "1.1.1.1", want: Entry{Kind: KindIP, Value: "1.1.1.1"}},
		{name: "IP 带端口", input: "1.1.1.1:8443", want: Entry{Kind: KindIP, Value: "1.1.1.1", Port: 8443}},
		{name: "IPv6 带端口", input: "[2606:4700::1]:443", want: Entry{Kind: KindIP, Value: "2606:4700::1", Port: 443}},
		{name: "IPv6 不带端口", input: "2606:4700::1", want: Entry{Kind: KindIP, Value: "2606:4700::1"}},
		{name: "IPv6 方括号不带端口", input: "[2606:4700::1]", want: Entry{Kind: KindIP, Value: "2606:4700::1"}},
		{name: "IP 区间", input: "1.1.1.1-1.1.1.100", want: Entry{Kind: KindRange, Value: "1.1.1.1-1.1.1.100"}},
		{name: "域名", input: "example.com", want: Entry{Kind: KindHost, Value: "example.com"}},
		{name: "域名带端口", input: "example.com:8443", want: Entry{Kind: KindHost, Value: "example.com", Port: 8443}},
		{name: "域名转小写", input: "Example.COM", want: Entry{Kind: KindHost, Value: "example.com"}},
		{name: "URL 取主机名", input: "https://example.com/path?q=1", want: Entry{Kind: KindHost, Value: "example.com"}},
		{name: "URL 带端口", input: "https://example.com:8443/path", want: Entry{Kind: KindHost, Value: "example.com", Port: 8443}},
		{name: "URL 是 IP", input: "http://1.1.1.1:8080/x", want: Entry{Kind: KindIP, Value: "1.1.1.1", Port: 8080}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := ParseSourceText(tt.input)
			if err != nil {
				t.Fatalf("ParseSourceText(%q) 返回错误：%v", tt.input, err)
			}
			if len(parsed.Entries) != 1 {
				t.Fatalf("解析出 %d 个条目，期望 1：%v", len(parsed.Entries), parsed.Entries)
			}
			if got := parsed.Entries[0]; got != tt.want {
				t.Errorf("条目 = %+v，期望 %+v", got, tt.want)
			}
		})
	}
}

func TestParseSourceTextIgnoresCommentsAndBlanks(t *testing.T) {
	input := strings.Join([]string{
		"# 这是整行注释",
		"",
		"   ",
		"1.1.1.1",
		"1.1.1.2 # 这是行尾注释",
		"1.1.1.3\t# 制表符前的注释",
		"\t2.2.2.2",
	}, "\n")

	parsed, err := ParseSourceText(input)
	if err != nil {
		t.Fatalf("ParseSourceText 返回错误：%v", err)
	}
	want := []string{"1.1.1.1", "1.1.1.2", "1.1.1.3", "2.2.2.2"}
	if len(parsed.Entries) != len(want) {
		t.Fatalf("解析出 %d 个条目，期望 %d：%v", len(parsed.Entries), len(want), parsed.Entries)
	}
	for i, ip := range want {
		if parsed.Entries[i].Value != ip {
			t.Errorf("第 %d 个条目 = %q，期望 %q", i, parsed.Entries[i].Value, ip)
		}
	}
	if parsed.Ignored != 0 {
		t.Errorf("Ignored = %d，期望 0：注释与空行不算无法识别", parsed.Ignored)
	}
}

func TestParseSourceTextKeepsHashInsideURL(t *testing.T) {
	// URL 里的 # 不是注释起点：它前面没有空白。
	parsed, err := ParseSourceText("https://example.com/a#frag")
	if err != nil {
		t.Fatalf("ParseSourceText 返回错误：%v", err)
	}
	if len(parsed.Entries) != 1 || parsed.Entries[0].Value != "example.com" {
		t.Fatalf("条目 = %v，期望主机名 example.com", parsed.Entries)
	}
}

func TestParseSourceTextSplitsMultipleTokensPerLine(t *testing.T) {
	input := "1.1.1.1, 1.1.1.2; 1.1.1.3|1.1.1.4 1.1.1.5"

	parsed, err := ParseSourceText(input)
	if err != nil {
		t.Fatalf("ParseSourceText 返回错误：%v", err)
	}
	if len(parsed.Entries) != 5 {
		t.Fatalf("解析出 %d 个条目，期望 5：%v", len(parsed.Entries), parsed.Entries)
	}
}

func TestParseSourceTextCounts(t *testing.T) {
	input := strings.Join([]string{
		"104.16.0.0/12",
		"104.16.0.0/16",
		"1.1.1.1",
		"1.1.1.1:8443",
		"1.1.1.1-1.1.1.100",
		"example.com",
		"https://example.com/x",
		"这不是来源",
	}, "\n")

	parsed, err := ParseSourceText(input)
	if err != nil {
		t.Fatalf("ParseSourceText 返回错误：%v", err)
	}
	if parsed.CIDRs != 2 {
		t.Errorf("CIDRs = %d，期望 2", parsed.CIDRs)
	}
	if parsed.IPs != 2 {
		t.Errorf("IPs = %d，期望 2", parsed.IPs)
	}
	if parsed.Ranges != 1 {
		t.Errorf("Ranges = %d，期望 1", parsed.Ranges)
	}
	if parsed.Hosts != 2 {
		t.Errorf("Hosts = %d，期望 2", parsed.Hosts)
	}
	if parsed.Ports != 1 {
		t.Errorf("Ports = %d，期望 1（只有显式端口的条目才计数）", parsed.Ports)
	}
	if parsed.Ignored != 1 {
		t.Errorf("Ignored = %d，期望 1", parsed.Ignored)
	}
	if parsed.Empty() {
		t.Error("Empty() 应为 false")
	}
}

func TestParseSourceTextOnlyCommentsIsNotAnError(t *testing.T) {
	parsed, err := ParseSourceText("# 只有注释\n\n")
	if err != nil {
		t.Fatalf("只有注释不应报错，实际：%v", err)
	}
	if !parsed.Empty() {
		t.Errorf("期望空结果，实际 %v", parsed.Entries)
	}
	if parsed.Ignored != 0 {
		t.Errorf("Ignored = %d，期望 0", parsed.Ignored)
	}
}

func TestParseSourceTextAllUnrecognizedReturnsError(t *testing.T) {
	_, err := ParseSourceText("完全看不懂的内容\n还有一行")
	if err == nil {
		t.Fatal("一条都没解析出来时应返回错误")
	}
}

func TestParseSourceTextRejectsBadTokens(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "非法网段", input: "1.1.1.1/33"},
		{name: "区间起点大于终点", input: "1.1.1.100-1.1.1.1"},
		{name: "区间两端地址族不同", input: "1.1.1.1-2606:4700::1"},
		{name: "端口越界", input: "1.1.1.1:70000"},
		{name: "没有点的单词", input: "localhost"},
		{name: "纯数字不是域名", input: "1.2.3.4.5"},
		{name: "顶级段不是字母", input: "example.123"},
		{name: "段首是连字符", input: "-example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := ParseSourceText(tt.input)
			if err == nil {
				t.Fatalf("期望返回错误，实际解析出 %v", parsed.Entries)
			}
			if parsed.Ignored != 1 {
				t.Errorf("Ignored = %d，期望 1", parsed.Ignored)
			}
		})
	}
}

func TestSplitHostPortLoose(t *testing.T) {
	tests := []struct {
		in       string
		wantHost string
		wantPort int
	}{
		{in: "1.1.1.1", wantHost: "1.1.1.1"},
		{in: "1.1.1.1:443", wantHost: "1.1.1.1", wantPort: 443},
		{in: "example.com:8443", wantHost: "example.com", wantPort: 8443},
		{in: "1.1.1.1:0", wantHost: "1.1.1.1:0"},
		{in: "1.1.1.1:70000", wantHost: "1.1.1.1:70000"},
		{in: "1.1.1.1:", wantHost: "1.1.1.1:"},
		{in: ":443", wantHost: ":443"},
	}

	for _, tt := range tests {
		host, port := splitHostPortLoose(tt.in)
		if host != tt.wantHost || port != tt.wantPort {
			t.Errorf("splitHostPortLoose(%q) = (%q, %d)，期望 (%q, %d)", tt.in, host, port, tt.wantHost, tt.wantPort)
		}
	}
}

func TestIsHostname(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{in: "example.com", want: true},
		{in: "a.b.co", want: true},
		{in: "my-host.example.com", want: true},
		{in: "example.com.", want: true},
		{in: "localhost", want: false},
		{in: "", want: false},
		{in: ".", want: false},
		{in: "example.c", want: false},
		{in: "example.123", want: false},
		{in: "-example.com", want: false},
		{in: "example-.com", want: false},
		{in: "exa mple.com", want: false},
	}

	for _, tt := range tests {
		if got := isHostname(tt.in); got != tt.want {
			t.Errorf("isHostname(%q) = %v，期望 %v", tt.in, got, tt.want)
		}
	}
}

// fakeResolver 返回预设结果，避免单元测试真的查 DNS。
type fakeResolver struct {
	results map[string][]net.IPAddr
	failed  map[string]bool
	calls   []string
	// err 非空时所有查询都返回它，用来模拟「查询过程中被取消」这类
	// 与域名本身无关的失败。
	err error
}

func (r *fakeResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	r.calls = append(r.calls, host)
	if r.err != nil {
		return nil, r.err
	}
	if r.failed[host] {
		return nil, errors.New("解析失败")
	}
	return r.results[host], nil
}

func TestResolveHosts(t *testing.T) {
	resolver := &fakeResolver{
		results: map[string][]net.IPAddr{
			"a.example.com":     {{IP: net.ParseIP("1.1.1.1")}, {IP: net.ParseIP("1.1.1.2")}},
			"b.example.com":     {{IP: net.ParseIP("1.1.1.1")}},
			"empty.example.com": {},
		},
		failed: map[string]bool{"bad.example.com": true},
	}

	ips, failed, err := ResolveHosts(context.Background(), resolver, []string{
		"a.example.com", "b.example.com", "bad.example.com", "empty.example.com",
	})
	if err != nil {
		t.Fatalf("ResolveHosts 返回错误：%v", err)
	}
	if want := []string{"1.1.1.1", "1.1.1.2"}; !reflect.DeepEqual(ips, want) {
		t.Errorf("ips = %v，期望 %v（应去重并保持首次出现顺序）", ips, want)
	}
	if want := []string{"bad.example.com", "empty.example.com"}; !reflect.DeepEqual(failed, want) {
		t.Errorf("failed = %v，期望 %v", failed, want)
	}
}

func TestResolveHostsRejectsNilResolver(t *testing.T) {
	if _, _, err := ResolveHosts(context.Background(), nil, []string{"a.com"}); err == nil {
		t.Error("解析器为空时应返回错误")
	}
}

func TestResolveHostsStopsWhenContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	resolver := &fakeResolver{results: map[string][]net.IPAddr{}}
	if _, _, err := ResolveHosts(ctx, resolver, []string{"a.com"}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v，期望 context.Canceled", err)
	}
	if len(resolver.calls) != 0 {
		t.Errorf("取消后不应再查 DNS，实际查询 %v", resolver.calls)
	}
}

func TestDefaultResolverIsUsable(t *testing.T) {
	if DefaultResolver() == nil {
		t.Error("DefaultResolver 不应为 nil")
	}
}

// 解析被取消时返回错误，而不是把域名记进「解析失败」。
//
// 记成失败会让界面把它显示成「域名有问题」，而用户只是点了停止。
func TestResolveHostsReportsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// 解析器原样回传 ctx 的错误，模拟「查询过程中被取消」。
	resolver := &fakeResolver{err: context.Canceled}
	_, _, err := ResolveHosts(ctx, resolver, []string{"edge.example.com"})
	if err == nil {
		t.Fatal("取消后应当返回错误，而不是把域名记成解析失败")
	}
}

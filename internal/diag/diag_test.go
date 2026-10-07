package diag

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"cloudtrace/internal/config"
)

// 固定时钟：诊断结果里带时间戳，用例不该依赖真实时间。
var fixedNow = time.Date(2026, 10, 6, 15, 4, 5, 0, time.Local)

// fakeOptions 造一组可控的检查实现。
//
// 四项检查全部注入：真机上的网络状态没法摆布，「DNS 解不出但 TCP 能通」这种
// 中间态更是撞不上，而它恰恰是最需要被正确解读的一种。
type fakeOptions struct {
	resolveErr error
	resolveFor time.Duration
	addrs      []string

	dialErr error
	dialFor time.Duration

	traceBody string
	traceErr  error
}

func (f fakeOptions) options() Options {
	return Options{
		Now: func() time.Time { return fixedNow },
		Resolve: func(context.Context, string) ([]string, error) {
			if f.resolveErr != nil {
				return nil, f.resolveErr
			}
			return f.addrs, nil
		},
		Dial: func(context.Context, string) error { return f.dialErr },
		FetchTrace: func(context.Context) (string, error) {
			if f.traceErr != nil {
				return "", f.traceErr
			}
			return f.traceBody, nil
		},
	}
}

const goodTrace = "fl=1a2b3c\nip=203.0.113.7\nts=1700000000.000\nloc=CN\ncolo=LAX\n"

func TestRunAllGreen(t *testing.T) {
	report := Run(context.Background(), fakeOptions{
		addrs:     []string{"104.16.132.229"},
		traceBody: goodTrace,
	}.options())

	if report.Status != StatusOK {
		t.Fatalf("总体结论 = %q，期望 ok；items=%+v", report.Status, report.Items)
	}
	if len(report.Items) != 4 {
		t.Fatalf("检查项数 = %d，期望 4", len(report.Items))
	}
	for _, item := range report.Items {
		if item.Status != StatusOK {
			t.Errorf("%s 的状态 = %q，期望 ok", item.Key, item.Status)
		}
		// 全绿时不该给建议：没人想在一切正常的时候读一段怎么修。
		if item.Advice != "" {
			t.Errorf("%s 在 ok 状态下给了建议：%q", item.Key, item.Advice)
		}
	}
}

// DNS 解不出时必须判 bad，并且给出「换 DNS / 关代理」这种能直接照做的建议。
func TestRunDNSFailureIsBad(t *testing.T) {
	report := Run(context.Background(), fakeOptions{
		resolveErr: errors.New("no such host"),
		traceBody:  goodTrace,
	}.options())

	item := findItem(t, report, KeyDNS)
	if item.Status != StatusBad {
		t.Errorf("DNS 状态 = %q，期望 bad", item.Status)
	}
	if item.Advice == "" {
		t.Error("DNS 失败时应当给建议")
	}
	if report.Status != StatusBad {
		t.Errorf("总体结论 = %q，期望 bad（取最差）", report.Status)
	}
}

// 解析得到空结果也判 bad：DNS 服务器回了应答，但没给出地址，扫描照样没结果。
func TestRunEmptyDNSAnswerIsBad(t *testing.T) {
	report := Run(context.Background(), fakeOptions{
		addrs:     nil,
		traceBody: goodTrace,
	}.options())

	item := findItem(t, report, KeyDNS)
	if item.Status != StatusBad {
		t.Errorf("DNS 状态 = %q，期望 bad", item.Status)
	}
	if item.Advice == "" {
		t.Error("空应答时应当给建议")
	}
}

func TestRunTCPFailureIsBad(t *testing.T) {
	report := Run(context.Background(), fakeOptions{
		addrs:     []string{"104.16.132.229"},
		dialErr:   errors.New("i/o timeout"),
		traceBody: goodTrace,
	}.options())

	item := findItem(t, report, KeyTCP)
	if item.Status != StatusBad {
		t.Errorf("TCP 状态 = %q，期望 bad", item.Status)
	}
}

func TestRunTraceUnreachableIsBad(t *testing.T) {
	report := Run(context.Background(), fakeOptions{
		addrs:    []string{"104.16.132.229"},
		traceErr: errors.New("connection reset"),
	}.options())

	item := findItem(t, report, KeyTrace)
	if item.Status != StatusBad {
		t.Errorf("trace 状态 = %q，期望 bad", item.Status)
	}
}

// 有响应但没有 loc：回的不是 Cloudflare 的 trace，判 warn 而不是 ok。
//
// 这是「被中间设备改写」的典型形态——TCP 通、HTTP 也有响应，只有内容不对。
func TestRunTraceWithoutLocIsWarn(t *testing.T) {
	report := Run(context.Background(), fakeOptions{
		addrs:     []string{"104.16.132.229"},
		traceBody: "<html>hello</html>\n",
	}.options())

	if item := findItem(t, report, KeyTrace); item.Status != StatusWarn {
		t.Errorf("trace 状态 = %q，期望 warn", item.Status)
	}
	if item := findItem(t, report, KeyEgress); item.Status != StatusWarn {
		t.Errorf("拿不到出口时 egress 状态 = %q，期望 warn", item.Status)
	}
}

// 出口国家与扫描时那条横幅用同一套判定：CN 不提示，JP 提示。
func TestEgressFollowsProxyWarningRule(t *testing.T) {
	cases := []struct {
		loc    string
		status Status
	}{
		{"CN", StatusOK},
		{"JP", StatusWarn},
		{"US", StatusWarn},
		{"XX", StatusOK},
		{"T1", StatusOK},
		{"", StatusWarn},
	}
	for _, tc := range cases {
		if got := checkEgress(tc.loc).Status; got != tc.status {
			t.Errorf("loc=%q 时状态 = %q，期望 %q", tc.loc, got, tc.status)
		}
	}
}

// 前面失败了也要把后面跑完：一张全红的表比一条错误信息更能说明问题在哪。
func TestRunKeepsGoingAfterFailure(t *testing.T) {
	report := Run(context.Background(), fakeOptions{
		resolveErr: errors.New("no such host"),
		dialErr:    errors.New("i/o timeout"),
		traceErr:   errors.New("connection reset"),
	}.options())

	if len(report.Items) != 4 {
		t.Fatalf("检查项数 = %d，期望 4（失败也要跑完）", len(report.Items))
	}
}

// ---- 诊断包 ----

func TestBundleRedactsSecrets(t *testing.T) {
	cfg := config.Default()
	cfg.Server.Token = "super-secret-token"
	cfg.Net.Proxy = "http://user:pass@proxy.local:8080"
	cfg.Source.RemoteURLs = []config.RemoteSource{
		{URL: "https://example.com/list.txt?token=abc123", Enabled: true},
	}

	bundle := Bundle(cfg, "", "v1.2.3", nil, fixedNow)

	if strings.Contains(bundle, "super-secret-token") {
		t.Error("诊断包里出现了访问 Token")
	}
	if strings.Contains(bundle, "pass@") || strings.Contains(bundle, "user:") {
		t.Error("诊断包里出现了代理凭据")
	}
	if strings.Contains(bundle, "abc123") {
		t.Error("诊断包里出现了远端源的查询串")
	}
	// 地址本身要留下：反馈问题时「用的是哪个源」是关键信息。
	if !strings.Contains(bundle, "example.com") {
		t.Error("脱敏把远端源地址整个抹掉了，应当只去掉查询串")
	}
	if !strings.Contains(bundle, "v1.2.3") {
		t.Error("诊断包缺少版本号")
	}
}

func TestBundleIncludesLogTail(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("建日志目录失败：%v", err)
	}
	name := "cloudtrace-" + fixedNow.Format("20060102") + ".log"
	lines := make([]string, 0, maxLogLines+50)
	for i := 0; i < maxLogLines+50; i++ {
		lines = append(lines, "line-"+strconv.Itoa(i))
	}
	if err := os.WriteFile(filepath.Join(logDir, name), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("写日志失败：%v", err)
	}

	bundle := Bundle(config.Default(), dir, "dev", nil, fixedNow)

	// 只带最后 maxLogLines 行。
	if strings.Contains(bundle, "line-0\n") {
		t.Error("诊断包带上了过老的日志行，应当只保留末尾若干行")
	}
	if !strings.Contains(bundle, "line-"+strconv.Itoa(maxLogLines+49)) {
		t.Error("诊断包缺少最后一行日志")
	}
}

func TestBundleWithoutReportSaysSo(t *testing.T) {
	bundle := Bundle(config.Default(), "", "dev", nil, fixedNow)
	if !strings.Contains(bundle, "本次没有跑诊断") {
		t.Error("没跑过诊断时应当在包里说明，而不是留一段空白")
	}
}

func TestBundleNameIsSortable(t *testing.T) {
	name := BundleName(fixedNow)
	if name != "cloudtrace-diag-20261006-150405.txt" {
		t.Errorf("诊断包文件名 = %q", name)
	}
}

func findItem(t *testing.T, report Report, key string) Item {
	t.Helper()
	for _, item := range report.Items {
		if item.Key == key {
			return item
		}
	}
	t.Fatalf("报告里没有 %q 这一项", key)
	return Item{}
}

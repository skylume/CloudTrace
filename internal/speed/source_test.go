package speed

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock 是可控的取时函数。
type fakeClock struct {
	now time.Time
}

func (c *fakeClock) get() time.Time          { return c.now }
func (c *fakeClock) advance(d time.Duration) { c.now = c.now.Add(d) }

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(1700000000, 0)}
}

// countingProbe 记录被调用次数，并按固定结果作答。
type countingProbe struct {
	calls int64
	info  ISPInfo
	err   error
}

func (p *countingProbe) probe(context.Context) (ISPInfo, error) {
	atomic.AddInt64(&p.calls, 1)
	return p.info, p.err
}

func (p *countingProbe) count() int { return int(atomic.LoadInt64(&p.calls)) }

func TestResolveFixedModes(t *testing.T) {
	tests := []struct {
		mode   string
		custom string
		want   string
	}{
		{URLModeOfficial, "", officialSpeedURL},
		{URLModeMobileFriendly, "", mobileFriendlySpeedURL},
		{URLModeMobileOnly, "", mobileOnlySpeedURL},
		{URLModeCustom, "https://example.com/down", "https://example.com/down"},
		{URLModeCustom, "  example.com/down  ", "example.com/down"},
	}
	for _, tc := range tests {
		r := NewSourceResolver((&countingProbe{}).probe, newFakeClock().get, time.Minute)
		got, err := r.Resolve(context.Background(), tc.mode, tc.custom)
		if err != nil {
			t.Fatalf("%s 解析失败：%v", tc.mode, err)
		}
		if got != tc.want {
			t.Errorf("%s 解析结果 = %q，期望 %q", tc.mode, got, tc.want)
		}
	}
}

// 选了自定义源却没填地址是配置错误，必须在启动前拦住。
func TestResolveCustomWithoutURLFails(t *testing.T) {
	r := NewSourceResolver((&countingProbe{}).probe, newFakeClock().get, time.Minute)
	if _, err := r.Resolve(context.Background(), URLModeCustom, "   "); err == nil {
		t.Fatal("空的自定义地址应当报错")
	}
}

// 出口探测失败不能阻断测速：拿不到 ISP 只影响「要不要换移动源」。
func TestResolveAutoFallsBackToOfficialOnProbeFailure(t *testing.T) {
	probe := &countingProbe{err: errors.New("网络不可达")}
	r := NewSourceResolver(probe.probe, newFakeClock().get, time.Minute)

	got, err := r.Resolve(context.Background(), URLModeAuto, "")
	if err != nil {
		t.Fatalf("探测失败不应返回错误：%v", err)
	}
	if got != officialSpeedURL {
		t.Errorf("回退结果 = %q，期望官方源", got)
	}
}

func TestResolveAutoPicksMobileSourceForChinaMobile(t *testing.T) {
	cases := []struct {
		name string
		info ISPInfo
	}{
		{"按 AS 号识别", ISPInfo{ASN: 9808, Org: "Some Other Name"}},
		{"按组织名识别", ISPInfo{ASN: 99999, Org: "China Mobile Communications Group"}},
		{"按中文关键词识别", ISPInfo{ASN: 99999, Org: "中国移动"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probe := &countingProbe{info: tc.info}
			r := NewSourceResolver(probe.probe, newFakeClock().get, time.Minute)
			// 固定随机选择，让断言可复现。
			r.pick = func(urls []string) string { return urls[0] }

			got, err := r.Resolve(context.Background(), URLModeAuto, "")
			if err != nil {
				t.Fatalf("解析失败：%v", err)
			}
			if got != mobileFriendlySpeedURL {
				t.Errorf("移动宽带应选移动源，实际 %q", got)
			}
		})
	}
}

func TestResolveAutoKeepsOfficialForOtherISP(t *testing.T) {
	probe := &countingProbe{info: ISPInfo{ASN: 4134, Org: "Chinanet"}}
	r := NewSourceResolver(probe.probe, newFakeClock().get, time.Minute)

	got, err := r.Resolve(context.Background(), URLModeAuto, "")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if got != officialSpeedURL {
		t.Errorf("非移动宽带应用官方源，实际 %q", got)
	}
}

// 每测一个目标探一次既慢又容易被对方限流，所以结果要在 TTL 内复用。
func TestResolveAutoCachesWithinTTL(t *testing.T) {
	clock := newFakeClock()
	probe := &countingProbe{info: ISPInfo{ASN: 4134, Org: "Chinanet"}}
	r := NewSourceResolver(probe.probe, clock.get, 10*time.Minute)

	for i := 0; i < 5; i++ {
		if _, err := r.Resolve(context.Background(), URLModeAuto, ""); err != nil {
			t.Fatalf("解析失败：%v", err)
		}
	}
	if probe.count() != 1 {
		t.Errorf("探测次数 = %d，期望缓存命中只探 1 次", probe.count())
	}

	clock.advance(9 * time.Minute)
	if _, err := r.Resolve(context.Background(), URLModeAuto, ""); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if probe.count() != 1 {
		t.Errorf("TTL 内探测次数 = %d，期望仍为 1", probe.count())
	}
}

func TestResolveAutoRefreshesAfterTTL(t *testing.T) {
	clock := newFakeClock()
	probe := &countingProbe{info: ISPInfo{ASN: 4134, Org: "Chinanet"}}
	r := NewSourceResolver(probe.probe, clock.get, 10*time.Minute)

	if _, err := r.Resolve(context.Background(), URLModeAuto, ""); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	clock.advance(11 * time.Minute)
	if _, err := r.Resolve(context.Background(), URLModeAuto, ""); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if probe.count() != 2 {
		t.Errorf("探测次数 = %d，期望 TTL 到期后重探一次", probe.count())
	}
}

// TTL <= 0 回落到默认值，而不是变成「每次都重探」。
func TestNewSourceResolverDefaultsTTL(t *testing.T) {
	r := NewSourceResolver((&countingProbe{}).probe, nil, 0)
	if r.ttl != DefaultSourceTTL {
		t.Errorf("TTL = %v，期望 %v", r.ttl, DefaultSourceTTL)
	}
	if r.now == nil || r.pick == nil {
		t.Error("未注入的依赖应当补上默认实现")
	}
}

// 无法识别的模式按自动选源处理：配置里写错一个词不该让整轮测速失败。
func TestResolveUnknownModeUsesAuto(t *testing.T) {
	probe := &countingProbe{info: ISPInfo{ASN: 4134, Org: "Chinanet"}}
	r := NewSourceResolver(probe.probe, newFakeClock().get, time.Minute)

	got, err := r.Resolve(context.Background(), "definitely-not-a-mode", "")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if got != officialSpeedURL {
		t.Errorf("结果 = %q，期望走自动选源拿到官方源", got)
	}
	if probe.count() != 1 {
		t.Errorf("探测次数 = %d，期望走了一次出口探测", probe.count())
	}
}

func TestIsChinaMobile(t *testing.T) {
	tests := []struct {
		name string
		info ISPInfo
		want bool
	}{
		{"AS 号命中", ISPInfo{ASN: 56040}, true},
		{"组织名命中 cmnet", ISPInfo{Org: "CMNET-GD"}, true},
		{"组织名大小写不敏感", ISPInfo{Org: "ChinaMobile"}, true},
		{"中文命中", ISPInfo{Org: "中国移动通信"}, true},
		{"电信不算移动", ISPInfo{ASN: 4134, Org: "Chinanet"}, false},
		{"空信息", ISPInfo{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isChinaMobile(tc.info); got != tc.want {
				t.Errorf("isChinaMobile(%+v) = %v，期望 %v", tc.info, got, tc.want)
			}
		})
	}
}

func TestPickRandomReturnsOneOfInputs(t *testing.T) {
	urls := []string{"a", "b", "c"}
	for i := 0; i < 50; i++ {
		got := pickRandom(urls)
		if got != "a" && got != "b" && got != "c" {
			t.Fatalf("挑出了列表外的值：%q", got)
		}
	}
	if got := pickRandom(nil); got != officialSpeedURL {
		t.Errorf("空列表应回退官方源，实际 %q", got)
	}
}

func TestCheckSpeedURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		ok   bool
	}{
		{"带协议", "https://speed.cloudflare.com/__down", true},
		{"不带协议", "speed.cloudflare.com/__down", true},
		{"带端口", "speed.cloudflare.com:8443/__down", true},
		{"空地址", "   ", false},
		{"缺主机名", "https:///__down", false},
		{"协议不支持", "ftp://example.com/x", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSpeedURL(tc.raw)
			if tc.ok && err != nil {
				t.Errorf("应当通过校验，实际报错：%v", err)
			}
			if !tc.ok && err == nil {
				t.Error("应当报错但通过了")
			}
		})
	}
}

// 选源决策要留下可查的记录，回退尤其需要——用户看到速度不对时会先怀疑
// 用错了源。
func TestResolveLogsDecision(t *testing.T) {
	var lines []string
	r := NewSourceResolver((&countingProbe{err: errors.New("boom")}).probe, newFakeClock().get, time.Minute)
	r.SetLogger(func(format string, args ...any) {
		lines = append(lines, format)
	})

	if _, err := r.Resolve(context.Background(), URLModeAuto, ""); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "回退官方测速源") {
		t.Errorf("日志 = %v，期望记录一次回退", lines)
	}
}

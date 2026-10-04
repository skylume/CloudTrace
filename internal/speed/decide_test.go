package speed

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// 固定模式的选源：用户指定了什么就用什么，理由都是「你指定的源」。
func TestDecidePinnedModes(t *testing.T) {
	cases := []struct {
		mode   string
		custom string
		want   string
	}{
		{URLModeOfficial, "", officialSpeedURL},
		{URLModeMobileFriendly, "", mobileFriendlySpeedURL},
		{URLModeMobileOnly, "", mobileOnlySpeedURL},
		{URLModeCustom, "https://example.com/x.bin", "https://example.com/x.bin"},
	}

	for _, c := range cases {
		r := NewSourceResolver(nil, nil, DefaultSourceTTL)
		decision, err := r.Decide(context.Background(), c.mode, c.custom)
		if err != nil {
			t.Fatalf("%s 选源失败：%v", c.mode, err)
		}
		if decision.URL != c.want {
			t.Errorf("%s 选中 %q，期望 %q", c.mode, decision.URL, c.want)
		}
		if decision.Code != ReasonPinned {
			t.Errorf("%s 的理由 = %q，期望 %q", c.mode, decision.Code, ReasonPinned)
		}
	}
}

// 选了自定义源却没给地址要报错，而不是回退到官方源悄悄测一遍。
func TestDecideCustomWithoutURLFails(t *testing.T) {
	r := NewSourceResolver(nil, nil, DefaultSourceTTL)

	if _, err := r.Decide(context.Background(), URLModeCustom, "   "); err == nil {
		t.Fatal("自定义源地址为空时应当报错")
	}
}

// 自动选源要把「为什么」说清楚——这是这个功能存在的全部意义。
func TestDecideAutoReasons(t *testing.T) {
	cases := []struct {
		name     string
		info     ISPInfo
		err      error
		wantCode string
		wantURL  string
	}{
		{
			name:     "出口是中国移动",
			info:     ISPInfo{IP: "100.64.0.1", ASN: 9808, Org: "CHINA MOBILE"},
			wantCode: ReasonMobile,
			wantURL:  mobileFriendlySpeedURL,
		},
		{
			name:     "出口不是中国移动",
			info:     ISPInfo{IP: "100.64.0.1", ASN: 4134, Org: "CHINANET"},
			wantCode: ReasonNotMobile,
			wantURL:  officialSpeedURL,
		},
		{
			name:     "探测失败",
			err:      errors.New("网络不通"),
			wantCode: ReasonProbeFailed,
			wantURL:  officialSpeedURL,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			probe := &countingProbe{info: c.info, err: c.err}
			r := NewSourceResolver(probe.probe, newFakeClock().get, DefaultSourceTTL)

			decision, err := r.Decide(context.Background(), URLModeAuto, "")
			if err != nil {
				t.Fatalf("选源失败：%v", err)
			}
			if decision.Code != c.wantCode {
				t.Errorf("理由 = %q，期望 %q", decision.Code, c.wantCode)
			}
			if c.wantURL == mobileFriendlySpeedURL {
				// 移动源在友好与专属之间随机挑一个，两个都算对。
				if decision.URL != mobileFriendlySpeedURL && decision.URL != mobileOnlySpeedURL {
					t.Errorf("选中 %q，期望某个移动源", decision.URL)
				}
			} else if decision.URL != c.wantURL {
				t.Errorf("选中 %q，期望 %q", decision.URL, c.wantURL)
			}
		})
	}
}

// 出口是移动时带上 AS 信息：只说「用了移动源」不够，用户想知道依据是什么。
func TestDecideAutoMobileCarriesISPDetail(t *testing.T) {
	probe := &countingProbe{info: ISPInfo{IP: "100.64.0.1", ASN: 9808, Org: "CHINA MOBILE"}}
	r := NewSourceResolver(probe.probe, newFakeClock().get, DefaultSourceTTL)

	decision, err := r.Decide(context.Background(), URLModeAuto, "")
	if err != nil {
		t.Fatalf("选源失败：%v", err)
	}
	if !strings.Contains(decision.Detail, "9808") {
		t.Errorf("补充说明 = %q，期望包含 AS 号", decision.Detail)
	}
}

/**
 * TTL 内复用上一次的判断，且**理由原样保留**。
 *
 * 缓存里存的是整份判定而不是选出的地址：只存地址的话，第二次说不出「为什么
 * 选它」，而那个判断同时是自适应降并发的唯一信号来源——缓存一命中，信号就
 * 丢了。
 */
func TestDecideAutoSecondCallIsCached(t *testing.T) {
	probe := &countingProbe{info: ISPInfo{IP: "100.64.0.1", ASN: 9808, Org: "CHINA MOBILE"}}
	r := NewSourceResolver(probe.probe, newFakeClock().get, DefaultSourceTTL)

	first, _ := r.Decide(context.Background(), URLModeAuto, "")
	if first.Code != ReasonMobile {
		t.Fatalf("首次理由 = %q，期望 %q", first.Code, ReasonMobile)
	}

	second, _ := r.Decide(context.Background(), URLModeAuto, "")
	if second.Code != ReasonMobile {
		t.Errorf("第二次理由 = %q，期望沿用 %q", second.Code, ReasonMobile)
	}
	if second.URL != first.URL {
		t.Errorf("第二次选中 %q，与首次 %q 不一致", second.URL, first.URL)
	}
	if probe.count() != 1 {
		t.Errorf("探测了 %d 次，期望只探一次", probe.count())
	}
}

// 探测没给 AS 信息时不要编一个「AS0」出来。
func TestDescribeISPSkipsEmptyInfo(t *testing.T) {
	if got := describeISP(ISPInfo{}); got != "" {
		t.Errorf("没有 AS 信息时 = %q，期望空串", got)
	}
	if got := describeISP(ISPInfo{ASN: 9808}); got != "AS9808" {
		t.Errorf("只有 AS 号时 = %q", got)
	}
	if got := describeISP(ISPInfo{Org: "CHINA MOBILE"}); got != "CHINA MOBILE" {
		t.Errorf("只有组织名时 = %q", got)
	}
}

/**
 * MobileExit 与选源共用同一份缓存。
 *
 * 「智能推荐」要按网络环境给建议，问的就是同一件事（出口是谁）。各探一次的话，
 * 用户每点一次按钮就发一轮探测请求。
 */
func TestMobileExitSharesCacheWithDecide(t *testing.T) {
	probe := &countingProbe{info: ISPInfo{IP: "100.64.0.1", ASN: 9808, Org: "CHINA MOBILE"}}
	r := NewSourceResolver(probe.probe, newFakeClock().get, DefaultSourceTTL)

	if !r.MobileExit(context.Background()) {
		t.Fatal("中国移动出口应判定为移动宽带")
	}
	if n := probe.count(); n != 1 {
		t.Fatalf("探测次数 = %d，期望 1", n)
	}

	// 选源与再问一次都该命中同一份缓存。
	if _, err := r.Decide(context.Background(), URLModeAuto, ""); err != nil {
		t.Fatalf("选源失败：%v", err)
	}
	_ = r.MobileExit(context.Background())
	if n := probe.count(); n != 1 {
		t.Errorf("又探测了一次，共 %d 次", n)
	}
}

// 非移动出口返回 false——也就是「没有可推荐的调整」。
func TestMobileExitFalseForOtherISPs(t *testing.T) {
	probe := &countingProbe{info: ISPInfo{IP: "1.1.1.1", ASN: 13335, Org: "CLOUDFLARENET"}}
	r := NewSourceResolver(probe.probe, newFakeClock().get, DefaultSourceTTL)

	if r.MobileExit(context.Background()) {
		t.Error("非移动出口不该判定为移动宽带")
	}
}

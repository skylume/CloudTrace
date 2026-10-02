package speed

import (
	"context"
	"net/netip"
	"testing"

	"cloudtrace/internal/geo"
)

// fixedASN 造一个只认某个地址的本地库。
func fixedASN(want string, info geo.ASNInfo) geo.LookupFunc {
	return func(addr netip.Addr) (geo.ASNInfo, bool) {
		if addr.String() != want {
			return geo.ASNInfo{}, false
		}
		return info, true
	}
}

// 出口探测接口没给 AS 信息时，本地库要能补上，判定中国移动才不会退化成
// 纯关键词匹配。
func TestResolveAutoUsesLocalASN(t *testing.T) {
	probe := &countingProbe{info: ISPInfo{IP: "100.64.0.1"}}
	r := NewSourceResolver(probe.probe, newFakeClock().get, DefaultSourceTTL)
	r.SetASNLookup(fixedASN("100.64.0.1", geo.ASNInfo{ASN: 9808, Org: "CHINA MOBILE"}))

	got, err := r.Resolve(context.Background(), URLModeAuto, "")
	if err != nil {
		t.Fatalf("选源失败：%v", err)
	}
	if got != mobileFriendlySpeedURL && got != mobileOnlySpeedURL {
		t.Fatalf("选到 %q，期望移动测速源", got)
	}
}

// 本地库给出的结论优先于探测接口：库每小时更新，且不依赖接口是否还返回
// 这两个字段。
func TestResolveAutoPrefersLocalASN(t *testing.T) {
	probe := &countingProbe{info: ISPInfo{IP: "100.64.0.1", ASN: 4134, Org: "CHINANET"}}
	r := NewSourceResolver(probe.probe, newFakeClock().get, DefaultSourceTTL)
	r.SetASNLookup(fixedASN("100.64.0.1", geo.ASNInfo{ASN: 9808, Org: "CHINA MOBILE"}))

	got, err := r.Resolve(context.Background(), URLModeAuto, "")
	if err != nil {
		t.Fatalf("选源失败：%v", err)
	}
	if got != mobileFriendlySpeedURL && got != mobileOnlySpeedURL {
		t.Fatalf("选到 %q，期望按本地库判定为移动", got)
	}
}

// 库不可用时回退到关键词判定：这条路径必须一直在。
func TestResolveAutoFallsBackToKeywordsWithoutASN(t *testing.T) {
	probe := &countingProbe{info: ISPInfo{IP: "100.64.0.1", Org: "CMNET-BJ"}}
	r := NewSourceResolver(probe.probe, newFakeClock().get, DefaultSourceTTL)

	got, err := r.Resolve(context.Background(), URLModeAuto, "")
	if err != nil {
		t.Fatalf("选源失败：%v", err)
	}
	if got != mobileFriendlySpeedURL && got != mobileOnlySpeedURL {
		t.Fatalf("没有本地库时应当靠关键词判定，实际选到 %q", got)
	}
}

// 硬编码 AS 表也要继续生效：这是最后一道回退。
func TestResolveAutoFallsBackToHardcodedASN(t *testing.T) {
	probe := &countingProbe{info: ISPInfo{ASN: 24400}}
	r := NewSourceResolver(probe.probe, newFakeClock().get, DefaultSourceTTL)

	got, err := r.Resolve(context.Background(), URLModeAuto, "")
	if err != nil {
		t.Fatalf("选源失败：%v", err)
	}
	if got != mobileFriendlySpeedURL && got != mobileOnlySpeedURL {
		t.Fatalf("硬编码 AS 表没有生效，实际选到 %q", got)
	}
}

func TestFillASN(t *testing.T) {
	lookup := fixedASN("100.64.0.1", geo.ASNInfo{ASN: 9808, Org: "CHINA MOBILE"})

	cases := []struct {
		name string
		in   ISPInfo
		asn  geo.LookupFunc
		want ISPInfo
	}{
		{
			name: "补全缺失的字段",
			in:   ISPInfo{IP: "100.64.0.1"},
			asn:  lookup,
			want: ISPInfo{IP: "100.64.0.1", ASN: 9808, Org: "CHINA MOBILE"},
		},
		{
			name: "库里有就覆盖接口给的",
			in:   ISPInfo{IP: "100.64.0.1", ASN: 4134, Org: "CHINANET"},
			asn:  lookup,
			want: ISPInfo{IP: "100.64.0.1", ASN: 9808, Org: "CHINA MOBILE"},
		},
		{
			name: "库里查不到时原样保留",
			in:   ISPInfo{IP: "203.0.113.1", ASN: 4134, Org: "CHINANET"},
			asn:  lookup,
			want: ISPInfo{IP: "203.0.113.1", ASN: 4134, Org: "CHINANET"},
		},
		{
			name: "接口没给出口地址时无从反查",
			in:   ISPInfo{ASN: 4134, Org: "CHINANET"},
			asn:  lookup,
			want: ISPInfo{ASN: 4134, Org: "CHINANET"},
		},
		{
			name: "地址不是合法 IP 时跳过",
			in:   ISPInfo{IP: "not-an-ip", ASN: 4134},
			asn:  lookup,
			want: ISPInfo{IP: "not-an-ip", ASN: 4134},
		},
		{
			name: "没有本地库时原样返回",
			in:   ISPInfo{IP: "100.64.0.1"},
			asn:  nil,
			want: ISPInfo{IP: "100.64.0.1"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := NewSourceResolver(nil, nil, DefaultSourceTTL)
			r.SetASNLookup(c.asn)
			if got := r.fillASN(c.in); got != c.want {
				t.Errorf("fillASN = %+v，期望 %+v", got, c.want)
			}
		})
	}
}

// 出口地址带空白也要能反查：上游给的字段格式不保证。
func TestFillASNTrimsAddress(t *testing.T) {
	r := NewSourceResolver(nil, nil, DefaultSourceTTL)
	r.SetASNLookup(fixedASN("100.64.0.1", geo.ASNInfo{ASN: 9808}))

	got := r.fillASN(ISPInfo{IP: " 100.64.0.1 "})
	if got.ASN != 9808 {
		t.Fatalf("带空白的地址没有反查成功：%+v", got)
	}
}

// 非中国移动的运营商不该被误判：本地库给了别的 AS 时要走官方源。
func TestResolveAutoKeepsOfficialForOtherISPWithLocalASN(t *testing.T) {
	probe := &countingProbe{info: ISPInfo{IP: "100.64.0.1"}}
	r := NewSourceResolver(probe.probe, newFakeClock().get, DefaultSourceTTL)
	r.SetASNLookup(fixedASN("100.64.0.1", geo.ASNInfo{ASN: 13335, Org: "CLOUDFLARENET"}))

	got, err := r.Resolve(context.Background(), URLModeAuto, "")
	if err != nil {
		t.Fatalf("选源失败：%v", err)
	}
	if got != officialSpeedURL {
		t.Fatalf("选到 %q，期望官方源", got)
	}
}

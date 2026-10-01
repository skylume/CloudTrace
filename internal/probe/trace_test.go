package probe

import (
	"testing"
)

const sampleTraceBody = "fl=123abc\n" +
	"h=www.cloudflare.com\n" +
	"ip=1.1.1.1\n" +
	"ts=1700000000.000\n" +
	"visit_scheme=https\n" +
	"uag=curl/8.0\n" +
	"colo=HKG\n" +
	"sliver=none\n" +
	"http=http/2\n" +
	"loc=HK\n" +
	"tls=TLSv1.3\n" +
	"sni=plaintext\n" +
	"warp=off\n" +
	"gateway=off\n"

func TestParseTrace(t *testing.T) {
	trace, err := ParseTrace(sampleTraceBody)
	if err != nil {
		t.Fatalf("ParseTrace 返回错误：%v", err)
	}

	want := map[string]string{
		"fl":           "123abc",
		"h":            "www.cloudflare.com",
		"ip":           "1.1.1.1",
		"colo":         "HKG",
		"loc":          "HK",
		"http":         "http/2",
		"warp":         "off",
		"gateway":      "off",
		"visit_scheme": "https",
	}
	for key, value := range want {
		if got := trace[key]; got != value {
			t.Errorf("trace[%q] = %q，期望 %q", key, got, value)
		}
	}
	if len(trace) != 14 {
		t.Errorf("解析到 %d 个字段，期望 14", len(trace))
	}
}

func TestParseTraceTolerance(t *testing.T) {
	tests := []struct {
		name string
		body string
		want map[string]string
	}{
		{
			name: "CRLF 换行",
			body: "colo=HKG\r\nloc=HK\r\n",
			want: map[string]string{"colo": "HKG", "loc": "HK"},
		},
		{
			name: "键统一转小写",
			body: "COLO=NRT\nLoc=JP\n",
			want: map[string]string{"colo": "NRT", "loc": "JP"},
		},
		{
			name: "值与键去掉首尾空白",
			body: "  colo = HKG  \n",
			want: map[string]string{"colo": "HKG"},
		},
		{
			name: "跳过空行与不含等号的行",
			body: "colo=HKG\n\n<html>\nloc=HK\n",
			want: map[string]string{"colo": "HKG", "loc": "HK"},
		},
		{
			name: "值里含等号时只切第一个",
			body: "uag=Mozilla/5.0 (a=b)\n",
			want: map[string]string{"uag": "Mozilla/5.0 (a=b)"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trace, err := ParseTrace(tt.body)
			if err != nil {
				t.Fatalf("ParseTrace 返回错误：%v", err)
			}
			if len(trace) != len(tt.want) {
				t.Fatalf("解析到 %d 个字段，期望 %d：%v", len(trace), len(tt.want), trace)
			}
			for key, value := range tt.want {
				if got := trace[key]; got != value {
					t.Errorf("trace[%q] = %q，期望 %q", key, got, value)
				}
			}
		})
	}
}

func TestParseTraceRejectsNonTraceBody(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "空响应体", body: ""},
		{name: "只有空白", body: "   \n\t\n"},
		{name: "HTML 错误页", body: "<html><body>502 Bad Gateway</body></html>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseTrace(tt.body); err == nil {
				t.Error("期望返回错误，实际为 nil")
			}
		})
	}
}

func TestExtractColoAndLoc(t *testing.T) {
	trace, err := ParseTrace(sampleTraceBody)
	if err != nil {
		t.Fatalf("ParseTrace 返回错误：%v", err)
	}
	if got := ExtractColo(trace); got != "HKG" {
		t.Errorf("ExtractColo = %q，期望 %q", got, "HKG")
	}
	if got := ExtractLoc(trace); got != "HK" {
		t.Errorf("ExtractLoc = %q，期望 %q", got, "HK")
	}

	// 小写值也要统一转成大写，否则黑白名单对不上。
	lower, err := ParseTrace("colo=hkg\nloc=hk\n")
	if err != nil {
		t.Fatalf("ParseTrace 返回错误：%v", err)
	}
	if got := ExtractColo(lower); got != "HKG" {
		t.Errorf("小写 colo 归一化 = %q，期望 %q", got, "HKG")
	}
	if got := ExtractLoc(lower); got != "HK" {
		t.Errorf("小写 loc 归一化 = %q，期望 %q", got, "HK")
	}
}

func TestExtractColoAndLocOnMissingKeys(t *testing.T) {
	trace := map[string]string{"ip": "1.1.1.1"}
	if got := ExtractColo(trace); got != "" {
		t.Errorf("ExtractColo = %q，期望空串", got)
	}
	if got := ExtractLoc(trace); got != "" {
		t.Errorf("ExtractLoc = %q，期望空串", got)
	}
	if got := ExtractColo(nil); got != "" {
		t.Errorf("nil 表 ExtractColo = %q，期望空串", got)
	}
}

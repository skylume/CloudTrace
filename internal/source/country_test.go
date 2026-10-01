package source

import "testing"

func TestNormalizeCountry(t *testing.T) {
	tests := []struct {
		name  string
		label string
		want  string
	}{
		{name: "两位代码", label: "US", want: "US"},
		{name: "两位代码小写", label: "us", want: "US"},
		{name: "三位代码", label: "USA", want: "US"},
		{name: "三位代码必须全大写", label: "hkg", want: ""},
		{name: "主机名标签 com 不是国家码", label: "com", want: ""},
		{name: "英文单词 can 不是国家码", label: "can", want: ""},
		{name: "全大写时 COM 才是科摩罗", label: "COM", want: "KM"},
		{name: "香港", label: "香港", want: "HK"},
		{name: "中国香港", label: "中国香港", want: "HK"},
		{name: "中国澳门", label: "中国澳门", want: "MO"},
		{name: "中国台湾", label: "中国台湾", want: "TW"},
		{name: "日本", label: "日本", want: "JP"},
		{name: "新加坡", label: "新加坡", want: "SG"},
		{name: "带括号的中文名", label: "刚果(布)", want: "CG"},
		{name: "全角括号的中文名", label: "刚果（金）", want: "CD"},
		{name: "emoji 国旗", label: "🇭🇰", want: "HK"},
		{name: "emoji 国旗加英文", label: "🇺🇸 United States", want: "US"},
		{name: "emoji 国旗加中文", label: "🇨🇳 中国", want: "CN"},
		{name: "序号前缀加中文", label: "1.香港", want: "HK"},
		{name: "代码加名字混排", label: "HK 香港", want: "HK"},
		{name: "代码带后缀", label: "HKG-1", want: "HK"},
		{name: "代码两侧有空白", label: "  HK  ", want: "HK"},
		{name: "三位代码优先于两位误判", label: "SGP", want: "SG"},

		{name: "空串", label: "", want: ""},
		{name: "纯空白", label: "   ", want: ""},
		{name: "主机名不能被当成国家码", label: "us1.example.com", want: ""},
		{name: "普通英文单词", label: "example", want: ""},
		{name: "非国家码的三字母", label: "NRT", want: ""},
		{name: "不带点的单词", label: "localhost", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeCountry(tt.label); got != tt.want {
				t.Errorf("NormalizeCountry(%q) = %q，期望 %q", tt.label, got, tt.want)
			}
		})
	}
}

func TestLookupInTableIsExact(t *testing.T) {
	tests := []struct {
		name  string
		table string
		key   string
		want  string
	}{
		{name: "命中前缀更短的名字", table: cnToCodeTable, key: "中国", want: "CN"},
		{name: "命中更长的名字", table: cnToCodeTable, key: "中国香港", want: "HK"},
		{name: "命中末尾条目", table: alpha3Table, key: "ZWE", want: "ZW"},
		{name: "命中首条目", table: alpha3Table, key: "AFG", want: "AF"},
		{name: "查不到", table: alpha3Table, key: "ZZZ", want: ""},
		{name: "空键", table: alpha3Table, key: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lookupInTable(tt.table, tt.key); got != tt.want {
				t.Errorf("lookupInTable(%q) = %q，期望 %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestIsAlpha2(t *testing.T) {
	tests := []struct {
		code string
		want bool
	}{
		{code: "US", want: true},
		{code: "HK", want: true},
		{code: "ZW", want: true},
		{code: "XX", want: false},
		{code: "ZZ", want: false},
		{code: "U", want: false},
		{code: "USA", want: false},
		{code: "", want: false},
	}

	for _, tt := range tests {
		if got := isAlpha2(tt.code); got != tt.want {
			t.Errorf("isAlpha2(%q) = %v，期望 %v", tt.code, got, tt.want)
		}
	}
}

func TestCountryFromFlag(t *testing.T) {
	tests := []struct {
		label string
		want  string
	}{
		{label: "🇭🇰", want: "HK"},
		{label: "🇯🇵", want: "JP"},
		{label: "前缀 🇸🇬", want: "SG"},
		{label: "没有国旗", want: ""},
		{label: "只有一个指示符 🇭", want: ""},
	}

	for _, tt := range tests {
		if got := countryFromFlag(tt.label); got != tt.want {
			t.Errorf("countryFromFlag(%q) = %q，期望 %q", tt.label, got, tt.want)
		}
	}
}

func TestChineseRuns(t *testing.T) {
	tests := []struct {
		token string
		want  []string
	}{
		{token: "香港", want: []string{"香港"}},
		{token: "1.香港", want: []string{"香港"}},
		{token: "香港HK", want: []string{"香港"}},
		{token: "刚果(布)", want: []string{"刚果(布)"}},
		{token: "HK", want: nil},
		{token: "", want: nil},
	}

	for _, tt := range tests {
		got := chineseRuns(tt.token)
		if len(got) != len(tt.want) {
			t.Fatalf("chineseRuns(%q) = %v，期望 %v", tt.token, got, tt.want)
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("chineseRuns(%q)[%d] = %q，期望 %q", tt.token, i, got[i], tt.want[i])
			}
		}
	}
}

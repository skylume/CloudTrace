package geo

import "testing"

// 常见数据中心必须都能翻译出来，否则结果表里的「地区」列对用户没有意义。
func TestRegionNameCoversCommonColos(t *testing.T) {
	cases := map[string]string{
		"HKG": "香港",
		"NRT": "东京",
		"LAX": "洛杉矶",
		"SIN": "新加坡",
		"FRA": "法兰克福",
		"SJC": "圣何塞",
		"GRU": "圣保罗",
		"BRU": "布鲁塞尔",
	}
	for code, want := range cases {
		if got := RegionName(code); got != want {
			t.Errorf("RegionName(%q) = %q，期望 %q", code, got, want)
		}
	}
}

// 表里至少有 80 个数据中心，这是「常见 colo 都覆盖到」的下限。
func TestAirportTableIsLargeEnough(t *testing.T) {
	entries := 0
	for i := 0; i < len(airportTable); i++ {
		if airportTable[i] == '=' {
			entries++
		}
	}
	if entries < 80 {
		t.Fatalf("数据中心表只有 %d 条，至少要 80 条", entries)
	}
}

// 认不出来时原样返回代码：显示代码比显示空白有用，用户至少能拿它去搜。
func TestRegionNameFallsBackToCode(t *testing.T) {
	if got := RegionName("XYZ"); got != "XYZ" {
		t.Errorf("RegionName(%q) = %q，期望原样返回", "XYZ", got)
	}
	if got := RegionName(""); got != "" {
		t.Errorf("空代码应当返回空串，实际 %q", got)
	}
}

// 代码大小写与空白都不敏感：上游给的写法并不统一。
func TestRegionNameNormalizesInput(t *testing.T) {
	for _, in := range []string{"hkg", " HKG ", "Hkg"} {
		if got := RegionName(in); got != "香港" {
			t.Errorf("RegionName(%q) = %q，期望 香港", in, got)
		}
	}
}

func TestAirportNameDistinguishesUnknown(t *testing.T) {
	// 与 RegionName 的区别就在这里：未知时给空串而不是代码。
	if got := AirportName("XYZ"); got != "" {
		t.Errorf("AirportName(%q) = %q，期望空串", "XYZ", got)
	}
	if got := AirportName("hkg"); got != "香港" {
		t.Errorf("AirportName(%q) = %q，期望 香港", "hkg", got)
	}
}

func TestCountryFromColo(t *testing.T) {
	cases := map[string]string{
		"HKG": "HK",
		"TPE": "TW",
		"MFM": "MO",
		"NRT": "JP",
		"LAX": "US",
		"SJC": "US",
		"YYZ": "CA",
		"LHR": "GB",
		"FRA": "DE",
		"SYD": "AU",
		"XYZ": "",
		"":    "",
	}
	for code, want := range cases {
		if got := CountryFromColo(code); got != want {
			t.Errorf("CountryFromColo(%q) = %q，期望 %q", code, got, want)
		}
	}
}

// 中文地名到国家码的表不能有「指向不存在的名字」的条目：
// 表里写了 布鲁塞尔=BE 却没有 BRU，就会有一条永远查不到的国家映射。
func TestNameToCountryEntriesAreReachable(t *testing.T) {
	names := map[string]bool{}
	for i := 0; i < len(airportTable); i++ {
		if airportTable[i] != '=' {
			continue
		}
		rest := airportTable[i+1:]
		end := 0
		for end < len(rest) && rest[end] != ' ' {
			end++
		}
		names[rest[:end]] = true
	}

	for i := 0; i < len(nameToCountryTable); i++ {
		if nameToCountryTable[i] != '=' {
			continue
		}
		// 回退到地名起点：向前找空格。
		start := i - 1
		for start >= 0 && nameToCountryTable[start] != ' ' {
			start--
		}
		name := nameToCountryTable[start+1 : i]
		if !names[name] {
			t.Errorf("国家映射里的地名 %q 在数据中心表里没有任何代码对应，这条映射永远用不上", name)
		}
	}
}

func TestLookupTableMatchesWholeSegment(t *testing.T) {
	table := " AAA=1 BAAA=2 "
	if got := lookupTable(table, "AAA"); got != "1" {
		t.Errorf("lookupTable 匹配到了错误的一段：%q", got)
	}
	if got := lookupTable(table, "AA"); got != "" {
		t.Errorf("不完整的键不该匹配上：%q", got)
	}
	if got := lookupTable(table, "BAAA"); got != "2" {
		t.Errorf("lookupTable(BAAA) = %q，期望 2", got)
	}
	if got := lookupTable(table, ""); got != "" {
		t.Errorf("空键应当返回空串，实际 %q", got)
	}
}

// 表尾没有收尾空格时也要能取到最后一个值。
func TestLookupTableHandlesLastEntry(t *testing.T) {
	if got := lookupTable(" AAA=1 BBB=2", "BBB"); got != "2" {
		t.Errorf("lookupTable 取表尾条目 = %q，期望 2", got)
	}
}

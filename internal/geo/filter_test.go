package geo

import (
	"testing"

	"cloudtrace/internal/model"
)

// ---------------------------------------------------------------------------

func TestMatchASNEmptyFilterMatchesAll(t *testing.T) {
	rec := model.IPRecord{IP: "1.1.1.1"}
	if !MatchASN(rec, nil) {
		t.Error("没有过滤条件时不该筛掉任何东西")
	}
	if !MatchASN(rec, []string{"", "  "}) {
		t.Error("只有空白的过滤条件等同于没有条件")
	}
}

func TestMatchASNByNumber(t *testing.T) {
	rec := model.IPRecord{IP: "1.1.1.1", ASN: 9808, ASOrg: "CHINA MOBILE"}
	for _, token := range []string{"9808", "AS9808", "as9808", " 9808 "} {
		if !MatchASN(rec, []string{token}) {
			t.Errorf("条件 %q 应当命中 AS9808", token)
		}
	}
	if MatchASN(rec, []string{"4134"}) {
		t.Error("不匹配的 AS 号不该命中")
	}
}

func TestMatchASNByKeyword(t *testing.T) {
	rec := model.IPRecord{IP: "1.1.1.1", ASN: 9808, ASOrg: "CHINA MOBILE COMMUNICATIONS"}
	for _, token := range []string{"china mobile", "CHINA MOBILE", "mobile", "移动"} {
		if token == "移动" {
			continue
		}
		if !MatchASN(rec, []string{token}) {
			t.Errorf("关键词 %q 应当命中", token)
		}
	}
	// 中文关键词要能匹配中文组织名。
	cn := model.IPRecord{IP: "2.2.2.2", ASN: 9808, ASOrg: "中国移动通信"}
	if !MatchASN(cn, []string{"移动"}) {
		t.Error("中文关键词没有命中中文组织名")
	}
	if MatchASN(rec, []string{"telecom"}) {
		t.Error("不匹配的关键词不该命中")
	}
}

// 组织名里出现数字时不能被误判成 AS 号。
func TestMatchASNTreatsMixedTokenAsKeyword(t *testing.T) {
	rec := model.IPRecord{IP: "1.1.1.1", ASN: 9808, ASOrg: "CHINA NET 3"}
	if !MatchASN(rec, []string{"net 3"}) {
		t.Error("含数字但不是纯 AS 号的条件应当按关键词匹配")
	}
}

// 记录没有 ASN 信息时（库不可用或查不到），只有空条件才命中。
//
// 拿一个空的组织名去匹配关键词会得到假阳性，而过滤层宁可少筛也不能多筛：
// 筛掉一个可用节点，用户是察觉不到的。
func TestMatchASNWithoutASNInfo(t *testing.T) {
	rec := model.IPRecord{IP: "1.1.1.1"}
	if !MatchASN(rec, nil) {
		t.Error("空条件应当命中")
	}
	if MatchASN(rec, []string{"9808"}) {
		t.Error("没有 ASN 信息时按 AS 号过滤不该命中")
	}
	if MatchASN(rec, []string{"china"}) {
		t.Error("没有组织名时按关键词过滤不该命中")
	}
}

func TestFilterByASN(t *testing.T) {
	records := []model.IPRecord{
		{IP: "1.1.1.1", ASN: 9808, ASOrg: "CHINA MOBILE"},
		{IP: "2.2.2.2", ASN: 4134, ASOrg: "CHINANET"},
		{IP: "3.3.3.3", ASN: 13335, ASOrg: "CLOUDFLARENET"},
	}

	if got := FilterByASN(records, nil); len(got) != 3 {
		t.Fatalf("空条件筛出 %d 条，期望原样返回 3 条", len(got))
	}

	got := FilterByASN(records, QuickFilter("china_mobile"))
	if len(got) != 1 || got[0].IP != "1.1.1.1" {
		t.Fatalf("仅中国移动筛出 %+v", got)
	}
	got = FilterByASN(records, QuickFilter("china_telecom"))
	if len(got) != 1 || got[0].IP != "2.2.2.2" {
		t.Fatalf("仅中国电信筛出 %+v", got)
	}
	if got := FilterByASN(records, QuickFilter("china_unicom")); len(got) != 0 {
		t.Fatalf("没有联通的节点却筛出了 %+v", got)
	}
}

func TestQuickFilter(t *testing.T) {
	names := QuickFilterNames()
	if len(names) != 3 {
		t.Fatalf("快捷选项数 = %d，期望 3", len(names))
	}
	for _, name := range names {
		tokens := QuickFilter(name)
		if len(tokens) == 0 {
			t.Errorf("快捷选项 %s 没有任何匹配词", name)
		}
		// 返回的是副本：外部改动不能影响内置表。
		tokens[0] = "mutated"
		if QuickFilter(name)[0] == "mutated" {
			t.Errorf("快捷选项 %s 返回了共享切片", name)
		}
	}
	if got := QuickFilter("nope"); got != nil {
		t.Errorf("未知选项应当返回 nil，实际 %v", got)
	}
	// 名字大小写不敏感。
	if len(QuickFilter("CHINA_MOBILE")) == 0 {
		t.Error("快捷选项名应当大小写不敏感")
	}
}

func TestParseASNToken(t *testing.T) {
	cases := []struct {
		in   string
		want uint32
		ok   bool
	}{
		{"9808", 9808, true},
		{"AS9808", 9808, true},
		{"as13335", 13335, true},
		{" 4134 ", 4134, true},
		{"0", 0, false},
		{"AS0", 0, false},
		{"", 0, false},
		{"AS", 0, false},
		{"china", 0, false},
		{"net 3", 0, false},
		{"9808x", 0, false},
	}
	for _, c := range cases {
		got, ok := parseASNToken(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("parseASNToken(%q) = (%d, %v)，期望 (%d, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

// 过滤不参与前置过滤：这里只验证「筛出来的仍是原记录」，没有别的副作用。
func TestFilterByASNKeepsOriginalRecords(t *testing.T) {
	records := []model.IPRecord{{IP: "1.1.1.1", ASN: 9808, ASOrg: "CHINA MOBILE", Latency: 42}}
	got := FilterByASN(records, []string{"9808"})
	if len(got) != 1 || got[0].Latency != 42 {
		t.Fatalf("筛出的记录被改动了：%+v", got)
	}
}

func TestMatchASNWithBlankOrgName(t *testing.T) {
	// 组织名里只有空白时，不能把空白当成一次关键词命中。
	rec := model.IPRecord{IP: "1.1.1.1", ASN: 9808, ASOrg: "   "}
	if MatchASN(rec, []string{"china"}) {
		t.Error("空白的组织名不该被当成命中关键词")
	}
	if !MatchASN(rec, []string{"9808"}) {
		t.Error("AS 号匹配不依赖组织名")
	}
	// 只有空白的条件等同于没有条件。
	if !MatchASN(rec, []string{"  "}) {
		t.Error("只有空白的条件应当等同于没有条件")
	}
}

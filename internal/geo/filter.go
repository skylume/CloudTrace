package geo

import (
	"strconv"
	"strings"

	"cloudtrace/internal/model"
)

// 运营商快捷选项。
//
// 每个选项是一组匹配词，写进 geo.filter_asn 之后由本包的匹配逻辑生效。
// 中国移动的 AS 号是硬编码的已知集合，组织名关键词是它的兜底：运营商改
// 组织名的事发生过，只认关键词会失效；而只认 AS 号又漏掉新分配的小号。
var quickFilters = map[string][]string{
	"china_mobile": {"9808", "24400", "56040", "56041", "56044",
		"cmi", "cmnet", "chinamobile", "china mobile", "cmcc", "mobile communications", "移动"},
	"china_telecom": {"4134", "4809", "23724", "chinatelecom", "china telecom", "电信"},
	"china_unicom":  {"4837", "9929", "17621", "chinaunicom", "china unicom", "联通"},
}

// QuickFilterNames 返回全部快捷选项的名字，顺序固定。
func QuickFilterNames() []string {
	return []string{"china_mobile", "china_telecom", "china_unicom"}
}

// QuickFilter 返回某个快捷选项对应的匹配词；未知选项返回 nil。
func QuickFilter(name string) []string {
	tokens, ok := quickFilters[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return nil
	}
	return append([]string(nil), tokens...)
}

// MatchASN 判断一条记录是否命中过滤条件。
//
// 条件里的每一项可以是 AS 号（9808 或 AS9808），也可以是组织名关键词。
// 空条件视为全部命中——用户没设过滤时不该筛掉任何东西。
//
// 记录本身没有 ASN 信息时（库不可用、或那个 IP 查不到），只有空条件才会
// 命中：拿一个空的组织名去匹配关键词会得到假阳性，而**过滤层宁可少筛也
// 不能多筛**——筛掉一个可用节点，用户是察觉不到的。
func MatchASN(rec model.IPRecord, filter []string) bool {
	if len(filter) == 0 {
		return true
	}
	org := strings.ToLower(strings.TrimSpace(rec.ASOrg))
	usable := false
	for _, raw := range filter {
		token := strings.TrimSpace(raw)
		if token == "" {
			continue
		}
		usable = true
		if asn, ok := parseASNToken(token); ok {
			if rec.ASN == asn {
				return true
			}
			continue
		}
		if org != "" && strings.Contains(org, strings.ToLower(token)) {
			return true
		}
	}
	// 条件里全是没有内容的项，等同于没有条件：这类条件多半来自界面上被清空
	// 的输入框，按「什么都不匹配」处理会把结果整片清掉。
	return !usable
}

// FilterByASN 按条件筛出记录，条件为空时原样返回。
func FilterByASN(records []model.IPRecord, filter []string) []model.IPRecord {
	if len(filter) == 0 {
		return records
	}
	out := make([]model.IPRecord, 0, len(records))
	for _, rec := range records {
		if MatchASN(rec, filter) {
			out = append(out, rec)
		}
	}
	return out
}

// parseASNToken 尝试把一项条件解析成 AS 号。
//
// 只接受纯数字与 AS 前缀两种写法：组织名里也可能出现数字（「CHINA NET 3」），
// 把任何含数字的条件都当 AS 号会让关键词匹配失效。
func parseASNToken(token string) (uint32, bool) {
	text := strings.TrimSpace(token)
	if len(text) > 2 && (text[0] == 'A' || text[0] == 'a') && (text[1] == 'S' || text[1] == 's') {
		text = text[2:]
	}
	if text == "" {
		return 0, false
	}
	for i := 0; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return 0, false
		}
	}
	value, err := strconv.ParseUint(text, 10, 32)
	if err != nil || value == 0 {
		// AS 号 0 是保留值，不可能是一条有效的过滤条件。
		return 0, false
	}
	return uint32(value), true
}

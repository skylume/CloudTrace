package source

import (
	"strings"
	"unicode"
)

// labelSeparators 是切词用的分隔符。
//
// 注意中英文括号**不在**其中：地区中文名里带括号（如「刚果(布)」），
// 拆开就查不到表了，所以括号保留在词内，由中文片段提取统一处理。
const labelSeparators = " \t\r\n,;|/#-_.[]{}"

// NormalizeCountry 把各种地区写法归一化成两位地区代码，认不出来时返回空串。
//
// 依次尝试：两位 / 三位代码 → 地区中文名 → emoji 国旗。
// 上游数据源给的标签很杂：代码与名字混排、夹着序号与分隔符、甚至带 emoji，
// 所以先按分隔符切词，再逐词尝试。
//
// 大小写规则：两位代码大小写不敏感，三位代码**必须全大写**。
// 三位小写字母与英文单词、主机名标签大量重合（"com" 是科摩罗、"can" 是加拿大），
// 放开大小写会把 "us1.example.com" 里的 "com" 认成国家码；两位代码没有这个
// 问题，而用户手写 "us" 又很常见，所以只对三位收紧。
func NormalizeCountry(label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return ""
	}

	tokens := splitLabel(label)
	for _, token := range tokens {
		if code := matchAlphaCode(token); code != "" {
			return code
		}
	}
	for _, token := range tokens {
		for _, name := range chineseRuns(token) {
			if code := lookupInTable(cnToCodeTable, name); code != "" {
				return code
			}
		}
	}
	return countryFromFlag(label)
}

// splitLabel 按分隔符把标签切成词，丢弃空词。
func splitLabel(label string) []string {
	return strings.FieldsFunc(label, func(r rune) bool {
		return strings.ContainsRune(labelSeparators, r)
	})
}

// matchAlphaCode 在词里找连续的 ASCII 字母段，尝试当成地区代码。
//
// 三段与两段都试，但要求字母段后面不紧跟字母或数字：否则
// "us1.example.com" 这类主机名会被误判成国家码。
func matchAlphaCode(token string) string {
	for i := 0; i < len(token); {
		if !isASCIILetter(token[i]) {
			i++
			continue
		}
		start := i
		for i < len(token) && isASCIILetter(token[i]) {
			i++
		}
		run := strings.ToUpper(token[start:i])
		if len(run) < 2 || len(run) > 3 {
			continue
		}
		if i < len(token) && isASCIIAlnum(token[i]) {
			continue
		}
		if code := resolveAlphaCode(token[start:i], run); code != "" {
			return code
		}
	}
	return ""
}

// resolveAlphaCode 把两段或三段的字母代码解析成两位地区代码。
//
// raw 是原始写法（保留大小写），upper 是折叠大写后的写法。
func resolveAlphaCode(raw, upper string) string {
	if len(upper) == 3 {
		// 三位代码必须本来就是全大写，见 NormalizeCountry 的说明。
		if raw != upper {
			return ""
		}
		return lookupInTable(alpha3Table, upper)
	}
	if isAlpha2(upper) {
		return upper
	}
	return ""
}

// isAlpha2 判断两位代码是不是有效的地区代码。
//
// 有效的两位代码一定会作为三位代码的映射结果出现，所以直接在三段表里找
// "=XX "，不必再维护一份两位代码清单。
func isAlpha2(code string) bool {
	return strings.Contains(alpha3Table, "="+code+" ")
}

// chineseRuns 提取词里的中文片段，括号算作片段的一部分。
//
// 标签常写成「🇭🇰 香港」或「1.香港」，中文名前后混着别的字符，
// 所以要按「连续中文」切段后再查表。
func chineseRuns(token string) []string {
	var runs []string
	var current []rune

	flush := func() {
		if len(current) > 0 {
			runs = append(runs, string(current))
			current = nil
		}
	}
	for _, r := range token {
		if unicode.Is(unicode.Han, r) || isParen(r) {
			current = append(current, r)
			continue
		}
		flush()
	}
	flush()
	return runs
}

// isParen 判断是不是中英文括号。
func isParen(r rune) bool {
	switch r {
	case '(', ')', '（', '）':
		return true
	default:
		return false
	}
}

// countryFromFlag 从 emoji 国旗取地区代码。
//
// 国旗由两个区域指示符组成，U+1F1E6..U+1F1FF 依次对应 A..Z。
func countryFromFlag(label string) string {
	var letters []rune
	for _, r := range label {
		if r >= 0x1F1E6 && r <= 0x1F1FF {
			letters = append(letters, 'A'+r-0x1F1E6)
		}
	}
	if len(letters) < 2 {
		return ""
	}
	return string(letters[:2])
}

// lookupInTable 在「空格分隔的 KEY=VAL」常量表里精确查 KEY。
//
// 精确匹配靠两侧的空格定界：查 " 中国=" 不会命中 " 中国香港="。
func lookupInTable(table, key string) string {
	if key == "" {
		return ""
	}
	needle := " " + key + "="
	i := strings.Index(table, needle)
	if i < 0 {
		return ""
	}
	rest := table[i+len(needle):]
	if j := strings.IndexByte(rest, ' '); j >= 0 {
		return rest[:j]
	}
	return rest
}

func isASCIILetter(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

func isASCIIAlnum(b byte) bool {
	return isASCIILetter(b) || (b >= '0' && b <= '9')
}

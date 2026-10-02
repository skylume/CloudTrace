package exporter

import (
	"strings"

	"cloudtrace/internal/model"
)

// BOM 是 Excel 认的 UTF-8 字节序标记。
//
// 不带它，Windows 上的 Excel 会按本地代码页（GBK）解读文件，中文表头全部
// 变成乱码——这是导出功能最常见的一条差评，成本却只有三个字节。
var BOM = []byte{0xEF, 0xBB, 0xBF}

// CSV 把结果集渲染成 CSV。
//
// aliases 可覆盖表头文字（键 → 自定义名），未列出的用字段自带中文名。
func CSV(records []model.IPRecord, fields []FieldDef, aliases map[string]string, bom bool) []byte {
	var b strings.Builder
	if bom {
		b.Write(BOM)
	}

	// 表头
	head := make([]string, 0, len(fields))
	for _, f := range fields {
		label := f.Label
		if a, ok := aliases[f.Key]; ok && a != "" {
			label = a
		}
		head = append(head, quoteCell(label))
	}
	b.WriteString(strings.Join(head, ","))
	b.WriteString("\r\n")

	// 数据行。用 CRLF 而不是 LF：CSV 的既定约定，Excel 与多数解析器都按
	// CRLF 切分记录。
	for _, rec := range records {
		row := make([]string, 0, len(fields))
		for _, f := range fields {
			v, ok := Value(rec, f.Key)
			if !ok {
				continue
			}
			row = append(row, quoteCell(formatCSV(v)))
		}
		b.WriteString(strings.Join(row, ","))
		b.WriteString("\r\n")
	}

	return []byte(b.String())
}

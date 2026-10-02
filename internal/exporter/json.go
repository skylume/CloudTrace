package exporter

import (
	"encoding/json"
	"fmt"

	"cloudtrace/internal/model"
)

// JSON 把结果集渲染成 JSON。
//
// 与 CSV 的两点差异是刻意的：值保持原生类型（数字就是数字，便于下游解析），
// 不可达哨兵 -1 原样保留（CSV 里渲染成空单元格是为了 Excel 能用，JSON 是
// 无损格式，丢掉哨兵就等于把「没测到」和「真的 0 毫秒」混在一起）。
//
// aliases 同样生效：字段名是契约，表头别名是给人看的，两种格式都该尊重。
func JSON(records []model.IPRecord, fields []FieldDef, aliases map[string]string) ([]byte, error) {
	rows := make([]map[string]any, 0, len(records))
	for _, rec := range records {
		row := make(map[string]any, len(fields))
		for _, f := range fields {
			v, ok := Value(rec, f.Key)
			if !ok {
				continue
			}
			key := f.Key
			if a, ok := aliases[f.Key]; ok && a != "" {
				key = a
			}
			row[key] = v
		}
		rows = append(rows, row)
	}

	out, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("序列化导出结果失败：%w", err)
	}
	return append(out, '\n'), nil
}

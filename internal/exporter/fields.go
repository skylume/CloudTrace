// Package exporter 把结果集渲染成 CSV / JSON / TXT 三种文本。
//
// 字段清单由本包下发（Fields），前端不硬编码任何字段名——旧项目「前后端
// 字段手工同步漂移」的根因就是两端各写一份。
package exporter

import (
	"encoding/json"
	"strconv"
	"strings"

	"cloudtrace/internal/model"
)

// 字段类型，供前端决定列宽、对齐与筛选控件。
const (
	TypeString = "string"
	TypeInt    = "int"
	TypeFloat  = "float"
	TypeBool   = "bool"
	TypeObject = "object"
)

// 字段分组，前端按分组折叠展示。
const (
	GroupBasic  = "基础"
	GroupDelay  = "延迟"
	GroupGeo    = "归属"
	GroupSpeed  = "测速"
	GroupDetail = "明细"
)

// FieldDef 描述一个可导出字段。
type FieldDef struct {
	// Key 与结果记录里的 JSON 字段名一致，不做转换。
	Key string `json:"key"`
	// Label 是表头用的中文名。
	Label string `json:"label"`
	// Type 取值见 Type* 常量。
	Type string `json:"type"`
	// Group 取值见 Group* 常量。
	Group string `json:"group"`
	// Default 表示是否默认选中。默认选中的那批就是「精简预设」。
	Default bool `json:"default"`
}

// Fields 返回全部可导出字段，顺序即表格默认列序。
func Fields() []FieldDef {
	return []FieldDef{
		{"ip", "地址", TypeString, GroupBasic, true},
		{"port", "端口", TypeInt, GroupBasic, true},
		{"use_tls", "TLS", TypeBool, GroupBasic, false},
		{"latency", "延迟(ms)", TypeFloat, GroupDelay, true},
		{"latency_avg", "平均延迟(ms)", TypeFloat, GroupDelay, true},
		{"latency_max", "最大延迟(ms)", TypeFloat, GroupDelay, false},
		{"jitter", "抖动(ms)", TypeFloat, GroupDelay, false},
		{"loss", "丢包率", TypeFloat, GroupDelay, true},
		{"sent", "发送数", TypeInt, GroupDelay, false},
		{"recv", "接收数", TypeInt, GroupDelay, false},
		{"colo", "数据中心", TypeString, GroupGeo, true},
		{"region_name", "地区", TypeString, GroupGeo, true},
		{"loc", "出口国家", TypeString, GroupGeo, false},
		{"asn", "ASN", TypeInt, GroupGeo, false},
		{"as_org", "ASN 归属", TypeString, GroupGeo, false},
		{"geo_warn", "地理警告", TypeString, GroupGeo, false},
		{"speed_mbps", "下载速度(MB/s)", TypeFloat, GroupSpeed, true},
		{"score", "评分", TypeFloat, GroupSpeed, true},
		{"trace", "节点明细", TypeObject, GroupDetail, false},
	}
}

// 字段预设。
const (
	PresetAll    = "all"
	PresetSlim   = "slim"
	PresetIPPort = "ip_port"
)

// slimKeys 是「精简」预设：一眼能看出该选哪个，又不至于列太宽。
var slimKeys = []string{
	"ip", "port", "latency", "latency_avg", "loss",
	"colo", "region_name", "speed_mbps", "score",
}

var ipPortKeys = []string{"ip", "port"}

// Preset 是一个字段预设。
type Preset struct {
	// ID 是预设标识，与配置里的 export.default_fields 取值一致。
	ID string `json:"id"`
	// Name 是界面上显示的名字。
	Name string `json:"name"`
	// Keys 是预设包含的字段键。
	Keys []string `json:"keys"`
}

// Presets 返回全部字段预设，顺序即界面上的排列顺序。
//
// 预设的中文名也由后端下发，理由与字段清单完全相同：前端各写一份就必然会
// 漂移，而且「后端加了预设、前端还没跟上」这种不同步没有地方能发现。
func Presets() []Preset {
	return []Preset{
		{ID: PresetAll, Name: "全部字段", Keys: PresetKeys(PresetAll)},
		{ID: PresetSlim, Name: "精简", Keys: PresetKeys(PresetSlim)},
		{ID: PresetIPPort, Name: "仅 IP:端口", Keys: PresetKeys(PresetIPPort)},
	}
}

// PresetKeys 返回预设对应的字段键列表。
//
// 未知预设按 all 处理：多一个预设不该让导出直接失败，用户选了什么就导出
// 尽可能多的东西，再让他自己减。
func PresetKeys(preset string) []string {
	switch preset {
	case PresetSlim:
		return append([]string(nil), slimKeys...)
	case PresetIPPort:
		return append([]string(nil), ipPortKeys...)
	case PresetAll:
		out := make([]string, 0, len(Fields()))
		for _, f := range Fields() {
			out = append(out, f.Key)
		}
		return out
	default:
		return PresetKeys(PresetAll)
	}
}

// DefaultKeys 返回默认选中的字段键列表。
func DefaultKeys() []string {
	out := make([]string, 0, len(Fields()))
	for _, f := range Fields() {
		if f.Default {
			out = append(out, f.Key)
		}
	}
	return out
}

// ResolveFields 把字段键列表解析成字段定义。
//
// 空列表表示「用默认选中」，未知键直接跳过而不是报错：字段清单会随功能
// 增加，一份旧配置里带着已下线的键，不该让导出整个失败。
func ResolveFields(keys []string) []FieldDef {
	if len(keys) == 0 {
		keys = DefaultKeys()
	}
	all := Fields()
	out := make([]FieldDef, 0, len(keys))
	for _, k := range keys {
		for _, f := range all {
			if f.Key == k {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

// Value 取某条记录在指定字段上的原始值。
//
// 返回的是原生类型而不是字符串：JSON 导出要保留数字与布尔，CSV 导出再各自
// 格式化。统一先转成字符串会让 JSON 里出现一堆带引号的数字。
func Value(rec model.IPRecord, key string) (any, bool) {
	switch key {
	case "ip":
		return rec.IP, true
	case "port":
		return rec.Port, true
	case "use_tls":
		return rec.UseTLS, true
	case "latency":
		return rec.Latency, true
	case "latency_avg":
		return rec.LatencyAvg, true
	case "latency_max":
		return rec.LatencyMax, true
	case "jitter":
		return rec.Jitter, true
	case "loss":
		return rec.Loss, true
	case "sent":
		return rec.Sent, true
	case "recv":
		return rec.Recv, true
	case "colo":
		return rec.Colo, true
	case "region_name":
		return rec.RegionName, true
	case "loc":
		return rec.Loc, true
	case "asn":
		return rec.ASN, true
	case "as_org":
		return rec.ASOrg, true
	case "geo_warn":
		return rec.GeoWarn, true
	case "speed_mbps":
		return rec.SpeedMBps, true
	case "score":
		return rec.Score, true
	case "trace":
		if rec.Trace == nil {
			return nil, true
		}
		return rec.Trace, true
	}
	return nil, false
}

// formatCSV 把字段值渲染成 CSV 里的一个单元格。
//
// 不可达哨兵（延迟字段的 -1）渲染成**空单元格**而不是 -1：「-1 毫秒」没有
// 意义，而且会污染平均值；空单元格在 Excel 里会被 AVERAGE 直接忽略，正是
// 「这条没有数据」的正确表达。JSON 导出保留原始值，保证无损。
func formatCSV(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "是"
		}
		return "否"
	case int:
		return strconv.Itoa(t)
	case uint32:
		return strconv.FormatUint(uint64(t), 10)
	case float64:
		if t == model.Unreachable {
			return ""
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	}
	// 剩下的（如节点明细）序列化成 JSON 塞进一个单元格。
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// needsQuoting 判断单元格是否需要加引号。
func needsQuoting(s string) bool {
	return strings.ContainsAny(s, ",\"\r\n")
}

// quoteCell 按 RFC 4180 转义单元格内容。
func quoteCell(s string) string {
	if !needsQuoting(s) {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

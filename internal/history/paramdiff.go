package history

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ParamDiff 是一处参数差异。
type ParamDiff struct {
	// Key 是参数名（与参数快照里的字段名一致）。
	Key string `json:"key"`
	// Label 是给用户看的名字，没有收录时退回 Key。
	Label string `json:"label"`
	// From 是历史记录当时的值，To 是当前的值。
	From string `json:"from"`
	To   string `json:"to"`
}

// paramLabels 是常见参数的中文名。
//
// 没收录的键原样显示：参数会随功能增加，与其在标签表里漏掉一个就显示成
// 空白，不如退回键名——用户至少知道是哪一项不同。
var paramLabels = map[string]string{
	// 扫描
	"mode":              "探测方式",
	"workers":           "并发数",
	"sample_max":        "采样上限",
	"latency_threshold": "延迟阈值",
	"ping_times":        "探测次数",
	"port":              "端口",
	"ip_version":        "IP 版本",
	"source_mode":       "来源方式",
	"custom_source":     "自定义来源",
	"pre_filter_ports":  "前置端口过滤",
	"allowed_regions":   "地区白名单",
	"blocked_regions":   "地区黑名单",
	"two_phase":         "两阶段扫描",
	"verify_nodes":      "节点明细采集",
	"timeout_ms":        "探测超时",
	"retry":             "重试次数",
	// 测速
	"scope":               "测速范围",
	"url_mode":            "测速源",
	"custom_url":          "自定义测速地址",
	"use_tls":             "TLS",
	"concurrency":         "测速并发",
	"target_qualified":    "合格数目标",
	"interval_ms":         "测速间隔",
	"min_speed":           "合格线",
	"weight_speed":        "速度权重",
	"weight_latency":      "延迟权重",
	"weight_jitter":       "抖动权重",
	"per_region_topn":     "分地区 TopN",
	"download_duration_s": "下载时长",
	"breaker_429":         "限流熔断阈值",
	"usability_check":     "可用性预校验",
}

// ignoreParams 是参与比对时会淹没重点的字段。
//
// Targets 是本次要测的节点清单，属于数据而不是参数：两份历史之间节点不同
// 是正常的，把它列进「参数差异」只会让用户以为设置变了。
var ignoreParams = map[string]bool{
	"targets": true,
}

// DiffParams 比较两份参数快照，返回有差异的项。
//
// 比对是通用的 JSON 对象比对，而不是逐字段写死：参数会随功能增加，写死的
// 比对表每加一个参数就得改一次，漏改的表现是「明明参数不同却不提示」。
//
// 任一侧为空时返回空结果：没有可比的东西，不该凭空报出一堆差异。
func DiffParams(snapshot, current json.RawMessage) ([]ParamDiff, error) {
	old, err := decodeObject(snapshot)
	if err != nil {
		return nil, err
	}
	now, err := decodeObject(current)
	if err != nil {
		return nil, err
	}
	if len(old) == 0 || len(now) == 0 {
		return nil, nil
	}

	keys := make(map[string]bool, len(old)+len(now))
	for k := range old {
		keys[k] = true
	}
	for k := range now {
		keys[k] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		if ignoreParams[k] {
			continue
		}
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	out := make([]ParamDiff, 0, 4)
	for _, k := range sorted {
		from, okA := old[k]
		to, okB := now[k]
		if okA == okB && sameValue(from, to) {
			continue
		}
		out = append(out, ParamDiff{
			Key:   k,
			Label: labelOf(k),
			From:  formatValue(from, okA),
			To:    formatValue(to, okB),
		})
	}
	return out, nil
}

func decodeObject(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("参数快照不是 JSON 对象：%w", err)
	}
	return out, nil
}

func labelOf(key string) string {
	if l, ok := paramLabels[key]; ok {
		return l
	}
	return key
}

// sameValue 比较两个已解析的 JSON 值。
//
// 走一次重新序列化再比字符串：数字在 JSON 里都是 float64，直接比会有
// 精度问题；序列化之后 1 与 1.0 归一成同一种写法，比字符串既简单又稳。
func sameValue(a, b any) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return string(ja) == string(jb)
}

// formatValue 把参数值渲染成一行可读文本。
func formatValue(v any, present bool) string {
	if !present {
		return "未设置"
	}
	switch t := v.(type) {
	case nil:
		return "未设置"
	case bool:
		if t {
			return "开"
		}
		return "关"
	case string:
		if t == "" {
			return "未设置"
		}
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case []any:
		if len(t) == 0 {
			return "无"
		}
		parts := make([]string, 0, len(t))
		for _, item := range t {
			parts = append(parts, formatValue(item, true))
		}
		return strings.Join(parts, "、")
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}

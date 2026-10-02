package history

import (
	"encoding/json"
	"strings"
	"testing"
)

// 两个键都变了：要能同时给出「改前」与「改后」，并且带上中文名。
func TestDiffParamsReportsChangedKeys(t *testing.T) {
	from := json.RawMessage(`{"workers":150,"port":443}`)
	to := json.RawMessage(`{"workers":200,"port":8443}`)

	got, err := DiffParams(from, to)
	if err != nil {
		t.Fatalf("比对失败：%v", err)
	}
	if len(got) != 2 {
		t.Fatalf("差异项数 = %d，期望 2（%+v）", len(got), got)
	}

	// 按键排序：port 在 workers 前面。
	if got[0].Key != "port" || got[0].Label != "端口" {
		t.Fatalf("第 1 项 = %+v，期望端口", got[0])
	}
	if got[0].From != "443" || got[0].To != "8443" {
		t.Fatalf("端口差异 = %q → %q", got[0].From, got[0].To)
	}
	if got[1].Key != "workers" || got[1].Label != "并发数" {
		t.Fatalf("第 2 项 = %+v，期望并发数", got[1])
	}
	if got[1].From != "150" || got[1].To != "200" {
		t.Fatalf("并发数差异 = %q → %q", got[1].From, got[1].To)
	}
}

// 参数完全一致时不该报出任何差异。
func TestDiffParamsIdenticalIsEmpty(t *testing.T) {
	raw := json.RawMessage(`{"workers":150,"port":443,"use_tls":true}`)

	got, err := DiffParams(raw, raw)
	if err != nil {
		t.Fatalf("比对失败：%v", err)
	}
	if len(got) != 0 {
		t.Fatalf("相同的参数报出了差异：%+v", got)
	}
}

// 数字在 JSON 里都是 float64，1 与 1.0 必须视作同一个值。
func TestDiffParamsNumbersCompareByValue(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool // 是否应报出差异
	}{
		{"整数与小数写法", `{"port":1}`, `{"port":1.0}`, false},
		{"零与零点零", `{"port":0}`, `{"port":0.0}`, false},
		{"数值不同", `{"port":1}`, `{"port":2}`, true},
		{"小数不同", `{"min_speed":1.5}`, `{"min_speed":1.6}`, true},
		{"字符串与数字不同型", `{"port":"443"}`, `{"port":443}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DiffParams(json.RawMessage(tc.a), json.RawMessage(tc.b))
			if err != nil {
				t.Fatalf("比对失败：%v", err)
			}
			if (len(got) > 0) != tc.want {
				t.Fatalf("%s vs %s 的差异 = %+v，期望有差异 = %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// 布尔值渲染成「开 / 关」而不是 true / false。
func TestDiffParamsRendersBooleansAsChinese(t *testing.T) {
	got, err := DiffParams(
		json.RawMessage(`{"use_tls":false}`),
		json.RawMessage(`{"use_tls":true}`),
	)
	if err != nil {
		t.Fatalf("比对失败：%v", err)
	}
	if len(got) != 1 {
		t.Fatalf("差异项数 = %d，期望 1", len(got))
	}
	if got[0].From != "关" || got[0].To != "开" {
		t.Fatalf("布尔差异 = %q → %q，期望 关 → 开", got[0].From, got[0].To)
	}
	if got[0].Label != "TLS" {
		t.Fatalf("标签 = %q，期望 TLS", got[0].Label)
	}
}

// 只有一侧存在的键，另一侧渲染成「未设置」，而不是留空。
func TestDiffParamsMissingKeyShowsUnset(t *testing.T) {
	t.Run("新增的键", func(t *testing.T) {
		got, err := DiffParams(
			json.RawMessage(`{"workers":150}`),
			json.RawMessage(`{"workers":150,"two_phase":true}`),
		)
		if err != nil {
			t.Fatalf("比对失败：%v", err)
		}
		if len(got) != 1 || got[0].From != "未设置" || got[0].To != "开" {
			t.Fatalf("差异 = %+v，期望 未设置 → 开", got)
		}
	})

	t.Run("消失的键", func(t *testing.T) {
		got, err := DiffParams(
			json.RawMessage(`{"workers":150,"two_phase":true}`),
			json.RawMessage(`{"workers":150}`),
		)
		if err != nil {
			t.Fatalf("比对失败：%v", err)
		}
		if len(got) != 1 || got[0].From != "开" || got[0].To != "未设置" {
			t.Fatalf("差异 = %+v，期望 开 → 未设置", got)
		}
	})

	t.Run("显式 null 当作未设置", func(t *testing.T) {
		got, err := DiffParams(
			json.RawMessage(`{"custom_source":null}`),
			json.RawMessage(`{"custom_source":"https://example.com/ips.txt"}`),
		)
		if err != nil {
			t.Fatalf("比对失败：%v", err)
		}
		if len(got) != 1 || got[0].From != "未设置" {
			t.Fatalf("差异 = %+v，期望历史侧未设置", got)
		}
	})

	t.Run("空字符串当作未设置", func(t *testing.T) {
		got, err := DiffParams(
			json.RawMessage(`{"custom_url":""}`),
			json.RawMessage(`{"custom_url":"https://example.com/100MB"}`),
		)
		if err != nil {
			t.Fatalf("比对失败：%v", err)
		}
		if len(got) != 1 || got[0].From != "未设置" {
			t.Fatalf("差异 = %+v，期望历史侧未设置", got)
		}
	})
}

// 节点清单属于数据而不是参数，两边不同是正常的，不该淹没真正的设置差异。
func TestDiffParamsIgnoresTargets(t *testing.T) {
	got, err := DiffParams(
		json.RawMessage(`{"workers":150,"targets":["1.1.1.1:443"]}`),
		json.RawMessage(`{"workers":150,"targets":["1.1.1.2:443","1.1.1.3:443"]}`),
	)
	if err != nil {
		t.Fatalf("比对失败：%v", err)
	}
	if len(got) != 0 {
		t.Fatalf("节点清单差异被报了出来：%+v", got)
	}
}

// 列表渲染成顿号连接的一行；空列表显示「无」。
func TestDiffParamsRendersSlices(t *testing.T) {
	t.Run("有元素", func(t *testing.T) {
		got, err := DiffParams(
			json.RawMessage(`{"allowed_regions":["HKG","NRT"]}`),
			json.RawMessage(`{"allowed_regions":["HKG"]}`),
		)
		if err != nil {
			t.Fatalf("比对失败：%v", err)
		}
		if len(got) != 1 || got[0].From != "HKG、NRT" || got[0].To != "HKG" {
			t.Fatalf("差异 = %+v，期望 HKG、NRT → HKG", got)
		}
	})

	t.Run("空列表", func(t *testing.T) {
		got, err := DiffParams(
			json.RawMessage(`{"blocked_regions":[]}`),
			json.RawMessage(`{"blocked_regions":["NRT"]}`),
		)
		if err != nil {
			t.Fatalf("比对失败：%v", err)
		}
		if len(got) != 1 || got[0].From != "无" {
			t.Fatalf("差异 = %+v，期望空列表显示「无」", got)
		}
	})
}

// 没收录中文名的键退回键名，用户至少知道是哪一项不同。
func TestDiffParamsUnknownKeyFallsBackToKey(t *testing.T) {
	got, err := DiffParams(
		json.RawMessage(`{"brand_new_knob":1}`),
		json.RawMessage(`{"brand_new_knob":2}`),
	)
	if err != nil {
		t.Fatalf("比对失败：%v", err)
	}
	if len(got) != 1 || got[0].Label != "brand_new_knob" {
		t.Fatalf("差异 = %+v，期望标签退回键名", got)
	}
}

// 任一侧为空（没传、传了空对象、传了 null）都不该凭空报差异。
func TestDiffParamsEmptySideMeansNoDiff(t *testing.T) {
	full := json.RawMessage(`{"workers":150}`)
	cases := []struct {
		name     string
		snapshot json.RawMessage
		current  json.RawMessage
	}{
		{"两侧都是 nil", nil, nil},
		{"快照为 nil", nil, full},
		{"当前为 nil", full, nil},
		{"两侧都是空对象", json.RawMessage(`{}`), json.RawMessage(`{}`)},
		{"快照为空对象", json.RawMessage(`{}`), full},
		{"当前为空对象", full, json.RawMessage(`{}`)},
		{"两侧都是 null", json.RawMessage(`null`), json.RawMessage(`null`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DiffParams(tc.snapshot, tc.current)
			if err != nil {
				t.Fatalf("比对失败：%v", err)
			}
			if len(got) != 0 {
				t.Fatalf("空快照报出了差异：%+v", got)
			}
		})
	}
}

// 不是 JSON 对象时明确报错，而不是静默当成「没有差异」。
func TestDiffParamsRejectsNonObject(t *testing.T) {
	cases := []struct {
		name     string
		snapshot string
		current  string
	}{
		{"快照是数组", `[1,2]`, `{"workers":150}`},
		{"当前是数组", `{"workers":150}`, `[1,2]`},
		{"快照是裸数字", `150`, `{"workers":150}`},
		{"快照被截断", `{"workers":`, `{"workers":150}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DiffParams(json.RawMessage(tc.snapshot), json.RawMessage(tc.current)); err == nil {
				t.Fatalf("%s 应当报错", tc.name)
			}
		})
	}
}

// 差异项按键排序，保证同一对参数每次给出的顺序都一样。
func TestDiffParamsIsSortedByKey(t *testing.T) {
	from := json.RawMessage(`{"zebra":1,"alpha":1,"middle":1}`)
	to := json.RawMessage(`{"zebra":2,"alpha":2,"middle":2}`)

	got, err := DiffParams(from, to)
	if err != nil {
		t.Fatalf("比对失败：%v", err)
	}
	if len(got) != 3 {
		t.Fatalf("差异项数 = %d，期望 3", len(got))
	}
	keys := []string{got[0].Key, got[1].Key, got[2].Key}
	if strings.Join(keys, ",") != "alpha,middle,zebra" {
		t.Fatalf("顺序 = %v，期望按字母序", keys)
	}
}

// 结构化的值（嵌套对象）退回 JSON 原文，不丢信息。
func TestDiffParamsRendersNestedValueAsJSON(t *testing.T) {
	got, err := DiffParams(
		json.RawMessage(`{"weight_speed":{"value":1}}`),
		json.RawMessage(`{"weight_speed":{"value":2}}`),
	)
	if err != nil {
		t.Fatalf("比对失败：%v", err)
	}
	if len(got) != 1 {
		t.Fatalf("差异项数 = %d，期望 1", len(got))
	}
	if got[0].From != `{"value":1}` || got[0].To != `{"value":2}` {
		t.Fatalf("嵌套值渲染 = %q → %q", got[0].From, got[0].To)
	}
}

// 参数会随功能增加，中文名表必须覆盖到，否则用户看到的是英文键名。
func TestParamLabelsCoverKnownKeys(t *testing.T) {
	// 这些键会出现在参数快照里，是用户最常调的一批。
	keys := []string{
		"workers", "port", "ip_version", "source_mode", "two_phase",
		"concurrency", "min_speed", "url_mode", "use_tls", "scope",
	}
	for _, k := range keys {
		if labelOf(k) == k {
			t.Errorf("参数 %q 没有中文名", k)
		}
	}
	if labelOf("no_such_param") != "no_such_param" {
		t.Error("未收录的键应当退回键名")
	}
}

// formatValue 的各个分支。
func TestFormatValueBranches(t *testing.T) {
	cases := []struct {
		name    string
		value   any
		present bool
		want    string
	}{
		{"缺失", nil, false, "未设置"},
		{"null", nil, true, "未设置"},
		{"真", true, true, "开"},
		{"假", false, true, "关"},
		{"空串", "", true, "未设置"},
		{"普通串", "tcping", true, "tcping"},
		{"整数", float64(443), true, "443"},
		{"小数", 1.5, true, "1.5"},
		{"大数", float64(5000), true, "5000"},
		{"空数组", []any{}, true, "无"},
		{"数组", []any{"HKG", float64(1)}, true, "HKG、1"},
		{"嵌套数组", []any{[]any{"a", "b"}}, true, "a、b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatValue(tc.value, tc.present); got != tc.want {
				t.Fatalf("formatValue(%v, %v) = %q，期望 %q", tc.value, tc.present, got, tc.want)
			}
		})
	}
}

// sameValue 对无法序列化的值返回 false（宁可报差异，也不要静默漏掉）。
func TestSameValueUnmarshalableIsNotEqual(t *testing.T) {
	ch := make(chan int)
	if sameValue(ch, ch) {
		t.Fatal("无法序列化的值应当判为不相等")
	}
}

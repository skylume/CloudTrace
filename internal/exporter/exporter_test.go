package exporter

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"cloudtrace/internal/model"
)

func sampleRecords() []model.IPRecord {
	return []model.IPRecord{
		{
			IP: "1.1.1.1", Port: 443, UseTLS: true,
			Latency: 61.5, LatencyAvg: 63.2, LatencyMax: 70, Jitter: 5.5, Loss: 0,
			Sent: 3, Recv: 3, Colo: "LAX", RegionName: "美国", Loc: "US",
			ASN: 13335, ASOrg: "Cloudflare", SpeedMBps: 8.5, Score: 8.3,
		},
		{
			IP: "2606:4700::1", Port: 8443,
			Latency: model.Unreachable, LatencyAvg: model.Unreachable,
			LatencyMax: model.Unreachable, Loss: 1,
			Sent: 3, Recv: 0, Colo: "", RegionName: "",
		},
	}
}

// 前端不硬编码任何字段名，因此字段清单必须真的能拿到、且自带中文名。
func TestFieldsAreSelfDescribing(t *testing.T) {
	all := Fields()
	if len(all) == 0 {
		t.Fatal("字段清单为空")
	}
	seen := map[string]bool{}
	for _, f := range all {
		if f.Key == "" || f.Label == "" || f.Type == "" || f.Group == "" {
			t.Errorf("字段定义不完整：%+v", f)
		}
		if seen[f.Key] {
			t.Errorf("字段键重复：%s", f.Key)
		}
		seen[f.Key] = true
	}
	for _, want := range []string{"ip", "port", "latency", "colo", "speed_mbps", "score"} {
		if !seen[want] {
			t.Errorf("字段清单缺少 %s", want)
		}
	}

	// 默认选中的字段必须是「精简预设」的子集：一眼能看出该选哪个。
	def := DefaultKeys()
	if len(def) == 0 || len(def) == len(all) {
		t.Fatalf("默认选中 %d 个（全部 %d 个），期望介于两者之间", len(def), len(all))
	}
}

func TestPresetKeys(t *testing.T) {
	if got := PresetKeys(PresetIPPort); len(got) != 2 || got[0] != "ip" || got[1] != "port" {
		t.Errorf("ip_port 预设 = %v", got)
	}
	slim := PresetKeys(PresetSlim)
	if len(slim) < 5 || len(slim) >= len(Fields()) {
		t.Errorf("精简预设 = %v，期望是全部字段的一个真子集", slim)
	}
	if len(PresetKeys(PresetAll)) != len(Fields()) {
		t.Error("全部预设应覆盖所有字段")
	}
	// 未知预设按全部处理，不该让导出失败。
	if len(PresetKeys("nonsense")) != len(Fields()) {
		t.Error("未知预设应退回全部字段")
	}
	// 返回的是副本，改了不能污染内部表。
	got := PresetKeys(PresetIPPort)
	got[0] = "污染"
	if PresetKeys(PresetIPPort)[0] != "ip" {
		t.Error("预设返回了内部切片")
	}
}

// 未知字段跳过而不是报错：字段清单会随功能增加，旧配置里的老键不该让导出失败。
func TestResolveFieldsSkipsUnknown(t *testing.T) {
	got := ResolveFields([]string{"port", "已经下线的字段", "ip"})
	if len(got) != 2 || got[0].Key != "port" || got[1].Key != "ip" {
		t.Fatalf("解析结果 = %+v，期望按顺序保留已知的两个", got)
	}
	if len(ResolveFields(nil)) != len(DefaultKeys()) {
		t.Error("空列表应退回默认选中的字段")
	}
}

func TestCSVBasics(t *testing.T) {
	out := string(CSV(sampleRecords(), ResolveFields(PresetKeys(PresetSlim)), nil, false))
	lines := strings.Split(strings.TrimRight(out, "\r\n"), "\r\n")
	if len(lines) != 3 {
		t.Fatalf("CSV 行数 = %d，期望表头 + 2 行：\n%s", len(lines), out)
	}
	if !strings.Contains(lines[0], "地址") || !strings.Contains(lines[0], "下载速度(MB/s)") {
		t.Errorf("表头 = %q，期望带中文名", lines[0])
	}
	if !strings.HasPrefix(lines[1], "1.1.1.1,443") {
		t.Errorf("第一行 = %q", lines[1])
	}
	// 记录用 CRLF 分隔（CSV 的既定约定）。
	if !strings.Contains(out, "\r\n") {
		t.Error("CSV 应使用 CRLF 分隔记录")
	}
}

// 不可达哨兵渲染成空单元格：-1 毫秒没有意义，空单元格才会被 Excel 的
// AVERAGE 忽略。
func TestCSVRendersUnreachableAsEmpty(t *testing.T) {
	fields := ResolveFields([]string{"ip", "latency", "latency_avg"})
	out := string(CSV(sampleRecords(), fields, nil, false))
	lines := strings.Split(strings.TrimRight(out, "\r\n"), "\r\n")

	if !strings.Contains(lines[1], "61.5") {
		t.Errorf("可达节点应有延迟：%q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "2606:4700::1,,") {
		t.Errorf("不可达节点的延迟应为空单元格：%q", lines[2])
	}
}

func TestCSVBOM(t *testing.T) {
	withBOM := CSV(sampleRecords(), ResolveFields(PresetKeys(PresetIPPort)), nil, true)
	if len(withBOM) < 3 || withBOM[0] != 0xEF || withBOM[1] != 0xBB || withBOM[2] != 0xBF {
		t.Fatalf("CSV 缺少 BOM：% x", withBOM[:min(3, len(withBOM))])
	}
	without := CSV(sampleRecords(), ResolveFields(PresetKeys(PresetIPPort)), nil, false)
	if without[0] == 0xEF {
		t.Error("未要求时不应带 BOM")
	}
}

func TestCSVQuotesSpecialChars(t *testing.T) {
	recs := []model.IPRecord{{IP: "1.1.1.1", Port: 443, Colo: `A,B"C`}}
	out := string(CSV(recs, ResolveFields([]string{"colo"}), nil, false))
	if !strings.Contains(out, `"A,B""C"`) {
		t.Errorf("含逗号与引号的单元格未正确转义：%s", out)
	}
}

func TestCSVUsesAliases(t *testing.T) {
	out := string(CSV(sampleRecords(), ResolveFields([]string{"ip", "latency"}),
		map[string]string{"ip": "节点地址", "latency": "延迟"}, false))
	head := strings.SplitN(out, "\r\n", 2)[0]
	if head != "节点地址,延迟" {
		t.Errorf("表头 = %q，期望用别名", head)
	}
}

// JSON 是无损格式：值保持原生类型，不可达哨兵原样保留。
func TestJSONKeepsNativeTypesAndSentinel(t *testing.T) {
	fields := ResolveFields([]string{"ip", "port", "latency", "use_tls", "score"})
	data, err := JSON(sampleRecords(), fields, nil)
	if err != nil {
		t.Fatalf("导出失败：%v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(data, &rows); err != nil {
		t.Fatalf("导出的 JSON 无法解析：%v（原文 %s）", err, data)
	}
	if len(rows) != 2 {
		t.Fatalf("行数 = %d，期望 2", len(rows))
	}
	if rows[0]["port"] != float64(443) {
		t.Errorf("端口应是数字，实际 %#v", rows[0]["port"])
	}
	if rows[0]["use_tls"] != true {
		t.Errorf("TLS 应是布尔，实际 %#v", rows[0]["use_tls"])
	}
	if rows[1]["latency"] != model.Unreachable {
		t.Errorf("不可达哨兵应原样保留，实际 %#v", rows[1]["latency"])
	}
}

func TestJSONUsesAliasesAsKeys(t *testing.T) {
	data, err := JSON(sampleRecords(), ResolveFields([]string{"ip"}),
		map[string]string{"ip": "地址"})
	if err != nil {
		t.Fatalf("导出失败：%v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(data, &rows); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if _, ok := rows[0]["地址"]; !ok {
		t.Errorf("别名未生效：%s", data)
	}
}

func TestTXTPutsIPv6InBrackets(t *testing.T) {
	out := strings.TrimRight(string(TXT(sampleRecords())), "\n")
	want := "1.1.1.1:443\n[2606:4700::1]:8443"
	if out != want {
		t.Errorf("TXT = %q，期望 %q", out, want)
	}
}

func TestHostPort(t *testing.T) {
	cases := []struct {
		ip   string
		port int
		want string
	}{
		{"1.1.1.1", 443, "1.1.1.1:443"},
		{"2606:4700::1", 443, "[2606:4700::1]:443"},
		{"[2606:4700::1]", 443, "[2606:4700::1]:443"}, // 已有括号不重复加
		{"1.1.1.1", 0, "1.1.1.1"},
		{"", 443, ""},
	}
	for _, tc := range cases {
		if got := HostPort(tc.ip, tc.port); got != tc.want {
			t.Errorf("HostPort(%q, %d) = %q，期望 %q", tc.ip, tc.port, got, tc.want)
		}
	}
}

func TestExportDispatchesByFormat(t *testing.T) {
	cases := []struct {
		format string
		ct     string
		ext    string
	}{
		{"csv", "text/csv; charset=utf-8", "csv"},
		{"json", "application/json; charset=utf-8", "json"},
		{"txt", "text/plain; charset=utf-8", "txt"},
	}
	for _, tc := range cases {
		data, ct, ext, err := Export(sampleRecords(), Options{Format: tc.format})
		if err != nil {
			t.Fatalf("%s 导出失败：%v", tc.format, err)
		}
		if len(data) == 0 {
			t.Errorf("%s 导出为空", tc.format)
		}
		if ct != tc.ct || ext != tc.ext {
			t.Errorf("%s 的内容类型/扩展名 = %q / %q，期望 %q / %q", tc.format, ct, ext, tc.ct, tc.ext)
		}
	}
	if _, _, _, err := Export(sampleRecords(), Options{Format: "xlsx"}); err == nil {
		t.Error("未知格式应报错")
	}
}

func TestFilenameTemplate(t *testing.T) {
	ts := time.Date(2026, 9, 27, 14, 30, 12, 0, time.UTC)
	cases := []struct {
		tmpl string
		want string
	}{
		{"cloudtrace_{type}_{ts}", "cloudtrace_scan_20260927_143012.csv"},
		{"{date}-result", "20260927-result.csv"},
		{"result.csv", "result.csv"}, // 已有扩展名不重复
		{"result.CSV", "result.CSV"}, // 大小写不敏感
		{"  spaced  ", "spaced.csv"}, // 首尾空白与结尾点会被去掉
		{"", "cloudtrace_scan_20260927_143012.csv"},
		{"a/b:c*d", "a_b_c_d.csv"}, // 非法字符换成下划线
	}
	for _, tc := range cases {
		if got := Filename(tc.tmpl, "scan", "csv", ts); got != tc.want {
			t.Errorf("Filename(%q) = %q，期望 %q", tc.tmpl, got, tc.want)
		}
	}
}

// 字段清单与实际记录必须对齐：清单里写了却取不到值的字段会导出成一列全空，
// 而且要到用户手里才发现。
func TestEveryDeclaredFieldResolves(t *testing.T) {
	rec := model.IPRecord{
		IP: "1.1.1.1", Port: 443, UseTLS: true,
		Latency: 20, LatencyAvg: 21, LatencyMax: 25, Jitter: 1.5, Loss: 0.25,
		Sent: 4, Recv: 3, Colo: "HKG", RegionName: "中国香港", Loc: "HK",
		ASN: 13335, ASOrg: "Cloudflare", GeoWarn: "代理出口与所在地区不一致",
		SpeedMBps: 7.5, Score: 7.2,
		Trace: map[string]string{"http": "HTTP/1.1 200"},
	}
	for _, f := range Fields() {
		v, ok := Value(rec, f.Key)
		if !ok {
			t.Errorf("字段 %s 取不到值", f.Key)
			continue
		}
		// 含值的字段必须渲染出非空单元格，否则这一列对用户是空的。
		if got := formatCSV(v); got == "" {
			t.Errorf("字段 %s 渲染成空单元格（值 %#v）", f.Key, v)
		}
	}
	if _, ok := Value(rec, "no_such_field"); ok {
		t.Error("未知字段不应返回值")
	}
}

// 未采集的明细应当渲染成空而不是 "null"：CSV 里的 null 会被当成文本。
func TestFormatCSVBranches(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"nil", nil, ""},
		{"字符串", "HKG", "HKG"},
		{"真", true, "是"},
		{"假", false, "否"},
		{"整数", 443, "443"},
		{"ASN 是无符号", uint32(13335), "13335"},
		{"浮点", 7.25, "7.25"},
		{"不可达哨兵", model.Unreachable, ""},
		{"明细对象", map[string]string{"http": "200"}, `{"http":"200"}`},
	}
	for _, tc := range cases {
		if got := formatCSV(tc.in); got != tc.want {
			t.Errorf("%s → %q，期望 %q", tc.name, got, tc.want)
		}
	}
}

// 没有采集明细时，明细列应当是空单元格。
func TestTraceAbsentRendersEmpty(t *testing.T) {
	rec := []model.IPRecord{{IP: "1.1.1.1", Port: 443}}
	out := string(CSV(rec, ResolveFields([]string{"ip", "trace"}), nil, false))
	line := strings.Split(strings.TrimRight(out, "\r\n"), "\r\n")[1]
	if line != "1.1.1.1," {
		t.Errorf("无明细的行 = %q，期望末尾是空单元格", line)
	}
}

func TestFilenameNeverEmpty(t *testing.T) {
	ts := time.Now()
	if got := Filename("{type}", "", "txt", ts); got == "" || got == ".txt" {
		t.Errorf("模板只有占位符时文件名 = %q，应当退回一个可用名字", got)
	}
}

// 预设清单也要下发：前端自己写一份中文名，早晚会与后端对不上。
func TestPresetsAreSelfDescribing(t *testing.T) {
	presets := Presets()
	if len(presets) != 3 {
		t.Fatalf("预设数 = %d，期望 3（全部 / 精简 / 仅 IP:端口）", len(presets))
	}

	known := map[string]bool{}
	for _, f := range Fields() {
		known[f.Key] = true
	}

	ids := map[string]bool{}
	for _, p := range presets {
		if p.ID == "" || p.Name == "" {
			t.Errorf("预设定义不完整：%+v", p)
		}
		if ids[p.ID] {
			t.Errorf("预设标识重复：%s", p.ID)
		}
		ids[p.ID] = true
		if len(p.Keys) == 0 {
			t.Errorf("预设 %s 没有任何字段", p.ID)
		}
		for _, k := range p.Keys {
			if !known[k] {
				t.Errorf("预设 %s 引用了不存在的字段 %q", p.ID, k)
			}
		}
	}

	// 预设的键必须与 PresetKeys 一致，否则前端拿到的是两套。
	for _, p := range presets {
		if len(p.Keys) != len(PresetKeys(p.ID)) {
			t.Errorf("预设 %s 的键与 PresetKeys 不一致", p.ID)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"cloudtrace/internal/config"
	"cloudtrace/internal/exporter"
	"cloudtrace/internal/history"
	"cloudtrace/internal/model"
)

// seedSpeedHistory 存一份带速度与地区的测速历史。
//
// 参数与 seedHistory 不同（它固定存 scan），因为「最新一份」的挑选是按类型
// 分目录的，两类混在一起才能验证优先级。
func seedSpeedHistory(t *testing.T, st *testStack, results []model.IPRecord) history.HistoryRecord {
	t.Helper()
	raw, err := json.Marshal(model.SpeedParams{
		Targets:   results,
		IPVersion: 4,
	})
	if err != nil {
		t.Fatalf("序列化参数失败：%v", err)
	}
	rec, err := st.svc.History.Save(history.HistoryRecord{
		Type:      history.TypeSpeed,
		IPVersion: 4,
		Params:    raw,
		Summary:   model.Summarize(results),
		Results:   results,
	})
	if err != nil {
		t.Fatalf("写入历史失败：%v", err)
	}
	return rec
}

// measuredRecords 造 n 条既有速度又有地区的记录。
func measuredRecords(n int) []model.IPRecord {
	out := make([]model.IPRecord, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, model.IPRecord{
			IP:         fmt.Sprintf("1.1.1.%d", i+1),
			Port:       443,
			Latency:    float64(50 + i*10),
			LatencyAvg: float64(50 + i*10),
			Sent:       3,
			Recv:       3,
			Colo:       []string{"HKG", "NRT"}[i%2],
			RegionName: []string{"中国香港", "日本"}[i%2],
			SpeedMBps:  float64(10 - i),
			Score:      float64(100 - i*5),
		})
	}
	return out
}

// 字段清单必须真的能拿到，而且带中文名——前端一个字都不硬编码。
func TestExportFieldsEndpoint(t *testing.T) {
	st := newTestStack(t, nil)

	resp := get(t, st.ts.URL+exportFieldsRoute)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", resp.StatusCode)
	}

	var body exportFieldsResp
	if err := json.Unmarshal([]byte(bodyOf(t, resp)), &body); err != nil {
		t.Fatalf("响应不是合法 JSON：%v", err)
	}
	if len(body.Fields) != len(exporter.Fields()) {
		t.Fatalf("字段数 = %d，期望 %d", len(body.Fields), len(exporter.Fields()))
	}
	if len(body.Presets) != 3 {
		t.Fatalf("预设数 = %d，期望 3", len(body.Presets))
	}
	for _, f := range body.Fields {
		if f.Key == "" || f.Label == "" || f.Type == "" || f.Group == "" {
			t.Errorf("字段定义不完整：%+v", f)
		}
	}
	for _, want := range []string{config.FormatCSV, config.FormatJSON, config.FormatTXT} {
		found := false
		for _, f := range body.Formats {
			if f == want {
				found = true
			}
		}
		if !found {
			t.Errorf("格式清单里缺少 %s：%v", want, body.Formats)
		}
	}
}

func TestExportFieldsRejectsNonGet(t *testing.T) {
	st := newTestStack(t, nil)
	resp := postJSON(t, st.ts.URL+exportFieldsRoute, `{}`)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("状态码 = %d，期望 405", resp.StatusCode)
	}
}

// 导出命令要返回一个能真的下载到内容的地址。
func TestWSExportProducesDownloadableFile(t *testing.T) {
	st := newTestStack(t, nil)
	seedSpeedHistory(t, st, measuredRecords(3))

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"export","data":{"format":"csv","fields":["ip","port","speed_mbps"]}}`)
	m := readUntil(t, conn, cmdExport, 5*time.Second)

	var resp exportResp
	decode(t, m, &resp)
	if resp.Count != 3 || resp.Total != 3 {
		t.Fatalf("导出条数 = %d/%d，期望 3/3", resp.Count, resp.Total)
	}
	if !strings.HasSuffix(resp.Name, ".csv") {
		t.Errorf("文件名 = %q，期望以 .csv 结尾", resp.Name)
	}
	if !strings.HasPrefix(resp.URL, downloadRoute) {
		t.Fatalf("下载地址 = %q，期望以 %s 开头", resp.URL, downloadRoute)
	}

	dl := get(t, st.ts.URL+resp.URL)
	if dl.StatusCode != http.StatusOK {
		t.Fatalf("下载状态码 = %d，期望 200", dl.StatusCode)
	}
	if ct := dl.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("内容类型 = %q，期望 text/csv", ct)
	}
	body := bodyOf(t, dl)
	if !strings.HasPrefix(body, string(exporter.BOM)) {
		t.Error("CSV 缺少 BOM，Excel 打开会乱码")
	}
	lines := strings.Split(strings.TrimRight(body, "\r\n"), "\r\n")
	if len(lines) != 4 {
		t.Fatalf("CSV 行数 = %d，期望表头 + 3 行", len(lines))
	}
	if !strings.Contains(lines[0], "地址") || !strings.Contains(lines[0], "下载速度") {
		t.Errorf("表头没有中文列名：%q", lines[0])
	}
}

func TestWSExportJSONAndTXT(t *testing.T) {
	st := newTestStack(t, nil)
	seedSpeedHistory(t, st, measuredRecords(2))

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	// JSON：值保持原生类型，前端拿到的不是一堆带引号的数字。
	send(t, conn, `{"type":"export","data":{"format":"json","fields":["ip","port"]}}`)
	var jsonResp exportResp
	decode(t, readUntil(t, conn, cmdExport, 5*time.Second), &jsonResp)

	raw := bodyOf(t, get(t, st.ts.URL+jsonResp.URL))
	var arr []map[string]any
	if err := json.Unmarshal([]byte(raw), &arr); err != nil {
		t.Fatalf("JSON 导出不是合法数组：%v（原文 %s）", err, raw)
	}
	if len(arr) != 2 {
		t.Fatalf("JSON 条数 = %d，期望 2", len(arr))
	}
	if _, ok := arr[0]["port"].(float64); !ok {
		t.Errorf("端口不是数字类型：%T", arr[0]["port"])
	}

	// TXT：每行 ip:port，忽略字段选择。
	send(t, conn, `{"type":"export","data":{"format":"txt"}}`)
	var txtResp exportResp
	decode(t, readUntil(t, conn, cmdExport, 5*time.Second), &txtResp)
	if !strings.HasSuffix(txtResp.Name, ".txt") {
		t.Errorf("文件名 = %q，期望以 .txt 结尾", txtResp.Name)
	}
	txt := strings.TrimRight(bodyOf(t, get(t, st.ts.URL+txtResp.URL)), "\n")
	lines := strings.Split(txt, "\n")
	if len(lines) != 2 {
		t.Fatalf("TXT 行数 = %d，期望 2：%q", len(lines), txt)
	}
	for _, line := range lines {
		if !strings.Contains(line, ":443") {
			t.Errorf("TXT 行 %q 不是 ip:port 形式", line)
		}
	}
}

// IPv6 导出必须自动加方括号，否则下游解析不出端口。
func TestWSExportIPv6GetsBrackets(t *testing.T) {
	st := newTestStack(t, nil)
	seedSpeedHistory(t, st, []model.IPRecord{{
		IP: "2606:4700::1", Port: 8443, Latency: 60, LatencyAvg: 60,
		Sent: 3, Recv: 3, Colo: "HKG", SpeedMBps: 5,
	}})

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"export","data":{"format":"txt"}}`)
	var resp exportResp
	decode(t, readUntil(t, conn, cmdExport, 5*time.Second), &resp)

	body := strings.TrimSpace(bodyOf(t, get(t, st.ts.URL+resp.URL)))
	if body != "[2606:4700::1]:8443" {
		t.Fatalf("TXT 内容 = %q，期望 [2606:4700::1]:8443", body)
	}
}

// 导出默认排除探测失败的节点：哨兵值导出去只会让下游拿到空数据。
func TestWSExportSkipsUnreachedByDefault(t *testing.T) {
	st := newTestStack(t, nil)
	records := measuredRecords(2)
	records = append(records, model.IPRecord{
		IP: "9.9.9.9", Port: 443, Latency: model.Unreachable,
		LatencyAvg: model.Unreachable, Loss: 1, Sent: 3, Recv: 0,
	})
	seedSpeedHistory(t, st, records)

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"export","data":{"format":"txt"}}`)
	var resp exportResp
	decode(t, readUntil(t, conn, cmdExport, 5*time.Second), &resp)

	if resp.Count != 2 || resp.Total != 3 {
		t.Fatalf("条数 = %d/%d，期望筛掉 1 条后 2/3", resp.Count, resp.Total)
	}
	if strings.Contains(bodyOf(t, get(t, st.ts.URL+resp.URL)), "9.9.9.9") {
		t.Error("探测失败的节点被导出了")
	}

	// 显式要求时才带上。
	send(t, conn, `{"type":"export","data":{"format":"txt","filter":{"include_unreached":true}}}`)
	var all exportResp
	decode(t, readUntil(t, conn, cmdExport, 5*time.Second), &all)
	if all.Count != 3 {
		t.Fatalf("显式带上不可达节点后条数 = %d，期望 3", all.Count)
	}
}

func TestWSExportFiltersByRegionAndLatency(t *testing.T) {
	st := newTestStack(t, nil)
	seedSpeedHistory(t, st, measuredRecords(5))

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	// 地区筛选大小写不敏感。
	send(t, conn, `{"type":"export","data":{"format":"txt","filter":{"regions":["hkg"]}}}`)
	var byRegion exportResp
	decode(t, readUntil(t, conn, cmdExport, 5*time.Second), &byRegion)
	body := bodyOf(t, get(t, st.ts.URL+byRegion.URL))
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		if line == "" {
			continue
		}
		// measuredRecords 的 HKG 是奇数号（i 为偶数），延迟 50/70/90。
		if strings.Contains(line, "1.1.1.2") || strings.Contains(line, "1.1.1.4") {
			t.Errorf("地区筛选把非 HKG 的 %q 也带上了", line)
		}
	}
	if byRegion.Count != 3 {
		t.Errorf("HKG 条数 = %d，期望 3", byRegion.Count)
	}

	send(t, conn, `{"type":"export","data":{"format":"txt","filter":{"max_latency":75}}}`)
	var byLatency exportResp
	decode(t, readUntil(t, conn, cmdExport, 5*time.Second), &byLatency)
	if byLatency.Count != 3 {
		t.Errorf("延迟 <=75ms 的条数 = %d，期望 3", byLatency.Count)
	}
}

// 排序在服务端做，导出的顺序要跟着排序参数走。
func TestWSExportRespectsSort(t *testing.T) {
	st := newTestStack(t, nil)
	seedSpeedHistory(t, st, measuredRecords(3))

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"export","data":{"format":"txt","sort":"speed_mbps"}}`)
	var asc exportResp
	decode(t, readUntil(t, conn, cmdExport, 5*time.Second), &asc)
	// speed_mbps 的自然方向是降序，最大速度的 1.1.1.1 应当在第一行。
	first := strings.Split(strings.TrimSpace(bodyOf(t, get(t, st.ts.URL+asc.URL))), "\n")[0]
	if !strings.Contains(first, "1.1.1.1") {
		t.Errorf("按速度排序后首行 = %q，期望 1.1.1.1（速度最快）", first)
	}

	// 显式反向。
	desc := false
	payload, _ := json.Marshal(exportReq{Format: "txt", Sort: "speed_mbps", Desc: &desc})
	send(t, conn, fmt.Sprintf(`{"type":"export","data":%s}`, payload))
	var reversed exportResp
	decode(t, readUntil(t, conn, cmdExport, 5*time.Second), &reversed)
	first = strings.Split(strings.TrimSpace(bodyOf(t, get(t, st.ts.URL+reversed.URL))), "\n")[0]
	if !strings.Contains(first, "1.1.1.3") {
		t.Errorf("显式升序后首行 = %q，期望 1.1.1.3（速度最慢）", first)
	}
}

// 指定历史 ID 时导出那一份，而不是最新一份。
func TestWSExportByHistoryID(t *testing.T) {
	st := newTestStack(t, nil)
	old := seedSpeedHistory(t, st, measuredRecords(2))
	// 再存一份不同参数的，确保「最新一份」不是 old。
	seedSpeedHistory(t, st, measuredRecords(4))

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, fmt.Sprintf(`{"type":"export","data":{"id":%q,"format":"txt"}}`, old.ID))
	var resp exportResp
	decode(t, readUntil(t, conn, cmdExport, 5*time.Second), &resp)
	if resp.Total != 2 {
		t.Fatalf("按 ID 导出的总数 = %d，期望 2", resp.Total)
	}
}

func TestWSExportRejectsBadParams(t *testing.T) {
	st := newTestStack(t, nil)
	seedSpeedHistory(t, st, measuredRecords(1))

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	cases := []struct {
		name string
		body string
	}{
		{"未知格式", `{"type":"export","data":{"format":"xml"}}`},
		{"未知排序维度", `{"type":"export","data":{"sort":"magic"}}`},
		{"未知结果类型", `{"type":"export","data":{"type":"ping"}}`},
		{"载荷不是 JSON", `{"type":"export","data":"nope"}`},
	}
	for _, c := range cases {
		send(t, conn, c.body)
		m := readUntil(t, conn, eventError, 5*time.Second)
		var p errorPayload
		decode(t, m, &p)
		if p.Code != CodeInvalidParam {
			t.Errorf("%s 的错误码 = %q，期望 %q", c.name, p.Code, CodeInvalidParam)
		}
	}
}

// 没有任何历史时导出要给一句能照做的提示，而不是「未知错误」。
func TestWSExportWithoutHistory(t *testing.T) {
	st := newTestStack(t, nil)

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"export","data":{"format":"csv"}}`)
	var p errorPayload
	decode(t, readUntil(t, conn, eventError, 5*time.Second), &p)
	if p.Code != CodeNotFound {
		t.Fatalf("错误码 = %q，期望 %q", p.Code, CodeNotFound)
	}
	if !strings.Contains(p.Msg, "扫描") {
		t.Errorf("提示信息没有告诉用户该做什么：%q", p.Msg)
	}
}

func TestDownloadUnknownID(t *testing.T) {
	st := newTestStack(t, nil)

	resp := get(t, st.ts.URL+downloadRoute+"deadbeef")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("状态码 = %d，期望 404", resp.StatusCode)
	}
	var p errorPayload
	if err := json.Unmarshal([]byte(bodyOf(t, resp)), &p); err != nil {
		t.Fatalf("响应不是合法 JSON：%v", err)
	}
	if p.Code != CodeNotFound {
		t.Errorf("错误码 = %q，期望 %q", p.Code, CodeNotFound)
	}
}

func TestDownloadRejectsNonGet(t *testing.T) {
	st := newTestStack(t, nil)
	resp := postJSON(t, st.ts.URL+downloadRoute+"x", `{}`)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("状态码 = %d，期望 405", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// 下载登记表
// ---------------------------------------------------------------------------

func TestDownloadStorePutGet(t *testing.T) {
	d := newDownloadStore()

	id, err := d.put("a.csv", "text/csv", []byte("hello"))
	if err != nil {
		t.Fatalf("登记失败：%v", err)
	}
	if len(id) != downloadIDBytes*2 {
		t.Errorf("标识长度 = %d，期望 %d", len(id), downloadIDBytes*2)
	}

	f, ok := d.get(id)
	if !ok {
		t.Fatal("刚登记的导出物取不到")
	}
	if f.name != "a.csv" || f.contentType != "text/csv" || string(f.data) != "hello" {
		t.Fatalf("取出的内容不对：%+v", f)
	}

	if _, ok := d.get("不存在"); ok {
		t.Error("不存在的标识不该取到内容")
	}
	if _, ok := d.get(""); ok {
		t.Error("空标识不该取到内容")
	}
}

func TestDownloadStoreExpires(t *testing.T) {
	d := newDownloadStore()
	now := time.Now()
	d.now = func() time.Time { return now }

	id, err := d.put("a.csv", "text/csv", []byte("x"))
	if err != nil {
		t.Fatalf("登记失败：%v", err)
	}
	if _, ok := d.get(id); !ok {
		t.Fatal("刚登记的导出物取不到")
	}

	now = now.Add(downloadTTL + time.Second)
	if _, ok := d.get(id); ok {
		t.Error("过期的导出物还能取到")
	}
	if d.count() != 0 {
		t.Errorf("过期清理后还剩 %d 份", d.count())
	}
}

func TestDownloadStoreEvictsOldest(t *testing.T) {
	d := newDownloadStore()

	ids := make([]string, 0, maxDownloads+3)
	for i := 0; i < maxDownloads+3; i++ {
		id, err := d.put(fmt.Sprintf("f%d.csv", i), "text/csv", []byte("x"))
		if err != nil {
			t.Fatalf("登记失败：%v", err)
		}
		ids = append(ids, id)
	}

	if d.count() != maxDownloads {
		t.Fatalf("保留份数 = %d，期望 %d", d.count(), maxDownloads)
	}
	if _, ok := d.get(ids[0]); ok {
		t.Error("最旧的一份没有被淘汰")
	}
	if _, ok := d.get(ids[len(ids)-1]); !ok {
		t.Error("最新的一份不该被淘汰")
	}
}

func TestContentDispositionHandlesChineseName(t *testing.T) {
	got := contentDisposition("扫描结果.csv")
	if !strings.Contains(got, "filename*=UTF-8''") {
		t.Errorf("缺少 RFC 5987 编码的文件名：%q", got)
	}
	if strings.Contains(got, "扫描结果") {
		t.Errorf("裸的中文名不该出现在响应头里：%q", got)
	}
	// 引号与反斜杠不能出现在 ASCII 兜底名里：它们能提前结束字符串。
	if strings.Contains(strings.Split(got, "filename*=")[0], `\`) {
		t.Errorf("ASCII 兜底名里有反斜杠：%q", got)
	}
}

func TestAsciiFallback(t *testing.T) {
	cases := map[string]string{
		"a.csv": "a.csv",
		// 非 ASCII 是逐字节替换的：一个中文字在 UTF-8 里是三个字节。
		"中.csv":      "___.csv",
		`bad"name`:   "bad_name",
		`back\slash`: "back_slash",
		"":           "export",
		"中":          "___",
	}
	for in, want := range cases {
		if got := asciiFallback(in); got != want {
			t.Errorf("asciiFallback(%q) = %q，期望 %q", in, got, want)
		}
	}
	// 结果必须是纯 ASCII，否则兜底就失去意义了。
	for _, in := range []string{"中.csv", "cloudtrace_测速.csv"} {
		for i := 0; i < len(asciiFallback(in)); i++ {
			if c := asciiFallback(in)[i]; c > 0x7e {
				t.Fatalf("asciiFallback(%q) 里还有非 ASCII 字节：%q", in, asciiFallback(in))
			}
		}
	}
}

func TestRFC5987Escape(t *testing.T) {
	if got := rfc5987Escape("a-b_c.txt"); got != "a-b_c.txt" {
		t.Errorf("允许的字符被转义了：%q", got)
	}
	if got := rfc5987Escape("a b"); got != "a%20b" {
		t.Errorf("空格 = %q，期望 a%%20b", got)
	}
	if got := rfc5987Escape("中"); got != "%E4%B8%AD" {
		t.Errorf("中文 = %q，期望 %%E4%%B8%%AD", got)
	}
	// 单引号必须转义：它是 filename* 里分隔字符集与值的那一个。
	if got := rfc5987Escape("a'b"); got != "a%27b" {
		t.Errorf("单引号 = %q，期望 a%%27b", got)
	}
}

func TestFilterRecordsEdgeCases(t *testing.T) {
	records := []model.IPRecord{
		{IP: "1.0.0.1", Port: 443, Latency: 10, LatencyAvg: 10, Sent: 1, Recv: 1, Colo: "HKG"},
		{IP: "1.0.0.2", Port: 443, Latency: model.Unreachable, Loss: 1, Sent: 1, Recv: 0},
	}

	// 空筛选条件只排除不可达节点。
	if got := filterRecords(records, exportFilter{}); len(got) != 1 || got[0].IP != "1.0.0.1" {
		t.Fatalf("空筛选结果 = %+v", got)
	}
	// 空地区列表不能变成「谁都不匹配」。
	if got := filterRecords(records, exportFilter{Regions: []string{"  "}}); len(got) != 1 {
		t.Fatalf("只有空白的地区列表被当成了筛选条件：%+v", got)
	}
	// 延迟上限对不可达节点不生效（那是 IncludeUnreached 管的事）。
	got := filterRecords(records, exportFilter{IncludeUnreached: true, MaxLatency: 5})
	if len(got) != 1 || got[0].IP != "1.0.0.2" {
		t.Fatalf("延迟上限不该筛掉不可达节点：%+v", got)
	}
}

func TestNormalizeRegions(t *testing.T) {
	if got := normalizeRegions(nil); got != nil {
		t.Errorf("空列表应当返回 nil：%v", got)
	}
	got := normalizeRegions([]string{" hkg ", "NRT", ""})
	if len(got) != 2 || !got["HKG"] || !got["NRT"] {
		t.Errorf("归一结果 = %v", got)
	}
}

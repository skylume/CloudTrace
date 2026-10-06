package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cloudtrace/internal/config"
	"cloudtrace/internal/model"
)

// latestBody 取 /latest 的文本内容。
func latestBody(t *testing.T, st *testStack) string {
	t.Helper()
	resp := get(t, st.ts.URL+latestRoute)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", resp.StatusCode)
	}
	return bodyOf(t, resp)
}

// 本地结果地址给的是可直接喂给下游的 ip:port 清单，探测失败的节点不能混进来。
func TestLatestPlainText(t *testing.T) {
	st := newTestStack(t, nil)
	records := []model.IPRecord{
		{IP: "1.1.1.1", Port: 443, Latency: 90, LatencyAvg: 90, Sent: 3, Recv: 3, Loss: 0, Colo: "HKG"},
		{IP: "1.1.1.2", Port: 443, Latency: 30, LatencyAvg: 30, Sent: 3, Recv: 3, Loss: 0, Colo: "NRT"},
		{IP: "9.9.9.9", Port: 443, Latency: model.Unreachable, LatencyAvg: model.Unreachable, Loss: 1, Sent: 3, Recv: 0},
	}
	seedSpeedHistory(t, st, records)

	resp := get(t, st.ts.URL+latestRoute)
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("内容类型 = %q，期望 text/plain", ct)
	}

	lines := strings.Split(strings.TrimRight(bodyOf(t, resp), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("/latest 行数 = %d，期望 2（不可达的节点要排除）：%q", len(lines), lines)
	}
	// 默认排序是丢包升序 → 延迟升序。
	if lines[0] != "1.1.1.2:443" || lines[1] != "1.1.1.1:443" {
		t.Fatalf("/latest 内容 = %q，期望按延迟升序", lines)
	}
}

func TestLatestJSON(t *testing.T) {
	st := newTestStack(t, nil)
	seedSpeedHistory(t, st, measuredRecords(3))

	resp := get(t, st.ts.URL+latestJSONRoute)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("内容类型 = %q，期望 application/json", ct)
	}

	var records []model.IPRecord
	if err := json.Unmarshal([]byte(bodyOf(t, resp)), &records); err != nil {
		t.Fatalf("响应不是合法 JSON 数组：%v", err)
	}
	if len(records) != 3 {
		t.Fatalf("条数 = %d，期望 3", len(records))
	}
	// 给的是完整记录，脚本可以按地区或速度自己再筛。
	if records[0].Colo == "" || records[0].SpeedMBps == 0 {
		t.Errorf("记录缺少地区或速度字段：%+v", records[0])
	}
}

// 同一批结果在文本与 JSON 两个地址上的顺序必须一致。
func TestLatestPlainAndJSONAgree(t *testing.T) {
	st := newTestStack(t, nil)
	seedSpeedHistory(t, st, measuredRecords(4))

	text := strings.Split(strings.TrimRight(latestBody(t, st), "\n"), "\n")

	resp := get(t, st.ts.URL+latestJSONRoute)
	var records []model.IPRecord
	if err := json.Unmarshal([]byte(bodyOf(t, resp)), &records); err != nil {
		t.Fatalf("响应不是合法 JSON：%v", err)
	}
	if len(text) != len(records) {
		t.Fatalf("文本 %d 行、JSON %d 条，两者不一致", len(text), len(records))
	}
	for i, rec := range records {
		if !strings.HasPrefix(text[i], rec.IP+":") {
			t.Fatalf("第 %d 行 = %q，与 JSON 的 %s 不一致", i, text[i], rec.IP)
		}
	}
}

// 测速结果优先于扫描结果：测速结果才是优选过的。
func TestLatestPrefersSpeedRecord(t *testing.T) {
	st := newTestStack(t, nil)
	seedHistory(t, st, scanParamsForTest(150), testRecords(5))
	seedSpeedHistory(t, st, measuredRecords(2))

	resp := get(t, st.ts.URL+latestJSONRoute)
	var records []model.IPRecord
	if err := json.Unmarshal([]byte(bodyOf(t, resp)), &records); err != nil {
		t.Fatalf("响应不是合法 JSON：%v", err)
	}
	if len(records) != 2 {
		t.Fatalf("条数 = %d，期望取测速结果的 2 条", len(records))
	}
	if records[0].SpeedMBps == 0 {
		t.Error("取到的是扫描结果（没有速度），应当优先测速结果")
	}
}

// 没有测速结果时退回扫描结果，而不是报错。
func TestLatestFallsBackToScanRecord(t *testing.T) {
	st := newTestStack(t, nil)
	seedHistory(t, st, scanParamsForTest(150), testRecords(3))

	resp := get(t, st.ts.URL+latestRoute)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", resp.StatusCode)
	}
	lines := strings.Split(strings.TrimRight(bodyOf(t, resp), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("行数 = %d，期望 3：%q", len(lines), lines)
	}
}

// IPv6 也要加方括号，否则下游解析不出端口。
func TestLatestBracketsIPv6(t *testing.T) {
	st := newTestStack(t, nil)
	seedSpeedHistory(t, st, []model.IPRecord{{
		IP: "2606:4700::1", Port: 8443, Latency: 50, LatencyAvg: 50,
		Sent: 3, Recv: 3, Colo: "HKG",
	}})

	if got := strings.TrimSpace(latestBody(t, st)); got != "[2606:4700::1]:8443" {
		t.Fatalf("/latest 内容 = %q，期望 [2606:4700::1]:8443", got)
	}
}

// 还没跑过任务时给 404，脚本靠状态码就能判断，不必猜空响应是什么意思。
func TestLatestWithoutHistoryReturns404(t *testing.T) {
	st := newTestStack(t, nil)

	for _, path := range []string{latestRoute, latestJSONRoute} {
		resp := get(t, st.ts.URL+path)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s 状态码 = %d，期望 404", path, resp.StatusCode)
		}
		var p errorPayload
		if err := json.Unmarshal([]byte(bodyOf(t, resp)), &p); err != nil {
			t.Fatalf("%s 响应不是合法 JSON：%v", path, err)
		}
		if p.Code != CodeNotFound {
			t.Errorf("%s 错误码 = %q，期望 %q", path, p.Code, CodeNotFound)
		}
	}
}

// 本地结果地址暴露的是用户的 IP 列表，绑到局域网时必须鉴权。
func TestLatestRequiresAuthWhenBoundPublicly(t *testing.T) {
	st := newTestStack(t, func(c *config.Config) {
		c.Server.Bind = "0.0.0.0"
		// 绑到局域网时必须配访问 Token，否则校验就不让保存。
		c.Server.Token = "test-token"
	})

	// 必须伪造来源地址：httptest 的请求都来自回环，而回环是免鉴权的
	// （见 TestLoopbackStaysExemptWhenBoundPublicly）。不伪造的话这条用例
	// 测的是「本机」，而它想测的是「局域网来的请求」。
	for _, path := range []string{latestRoute, latestJSONRoute, exportFieldsRoute} {
		rec := remoteRequest(t, st, http.MethodGet, path)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s 状态码 = %d，期望 401", path, rec.Code)
		}
	}
}

// HEAD 只要响应头。
func TestLatestHeadRequest(t *testing.T) {
	st := newTestStack(t, nil)
	seedSpeedHistory(t, st, measuredRecords(2))

	req, err := http.NewRequest(http.MethodHead, st.ts.URL+latestRoute, nil)
	if err != nil {
		t.Fatalf("构造请求失败：%v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败：%v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", resp.StatusCode)
	}
	if resp.Header.Get("Content-Length") == "" {
		t.Error("HEAD 响应缺少 Content-Length")
	}
	if bodyOf(t, resp) != "" {
		t.Error("HEAD 请求不该带响应体")
	}
}

func TestLatestOrderIsStable(t *testing.T) {
	// 排序结果必须稳定：同一份数据连续取两次要一模一样。
	st := newTestStack(t, nil)
	seedSpeedHistory(t, st, measuredRecords(5))

	first := latestBody(t, st)
	time.Sleep(5 * time.Millisecond)
	if second := latestBody(t, st); first != second {
		t.Fatalf("两次取值不一致：\n%q\n%q", first, second)
	}
}

// 没有结果与查不出来的状态码必须区分开，脚本靠它决定要不要重试。
func TestWriteLatestErrorMapping(t *testing.T) {
	rec := httptest.NewRecorder()
	writeLatestError(rec, fail(CodeNotFound, "还没有结果"))
	if rec.Code != http.StatusNotFound {
		t.Errorf("not_found 的状态码 = %d，期望 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), CodeNotFound) {
		t.Errorf("响应体缺少错误码：%s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	writeLatestError(rec, fail(CodeInvalidParam, "参数不对"))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid_param 的状态码 = %d，期望 400", rec.Code)
	}

	rec = httptest.NewRecorder()
	writeLatestError(rec, errors.New("别的问题"))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("其它错误的状态码 = %d，期望 500", rec.Code)
	}
}

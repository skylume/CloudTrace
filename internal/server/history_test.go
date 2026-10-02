package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"cloudtrace/internal/config"
	"cloudtrace/internal/history"
	"cloudtrace/internal/model"
	"cloudtrace/internal/task"
)

// unitServer 造一个只用于调用内部方法的 server，不启动 HTTP 服务。
//
// 存档与命令处理都是 server 上的方法，直接构造能省掉一整条「起 HTTP → 连
// WS → 跑真实网络任务」的链路；真实链路留给端到端用例。
func unitServer(t *testing.T, st *testStack) *server {
	t.Helper()
	return &server{
		cfg:    st.store,
		svc:    st.svc,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// seedHistory 往历史里存一份记录。
func seedHistory(t *testing.T, st *testStack, params any, results []model.IPRecord) history.HistoryRecord {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("序列化参数失败：%v", err)
	}
	rec, err := st.svc.History.Save(history.HistoryRecord{
		Type:      history.TypeScan,
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

func TestWSHistoryListEmpty(t *testing.T) {
	st := newTestStack(t, nil)
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"history/list"}`)

	m := readUntil(t, conn, cmdHistoryList, 3*time.Second)
	var resp historyListResp
	decode(t, m, &resp)
	if resp.Total != 0 || len(resp.Entries) != 0 {
		t.Fatalf("空历史返回了 %+v", resp)
	}
}

func TestWSHistoryListAndGet(t *testing.T) {
	st := newTestStack(t, nil)
	rec := seedHistory(t, st, scanParamsForTest(150), testRecords(3))

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"history/list","data":{"filter":{"type":"scan","ip_version":4}}}`)
	m := readUntil(t, conn, cmdHistoryList, 3*time.Second)
	var list historyListResp
	decode(t, m, &list)
	if list.Total != 1 || list.Entries[0].ID != rec.ID {
		t.Fatalf("列表 = %+v，期望一条 %s", list, rec.ID)
	}
	if list.Entries[0].Count != 3 {
		t.Fatalf("索引条目条数 = %d，期望 3", list.Entries[0].Count)
	}

	send(t, conn, fmt.Sprintf(`{"type":"history/get","data":{"id":%q}}`, rec.ID))
	m = readUntil(t, conn, cmdHistoryGet, 3*time.Second)
	var get historyGetResp
	decode(t, m, &get)
	if get.Record.ID != rec.ID || len(get.Record.Results) != 3 {
		t.Fatalf("详情 = %+v，期望 %s 且带 3 条结果", get.Record, rec.ID)
	}
}

func TestWSHistoryGetMissingReturnsNotFound(t *testing.T) {
	st := newTestStack(t, nil)
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"history/get","data":{"id":"20260927_143012_abcd0001"}}`)

	m := readUntil(t, conn, eventError, 3*time.Second)
	var p errorPayload
	decode(t, m, &p)
	if p.Code != CodeNotFound {
		t.Fatalf("code = %q，期望 %q", p.Code, CodeNotFound)
	}
}

// 加载历史时要能看出「这份是别的参数跑的」，而不是静默套用。
func TestWSHistoryLoadReportsParamDiff(t *testing.T) {
	st := newTestStack(t, nil)
	rec := seedHistory(t, st, scanParamsForTest(150), testRecords(2))

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	current, _ := json.Marshal(scanParamsForTest(200))
	send(t, conn, fmt.Sprintf(`{"type":"history/load","data":{"id":%q,"current":%s}}`, rec.ID, current))

	m := readUntil(t, conn, cmdHistoryLoad, 3*time.Second)
	var resp historyLoadResp
	decode(t, m, &resp)
	if resp.Record.ID != rec.ID {
		t.Fatalf("加载到的记录 = %s，期望 %s", resp.Record.ID, rec.ID)
	}
	if len(resp.ParamDiff) != 1 {
		t.Fatalf("参数差异 = %+v，期望只有并发数一项", resp.ParamDiff)
	}
	if resp.ParamDiff[0].Key != "workers" || resp.ParamDiff[0].Label != "并发数" {
		t.Fatalf("差异项 = %+v，期望 workers/并发数", resp.ParamDiff[0])
	}
	if resp.ParamDiff[0].From != "150" || resp.ParamDiff[0].To != "200" {
		t.Fatalf("差异值 = %s → %s，期望 150 → 200", resp.ParamDiff[0].From, resp.ParamDiff[0].To)
	}
}

func TestWSHistoryLoadWithIdenticalParamsHasNoDiff(t *testing.T) {
	st := newTestStack(t, nil)
	rec := seedHistory(t, st, scanParamsForTest(150), testRecords(2))

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	current, _ := json.Marshal(scanParamsForTest(150))
	send(t, conn, fmt.Sprintf(`{"type":"history/load","data":{"id":%q,"current":%s}}`, rec.ID, current))

	m := readUntil(t, conn, cmdHistoryLoad, 3*time.Second)
	var resp historyLoadResp
	decode(t, m, &resp)
	if len(resp.ParamDiff) != 0 {
		t.Fatalf("参数一致却报出差异：%+v", resp.ParamDiff)
	}
}

func TestWSHistoryDeleteReturnsUndoWindow(t *testing.T) {
	st := newTestStack(t, nil)
	rec := seedHistory(t, st, scanParamsForTest(150), testRecords(2))

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, fmt.Sprintf(`{"type":"history/delete","data":{"id":%q}}`, rec.ID))
	m := readUntil(t, conn, cmdHistoryDelete, 3*time.Second)
	var resp historyDeleteResp
	decode(t, m, &resp)
	if resp.ID != rec.ID {
		t.Fatalf("删除响应 = %+v", resp)
	}
	if resp.UndoMS <= 0 {
		t.Fatalf("可撤销窗口 = %d，必须为正，否则前端来不及给撤销入口", resp.UndoMS)
	}

	// 撤销窗口内还能救回来。
	if err := st.svc.History.Restore(rec.ID); err != nil {
		t.Fatalf("撤销失败：%v", err)
	}
	if _, err := st.svc.History.Load(rec.ID); err != nil {
		t.Fatalf("撤销后读不到记录：%v", err)
	}
}

func TestWSHistoryTag(t *testing.T) {
	st := newTestStack(t, nil)
	rec := seedHistory(t, st, scanParamsForTest(150), testRecords(2))

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, fmt.Sprintf(`{"type":"history/tag","data":{"id":%q,"tags":["移动家宽"],"note":"晚高峰","starred":true}}`, rec.ID))
	m := readUntil(t, conn, cmdHistoryTag, 3*time.Second)
	var resp historyTagResp
	decode(t, m, &resp)
	if resp.ID != rec.ID {
		t.Fatalf("打标签响应 = %+v", resp)
	}

	loaded, err := st.svc.History.Load(rec.ID)
	if err != nil {
		t.Fatalf("读取失败：%v", err)
	}
	if !loaded.Starred || loaded.Note != "晚高峰" || len(loaded.Tags) != 1 {
		t.Fatalf("标注未生效：%+v", loaded)
	}
}

// 对比两份参数不同的历史。
//
// 参数必须不同：同参数的重复存档会被自动去重合并成一份，那样第二份记录根本
// 不存在，对比也就无从谈起。这也正是「想比两次不同参数的跑分」的常见用法。
func TestWSHistoryCompare(t *testing.T) {
	st := newTestStack(t, nil)
	a := seedHistory(t, st, scanParamsForTest(150), []model.IPRecord{
		{IP: "1.1.1.1", Port: 443, Latency: 100, LatencyAvg: 100, Sent: 3, Recv: 3, Colo: "HKG"},
		{IP: "1.1.1.2", Port: 443, Latency: 200, LatencyAvg: 200, Sent: 3, Recv: 3, Colo: "NRT"},
	})
	b := seedHistory(t, st, scanParamsForTest(200), []model.IPRecord{
		{IP: "1.1.1.1", Port: 443, Latency: 100, LatencyAvg: 100, Sent: 3, Recv: 3, Colo: "HKG"},
		{IP: "1.1.1.3", Port: 443, Latency: 50, LatencyAvg: 50, Sent: 3, Recv: 3, Colo: "HKG"},
	})

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, fmt.Sprintf(`{"type":"history/compare","data":{"idA":%q,"idB":%q}}`, a.ID, b.ID))
	m := readUntil(t, conn, cmdHistoryCompare, 3*time.Second)
	var resp historyCompareResp
	decode(t, m, &resp)

	if resp.Diff.AddedCount != 1 || resp.Diff.RemovedCount != 1 || resp.Diff.UnchangedCount != 1 {
		t.Fatalf("对比结果 = %+v，期望 新增 1 / 消失 1 / 未变 1", resp.Diff)
	}
}

func TestWSHistoryCompareRequiresBothIDs(t *testing.T) {
	st := newTestStack(t, nil)
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"history/compare","data":{"idA":"20260927_143012_abcd0001"}}`)

	m := readUntil(t, conn, eventError, 3*time.Second)
	var p errorPayload
	decode(t, m, &p)
	if p.Code != CodeInvalidParam {
		t.Fatalf("code = %q，期望 %q", p.Code, CodeInvalidParam)
	}
}

// 历史有变更要广播出去，否则另一个标签页的列表不会刷新。
func TestWSHistoryChangedIsBroadcast(t *testing.T) {
	st := newTestStack(t, nil)
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	rec := seedHistory(t, st, scanParamsForTest(150), testRecords(1))

	m := readUntil(t, conn, history.TopicChanged, 3*time.Second)
	var change history.Change
	decode(t, m, &change)
	if change.ID != rec.ID || change.Action != history.ActionSave {
		t.Fatalf("变更事件 = %+v，期望 save/%s", change, rec.ID)
	}
}

// --- 存档规则 ---

func TestArchiveWritesRecordWithSnapshot(t *testing.T) {
	st := newTestStack(t, nil)
	srv := unitServer(t, st)

	params := scanParamsForTest(150)
	results := testRecords(3)
	srv.archiveScan(params, "标准", model.NewTaskResult(results), 12.5)

	entries, err := st.svc.History.List(history.Filter{})
	if err != nil {
		t.Fatalf("列表失败：%v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("历史条目数 = %d，期望 1", len(entries))
	}
	if entries[0].Preset != "标准" || entries[0].Duration != 12.5 {
		t.Fatalf("索引条目 = %+v，期望带上档位与耗时", entries[0])
	}

	rec, err := st.svc.History.Load(entries[0].ID)
	if err != nil {
		t.Fatalf("读取失败：%v", err)
	}
	got, err := rec.ScanParams()
	if err != nil {
		t.Fatalf("解析参数快照失败：%v", err)
	}
	if got.Workers != params.Workers || got.Port != params.Port {
		t.Fatalf("参数快照 = %+v，期望 %+v", got, params)
	}
	if rec.Count != 3 || len(rec.Results) != 3 {
		t.Fatalf("结果条数 = %d/%d，期望 3", rec.Count, len(rec.Results))
	}
}

// 自动存档关掉之后，任务照跑但不留痕。
func TestArchiveRespectsAutoSaveOff(t *testing.T) {
	st := newTestStack(t, func(c *config.Config) { c.History.AutoSave = false })
	srv := unitServer(t, st)

	srv.archiveScan(scanParamsForTest(150), "标准", model.NewTaskResult(testRecords(2)), 1)

	entries, _ := st.svc.History.List(history.Filter{})
	if len(entries) != 0 {
		t.Fatalf("自动存档已关闭，却写入了 %d 条", len(entries))
	}
}

// IP 版本异常时宁可丢弃也不能归档到错的地方：一旦落到 ipv6/ 目录，用户
// 按版本筛选就再也找不到它了。
func TestArchiveRejectsUnknownIPVersion(t *testing.T) {
	st := newTestStack(t, nil)
	srv := unitServer(t, st)

	srv.archiveScan(model.ScanParams{IPVersion: 0}, "", model.NewTaskResult(testRecords(2)), 1)

	entries, _ := st.svc.History.List(history.Filter{})
	if len(entries) != 0 {
		t.Fatalf("IP 版本非法却写入了 %d 条", len(entries))
	}
}

func TestArchiveSpeedMarksQualified(t *testing.T) {
	st := newTestStack(t, nil)
	srv := unitServer(t, st)

	results := []model.IPRecord{
		{IP: "1.1.1.1", Port: 443, SpeedMBps: 8, LatencyAvg: 60, Recv: 3, Sent: 3},
		{IP: "1.1.1.2", Port: 443, SpeedMBps: 0, LatencyAvg: 80, Recv: 3, Sent: 3},
	}
	srv.archiveSpeed(model.SpeedParams{IPVersion: 4, Scope: model.SpeedScopeSingle},
		"标准", model.NewTaskResult(results), 8)

	entries, _ := st.svc.History.List(history.Filter{Type: history.TypeSpeed})
	if len(entries) != 1 {
		t.Fatalf("测速历史条目数 = %d，期望 1", len(entries))
	}
	rec, err := st.svc.History.Load(entries[0].ID)
	if err != nil {
		t.Fatalf("读取失败：%v", err)
	}
	if rec.Summary.Qualified != 1 {
		t.Fatalf("合格数 = %d，期望只数真正测到速度的那条", rec.Summary.Qualified)
	}
	if rec.Type != history.TypeSpeed {
		t.Fatalf("类型 = %q，期望 speed", rec.Type)
	}
}

// 中止的任务绝不能留下历史：那会让用户在列表里挑到一份根本没扫完的节点，
// 而且看起来和完整结果毫无区别。
func TestTaskRunnerSkipsArchiveOnAbort(t *testing.T) {
	st := newTestStack(t, nil)

	var archived bool
	run := taskRunner(func(rep task.Reporter) (int, error) {
		// 模拟用户在跑的过程中按了停止。
		st.svc.Tasks.Abort()
		<-rep.Context().Done()
		return 2, errors.New("已取消")
	}, func(float64) { archived = true })

	if err := st.svc.Tasks.Start(model.PhaseScan, run); err != nil {
		t.Fatalf("启动任务失败：%v", err)
	}
	waitTaskIdle(t, st)

	if archived {
		t.Fatal("中止的任务被存档了")
	}
	entries, _ := st.svc.History.List(history.Filter{})
	if len(entries) != 0 {
		t.Fatalf("中止的任务留下了 %d 条历史", len(entries))
	}
}

func TestTaskRunnerSkipsArchiveOnError(t *testing.T) {
	st := newTestStack(t, nil)

	var archived bool
	run := taskRunner(func(task.Reporter) (int, error) {
		return 0, errors.New("没有生成任何候选")
	}, func(float64) { archived = true })

	if err := st.svc.Tasks.Start(model.PhaseScan, run); err != nil {
		t.Fatalf("启动任务失败：%v", err)
	}
	waitTaskIdle(t, st)

	if archived {
		t.Fatal("失败的任务被存档了")
	}
}

func TestTaskRunnerArchivesOnSuccessWithDuration(t *testing.T) {
	st := newTestStack(t, nil)

	var got float64
	var called bool
	run := taskRunner(func(task.Reporter) (int, error) {
		time.Sleep(5 * time.Millisecond)
		return 7, nil
	}, func(d float64) {
		called = true
		got = d
	})

	if err := st.svc.Tasks.Start(model.PhaseScan, run); err != nil {
		t.Fatalf("启动任务失败：%v", err)
	}
	waitTaskIdle(t, st)

	if !called {
		t.Fatal("任务正常跑完却没有存档")
	}
	if got <= 0 {
		t.Fatalf("耗时 = %v，期望为正", got)
	}
}

// waitTaskIdle 等任务退出运行态。
func waitTaskIdle(t *testing.T, st *testStack) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for st.svc.Tasks.Running() {
		if time.Now().After(deadline) {
			t.Fatal("任务在 5 秒内没有结束")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// scanParamsForTest 造一份扫描参数。
func scanParamsForTest(workers int) model.ScanParams {
	return model.ScanParams{
		Mode:             "tcping",
		Workers:          workers,
		SampleMax:        200,
		LatencyThreshold: 230,
		PingTimes:        3,
		Port:             443,
		IPVersion:        4,
		SourceMode:       "official",
		TimeoutMS:        1000,
	}
}

// testRecords 造 n 条带地区信息的结果。
func testRecords(n int) []model.IPRecord {
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
		})
	}
	return out
}

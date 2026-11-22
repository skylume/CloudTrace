package task

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"cloudtrace/internal/event"
	"cloudtrace/internal/model"
)

const waitTimeout = 3 * time.Second

// knownStage 判断阶段名是否属于仓库定义的任务阶段集合。
func knownStage(stage string) bool {
	return stage == model.PhaseScan || stage == model.PhaseSpeed
}

func TestNewManagerValidation(t *testing.T) {
	if _, err := NewManager(nil, nil, Options{}); err == nil {
		t.Error("事件总线为空时应返回错误")
	}

	bus := event.New(event.DefaultQueueSize)
	defer bus.Close()

	// logger 为空时回落到默认日志器，不算错误。
	if _, err := NewManager(bus, nil, Options{}); err != nil {
		t.Errorf("logger 为空不应失败：%v", err)
	}
}

func TestManagerStartValidation(t *testing.T) {
	m, _ := newTestManager(t)

	if err := m.Start("", func(Reporter) (Outcome, error) { return Outcome{}, nil }); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("空阶段 err = %v，期望 ErrInvalidParam", err)
	}
	if err := m.Start("scan", nil); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("空任务主体 err = %v，期望 ErrInvalidParam", err)
	}
	if m.Running() {
		t.Error("参数校验失败不应让任务进入运行态")
	}
}

func TestManagerBusyOnSecondStart(t *testing.T) {
	m, bus := newTestManager(t)
	rec := newRecorder(t, bus, "scan"+suffixDone)

	release := make(chan struct{})
	blocking := func(Reporter) (Outcome, error) {
		<-release
		return Outcome{Count: 1}, nil
	}

	if err := m.Start("scan", blocking); err != nil {
		t.Fatalf("首次启动失败：%v", err)
	}
	if err := m.Start("scan", blocking); !errors.Is(err, ErrBusy) {
		t.Fatalf("第二次启动 err = %v，期望 ErrBusy", err)
	}

	close(release)
	waitStatus(t, m, model.StatusDone, waitTimeout)
	rec.waitCount(t, "scan"+suffixDone, 1, waitTimeout)

	// 上一个任务结束后必须能再启动，否则「一次失败就再也扫不了」。
	if err := m.Start("scan", func(Reporter) (Outcome, error) { return Outcome{}, nil }); err != nil {
		t.Fatalf("结束后重新启动失败：%v", err)
	}
	waitStatus(t, m, model.StatusDone, waitTimeout)
}

func TestManagerAbortKeepsPartialAndSkipsDone(t *testing.T) {
	m, bus := newTestManager(t)
	rec := newRecorder(t, bus, "scan"+suffixDone, "scan"+suffixAbort)

	started := make(chan struct{})
	run := func(rep Reporter) (Outcome, error) {
		rep.SetTotal(100)
		rep.SetDone(37)
		rep.SetFunnel(model.Funnel{Generated: 100, LatencyOK: 37})
		close(started)
		<-rep.Context().Done()
		return Outcome{Count: 37}, nil
	}

	if err := m.Start("scan", run); err != nil {
		t.Fatalf("启动失败：%v", err)
	}
	<-started

	if !m.Abort() {
		t.Fatal("有任务在跑时 Abort 应返回 true")
	}
	// 重复中止是幂等的，但不该谎报「中止了一个任务」。
	waitStatus(t, m, model.StatusAborted, waitTimeout)
	if m.Abort() {
		t.Error("任务已结束后 Abort 应返回 false")
	}

	rec.waitCount(t, "scan"+suffixAbort, 1, waitTimeout)
	if got := rec.count("scan" + suffixDone); got != 0 {
		t.Errorf("中止发出了 %d 条 done 事件，期望 0 条——把中止当完成会写出不完整的历史", got)
	}

	st := m.Snapshot()
	if st.Done != 37 || st.Total != 100 {
		t.Errorf("中止后进度 = %d/%d，期望 37/100（部分结果必须保留）", st.Done, st.Total)
	}
	if st.Funnel.LatencyOK != 37 {
		t.Errorf("中止后漏斗 = %+v，期望保留已统计到的 37", st.Funnel)
	}
	if st.ETA != 0 {
		t.Errorf("中止后 ETA = %v，期望 0", st.ETA)
	}

	out, ok := rec.last("scan" + suffixAbort).(Outcome)
	if !ok {
		t.Fatalf("中止事件载荷类型 = %T，期望 Outcome", rec.last("scan"+suffixAbort))
	}
	if out.Count != 37 {
		t.Errorf("中止事件 count = %d，期望 37", out.Count)
	}
	// 中止同样是终态，也要能回答「停在哪一步、跑了多久」。
	if !knownStage(out.Stage) {
		t.Errorf("中止事件 stage = %q，期望属于 {scan, speed}", out.Stage)
	}
	if out.DurationMs < 0 {
		t.Errorf("中止事件 duration_ms = %d，期望 >= 0", out.DurationMs)
	}
}

func TestManagerDonePublishesOutcome(t *testing.T) {
	m, bus := newTestManager(t)
	rec := newRecorder(t, bus, "scan"+suffixDone, TopicState)

	run := func(rep Reporter) (Outcome, error) {
		rep.SetTotal(10)
		rep.SetDone(10)
		return Outcome{Count: 10, ID: "hist-1"}, nil
	}
	if err := m.Start("scan", run); err != nil {
		t.Fatalf("启动失败：%v", err)
	}

	rec.waitCount(t, "scan"+suffixDone, 1, waitTimeout)
	out, ok := rec.last("scan" + suffixDone).(Outcome)
	if !ok || out.Count != 10 || out.ID != "hist-1" {
		t.Fatalf("done 事件载荷 = %+v，期望 count=10 id=hist-1", rec.last("scan"+suffixDone))
	}
	if !knownStage(out.Stage) {
		t.Errorf("done 事件 stage = %q，期望属于 {scan, speed}", out.Stage)
	}
	if out.DurationMs < 0 {
		t.Errorf("done 事件 duration_ms = %d，期望 >= 0", out.DurationMs)
	}

	st := m.Snapshot()
	if st.Status != model.StatusDone || st.Done != 10 {
		t.Errorf("完成快照 = %+v", st)
	}
	if st.ErrorMsg != "" {
		t.Errorf("成功完成不应带错误信息：%q", st.ErrorMsg)
	}
}

func TestManagerFailurePublishesError(t *testing.T) {
	m, bus := newTestManager(t)
	rec := newRecorder(t, bus, TopicError, "scan"+suffixDone, FailedTopic("scan"))

	run := func(Reporter) (Outcome, error) { return Outcome{}, errors.New("候选池为空") }
	if err := m.Start("scan", run); err != nil {
		t.Fatalf("启动失败：%v", err)
	}

	st := waitStatus(t, m, model.StatusFailed, waitTimeout)
	if st.ErrorMsg != "候选池为空" {
		t.Errorf("失败信息 = %q，期望「候选池为空」", st.ErrorMsg)
	}
	rec.waitCount(t, TopicError, 1, waitTimeout)
	if got := rec.count("scan" + suffixDone); got != 0 {
		t.Errorf("失败发出了 %d 条 done 事件，期望 0 条", got)
	}
	if err, ok := rec.last(TopicError).(error); !ok || err.Error() != "候选池为空" {
		t.Errorf("error 事件载荷 = %v", rec.last(TopicError))
	}

	// 失败还要带结构化结果：阶段必须可识别，耗时不能为负。
	rec.waitCount(t, FailedTopic("scan"), 1, waitTimeout)
	out, ok := rec.last(FailedTopic("scan")).(Outcome)
	if !ok {
		t.Fatalf("failed 事件载荷类型 = %T，期望 Outcome", rec.last(FailedTopic("scan")))
	}
	if !knownStage(out.Stage) {
		t.Errorf("失败事件 stage = %q，期望属于 {scan, speed}", out.Stage)
	}
	if out.DurationMs < 0 {
		t.Errorf("失败事件 duration_ms = %d，期望 >= 0", out.DurationMs)
	}
}

func TestManagerPanicBecomesFailedNotCrash(t *testing.T) {
	m, bus := newTestManager(t)
	rec := newRecorder(t, bus, TopicError)

	run := func(Reporter) (Outcome, error) { panic("模拟任务主体越界") }
	if err := m.Start("scan", run); err != nil {
		t.Fatalf("启动失败：%v", err)
	}

	st := waitStatus(t, m, model.StatusFailed, waitTimeout)
	if st.ErrorMsg == "" {
		t.Error("panic 必须被记成失败并带上原因，不能静默吞掉")
	}
	rec.waitCount(t, TopicError, 1, waitTimeout)
}

func TestManagerAbortWhenIdle(t *testing.T) {
	m, _ := newTestManager(t)
	if m.Abort() {
		t.Error("空闲时 Abort 应返回 false")
	}
	if st := m.Snapshot(); st.Status != model.StatusIdle {
		t.Errorf("初始状态 = %s，期望 idle", st.Status)
	}
	if m.Running() {
		t.Error("初始不应处于运行态")
	}
}

func TestManagerProgressThrottledButSnapshotFresh(t *testing.T) {
	bus := event.New(event.DefaultQueueSize)
	defer bus.Close()

	// 节流间隔设成 1 小时：窗口内只应发出第一条进度。
	m, err := NewManager(bus, nil, Options{ProgressInterval: time.Hour})
	if err != nil {
		t.Fatalf("构造失败：%v", err)
	}
	rec := newRecorder(t, bus, TopicProgress)

	run := func(rep Reporter) (Outcome, error) {
		rep.SetTotal(1000)
		for i := 0; i <= 1000; i++ {
			rep.SetDone(i)
		}
		return Outcome{}, nil
	}
	if err := m.Start("scan", run); err != nil {
		t.Fatalf("启动失败：%v", err)
	}
	waitStatus(t, m, model.StatusDone, waitTimeout)
	// 任务进入 Done 只说明编排器跑完了自己的收尾，progress 事件还可能在总线的
	// 投递队列里。这里必须先等它到达，否则断言会随机读到 0 条。
	rec.waitCount(t, TopicProgress, 1, waitTimeout)

	if got := rec.count(TopicProgress); got != 1 {
		t.Errorf("progress 事件 %d 条，期望 1 条（节流未生效）", got)
	}
	// 事件被节流，但快照必须是最新的：断线重连靠它恢复视图。
	if st := m.Snapshot(); st.Done != 1000 || st.Total != 1000 {
		t.Errorf("快照进度 = %d/%d，期望 1000/1000", st.Done, st.Total)
	}
}

// 进度事件必须带上漏斗计数：前端靠它显示「生成 → 延迟达标 → 可用」。
func TestManagerProgressCarriesFunnel(t *testing.T) {
	bus := event.New(event.DefaultQueueSize)
	defer bus.Close()

	// 节流间隔取 1ns 等于不节流，每次上报都能发出，于是能断言最后一条的内容。
	m, err := NewManager(bus, nil, Options{ProgressInterval: time.Nanosecond})
	if err != nil {
		t.Fatalf("构造失败：%v", err)
	}
	rec := newRecorder(t, bus, TopicProgress)

	// 两次上报之间睡足 2ms：节流判定用的是墙上时钟，若两次调用落在同一个
	// 时间戳上，后一条会被当成窗口内重复而丢掉，断言就变成看运气。
	run := func(rep Reporter) (Outcome, error) {
		rep.SetTotal(10)
		time.Sleep(2 * time.Millisecond)
		rep.SetDone(3)
		time.Sleep(2 * time.Millisecond)
		rep.SetFunnel(model.Funnel{Generated: 100, LatencyOK: 40})
		return Outcome{}, nil
	}
	if err := m.Start("scan", run); err != nil {
		t.Fatalf("启动失败：%v", err)
	}
	waitStatus(t, m, model.StatusDone, waitTimeout)
	rec.waitCount(t, TopicProgress, 3, waitTimeout)

	payload := rec.last(TopicProgress)
	last, ok := payload.(Progress)
	if !ok {
		t.Fatalf("progress 载荷类型 = %T，期望 Progress", payload)
	}
	if last.Phase != "scan" || last.Done != 3 || last.Total != 10 {
		t.Errorf("进度 = %q %d/%d，期望 scan 3/10", last.Phase, last.Done, last.Total)
	}
	if last.Funnel.Generated != 100 || last.Funnel.LatencyOK != 40 {
		t.Errorf("漏斗 = %+v，期望 100/40", last.Funnel)
	}
	// 运行期间只发 progress、不重发 state，界面上的计时器只能读这里。
	if last.Elapsed <= 0 {
		t.Errorf("已耗时 = %v，期望大于 0", last.Elapsed)
	}
}

func TestManagerSnapshotElapsedAndETA(t *testing.T) {
	var clock atomic.Int64
	base := time.Unix(1700000000, 0)
	clock.Store(base.UnixNano())

	bus := event.New(event.DefaultQueueSize)
	defer bus.Close()

	m, err := NewManager(bus, nil, Options{
		Now:              func() time.Time { return time.Unix(0, clock.Load()) },
		ProgressInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("构造失败：%v", err)
	}

	hold := make(chan struct{})
	run := func(rep Reporter) (Outcome, error) {
		rep.SetTotal(100)
		rep.SetDone(25)
		<-hold
		return Outcome{}, nil
	}
	if err := m.Start("scan", run); err != nil {
		t.Fatalf("启动失败：%v", err)
	}
	waitDone(t, m, 25, waitTimeout)

	// 时钟前进 10 秒：已完成 25/100，线性外推剩余 30 秒。
	clock.Store(base.Add(10 * time.Second).UnixNano())
	st := m.Snapshot()
	if !closeTo(st.Elapsed, 10) {
		t.Errorf("已耗时 = %v，期望 10", st.Elapsed)
	}
	if !closeTo(st.ETA, 30) {
		t.Errorf("ETA = %v，期望 30", st.ETA)
	}
	if st.StartedAt != base.Unix() {
		t.Errorf("StartedAt = %d，期望 %d", st.StartedAt, base.Unix())
	}

	close(hold)
	waitStatus(t, m, model.StatusDone, waitTimeout)
}

func TestManagerUsesBaseContext(t *testing.T) {
	bus := event.New(event.DefaultQueueSize)
	defer bus.Close()

	// 父 context 取消后，任务必须跟着停：进程退出时不该留下孤儿任务。
	parent, cancelParent := context.WithCancel(context.Background())
	m, err := NewManager(bus, nil, Options{BaseContext: parent, ProgressInterval: time.Millisecond})
	if err != nil {
		t.Fatalf("构造失败：%v", err)
	}

	run := func(rep Reporter) (Outcome, error) {
		<-rep.Context().Done()
		return Outcome{}, nil
	}
	if err := m.Start("scan", run); err != nil {
		t.Fatalf("启动失败：%v", err)
	}

	cancelParent()
	waitStatus(t, m, model.StatusAborted, waitTimeout)
}

// 失败也必须下发结构化结果：只发 error 的话，前端无法回答「卡在哪一步、
// 跑了多久」。耗时用注入时钟造出确定值，避免断言落在真实时钟的抖动上。
func TestManagerFailurePublishesStageAndDuration(t *testing.T) {
	var clock atomic.Int64
	base := time.Unix(1700000000, 0)
	clock.Store(base.UnixNano())

	bus := event.New(event.DefaultQueueSize)
	defer bus.Close()

	m, err := NewManager(bus, nil, Options{
		Now:              func() time.Time { return time.Unix(0, clock.Load()) },
		ProgressInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("构造失败：%v", err)
	}
	failedTopic := FailedTopic("scan")
	rec := newRecorder(t, bus, failedTopic, TopicError)

	hold := make(chan struct{})
	run := func(Reporter) (Outcome, error) {
		<-hold
		return Outcome{}, errors.New("候选池为空")
	}
	if err := m.Start("scan", run); err != nil {
		t.Fatalf("启动失败：%v", err)
	}

	clock.Store(base.Add(1500 * time.Millisecond).UnixNano())
	close(hold)

	rec.waitCount(t, failedTopic, 1, waitTimeout)
	out, ok := rec.last(failedTopic).(Outcome)
	if !ok {
		t.Fatalf("failed 事件载荷类型 = %T，期望 Outcome", rec.last(failedTopic))
	}
	if out.Stage != model.PhaseScan {
		t.Errorf("失败事件 stage = %q，期望 %q", out.Stage, model.PhaseScan)
	}
	if out.DurationMs != 1500 {
		t.Errorf("失败事件 duration_ms = %d，期望 1500", out.DurationMs)
	}

	// 错误提示链路保持原样：失败仍要发 TopicError。
	rec.waitCount(t, TopicError, 1, waitTimeout)
	if err, ok := rec.last(TopicError).(error); !ok || err.Error() != "候选池为空" {
		t.Errorf("error 事件载荷 = %v", rec.last(TopicError))
	}
}

// 可继续的局部失败要变成告警而不是失败：任务整体成功，告警随 done 下发。
func TestManagerCollectsWarningsOnPartialFailure(t *testing.T) {
	m, bus := newTestManager(t)
	doneTopic := DoneTopic("scan")
	rec := newRecorder(t, bus, doneTopic)

	run := func(rep Reporter) (Outcome, error) {
		rep.SetTotal(10)
		rep.SetDone(10)
		Warn(rep, "数据源 A 不可用，已跳过")
		Warn(rep, "   ") // 空白告警没有信息量，必须丢弃。
		Warn(rep, "地区解析部分失败")
		return Outcome{Count: 8, Warnings: []string{"任务体自带的告警"}}, nil
	}
	if err := m.Start("scan", run); err != nil {
		t.Fatalf("启动失败：%v", err)
	}

	rec.waitCount(t, doneTopic, 1, waitTimeout)
	out, ok := rec.last(doneTopic).(Outcome)
	if !ok {
		t.Fatalf("done 事件载荷类型 = %T，期望 Outcome", rec.last(doneTopic))
	}
	want := []string{"任务体自带的告警", "数据源 A 不可用，已跳过", "地区解析部分失败"}
	if len(out.Warnings) != len(want) {
		t.Fatalf("warnings = %v，期望 %v", out.Warnings, want)
	}
	for i := range want {
		if out.Warnings[i] != want[i] {
			t.Errorf("warnings[%d] = %q，期望 %q", i, out.Warnings[i], want[i])
		}
	}
	if !knownStage(out.Stage) || out.DurationMs < 0 {
		t.Errorf("stage/duration = %q/%d，期望 stage 可识别且耗时非负", out.Stage, out.DurationMs)
	}
}

// 新字段必须 omitempty：没有扩展信息时，旧前端收到的 JSON 与旧版完全一致。
func TestOutcomeJSONOmitsEmptyExtensions(t *testing.T) {
	raw, err := json.Marshal(Outcome{Count: 3})
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	if got := string(raw); got != `{"count":3}` {
		t.Errorf("空扩展字段的 JSON = %s，期望 {\"count\":3}", got)
	}

	raw, err = json.Marshal(Outcome{
		Count:      3,
		Stage:      model.PhaseSpeed,
		DurationMs: 12,
		Warnings:   []string{"源不可用"},
	})
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("反序列化失败：%v", err)
	}
	for _, key := range []string{"stage", "duration_ms", "warnings"} {
		if _, ok := got[key]; !ok {
			t.Errorf("非空扩展字段 %q 未出现在 JSON：%s", key, raw)
		}
	}
}

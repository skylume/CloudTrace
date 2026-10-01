package task

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"cloudtrace/internal/event"
	"cloudtrace/internal/model"
)

const waitTimeout = 3 * time.Second

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
}

func TestManagerDonePublishesOutcome(t *testing.T) {
	m, bus := newTestManager(t)
	rec := newRecorder(t, bus, "scan"+suffixDone, TopicState)

	run := func(rep Reporter) (Outcome, error) {
		rep.SetPreset("fast")
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

	st := m.Snapshot()
	if st.Status != model.StatusDone || st.Preset != "fast" || st.Done != 10 {
		t.Errorf("完成快照 = %+v", st)
	}
	if st.ErrorMsg != "" {
		t.Errorf("成功完成不应带错误信息：%q", st.ErrorMsg)
	}
}

func TestManagerFailurePublishesError(t *testing.T) {
	m, bus := newTestManager(t)
	rec := newRecorder(t, bus, TopicError, "scan"+suffixDone)

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

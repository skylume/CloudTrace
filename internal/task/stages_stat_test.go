package task

import (
	"testing"
	"time"

	"cloudtrace/internal/event"
	"cloudtrace/internal/model"
)

func TestManagerProgressAndSnapshotCarryStages(t *testing.T) {
	bus := event.New(event.DefaultQueueSize)
	defer bus.Close()

	m, err := NewManager(bus, nil, Options{ProgressInterval: time.Nanosecond})
	if err != nil {
		t.Fatalf("构造失败：%v", err)
	}
	rec := newRecorder(t, bus, TopicProgress)

	started := make(chan struct{})
	release := make(chan struct{})
	run := func(rep Reporter) (Outcome, error) {
		stages := []model.StageStat{model.NewStageStat(model.StagePool, 10, 8, 0, 2, 0.1)}
		SetStages(rep, stages)
		// 调用方手里的切片被改掉，不应影响已经交给编排层的那份。
		stages[0].OK = 999
		close(started)
		<-release
		return Outcome{}, nil
	}
	if err := m.Start("scan", run); err != nil {
		t.Fatalf("启动失败：%v", err)
	}
	<-started

	snap := m.Snapshot()
	if len(snap.Stages) != 1 {
		t.Fatalf("Snapshot().Stages 长度 = %d，期望 1", len(snap.Stages))
	}
	if snap.Stages[0].Name != model.StagePool || snap.Stages[0].OK != 8 || snap.Stages[0].Done != 10 {
		t.Errorf("阶段快照 = %+v，期望 pool 8/10", snap.Stages[0])
	}

	// 修改快照不应写回内部状态。
	snap.Stages[0].OK = 1
	if got := m.Snapshot().Stages[0].OK; got != 8 {
		t.Errorf("修改快照后内部 OK = %d，期望 8", got)
	}

	close(release)
	waitStatus(t, m, model.StatusDone, waitTimeout)
	rec.waitCount(t, TopicProgress, 1, waitTimeout)

	last, ok := rec.last(TopicProgress).(Progress)
	if !ok {
		t.Fatalf("progress 载荷类型 = %T，期望 Progress", rec.last(TopicProgress))
	}
	if len(last.Stages) != 1 || last.Stages[0].Name != model.StagePool || last.Stages[0].Done != 10 {
		t.Errorf("progress.Stages = %+v，期望 pool 10", last.Stages)
	}
}

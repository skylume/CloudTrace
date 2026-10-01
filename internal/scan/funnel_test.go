package scan

import (
	"sync"
	"testing"

	"cloudtrace/internal/model"
)

func TestFunnelCountsEachStage(t *testing.T) {
	f := NewFunnel(nil)

	f.Set(StageGenerated, 10000)
	f.Set(StageLatencyOK, 240)
	f.Set(StageRegionOK, 231)
	got := f.Set(StageUsable, 180)

	want := model.Funnel{Generated: 10000, LatencyOK: 240, RegionOK: 231, Usable: 180}
	if got != want {
		t.Fatalf("漏斗 = %+v，期望 %+v", got, want)
	}
	if f.Snapshot() != want {
		t.Errorf("快照 = %+v，期望 %+v", f.Snapshot(), want)
	}
}

func TestFunnelClampsToPreviousStage(t *testing.T) {
	// 后一级不可能多于前一级。统计口径出错时宁可显示成不增长，
	// 也不要出现「可用 300 / 生成 100」这种自相矛盾的界面。
	f := NewFunnel(nil)
	f.Set(StageGenerated, 100)
	f.Set(StageLatencyOK, 500)
	f.Set(StageRegionOK, 400)
	f.Set(StageUsable, 300)

	got := f.Snapshot()
	if got.LatencyOK != 100 {
		t.Errorf("延迟达标 = %d，期望被钳到 100", got.LatencyOK)
	}
	if got.RegionOK != 100 {
		t.Errorf("地区解析 = %d，期望被钳到 100", got.RegionOK)
	}
	if got.Usable != 100 {
		t.Errorf("可用 = %d，期望被钳到 100", got.Usable)
	}
}

func TestFunnelLoweringEarlierStagePullsLaterOnesDown(t *testing.T) {
	f := NewFunnel(nil)
	f.Set(StageGenerated, 100)
	f.Set(StageLatencyOK, 80)
	f.Set(StageRegionOK, 60)
	f.Set(StageUsable, 40)

	// 回填一个比所有后级都小的候选池总数（例如重算后），后面几级必须
	// 跟着收回来；只把总数改小、后级不动会得到自相矛盾的漏斗。
	got := f.Set(StageGenerated, 30)
	if got.LatencyOK != 30 || got.RegionOK != 30 || got.Usable != 30 {
		t.Errorf("漏斗 = %+v，期望后三级都收到 30", got)
	}
}

func TestFunnelLoweringEarlierStageKeepsSmallerLaterValue(t *testing.T) {
	// 新上限仍然高于后级时不该改动它：40 ≤ 50，本来就合法。
	f := NewFunnel(nil)
	f.Set(StageGenerated, 100)
	f.Set(StageLatencyOK, 80)
	f.Set(StageRegionOK, 60)
	f.Set(StageUsable, 40)

	got := f.Set(StageGenerated, 50)
	if got.Usable != 40 {
		t.Errorf("可用 = %d，期望保持 40（并未越界）", got.Usable)
	}
	if got.LatencyOK != 50 || got.RegionOK != 50 {
		t.Errorf("漏斗 = %+v，期望前两级收到 50", got)
	}
}

func TestFunnelUsableIsIndependentOfRegionResolution(t *testing.T) {
	// 关闭明细采集时拿不到地区：地区解析为 0，但结果依然可用。
	// 把两者串成一条链的话，这里会把 38 条结果全数抹掉。
	f := NewFunnel(nil)
	f.Set(StageGenerated, 100)
	f.Set(StageLatencyOK, 40)
	f.Set(StageRegionOK, 0)
	got := f.Set(StageUsable, 38)

	if got.RegionOK != 0 {
		t.Errorf("地区解析 = %d，期望 0", got.RegionOK)
	}
	if got.Usable != 38 {
		t.Errorf("可用 = %d，期望 38（不得被地区解析数抹掉）", got.Usable)
	}
}

func TestFunnelUsableStillBoundedByLatencyOK(t *testing.T) {
	f := NewFunnel(nil)
	f.Set(StageGenerated, 100)
	f.Set(StageLatencyOK, 10)
	if got := f.Set(StageUsable, 99); got.Usable != 10 {
		t.Errorf("可用 = %d，期望被钳到延迟达标数 10", got.Usable)
	}
}

func TestFunnelNegativeBecomesZero(t *testing.T) {
	f := NewFunnel(nil)
	f.Set(StageGenerated, 10)
	if got := f.Set(StageLatencyOK, -5); got.LatencyOK != 0 {
		t.Errorf("延迟达标 = %d，期望 0", got.LatencyOK)
	}
}

func TestFunnelNotifiesOnEveryChange(t *testing.T) {
	var (
		mu       sync.Mutex
		received []model.Funnel
	)
	f := NewFunnel(func(snapshot model.Funnel) {
		mu.Lock()
		received = append(received, snapshot)
		mu.Unlock()
	})

	f.Set(StageGenerated, 100)
	f.Set(StageLatencyOK, 50)
	f.Set(StageRegionOK, 45)
	f.Set(StageUsable, 30)

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 4 {
		t.Fatalf("回调 %d 次，期望 4 次（每次变更都要上报）", len(received))
	}
	if received[3].Usable != 30 || received[3].Generated != 100 {
		t.Errorf("最后一次回调 = %+v", received[3])
	}
}

func TestFunnelWithoutCallbackIsSafe(t *testing.T) {
	f := NewFunnel(nil)
	f.Set(StageGenerated, 1)
	if got := f.Snapshot(); got.Generated != 1 {
		t.Errorf("漏斗 = %+v，期望 generated=1", got)
	}
}

package task

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// stepOutcome 是带成功标记的结果，用来区分「零值」与「真的没跑成功」。
type stepOutcome struct {
	OK bool
	V  int
}

func TestRunBoundedProcessesAllItems(t *testing.T) {
	items := make([]int, 100)
	for i := range items {
		items[i] = i
	}

	results, err := RunBounded(context.Background(), items, 8,
		func(_ context.Context, v int) (int, bool) { return v, false }, nil)
	if err != nil {
		t.Fatalf("返回错误：%v", err)
	}
	if len(results) != len(items) {
		t.Fatalf("结果数 = %d，期望 %d", len(results), len(items))
	}
	// 结果必须按输入顺序排列，否则调用方无法把结果与输入对齐。
	for i, got := range results {
		if got != i {
			t.Fatalf("第 %d 个结果 = %d，期望 %d（顺序错乱）", i, got, i)
		}
	}
}

func TestRunBoundedEmptyItems(t *testing.T) {
	results, err := RunBounded(context.Background(), []int(nil), 4,
		func(_ context.Context, v int) (int, bool) { return v, false }, nil)
	if err != nil {
		t.Fatalf("返回错误：%v", err)
	}
	if results != nil {
		t.Errorf("空输入应返回 nil，实际 %v", results)
	}
}

func TestRunBoundedWorkersClamped(t *testing.T) {
	tests := []struct {
		name    string
		workers int
	}{
		{name: "零并发", workers: 0},
		{name: "负并发", workers: -3},
		{name: "并发大于项数", workers: 1000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := []int{1, 2, 3}
			results, err := RunBounded(context.Background(), items, tt.workers,
				func(_ context.Context, v int) (int, bool) { return v * 2, false }, nil)
			if err != nil {
				t.Fatalf("返回错误：%v", err)
			}
			if len(results) != 3 {
				t.Fatalf("结果数 = %d，期望 3", len(results))
			}
		})
	}
}

func TestRunBoundedConvergenceStopsDispatching(t *testing.T) {
	items := make([]int, 200)
	var calls atomic.Int64

	results, err := RunBounded(context.Background(), items, 1,
		func(_ context.Context, v int) (int, bool) {
			calls.Add(1)
			// 第一项就宣告「够了」。
			return v, v == 0
		}, nil)
	if err != nil {
		t.Fatalf("收敛属于正常结束，不应返回错误：%v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("实际执行 %d 项，期望 1 项（收敛后不得继续派发）", got)
	}
	if len(results) != 1 || results[0] != 0 {
		t.Errorf("结果 = %v，期望 [0]", results)
	}
}

func TestRunBoundedConvergenceCancelsInFlight(t *testing.T) {
	// 0 号与 1 号同时派发：0 号触发收敛后，正在跑的 1 号必须被取消，
	// 而不是等它自己跑完。
	items := []int{0, 1, 2, 3, 4, 5}
	peerStarted := make(chan struct{})

	results, err := RunBounded(context.Background(), items, 2,
		func(ctx context.Context, v int) (int, bool) {
			switch v {
			case 0:
				<-peerStarted
				return v, true
			case 1:
				close(peerStarted)
				<-ctx.Done()
				return v, false
			default:
				return v, false
			}
		}, nil)
	if err != nil {
		t.Fatalf("返回错误：%v", err)
	}
	if len(results) != 2 {
		t.Fatalf("结果数 = %d（%v），期望 2：只应完成已派发的两项", len(results), results)
	}
	for i, got := range results {
		if got != i {
			t.Errorf("第 %d 个结果 = %d，期望 %d", i, got, i)
		}
	}
}

func TestRunBoundedRecoversPanic(t *testing.T) {
	items := []int{0, 1, 2, 3}

	results, err := RunBounded(context.Background(), items, 1,
		func(_ context.Context, v int) (stepOutcome, bool) {
			if v == 1 {
				panic("模拟工作项内部异常")
			}
			return stepOutcome{OK: true, V: v}, false
		}, nil)
	if err != nil {
		t.Fatalf("单项 panic 不应让整体失败：%v", err)
	}
	if len(results) != 4 {
		t.Fatalf("结果数 = %d，期望 4", len(results))
	}
	// panic 的那一项得到零值，调用方靠 OK 字段把它计入失败，而不是消失。
	if results[1].OK {
		t.Errorf("panic 项 = %+v，期望 OK 为 false", results[1])
	}
	for _, i := range []int{0, 2, 3} {
		if !results[i].OK || results[i].V != i {
			t.Errorf("第 %d 项 = %+v，期望 OK 且 V=%d", i, results[i], i)
		}
	}
}

func TestRunBoundedParentCancelReturnsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	items := make([]int, 100)
	results, err := RunBounded(ctx, items, 1,
		func(_ context.Context, v int) (int, bool) {
			cancel()
			return v, false
		}, nil)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v，期望 context.Canceled", err)
	}
	// 取消不是丢结果的理由：已经拿到的要带回去。
	if len(results) == 0 {
		t.Error("取消后仍应返回已完成项")
	}
}

func TestRunBoundedProgressThrottledAndFlushed(t *testing.T) {
	items := make([]int, 400)
	var (
		calls  atomic.Int64
		lastN  atomic.Int64
		lastTo atomic.Int64
	)

	_, err := RunBounded(context.Background(), items, 4,
		func(_ context.Context, v int) (int, bool) { return v, false },
		func(done, total int) {
			calls.Add(1)
			lastN.Store(int64(done))
			lastTo.Store(int64(total))
		})
	if err != nil {
		t.Fatalf("返回错误：%v", err)
	}

	// 不节流的话 400 项就是 400 次回调，会把推送量放大几百倍。
	if got := calls.Load(); got >= int64(len(items)) {
		t.Errorf("进度回调 %d 次，期望远少于 %d 次（未节流）", got, len(items))
	}
	if calls.Load() == 0 {
		t.Fatal("一次进度回调都没有")
	}
	// 收尾必须补发最终值，否则进度条会永远停在 99%。
	if lastN.Load() != int64(len(items)) || lastTo.Load() != int64(len(items)) {
		t.Errorf("最后一次进度 = (%d, %d)，期望 (%d, %d)",
			lastN.Load(), lastTo.Load(), len(items), len(items))
	}
}

func TestRunBoundedConcurrentSafety(t *testing.T) {
	// 供 -race 使用：多 worker 并发写入各自的结果槽位。
	items := make([]int, 500)
	results, err := RunBounded(context.Background(), items, 32,
		func(_ context.Context, v int) (int, bool) { return v, false },
		func(int, int) {})
	if err != nil {
		t.Fatalf("返回错误：%v", err)
	}
	if len(results) != len(items) {
		t.Fatalf("结果数 = %d，期望 %d", len(results), len(items))
	}
}

func TestInvokeKeepsZeroValueOnPanic(t *testing.T) {
	res, converge := invoke(context.Background(), 7,
		func(context.Context, int) (stepOutcome, bool) {
			panic("boom")
		})
	if res.OK || converge {
		t.Errorf("panic 后应得到零值，实际 %+v converge=%v", res, converge)
	}
}

func TestEstimateETA(t *testing.T) {
	tests := []struct {
		name    string
		elapsed float64
		done    int
		total   int
		want    float64
	}{
		{name: "完成一半", elapsed: 10, done: 50, total: 100, want: 10},
		{name: "刚开始", elapsed: 2, done: 1, total: 5, want: 8},
		{name: "一个都没完成", elapsed: 3, done: 0, total: 10, want: 0},
		{name: "总数为零", elapsed: 3, done: 0, total: 0, want: 0},
		{name: "已全部完成", elapsed: 3, done: 10, total: 10, want: 0},
		{name: "耗时为零", elapsed: 0, done: 5, total: 10, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := estimateETA(tt.elapsed, tt.done, tt.total); got != tt.want {
				t.Errorf("estimateETA(%v, %d, %d) = %v，期望 %v",
					tt.elapsed, tt.done, tt.total, got, tt.want)
			}
		})
	}
}

func TestProgressTrackerThrottlesThenFlushes(t *testing.T) {
	p := newProgressTracker(5 * time.Millisecond)

	var calls []int
	record := func(done, _ int) { calls = append(calls, done) }

	// 领先节流：第一次必须立刻发出去。
	p.report(1, 100, record)
	p.report(2, 100, record)
	if len(calls) != 1 || calls[0] != 1 {
		t.Fatalf("首次节流窗口内 calls = %v，期望 [1]", calls)
	}

	time.Sleep(30 * time.Millisecond)
	p.report(3, 100, record)
	if len(calls) != 2 || calls[1] != 3 {
		t.Fatalf("跨过间隔后 calls = %v，期望 [1 3]", calls)
	}

	// 补发：最后一次上报不是最终值时必须补上。
	p.flush(100, 100, record)
	if len(calls) != 3 || calls[2] != 100 {
		t.Fatalf("补发后 calls = %v，期望 [1 3 100]", calls)
	}

	// 已经上报过同一个值就不重复发。
	p.flush(100, 100, record)
	if len(calls) != 3 {
		t.Fatalf("重复补发产生了多余事件：%v", calls)
	}
}

func TestProgressTrackerDefaultsAndNilCallback(t *testing.T) {
	p := newProgressTracker(0)
	if p.interval != ProgressInterval {
		t.Errorf("interval = %v，期望回落到 %v", p.interval, ProgressInterval)
	}

	// 回调为 nil 时不得 panic：调用方常常不关心进度。
	p.report(1, 2, nil)
	p.flush(2, 2, nil)
}

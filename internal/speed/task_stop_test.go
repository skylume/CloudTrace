package speed

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cloudtrace/internal/model"
	"cloudtrace/internal/probe"
)

// ---------------------------------------------------------------------------
// 提前收敛
// ---------------------------------------------------------------------------

// 收够合格结果就不再派发新目标。参考实现里「收敛后仍跑完全部」是已知 bug，
// 这里用下载次数直接把它钉住。
func TestRunEarlyConvergenceStopsDispatching(t *testing.T) {
	d := newDeps()
	d.downloader.fn = func(context.Context, model.IPRecord) (float64, error) { return 10, nil }

	targets := make([]model.IPRecord, 0, 10)
	for i := 0; i < 10; i++ {
		targets = append(targets, target("1.1.1."+strconv.Itoa(i+1), 443))
	}
	params := convergeParams(targets...)
	params.TargetQualified = 2

	rep := newFakeReporter(context.Background())
	count, err := d.run(t, params, rep)
	if err != nil {
		t.Fatalf("测速失败：%v", err)
	}
	if count != 2 {
		t.Errorf("合格数 = %d，期望 2", count)
	}
	if got := d.downloader.count(); got != 2 {
		t.Errorf("下载次数 = %d，期望收敛后不再派发（2 次）", got)
	}
	if len(rep.partials()) != 2 {
		t.Errorf("推送结果 = %d 条，期望 2", len(rep.partials()))
	}
}

// 收敛要连「已派发的」一起取消，否则在跑的那几个还会各自跑满下载时长。
//
// 用一道栅栏让 4 个目标都进入下载后再触发收敛，这样断言不依赖调度时序。
func TestRunEarlyConvergenceCancelsInFlight(t *testing.T) {
	d := newDeps()

	var started atomic.Int64
	var cancelled atomic.Int64
	convergeIP := "1.1.1.1"

	d.downloader.fn = func(ctx context.Context, rec model.IPRecord) (float64, error) {
		started.Add(1)
		if rec.IP == convergeIP {
			// 等其余目标也进入下载，确保收敛发生时它们确实在跑。
			deadline := time.Now().Add(2 * time.Second)
			for started.Load() < 4 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			return 10, nil
		}
		select {
		case <-ctx.Done():
			cancelled.Add(1)
		case <-time.After(3 * time.Second):
		}
		return 0, ctx.Err()
	}

	targets := []model.IPRecord{
		target(convergeIP, 443), target("1.1.1.2", 443), target("1.1.1.3", 443),
		target("1.1.1.4", 443), target("1.1.1.5", 443), target("1.1.1.6", 443),
	}
	params := convergeParams(targets...)
	params.Concurrency = 4
	params.TargetQualified = 1

	rep := newFakeReporter(context.Background())
	if _, err := d.run(t, params, rep); err != nil {
		t.Fatalf("测速失败：%v", err)
	}

	if got := cancelled.Load(); got < 3 {
		t.Errorf("被取消的在跑目标 = %d，期望至少 3（收敛要取消已派发的任务）", got)
	}
	if got := d.downloader.count(); got > 4 {
		t.Errorf("下载次数 = %d，期望收敛后不再派发新目标", got)
	}
}

// 收敛目标为 1 时，第一个合格结果就该收工。
func TestRunConvergesOnFirstQualifiedResult(t *testing.T) {
	d := newDeps()
	d.downloader.fn = func(context.Context, model.IPRecord) (float64, error) { return 10, nil }

	params := convergeParams(target("1.1.1.1", 443), target("1.1.1.2", 443), target("1.1.1.3", 443))
	params.TargetQualified = 1

	if _, err := d.run(t, params, newFakeReporter(context.Background())); err != nil {
		t.Fatalf("测速失败：%v", err)
	}
	if got := d.downloader.count(); got != 1 {
		t.Errorf("下载次数 = %d，期望 1", got)
	}
}

// 不合格的结果不该把收敛计数喂满：0 MB/s 表示「没测出来」。
func TestRunConvergenceIgnoresUnqualifiedResults(t *testing.T) {
	d := newDeps()
	d.downloader.fn = speedByIP(map[string]float64{"1.1.1.1": 0, "1.1.1.2": 0, "1.1.1.3": 5})

	params := convergeParams(target("1.1.1.1", 443), target("1.1.1.2", 443), target("1.1.1.3", 443))
	params.TargetQualified = 1

	rep := newFakeReporter(context.Background())
	count, err := d.run(t, params, rep)
	if err != nil {
		t.Fatalf("测速失败：%v", err)
	}
	if count != 1 {
		t.Errorf("合格数 = %d，期望 1（前两个速度为 0 不算合格）", count)
	}
	if got := d.downloader.count(); got != 3 {
		t.Errorf("下载次数 = %d，期望 3（前两个不触发收敛）", got)
	}
}

// convergeParams 造一组「完全测速」的参数。
//
// 提前收敛只在这个范围下生效，所以收敛相关的用例都得用它。
func convergeParams(targets ...model.IPRecord) model.SpeedParams {
	params := baseParams(targets...)
	params.Scope = model.SpeedScopeAll
	return params
}

// 用户点名要测的集合不收敛。
//
// 勾选的单点、点名的整个地区都是用户明确要测的集合：收够 N 个就停下等于把
// 剩下的悄悄丢掉，而界面上不会留下任何「这些没测」的痕迹——用户只会看到
// 一大片空白，然后以为测速坏了。
func TestRunNoConvergenceOutsideFullScope(t *testing.T) {
	for _, scope := range []string{model.SpeedScopeSingle, model.SpeedScopeRegion} {
		t.Run(scope, func(t *testing.T) {
			d := newDeps()
			d.downloader.fn = func(context.Context, model.IPRecord) (float64, error) { return 10, nil }

			targets := make([]model.IPRecord, 0, 8)
			for i := 0; i < 8; i++ {
				targets = append(targets, target("1.1.1."+strconv.Itoa(i+1), 443))
			}
			params := baseParams(targets...)
			params.Scope = scope
			params.TargetQualified = 2

			if _, err := d.run(t, params, newFakeReporter(context.Background())); err != nil {
				t.Fatalf("测速失败：%v", err)
			}
			if got := d.downloader.count(); got != 8 {
				t.Errorf("下载次数 = %d，期望 8（这个范围不该提前收敛）", got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 429 熔断
// ---------------------------------------------------------------------------

// 连续 3 次限流即熔断，且**不得**当成「测速完成」。
func TestRunBreakerTripsAfterConsecutiveRateLimits(t *testing.T) {
	d := newDeps()
	d.downloader.fn = func(context.Context, model.IPRecord) (float64, error) {
		return 0, probe.ErrRateLimited
	}

	targets := make([]model.IPRecord, 0, 6)
	for i := 0; i < 6; i++ {
		targets = append(targets, target("1.1.1."+strconv.Itoa(i+1), 443))
	}
	params := baseParams(targets...)
	params.Breaker429 = 3

	rep := newFakeReporter(context.Background())
	_, err := d.run(t, params, rep)
	if err == nil {
		t.Fatal("熔断必须以错误结束，不能当成测速完成")
	}
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("错误 = %v，期望包装 ErrRateLimited", err)
	}
	if !strings.Contains(err.Error(), "连续 3 次") {
		t.Errorf("错误文案 %q 应说明连续次数", err.Error())
	}
	if got := d.downloader.count(); got != 3 {
		t.Errorf("下载次数 = %d，期望熔断后立即停止（3 次）", got)
	}
	if len(rep.partials()) != 0 {
		t.Errorf("熔断时不应有合格结果，实际 %d 条", len(rep.partials()))
	}
}

// 阈值取 1 时第一次限流就停。
func TestRunBreakerThresholdOne(t *testing.T) {
	d := newDeps()
	d.downloader.fn = func(context.Context, model.IPRecord) (float64, error) {
		return 0, probe.ErrRateLimited
	}

	params := baseParams(target("1.1.1.1", 443), target("1.1.1.2", 443))
	params.Breaker429 = 1

	if _, err := d.run(t, params, newFakeReporter(context.Background())); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("错误 = %v，期望熔断", err)
	}
	if got := d.downloader.count(); got != 1 {
		t.Errorf("下载次数 = %d，期望 1", got)
	}
}

// 中间夹一次成功就把连续计数清零：偶尔一次限流不该熔断。
func TestRunBreakerResetBySuccess(t *testing.T) {
	d := newDeps()
	d.downloader.fn = func(_ context.Context, rec model.IPRecord) (float64, error) {
		if rec.IP == "1.1.1.2" {
			return 10, nil
		}
		return 0, probe.ErrRateLimited
	}

	params := baseParams(
		target("1.1.1.1", 443), target("1.1.1.2", 443),
		target("1.1.1.3", 443), target("1.1.1.4", 443),
	)
	params.Breaker429 = 3

	rep := newFakeReporter(context.Background())
	count, err := d.run(t, params, rep)
	if err != nil {
		t.Fatalf("夹一次成功后不应熔断：%v", err)
	}
	if count != 1 {
		t.Errorf("合格数 = %d，期望 1", count)
	}
	if got := d.downloader.count(); got != 4 {
		t.Errorf("下载次数 = %d，期望全部跑完", got)
	}
}

// 连不上不等于被限速，中间夹一次普通失败同样打断连续计数。
func TestRunBreakerResetByOtherError(t *testing.T) {
	d := newDeps()
	d.downloader.fn = func(_ context.Context, rec model.IPRecord) (float64, error) {
		if rec.IP == "1.1.1.2" {
			return 0, errors.New("连接被重置")
		}
		return 0, probe.ErrRateLimited
	}

	params := baseParams(
		target("1.1.1.1", 443), target("1.1.1.2", 443),
		target("1.1.1.3", 443), target("1.1.1.4", 443),
	)
	params.Breaker429 = 3

	if _, err := d.run(t, params, newFakeReporter(context.Background())); err != nil {
		t.Fatalf("中间夹普通失败时不应熔断：%v", err)
	}
	if got := d.downloader.count(); got != 4 {
		t.Errorf("下载次数 = %d，期望全部跑完", got)
	}
}

// ---------------------------------------------------------------------------
// 中止
// ---------------------------------------------------------------------------

// 中止要保留已经测出来的结果，不能整批丢掉。
func TestRunAbortKeepsPartialResults(t *testing.T) {
	d := newDeps()

	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once

	d.downloader.fn = func(ctx context.Context, rec model.IPRecord) (float64, error) {
		if rec.IP == "1.1.1.1" {
			once.Do(cancel)
			return 10, nil
		}
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
		}
		return 0, ctx.Err()
	}

	params := baseParams(
		target("1.1.1.1", 443), target("1.1.1.2", 443), target("1.1.1.3", 443),
	)
	rep := newFakeReporter(ctx)

	count, err := d.run(t, params, rep)
	if err == nil {
		t.Fatal("中止应当以错误结束，交给编排层判定为 Aborted")
	}
	if count != 1 {
		t.Errorf("中止时保留的结果数 = %d，期望 1", count)
	}
	if got := len(rep.partials()); got != 1 {
		t.Errorf("已推送的结果 = %d 条，期望保留 1 条", got)
	}
}

// 串行间隔里点停止必须立刻响应，不能等间隔走完。
func TestRunAbortDuringIntervalReturnsPromptly(t *testing.T) {
	d := newDeps()
	d.downloader.fn = func(context.Context, model.IPRecord) (float64, error) { return 10, nil }

	params := baseParams(target("1.1.1.1", 443), target("1.1.1.2", 443))
	params.IntervalMS = 5000

	ctx, cancel := context.WithCancel(context.Background())
	rep := newFakeReporter(ctx)

	done := make(chan error, 1)
	go func() {
		_, err := d.run(t, params, rep)
		done <- err
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("中止后仍卡在串行间隔里，停止按钮失灵")
	}
}

// 下载过程中的 panic 不能让整个进程退出。
func TestRunPanicInDownloadDoesNotCrash(t *testing.T) {
	d := newDeps()
	d.downloader.fn = func(context.Context, model.IPRecord) (float64, error) {
		panic("探测层炸了")
	}

	params := baseParams(target("1.1.1.1", 443), target("1.1.1.2", 443))
	rep := newFakeReporter(context.Background())

	count, err := d.run(t, params, rep)
	if err != nil {
		t.Fatalf("单项 panic 不应让任务失败：%v", err)
	}
	if count != 0 {
		t.Errorf("合格数 = %d，期望 0", count)
	}
	if got := d.downloader.count(); got != 2 {
		t.Errorf("下载次数 = %d，期望其余目标照常执行", got)
	}
}

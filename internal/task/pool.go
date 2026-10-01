package task

import (
	"context"
	"sync"
	"sync/atomic"
)

// RunBounded 以固定数量的 worker 并发执行 items，返回已完成项的结果。
//
// 语义约定：
//   - workers <= 0 时按 1 处理，并自动收缩到 len(items)；
//   - fn 的第二个返回值表示「这一项触发了提前收敛」。任意一项返回 true
//     之后不再派发新任务，同时取消已派发的那些——否则「已经够了」的
//     判断形同虚设，剩下的任务照样会全部跑完；
//   - 单项 fn 内部 panic 会被兜住，该位置得到 R 的零值，其余项照常执行。
//     因此调用方应让 R 自带「是否成功」的标记，才能把这种失败计入统计；
//     静默丢掉会让失败消失在计数之外；
//   - 结果只包含已完成项，并保持输入顺序；
//   - onProgress 按节流间隔上报，结束时补发最终值；
//   - 只有父 context 被取消才返回错误。提前收敛属于正常结束，不返回错误，
//     调用方若要区分「收敛」与「用户中止」，看自己持有的父 context 即可。
func RunBounded[T any, R any](
	ctx context.Context,
	items []T,
	workers int,
	fn func(context.Context, T) (R, bool),
	onProgress func(done, total int),
) ([]R, error) {
	if len(items) == 0 {
		return nil, nil
	}
	if workers <= 0 {
		workers = 1
	}
	if workers > len(items) {
		workers = len(items)
	}

	total := len(items)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		next    atomic.Int64
		done    atomic.Int64
		stopped atomic.Bool

		mu      sync.Mutex
		results = make([]R, total)
		filled  = make([]bool, total)
	)
	progress := newProgressTracker(ProgressInterval)

	work := func() {
		for {
			if stopped.Load() || runCtx.Err() != nil {
				return
			}

			i := int(next.Add(1)) - 1
			if i >= total {
				return
			}

			res, converge := invoke(runCtx, items[i], fn)

			mu.Lock()
			results[i] = res
			filled[i] = true
			mu.Unlock()

			if converge && !stopped.Swap(true) {
				cancel()
			}

			progress.report(int(done.Add(1)), total, onProgress)
		}
	}

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			work()
		}()
	}
	wg.Wait()

	progress.flush(int(done.Load()), total, onProgress)

	out := make([]R, 0, total)
	mu.Lock()
	for i := 0; i < total; i++ {
		if filled[i] {
			out = append(out, results[i])
		}
	}
	mu.Unlock()

	if err := ctx.Err(); err != nil {
		return out, err
	}
	return out, nil
}

// invoke 调用 fn，并兜住它抛出的 panic。
//
// fn panic 时命名返回值保持零值：R 为零值、converge 为 false。
func invoke[T any, R any](ctx context.Context, item T, fn func(context.Context, T) (R, bool)) (res R, converge bool) {
	defer func() {
		if recover() != nil {
			converge = false
		}
	}()
	return fn(ctx, item)
}

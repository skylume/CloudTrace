package scan

import (
	"context"
	"sync/atomic"
	"time"

	"cloudtrace/internal/model"
	"cloudtrace/internal/probe"
	"cloudtrace/internal/task"
)

// probed 是一项探测结果与它对应的候选。
//
// 候选要跟结果绑在一起：并发执行时结果只保证「已完成项按输入顺序」返回，
// 中止后拿到的部分结果里没有候选信息就没法用。
type probed struct {
	cand Candidate
	res  probe.Result
}

// probeStages 执行延迟探测并返回通过阈值的项。
//
// 两阶段模式下阶段一只测一次、只覆盖粗扫规模，阶段二只重测达标的那批。
// 两阶段共用同一个 context：中止必须一次停掉全部，各用各的 context 会
// 出现「点了停止，另一个阶段还在跑」。
func (r *Runner) probeStages(ctx context.Context, rep Reporter, st *stages, funnel *Funnel, items []Candidate) ([]probed, error) {
	if !r.params.TwoPhase {
		st.begin(len(items))
		got, err := r.probePhase(ctx, rep, st, items, r.params.PingTimes, func(n int) {
			funnel.Set(StageLatencyOK, n)
		})
		if err != nil {
			return nil, err
		}
		passing := r.keepPassing(got)
		funnel.Set(StageLatencyOK, len(passing))
		return passing, nil
	}

	st.begin(len(items))
	coarse, err := r.probePhase(ctx, rep, st, items, 1, func(n int) {
		funnel.Set(StageLatencyOK, n)
	})
	if err != nil {
		return nil, err
	}
	passing := r.keepPassing(coarse)
	funnel.Set(StageLatencyOK, len(passing))
	st.advance(len(items))
	if len(passing) == 0 {
		// 粗扫一个都没达标，精扫只是把同样的失败再跑一遍。
		r.logger.Info("粗扫无候选达标，跳过精扫", "probed", len(items))
		return nil, nil
	}

	targets := make([]Candidate, 0, len(passing))
	for _, item := range passing {
		targets = append(targets, item.cand)
	}
	st.begin(len(targets))
	// 精扫不再更新「延迟达标」：那一级记录的是粗扫筛出来的规模，精扫只是
	// 在其中做更准的测量，让它回退会让漏斗看起来在倒着走。
	fine, err := r.probePhase(ctx, rep, st, targets, r.params.PingTimes, nil)
	if err != nil {
		return nil, err
	}
	return r.keepPassing(fine), nil
}

// probePhase 并发探测一批候选，返回每项的探测结果。
//
// onPass 在节流的进度回调里被调用，参数是「到目前为止通过阈值的项数」：
// 探测几千项要跑很久，只在阶段结束时算一次的话，整段等待里界面上什么都
// 不动。为 nil 表示这一阶段不需要更新漏斗。
func (r *Runner) probePhase(
	ctx context.Context,
	rep Reporter,
	st *stages,
	items []Candidate,
	times int,
	onPass func(int),
) ([]probed, error) {
	if len(items) == 0 {
		return nil, nil
	}
	var pass atomic.Int64
	return task.RunBounded(ctx, items, r.params.Workers,
		func(cctx context.Context, cand Candidate) (probed, bool) {
			item, hit := r.probeItem(cctx, cand, times)
			if hit {
				pass.Add(1)
			}
			return item, false
		},
		func(done, _ int) {
			st.advance(done)
			if onPass != nil {
				onPass(int(pass.Load()))
			}
		},
	)
}

// probeItem 探测单个候选并判定是否达标。
//
// 这里自己兜住 panic：单项异常不该带崩整批，但也不能悄悄消失，
// 否则结果条数对不上时无从排查。
func (r *Runner) probeItem(ctx context.Context, cand Candidate, times int) (out probed, hit bool) {
	defer func() {
		if rec := recover(); rec != nil {
			r.logger.Warn("探测异常", "ip", cand.IP, "err", rec)
			out, hit = probed{cand: cand}, false
		}
	}()

	res, err := r.probeWithRetry(ctx, cand, times)
	if err != nil {
		// 单项出错不能吞掉：日志留痕，结果用不可达的哨兵值表示，
		// 后续统计自然把它算作不达标。
		r.logger.Debug("探测出错", "ip", cand.IP, "port", cand.Port, "err", err)
	}
	return probed{cand: cand, res: res}, r.passed(cand, res)
}

// probeWithRetry 按配置重试探测。
//
// 只在「一次都没成功」时重试：已经有样本的结果再跑一遍只会把统计拉长，
// 反而失真。
func (r *Runner) probeWithRetry(ctx context.Context, c Candidate, times int) (probe.Result, error) {
	res, err := r.probeFn(ctx, c, times, r.timeout())
	for attempt := 0; attempt < r.params.Retry && err == nil && res.Recv == 0; attempt++ {
		if ctx.Err() != nil {
			break
		}
		res, err = r.probeFn(ctx, c, times, r.timeout())
	}
	return res, err
}

// keepPassing 留下通过延迟阈值的项。
func (r *Runner) keepPassing(items []probed) []probed {
	out := make([]probed, 0, len(items))
	for _, item := range items {
		if r.passed(item.cand, item.res) {
			out = append(out, item)
		}
	}
	return out
}

// passed 报告一次探测是否达标。
//
// 必须先判可达再比阈值：全部失败时延迟是负数哨兵值，它天然小于任何正
// 阈值，只看大小会把「完全连不上」当成「延迟极低」而放行。
func (r *Runner) passed(c Candidate, res probe.Result) bool {
	if res.Recv <= 0 || res.Latency == model.Unreachable {
		return false
	}
	return res.Latency <= r.threshold(c)
}

// threshold 返回这个候选的延迟阈值。
//
// HTTPing 的阈值要按是否走 TLS 放大：TLS 握手多花 2~3 个往返，用同一把
// 尺子会把所有 TLS 节点误杀。
func (r *Runner) threshold(c Candidate) float64 {
	base := float64(r.params.LatencyThreshold)
	if r.params.Mode == "httping" {
		return base * probe.ThresholdMultiplier(c.UseTLS)
	}
	return base
}

// effectiveSampleMax 返回本次扫描实际使用的采样上限。
func (r *Runner) effectiveSampleMax() int {
	if !r.params.TwoPhase {
		return r.params.SampleMax
	}
	if max := r.params.SampleMax; max > 0 && max < twoPhaseSampleCap {
		return max
	}
	return twoPhaseSampleCap
}

// timeout 返回单次探测的超时。
func (r *Runner) timeout() time.Duration {
	return time.Duration(r.params.TimeoutMS) * time.Millisecond
}

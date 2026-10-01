package speed

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"cloudtrace/internal/model"
	"cloudtrace/internal/probe"
	"cloudtrace/internal/score"
	"cloudtrace/internal/task"
)

// measured 是一条测速结果及其是否合格。
//
// 合格与「测过」是两回事：测出 0.3 MB/s 也是测过了，但没到合格线就不该进
// 结果集。用一个显式标记带出来，比事后靠 speed > 0 反推可靠。
type measured struct {
	rec model.IPRecord
	ok  bool
}

// measure 对全部目标执行下载测速，返回合格的结果。
//
// 三个提前退出条件都在这里汇合：熔断、收敛、用户中止。前两个属于正常结束，
// 只有中止会以错误返回，调用方据父 context 区分。
func (r *Runner) measure(ctx context.Context, rep task.Reporter, st *task.Stages, url string, targets []model.IPRecord) ([]model.IPRecord, error) {
	breaker := NewBreaker(r.params.Breaker429)
	pusher := newPusher(rep, r.now, partialInterval)

	var qualified atomic.Int64
	var started atomic.Int64
	var tripped atomic.Bool

	goal := int64(r.params.TargetQualified)
	interval := time.Duration(r.params.IntervalMS) * time.Millisecond
	duration := time.Duration(r.params.DownloadDurationS) * time.Second

	out, err := task.RunBounded(ctx, targets, r.params.Concurrency,
		func(ctx context.Context, rec model.IPRecord) (measured, bool) {
			// 熔断后不再派发新目标，并让在跑的那些一起停下来：剩下的目标
			// 只会继续拿到被限速的假速度。
			if breaker.Tripped() {
				return measured{rec: rec}, true
			}

			// 串行间隔从第二个目标起生效，第一个目标不必白等。
			if n := started.Add(1); n > 1 && !sleepCtx(ctx, interval) {
				return measured{rec: rec}, true
			}

			return r.measureOne(ctx, rec, url, duration, breaker, &qualified, goal, pusher, &tripped)
		},
		func(done, _ int) { st.Advance(done) },
	)
	pusher.flush()

	if tripped.Load() {
		return keepQualified(out), rateLimitedError(breaker)
	}
	return keepQualified(out), err
}

// measureOne 测一个目标，并决定是否就此收敛。
func (r *Runner) measureOne(
	ctx context.Context,
	rec model.IPRecord,
	url string,
	duration time.Duration,
	breaker *Breaker,
	qualified *atomic.Int64,
	goal int64,
	pusher *pusher,
	tripped *atomic.Bool,
) (measured, bool) {
	useTLS := probe.ResolveUseTLS(r.params.UseTLS, rec.Port)
	mbps, err := r.downloadFn(ctx, rec, url, duration, useTLS)

	switch {
	case errors.Is(err, probe.ErrRateLimited):
		if breaker.RateLimited() {
			tripped.Store(true)
			r.logger.Warn("测速源连续限流，已熔断", "streak", breaker.Streak(), "ip", rec.IP)
			return measured{rec: rec}, true
		}
		return measured{rec: rec}, false

	case err != nil:
		// 取消导致的中止不是失败，别记成一条错误日志。
		if ctx.Err() != nil {
			return measured{rec: rec}, true
		}
		// 非限流的失败同样打断「连续限流」的计数：连不上不等于被限速。
		breaker.Success()
		r.logger.Debug("下载测速失败", "ip", rec.IP, "port", rec.Port, "err", err)
		return measured{rec: rec}, false
	}

	breaker.Success()
	rec.SpeedMBps = mbps
	rec.Score = score.Score(mbps, rec.LatencyAvg, rec.Jitter, r.weights())

	if !r.qualified(mbps) {
		return measured{rec: rec}, false
	}

	pusher.add(rec)
	if qualified.Add(1) >= goal {
		r.logger.Info("已收够合格结果，提前收敛", "qualified", qualified.Load(), "goal", goal)
		return measured{rec: rec, ok: true}, true
	}
	return measured{rec: rec, ok: true}, false
}

// keepQualified 从结果里挑出合格的那些，并保持输入顺序。
func keepQualified(items []measured) []model.IPRecord {
	out := make([]model.IPRecord, 0, len(items))
	for _, item := range items {
		if item.ok {
			out = append(out, item.rec)
		}
	}
	return out
}

// qualified 报告一次测速结果是否达到合格线。
//
// 速度必须严格为正：0 在结果里表示「没测出来」，把它算作合格会让提前收敛
// 在一堆失败目标上触发。
func (r *Runner) qualified(mbps float64) bool {
	return mbps > 0 && mbps >= r.params.MinSpeed
}

// weights 返回评分权重。
func (r *Runner) weights() score.Weights {
	return score.Weights{
		Speed:   r.params.WeightSpeed,
		Latency: r.params.WeightLatency,
		Jitter:  r.params.WeightJitter,
	}
}

// sleepCtx 等待一段时间，中途被取消时返回 false。
//
// 不能用 time.Sleep：那会让「停止」按钮在串行间隔里失灵，用户点了没反应。
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// rateLimitedError 构造熔断错误。
//
// 包一层哨兵错误，服务端据此把它映射成网络类错误码，而不是落到「未分类」。
func rateLimitedError(b *Breaker) error {
	return fmt.Errorf("%w：%s", ErrRateLimited, b.Message())
}

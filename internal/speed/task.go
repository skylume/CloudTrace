package speed

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"cloudtrace/internal/model"
	"cloudtrace/internal/probe"
	"cloudtrace/internal/task"
)

// 默认值。请求里「没带某个字段」与「显式给了零值」在 JSON 上没法区分，
// 因此零值一律按未设置处理。
const (
	defaultConcurrency       = 1
	defaultTargetQualified   = 10
	defaultIntervalMS        = 1200
	defaultDownloadDurationS = 10
	defaultBreaker429        = 3
	defaultTimeoutMS         = 1000
	// defaultWeight 是速度与延迟权重的默认值。抖动权重默认就是 0（不参与），
	// 因此它不做这层兜底。
	defaultWeight = 1.0
)

// maxConcurrency 是测速并发上限。
//
// 并发越高越不准：多个目标同时下载会互相抢带宽，测出来的是「平均值」而不是
// 任何一个节点的真实速度。上限给到 16 是因为确实有人只想粗筛一遍。
const maxConcurrency = 16

// TopicPartial 是测速结果的增量推送，载荷为 []model.IPRecord。
const TopicPartial = "speed/partial"

// ErrRateLimited 表示测速源连续限流，已熔断。
//
// 熔断不是「测速完成」：继续测下去拿到的速度全是假的，会让用户按错误的结果
// 去配置节点。服务端据本错误把它报成网络类错误。
var ErrRateLimited = errors.New("测速源连续限流")

// DownloadFunc 测一个目标的下载速度（MB/s）。
type DownloadFunc func(ctx context.Context, target model.IPRecord, url string, duration time.Duration, useTLS bool) (float64, error)

// UsabilityFunc 在测速前快速确认目标是否还活着。
type UsabilityFunc func(ctx context.Context, target model.IPRecord, timeout time.Duration) (bool, error)

// SourceFunc 解析本次使用的测速地址。
type SourceFunc func(ctx context.Context, mode, customURL string) (string, error)

// Options 是测速执行器的构造参数。
//
// 网络原语全部从这里进来：测速流程本身不碰全局状态，离线测试才能复现出
// 同一批结果。
type Options struct {
	Params model.SpeedParams

	// 以下依赖为 nil 时使用真实网络实现。
	Download  DownloadFunc
	Usability UsabilityFunc
	Source    SourceFunc
	// Now 取时间，为 nil 时用 time.Now。注入是为了让推送节流可测。
	Now    func() time.Time
	Logger *slog.Logger

	// OnDone 在任务**正常跑完**时回调一次，交出本次的完整结果。
	//
	// 提前收敛也算正常跑完：用户要的就是够用的那批，把收敛后的结果存下来
	// 正是这个功能的意义。中止则不算——中止的结果残缺，存档会污染历史。
	//
	// 结果走这里而不是走事件总线：总线在队列满时会丢事件，前端丢几条只是
	// 少渲染几行，存档丢几条就是永久缺数据。
	OnDone func(model.TaskResult)
}

// Runner 按固定流水线执行一次测速。
type Runner struct {
	params     model.SpeedParams
	downloadFn DownloadFunc
	usability  UsabilityFunc
	sourceFn   SourceFunc
	now        func() time.Time
	logger     *slog.Logger
	onDone     func(model.TaskResult)
}

// NormalizeParams 把缺省字段补成可用值。
func NormalizeParams(p model.SpeedParams) model.SpeedParams {
	if p.Scope == "" {
		p.Scope = model.SpeedScopeSingle
	}
	if p.URLMode == "" {
		p.URLMode = URLModeAuto
	}
	if p.UseTLS == "" {
		p.UseTLS = probe.UseTLSAuto
	}
	if p.IPVersion == 0 {
		p.IPVersion = 4
	}
	if p.Concurrency == 0 {
		p.Concurrency = defaultConcurrency
	}
	if p.TargetQualified == 0 {
		p.TargetQualified = defaultTargetQualified
	}
	if p.IntervalMS == 0 {
		p.IntervalMS = defaultIntervalMS
	}
	if p.DownloadDurationS == 0 {
		p.DownloadDurationS = defaultDownloadDurationS
	}
	if p.Breaker429 == 0 {
		p.Breaker429 = defaultBreaker429
	}
	if p.TimeoutMS == 0 {
		p.TimeoutMS = defaultTimeoutMS
	}
	// 权重为 0 会让所有评分都变成 0，结果表看起来像「全都不合格」。
	// 抖动权重默认就是 0，是有意义的取值，因此只兜底前两项。
	if p.WeightSpeed == 0 {
		p.WeightSpeed = defaultWeight
	}
	if p.WeightLatency == 0 {
		p.WeightLatency = defaultWeight
	}
	return p
}

// ValidateParams 校验测速参数，返回的问题可以直接展示给用户。
func ValidateParams(p model.SpeedParams) error {
	switch p.Scope {
	case model.SpeedScopeSingle, model.SpeedScopeRegion, model.SpeedScopeAll:
	default:
		return fmt.Errorf("测速范围 %q 无法识别", p.Scope)
	}
	if len(p.Targets) == 0 {
		return errors.New("没有选中任何待测速的节点")
	}
	for i, rec := range p.Targets {
		if rec.IP == "" {
			return fmt.Errorf("第 %d 个待测节点的地址为空", i+1)
		}
		if rec.Port <= 0 || rec.Port > 65535 {
			return fmt.Errorf("第 %d 个待测节点的端口 %d 不在 1..65535 范围内", i+1, rec.Port)
		}
	}

	switch p.URLMode {
	case URLModeAuto, URLModeOfficial, URLModeMobileFriendly, URLModeMobileOnly:
	case URLModeCustom:
		if err := checkSpeedURL(p.CustomURL); err != nil {
			return err
		}
	default:
		return fmt.Errorf("测速源模式 %q 无法识别", p.URLMode)
	}

	switch p.UseTLS {
	case probe.UseTLSAuto, probe.UseTLSOn, probe.UseTLSOff:
	default:
		return fmt.Errorf("TLS 设置 %q 只能是 auto / true / false", p.UseTLS)
	}

	if p.Concurrency < 1 || p.Concurrency > maxConcurrency {
		return fmt.Errorf("测速并发 %d 不在 1..%d 范围内", p.Concurrency, maxConcurrency)
	}
	if p.TargetQualified < 1 {
		return fmt.Errorf("提前收敛的目标数 %d 必须至少为 1", p.TargetQualified)
	}
	if p.IntervalMS < 0 {
		return fmt.Errorf("测速间隔 %d 不能为负", p.IntervalMS)
	}
	if p.MinSpeed < 0 {
		return fmt.Errorf("最小速度 %v 不能为负", p.MinSpeed)
	}
	if p.WeightSpeed < 0 || p.WeightLatency < 0 || p.WeightJitter < 0 {
		return errors.New("评分权重不能为负")
	}
	if p.PerRegionTopN < 0 {
		return fmt.Errorf("分地区 TopN %d 不能为负", p.PerRegionTopN)
	}
	if p.DownloadDurationS < 1 {
		return fmt.Errorf("单次下载时长 %d 秒必须至少为 1", p.DownloadDurationS)
	}
	if p.Breaker429 < 1 {
		return fmt.Errorf("熔断阈值 %d 必须至少为 1", p.Breaker429)
	}
	if p.TimeoutMS <= 0 {
		return fmt.Errorf("可用性校验超时 %d 毫秒必须为正", p.TimeoutMS)
	}
	return nil
}

// NewRunner 构造测速执行器，并补齐未注入的依赖。
func NewRunner(opts Options) (*Runner, error) {
	params := NormalizeParams(opts.Params)
	if err := ValidateParams(params); err != nil {
		return nil, err
	}

	r := &Runner{
		params:     params,
		downloadFn: opts.Download,
		usability:  opts.Usability,
		sourceFn:   opts.Source,
		now:        opts.Now,
		logger:     opts.Logger,
		onDone:     opts.OnDone,
	}
	if r.now == nil {
		r.now = time.Now
	}
	if r.logger == nil {
		r.logger = slog.Default()
	}
	if r.downloadFn == nil {
		r.downloadFn = func(ctx context.Context, target model.IPRecord, url string, duration time.Duration, useTLS bool) (float64, error) {
			return probe.Download(ctx, target.IP, target.Port, url, duration, useTLS)
		}
	}
	if r.usability == nil {
		r.usability = func(ctx context.Context, target model.IPRecord, timeout time.Duration) (bool, error) {
			res, err := probe.TCPing(ctx, target.IP, target.Port, 1, timeout)
			if err != nil {
				return false, err
			}
			return res.Recv > 0, nil
		}
	}
	if r.sourceFn == nil {
		resolver := NewSourceResolver(nil, r.now, DefaultSourceTTL)
		r.sourceFn = resolver.Resolve
	}
	return r, nil
}

// Run 执行一次测速，返回合格结果条数。
//
// 流水线：挑目标 → 可用性预校验（可关）→ 选源 → 下载测速 → 熔断与收敛 →
// 评分并实时推送 → 汇总。顺序不可调换：可用性校验放在下载之后就没有意义，
// 选源放在下载之后则无处可用。
// SetOnDone 设置任务跑完后的结果回调。
//
// 与构造参数里的 OnDone 等价，单独给一个设置入口是为了让调用方能在拿到
// 执行器之后再接线：回调里往往要用到执行器本身。
func (r *Runner) SetOnDone(fn func(model.TaskResult)) { r.onDone = fn }

func (r *Runner) Run(rep task.Reporter) (int, error) {
	ctx := rep.Context()

	targets := selectTargets(r.params)
	if len(targets) == 0 {
		return 0, errors.New("没有可测速的目标")
	}
	// 漏斗复用扫描的三个计数：生成 = 待测目标，延迟达标 = 通过可用性校验，
	// 可用 = 达到合格线。地区解析对测速没有意义，保持为 0。
	rep.SetFunnel(model.Funnel{Generated: len(targets)})

	url, err := r.sourceFn(ctx, r.params.URLMode, r.params.CustomURL)
	if err != nil {
		return 0, err
	}

	st := task.NewStages(rep)
	usable := targets
	if r.params.UsabilityCheck {
		st.Begin(len(targets))
		usable, err = r.checkUsable(ctx, st, targets)
		if err != nil {
			return 0, err
		}
		if len(usable) == 0 {
			return 0, errors.New("可用性校验后没有剩余目标，请重新扫描")
		}
	}
	rep.SetFunnel(model.Funnel{Generated: len(targets), LatencyOK: len(usable)})

	st.Begin(len(usable))
	results, err := r.measure(ctx, rep, st, url, usable)
	rep.SetFunnel(model.Funnel{
		Generated: len(targets),
		LatencyOK: len(usable),
		Usable:    len(results),
	})
	if err != nil {
		return len(results), err
	}

	st.Finish()
	r.logger.Info("测速结束",
		"targets", len(targets), "usable", len(usable), "qualified", len(results), "url", url)

	r.finish(ctx, results)
	return len(results), nil
}

// finish 在任务正常跑完时把完整结果交出去。
//
// 提前收敛也算「正常跑完」：用户要的就是够用的那批结果，把收敛后的结果存
// 下来正是这个功能的意义。中止则不算——中止的结果残缺，存档会污染历史。
func (r *Runner) finish(ctx context.Context, records []model.IPRecord) {
	if r.onDone == nil || ctx.Err() != nil {
		return
	}
	r.onDone(model.NewTaskResult(records))
}

// usability 是一个目标及其是否通过可用性校验。
type usability struct {
	rec model.IPRecord
	ok  bool
}

// checkUsable 逐个确认目标是否还活着，返回仍可用的那些。
//
// 用与测速相同的并发数：这一步比测速便宜得多，没必要再单独设一个参数。
func (r *Runner) checkUsable(ctx context.Context, st *task.Stages, targets []model.IPRecord) ([]model.IPRecord, error) {
	timeout := time.Duration(r.params.TimeoutMS) * time.Millisecond

	out, err := task.RunBounded(ctx, targets, r.params.Concurrency,
		func(ctx context.Context, rec model.IPRecord) (usability, bool) {
			ok, uerr := r.usability(ctx, rec, timeout)
			if uerr != nil {
				// 校验本身出错（而不是「不可用」）时保守保留：多测一个只是
				// 浪费一点时间，误杀会让用户永远测不到这个节点。
				r.logger.Debug("可用性校验失败，保留该节点", "ip", rec.IP, "err", uerr)
				return usability{rec: rec, ok: true}, false
			}
			if !ok {
				r.logger.Debug("可用性校验未通过，跳过", "ip", rec.IP, "port", rec.Port)
			}
			// 第二个返回值是「触发提前收敛」，这里恒为 false：校验阶段没有
			// 「够了」这回事，返回 true 会让它在第一项之后就不再派发。
			return usability{rec: rec, ok: ok}, false
		},
		func(done, _ int) { st.Advance(done) },
	)
	if err != nil {
		return nil, err
	}

	kept := make([]model.IPRecord, 0, len(out))
	for _, item := range out {
		if item.ok {
			kept = append(kept, item.rec)
		}
	}
	return kept, nil
}

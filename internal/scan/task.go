package scan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"cloudtrace/internal/model"
	"cloudtrace/internal/probe"
	"cloudtrace/internal/source"
	"cloudtrace/internal/task"
)

// 扫描流程的常量。
const (
	// twoPhaseSampleCap 是两阶段扫描里粗扫阶段的采样上限。
	//
	// 粗扫的价值就是「十几秒先给出一批结果」，所以第一遍的规模必须封顶：
	// 用户把采样上限调到 5000 时若不封顶，粗扫一样要跑几分钟，两阶段就
	// 白设了。
	twoPhaseSampleCap = 500

	// perSlash24 是每个 /24 网段抽取的候选数。
	perSlash24 = 1

	// defaultTestHost 是 HTTP 探测与 trace 采集使用的测试域名。
	//
	// 用 Cloudflare 自家的测速域名：它由任意 CF 节点响应，trace 里回显的
	// 才是那个节点的真实归属。
	defaultTestHost = "speed.cloudflare.com"

	// cfRounds 是节点校验的轮数。
	//
	// 单轮结论太容易被一次网络抖动左右，因此至少三轮；探测层对不足三轮
	// 的请求也会自行抬到三轮。
	cfRounds = 3

	// resultChunk 是单次结果推送的最大条数。
	//
	// 把几千条记录塞进一个 WebSocket 帧会顶到帧大小上限，前端也要在一帧
	// 里做完全部渲染；分批推送让结果逐段出现。
	resultChunk = 50

	// defaultPingTimes 是探测次数留空（0）时使用的值。
	//
	// 0 的含义是「不指定，由默认值决定」，界面上也把它显示成「自动」。
	// 必须在补默认值这一步就换成一个真实次数：校验只接受 ≥1，原样带过去
	// 会被当成非法参数拒掉，而用户只是没填而已。
	defaultPingTimes = 4
)

// TopicResult 是扫描结果的增量推送，载荷为 []model.IPRecord。
const TopicResult = "scan/result"

// TopicNearest 是零结果时的回看推送。
//
// 只在一条都没达标时发一次：有结果时这些数据没有用处，白白多推一帧。
const TopicNearest = "scan/nearest"

// NearestPayload 是「差多少」的回看载荷。
type NearestPayload struct {
	// Threshold 是本次的延迟上限（毫秒），前端据此判断差了多少。
	Threshold float64 `json:"threshold"`
	// Items 是最接近阈值的若干条探测结果，按延迟升序。
	Items []model.IPRecord `json:"items"`
}

// ProbeFunc 探测单个候选的延迟统计。
type ProbeFunc func(ctx context.Context, c Candidate, times int, timeout time.Duration) (probe.Result, error)

// TraceFunc 采集单个候选的 trace 全字段。
type TraceFunc func(ctx context.Context, c Candidate, timeout time.Duration) (map[string]string, error)

// VerifyFunc 校验单个候选是否真的是 Cloudflare 节点。
type VerifyFunc func(ctx context.Context, c Candidate, rounds int, timeout time.Duration) (probe.CFResult, error)

// RemoteFunc 拉取远程源，返回解析出的记录与拉取失败的地址。
type RemoteFunc func(ctx context.Context, urls []string) ([]model.IPRecord, []string, error)

// Options 是扫描执行器的构造参数。
//
// 网络、DNS、随机种子全部从这里进来：扫描流程本身不碰全局状态，
// 离线测试才能复现出同一批候选与同一批结果。
type Options struct {
	Params model.ScanParams

	// Host 是 HTTP 探测与 trace 采集使用的测试域名，为空时取默认值。
	Host string
	// RemoteURLs 是远程源地址；为空时不发起任何远程拉取。
	RemoteURLs []string
	// RemoteOptions 是远端拉取的重试、间隔与合并方式。
	//
	// 零值表示「不重试、按并集合并」，与旧行为一致。
	RemoteOptions source.RemoteOptions
	// Seed 是采样的随机种子。
	Seed int64

	// Skip 是已经测过的地址，续测时跳过它们，不整批重来。
	//
	// 按 IP 而不是 IP:端口 去重：同一个地址换个端口重扫一遍对用户没有
	// 意义，他要的是「还有哪些没试过」。
	Skip map[string]struct{}

	// Enrich 在每条结果产出后补齐归属地（地区中文名、ASN、组织名）。
	//
	// 为 nil 时不做任何补齐。它会被每个节点调用一次，因此实现里**不允许
	// 发网络请求**：一次往返就会让整轮扫描慢一个数量级。
	Enrich func(rec *model.IPRecord)

	// 以下依赖为 nil 时使用真实网络实现。
	Probe    ProbeFunc
	Trace    TraceFunc
	Verify   VerifyFunc
	Remote   RemoteFunc
	Resolver source.Resolver
	Logger   *slog.Logger

	// OnDone 在任务结束时回调一次，交出本次的全部结果。
	//
	// 正常跑完与用户中止都会调用：中止的结果是半截的，但对「接着拿这批去
	// 测速」有用，所以照样交出——由调用方标成「已中止」，不会和完整扫描
	// 混在一起。
	//
	// 结果走这里而不是走事件总线：总线在队列满时会丢事件，前端丢几条只是
	// 少渲染几行，存档丢几条就是永久缺数据。
	OnDone func(model.TaskResult)
}

// Runner 按固定流水线执行一次扫描。
type Runner struct {
	params     model.ScanParams
	host       string
	remoteURLs []string
	// remoteOptions 是远端拉取的重试、间隔与合并方式。
	remoteOptions source.RemoteOptions
	seed          int64
	skip          map[string]struct{}

	probeFn  ProbeFunc
	traceFn  TraceFunc
	verifyFn VerifyFunc
	remoteFn RemoteFunc
	resolver source.Resolver
	logger   *slog.Logger
	enrich   func(rec *model.IPRecord)
	onDone   func(model.TaskResult)
}

// NormalizeParams 把缺省字段补成可用值。
//
// 请求里「没带某个字段」与「显式给了空值」在 JSON 上没法区分，所以统一
// 在这里按默认值补，补完再校验。
func NormalizeParams(p model.ScanParams) model.ScanParams {
	if p.Mode == "" {
		p.Mode = "tcping"
	}
	if p.SourceMode == "" {
		p.SourceMode = "official"
	}
	if p.IPVersion == 0 {
		p.IPVersion = 4
	}
	if p.PingTimes == 0 {
		p.PingTimes = defaultPingTimes
	}
	return p
}

// ValidateParams 校验扫描参数，返回的问题可以直接展示给用户。
func ValidateParams(p model.ScanParams) error {
	if p.Port <= 0 || p.Port > 65535 {
		return fmt.Errorf("端口 %d 不在 1..65535 范围内", p.Port)
	}
	if p.Workers < 1 {
		return fmt.Errorf("并发数 %d 必须至少为 1", p.Workers)
	}
	if p.SampleMax < 0 {
		return fmt.Errorf("采样上限 %d 不能为负", p.SampleMax)
	}
	if p.LatencyThreshold <= 0 {
		return fmt.Errorf("延迟阈值 %d 必须为正", p.LatencyThreshold)
	}
	if p.PingTimes < 1 {
		return fmt.Errorf("探测次数 %d 必须至少为 1", p.PingTimes)
	}
	if p.TimeoutMS <= 0 {
		return fmt.Errorf("探测超时 %d 毫秒必须为正", p.TimeoutMS)
	}
	if p.Retry < 0 {
		return fmt.Errorf("重试次数 %d 不能为负", p.Retry)
	}
	switch p.Mode {
	case "tcping", "httping":
	default:
		return fmt.Errorf("探测模式 %q 无法识别", p.Mode)
	}
	switch p.SourceMode {
	case "official", "custom", "both":
	default:
		return fmt.Errorf("来源模式 %q 无法识别", p.SourceMode)
	}
	if p.IPVersion != 4 && p.IPVersion != 6 {
		return fmt.Errorf("地址族 %d 只能是 4 或 6", p.IPVersion)
	}
	if p.UsesCustom() {
		if strings.TrimSpace(p.CustomSource) == "" {
			return errors.New("来源模式包含自定义，但来源内容为空")
		}
		// 解析放在校验里做：来源文本写错格式是很常见的事，
		// 早点报错比让任务跑起来再失败友好得多。
		if _, err := source.ParseSourceText(p.CustomSource); err != nil {
			return err
		}
	}
	return nil
}

// NewRunner 构造扫描执行器，并补齐未注入的依赖。
func NewRunner(opts Options) (*Runner, error) {
	params := NormalizeParams(opts.Params)
	if err := ValidateParams(params); err != nil {
		return nil, err
	}

	r := &Runner{
		params:        params,
		host:          opts.Host,
		remoteURLs:    opts.RemoteURLs,
		remoteOptions: opts.RemoteOptions,
		seed:          opts.Seed,
		probeFn:       opts.Probe,
		traceFn:       opts.Trace,
		verifyFn:      opts.Verify,
		skip:          opts.Skip,
		remoteFn:      opts.Remote,
		resolver:      opts.Resolver,
		logger:        opts.Logger,
		enrich:        opts.Enrich,
		onDone:        opts.OnDone,
	}
	if r.host == "" {
		r.host = defaultTestHost
	}
	if r.logger == nil {
		r.logger = slog.Default()
	}
	if r.resolver == nil {
		r.resolver = source.DefaultResolver()
	}
	if r.probeFn == nil {
		r.probeFn = func(ctx context.Context, c Candidate, times int, timeout time.Duration) (probe.Result, error) {
			if r.params.Mode == "httping" {
				return probe.HTTPing(ctx, c.IP, c.Port, r.host, c.UseTLS, times, timeout)
			}
			return probe.TCPing(ctx, c.IP, c.Port, times, timeout)
		}
	}
	if r.traceFn == nil {
		r.traceFn = func(ctx context.Context, c Candidate, timeout time.Duration) (map[string]string, error) {
			return probe.FetchTrace(ctx, c.IP, c.Port, r.host, c.UseTLS, timeout)
		}
	}
	if r.verifyFn == nil {
		r.verifyFn = func(ctx context.Context, c Candidate, rounds int, timeout time.Duration) (probe.CFResult, error) {
			target := probe.CFTarget{IP: c.IP, Port: c.Port, Host: r.host, UseTLS: c.UseTLS}
			return probe.VerifyCF(ctx, target, rounds, timeout)
		}
	}
	if r.remoteFn == nil {
		r.remoteFn = func(ctx context.Context, urls []string) ([]model.IPRecord, []string, error) {
			result, err := source.FetchRemote(ctx, urls, r.remoteOptions)
			if err != nil {
				return nil, nil, err
			}
			failed := make([]string, 0, len(result.Failed))
			for _, f := range result.Failed {
				failed = append(failed, f.URL)
			}
			return result.Records, failed, nil
		}
	}
	return r, nil
}

// SetOnDone 设置任务跑完后的结果回调。
//
// 与构造参数里的 OnDone 等价，单独给一个设置入口是为了让调用方能在拿到
// 执行器之后再接线：回调里往往要用到执行器本身。
func (r *Runner) SetOnDone(fn func(model.TaskResult)) { r.onDone = fn }

// Run 按流水线执行一次扫描，返回最终结果条数。
//
// 流水线顺序不可调换：前置过滤必须在任何 TCP 探测之前，延迟阈值过滤必须
// 在探测之后，归属地解析必须在明细采集之后（它复用的就是采集到的 trace
// 字段，零额外请求）。
func (r *Runner) Run(rep task.Reporter) (int, error) {
	ctx := rep.Context()
	funnel := NewFunnel(func(f model.Funnel) { rep.SetFunnel(f) })
	st := task.NewStages(rep)
	sink := &resultSink{rep: rep}

	// 阶段统计按完成顺序追加；每次整体替换，编排层负责拷贝。
	stages := make([]model.StageStat, 0, 4)
	report := func(stat model.StageStat) {
		stages = append(stages, stat)
		task.SetStages(rep, stages)
	}

	// ① 生成候选池。
	poolStart := time.Now()
	pool, err := buildPool(ctx, poolOptions{
		Params:     r.params,
		Host:       r.host,
		SampleMax:  r.effectiveSampleMax(),
		Seed:       r.seed,
		RemoteURLs: r.remoteURLs,
	}, r.remoteFn, r.resolver)
	if err != nil {
		return 0, err
	}
	generated := len(pool.Candidates)
	if len(r.skip) > 0 {
		pool.Candidates = dropKnown(pool.Candidates, r.skip)
	}
	funnel.Set(StageGenerated, len(pool.Candidates))
	// 生成阶段：OK 是进入探测的候选，Skipped 是已测过被跳过的那些。
	report(model.NewStageStat(model.StagePool, generated, len(pool.Candidates), 0, generated-len(pool.Candidates), time.Since(poolStart).Seconds()))
	r.logger.Info("候选池生成完毕",
		"candidates", len(pool.Candidates),
		"skipped_known", generated-len(pool.Candidates),
		"explicit", pool.Explicit,
		"sampled", pool.Sampled,
		"hosts_failed", len(pool.DroppedHosts),
		"remote_failed", len(pool.RemoteFailed))
	if len(pool.Candidates) == 0 {
		// 候选本来就有、只是全在已测集合里：这是续测跑完了，不是来源配错。
		if generated > 0 && len(r.skip) > 0 {
			return 0, &CodedError{Code: CodeAllSkipped, Msg: "这批地址都测过了，没有新的可扫"}
		}
		return 0, &CodedError{Code: CodeNoCandidate, Msg: "没有生成任何候选，请检查来源设置"}
	}

	// ② 前置过滤：这是「省掉无效探测」的唯一机会，放到探测之后等于白做。
	filterStart := time.Now()
	filtered := PreFilter(pool.Candidates, PreFilterOptions{
		Ports:          r.params.PreFilterPorts,
		BlockedRegions: r.params.BlockedRegions,
		AllowedRegions: r.params.AllowedRegions,
	})
	filterRejected := filtered.PortRejected + filtered.BlockRejected + filtered.AllowRejected
	report(model.NewStageStat(model.StageFilter, len(pool.Candidates), len(filtered.Kept), filterRejected, 0, time.Since(filterStart).Seconds()))
	r.logger.Info("前置过滤完成",
		"kept", len(filtered.Kept),
		"port_rejected", filtered.PortRejected,
		"block_rejected", filtered.BlockRejected,
		"allow_rejected", filtered.AllowRejected)
	if len(filtered.Kept) == 0 {
		return 0, &CodedError{Code: CodeAllFiltered, Msg: "前置过滤后没有剩余候选，请检查端口与地区设置"}
	}

	// ③④ 延迟探测与阈值过滤。
	probeStart := time.Now()
	passing, rejected, err := r.probeStages(ctx, rep, st, funnel, filtered.Kept)
	report(model.NewStageStat(model.StageProbe, len(filtered.Kept), len(passing), len(rejected), 0, time.Since(probeStart).Seconds()))
	if err != nil {
		return 0, err
	}

	// ⑤⑥ 节点校验、明细采集与归属地解析。
	collectStart := time.Now()
	records, err := r.collect(ctx, rep, st, sink, passing)
	report(model.NewStageStat(model.StageCollect, len(passing), len(records), len(passing)-len(records), 0, time.Since(collectStart).Seconds()))
	// 无论成功还是中止，已经产出的结果都要推出去：中止时用户要能保留
	// 已扫出的那部分。
	sink.flush()
	if err != nil {
		// 中止时也交付：存档要拿到这一批，历史里才查得到。是否算「完成」
		// 由编排层按 context 判定，这里只交数据。
		r.finish(records)
		return len(records), err
	}

	// 一条都没达标时补一批「差多少」的回看：否则用户只能靠重跑一次才知道
	// 是阈值定高了，还是这批地址根本不通。
	if len(records) == 0 {
		r.emitNearest(rep, rejected)
	}

	// ⑦ 汇总。
	funnel.Set(StageRegionOK, countRegion(records))
	funnel.Set(StageUsable, len(records))
	st.Finish()

	r.logger.Info("扫描结束",
		"generated", len(pool.Candidates),
		"latency_ok", funnel.Snapshot().LatencyOK,
		"usable", len(records))

	r.finish(records)
	return len(records), nil
}

// emitNearest 在零结果时补发一批最接近阈值的探测结果。
//
// 不带明细采集与归属地补齐：这一步发生在任务已经结束之后，再发网络请求
// 既拖长收尾，也让「零结果」这件事变得不干脆。
func (r *Runner) emitNearest(rep task.Reporter, rejected []probed) {
	items := nearestReachable(rejected, nearestLimit)
	if len(items) == 0 {
		return
	}
	recs := make([]model.IPRecord, 0, len(items))
	for _, item := range items {
		recs = append(recs, recordOf(item))
	}
	rep.Emit(TopicNearest, NearestPayload{
		Threshold: float64(r.params.LatencyThreshold),
		Items:     recs,
	})
}

// finish 把本次已产出的结果交出去。
//
// 中止时同样交付：用户按了停止，但扫出来的那批节点仍然有用，历史里要能
// 接着拿去测速。调用方会把它标成「已中止」，所以不会被当成一次完整扫描。
func (r *Runner) finish(records []model.IPRecord) {
	if r.onDone == nil {
		return
	}
	r.onDone(model.NewTaskResult(records))
}

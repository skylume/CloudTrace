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
)

// TopicResult 是扫描结果的增量推送，载荷为 []model.IPRecord。
const TopicResult = "scan/result"

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
	// Seed 是采样的随机种子。
	Seed int64

	// 以下依赖为 nil 时使用真实网络实现。
	Probe    ProbeFunc
	Trace    TraceFunc
	Verify   VerifyFunc
	Remote   RemoteFunc
	Resolver source.Resolver
	Logger   *slog.Logger
}

// Runner 按固定流水线执行一次扫描。
type Runner struct {
	params     model.ScanParams
	host       string
	remoteURLs []string
	seed       int64

	probeFn  ProbeFunc
	traceFn  TraceFunc
	verifyFn VerifyFunc
	remoteFn RemoteFunc
	resolver source.Resolver
	logger   *slog.Logger
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
		params:     params,
		host:       opts.Host,
		remoteURLs: opts.RemoteURLs,
		seed:       opts.Seed,
		probeFn:    opts.Probe,
		traceFn:    opts.Trace,
		verifyFn:   opts.Verify,
		remoteFn:   opts.Remote,
		resolver:   opts.Resolver,
		logger:     opts.Logger,
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
			result, err := source.FetchRemote(ctx, urls, source.RemoteOptions{})
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

	// ① 生成候选池。
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
	funnel.Set(StageGenerated, len(pool.Candidates))
	r.logger.Info("候选池生成完毕",
		"candidates", len(pool.Candidates),
		"explicit", pool.Explicit,
		"sampled", pool.Sampled,
		"hosts_failed", len(pool.DroppedHosts),
		"remote_failed", len(pool.RemoteFailed))
	if len(pool.Candidates) == 0 {
		return 0, errors.New("没有生成任何候选，请检查来源设置")
	}

	// ② 前置过滤：这是「省掉无效探测」的唯一机会，放到探测之后等于白做。
	filtered := PreFilter(pool.Candidates, PreFilterOptions{
		Ports:          r.params.PreFilterPorts,
		BlockedRegions: r.params.BlockedRegions,
		AllowedRegions: r.params.AllowedRegions,
	})
	r.logger.Info("前置过滤完成",
		"kept", len(filtered.Kept),
		"port_rejected", filtered.PortRejected,
		"block_rejected", filtered.BlockRejected,
		"allow_rejected", filtered.AllowRejected)
	if len(filtered.Kept) == 0 {
		return 0, errors.New("前置过滤后没有剩余候选，请检查端口与地区设置")
	}

	// ③④ 延迟探测与阈值过滤。
	passing, err := r.probeStages(ctx, rep, st, funnel, filtered.Kept)
	if err != nil {
		return 0, err
	}

	// ⑤⑥ 节点校验、明细采集与归属地解析。
	records, err := r.collect(ctx, rep, st, sink, passing)
	// 无论成功还是中止，已经产出的结果都要推出去：中止时用户要能保留
	// 已扫出的那部分。
	sink.flush()
	if err != nil {
		return len(records), err
	}

	// ⑦ 汇总。
	funnel.Set(StageRegionOK, countRegion(records))
	funnel.Set(StageUsable, len(records))
	st.Finish()

	r.logger.Info("扫描结束",
		"generated", len(pool.Candidates),
		"latency_ok", funnel.Snapshot().LatencyOK,
		"usable", len(records))
	return len(records), nil
}

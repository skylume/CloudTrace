package speed

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"

	"cloudtrace/internal/model"
	"cloudtrace/internal/probe"
)

// ---------------------------------------------------------------------------
// 测试替身
// ---------------------------------------------------------------------------

// fakeReporter 收集进度、漏斗与事件，供断言使用。
type fakeReporter struct {
	mu     sync.Mutex
	ctx    context.Context
	total  int
	done   int
	funnel model.Funnel
	events map[string][]any
}

func newFakeReporter(ctx context.Context) *fakeReporter {
	return &fakeReporter{ctx: ctx, events: make(map[string][]any)}
}

func (f *fakeReporter) Context() context.Context { return f.ctx }

func (f *fakeReporter) SetTotal(total int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.total = total
}

func (f *fakeReporter) SetDone(done int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.done = done
}

func (f *fakeReporter) SetFunnel(m model.Funnel) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.funnel = m
}

func (f *fakeReporter) SetPreset(string) {}

func (f *fakeReporter) Emit(topic string, payload any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events[topic] = append(f.events[topic], payload)
}

func (f *fakeReporter) snapshot() (total, done int, funnel model.Funnel) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.total, f.done, f.funnel
}

// partials 把分批推送的测速结果合并回一个列表。
func (f *fakeReporter) partials() []model.IPRecord {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []model.IPRecord
	for _, payload := range f.events[TopicPartial] {
		chunk, ok := payload.([]model.IPRecord)
		if !ok {
			panic("speed/partial 载荷不是 []model.IPRecord")
		}
		out = append(out, chunk...)
	}
	return out
}

// fakeDownloader 记录每次下载调用的参数，并按注入的函数作答。
type fakeDownloader struct {
	mu       sync.Mutex
	calls    []string
	urls     []string
	tlsModes []bool
	duration time.Duration
	fn       func(ctx context.Context, target model.IPRecord) (float64, error)
}

func (d *fakeDownloader) download(ctx context.Context, target model.IPRecord, url string, duration time.Duration, useTLS bool) (float64, error) {
	d.mu.Lock()
	d.calls = append(d.calls, target.IP)
	d.urls = append(d.urls, url)
	d.tlsModes = append(d.tlsModes, useTLS)
	d.duration = duration
	fn := d.fn
	d.mu.Unlock()

	if fn == nil {
		return 1, nil
	}
	return fn(ctx, target)
}

func (d *fakeDownloader) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.calls)
}

func (d *fakeDownloader) calledIPs() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.calls...)
}

func (d *fakeDownloader) lastURL() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.urls) == 0 {
		return ""
	}
	return d.urls[len(d.urls)-1]
}

func (d *fakeDownloader) tls() []bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]bool(nil), d.tlsModes...)
}

// speedByIP 返回一个按 IP 查表作答的下载函数。
func speedByIP(speeds map[string]float64) func(context.Context, model.IPRecord) (float64, error) {
	return func(_ context.Context, target model.IPRecord) (float64, error) {
		mbps, ok := speeds[target.IP]
		if !ok {
			return 0, fmt.Errorf("测试未定义 %s 的速度", target.IP)
		}
		return mbps, nil
	}
}

// fakeUsability 记录可用性校验调用，并按 IP 决定结论。
type fakeUsability struct {
	mu       sync.Mutex
	calls    []string
	unusable map[string]bool
	errs     map[string]error
}

func (u *fakeUsability) check(_ context.Context, target model.IPRecord, _ time.Duration) (bool, error) {
	u.mu.Lock()
	u.calls = append(u.calls, target.IP)
	u.mu.Unlock()

	if err, ok := u.errs[target.IP]; ok {
		return false, err
	}
	return !u.unusable[target.IP], nil
}

func (u *fakeUsability) count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.calls)
}

// deps 汇总一次测速的全部可注入依赖。
type deps struct {
	downloader *fakeDownloader
	usability  *fakeUsability
	sourceURL  string
	sourceErr  error
	sourceFn   SourceFunc
	now        func() time.Time
}

func newDeps() *deps {
	return &deps{
		downloader: &fakeDownloader{},
		usability:  &fakeUsability{unusable: map[string]bool{}, errs: map[string]error{}},
		sourceURL:  officialSpeedURL,
	}
}

func (d *deps) options(t *testing.T, params model.SpeedParams) Options {
	t.Helper()
	sourceFn := d.sourceFn
	if sourceFn == nil {
		url, err := d.sourceURL, d.sourceErr
		sourceFn = func(context.Context, string, string) (string, error) { return url, err }
	}
	return Options{
		Params:    params,
		Download:  d.downloader.download,
		Usability: d.usability.check,
		Source:    sourceFn,
		Now:       d.now,
		Logger:    slog.New(slog.NewTextHandler(discard{}, nil)),
	}
}

func (d *deps) runner(t *testing.T, params model.SpeedParams) *Runner {
	t.Helper()
	r, err := NewRunner(d.options(t, params))
	if err != nil {
		t.Fatalf("构造测速执行器失败：%v", err)
	}
	return r
}

// run 构造执行器并跑一次，返回结果条数与错误。
func (d *deps) run(t *testing.T, params model.SpeedParams, rep *fakeReporter) (int, error) {
	t.Helper()
	return d.runner(t, params).Run(rep)
}

// discard 丢弃日志输出，避免用例刷屏。
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// baseParams 返回一份最小可用参数：单点测速、不做可用性校验、间隔压到 1ms。
//
// 间隔的 0 值会被补齐成 1200ms（防限流的安全默认），测试里必须给一个极小
// 的正数，否则每个目标之间白等一秒多。
func baseParams(targets ...model.IPRecord) model.SpeedParams {
	return model.SpeedParams{
		Scope:             model.SpeedScopeSingle,
		Targets:           targets,
		URLMode:           URLModeOfficial,
		UseTLS:            probe.UseTLSAuto,
		Concurrency:       1,
		TargetQualified:   100,
		IntervalMS:        1,
		MinSpeed:          0,
		DownloadDurationS: 1,
		Breaker429:        3,
		TimeoutMS:         200,
	}
}

func target(ip string, port int) model.IPRecord {
	return model.IPRecord{IP: ip, Port: port, LatencyAvg: 50, Latency: 50}
}

// ---------------------------------------------------------------------------
// 参数
// ---------------------------------------------------------------------------

func TestNormalizeParams(t *testing.T) {
	got := NormalizeParams(model.SpeedParams{Targets: []model.IPRecord{target("1.1.1.1", 443)}})

	want := model.SpeedParams{
		Scope:             model.SpeedScopeSingle,
		Targets:           []model.IPRecord{target("1.1.1.1", 443)},
		IPVersion:         4,
		URLMode:           URLModeAuto,
		UseTLS:            probe.UseTLSAuto,
		Concurrency:       defaultConcurrency,
		TargetQualified:   defaultTargetQualified,
		IntervalMS:        defaultIntervalMS,
		WeightSpeed:       defaultWeight,
		WeightLatency:     defaultWeight,
		DownloadDurationS: defaultDownloadDurationS,
		Breaker429:        defaultBreaker429,
		TimeoutMS:         defaultTimeoutMS,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("补齐后的参数 = %+v，期望 %+v", got, want)
	}
}

// 抖动权重默认就是 0（不参与），不能被兜底成 1——那会让排序被抖动主导。
func TestNormalizeParamsKeepsZeroJitterWeight(t *testing.T) {
	got := NormalizeParams(model.SpeedParams{Targets: []model.IPRecord{target("1.1.1.1", 443)}})
	if got.WeightJitter != 0 {
		t.Errorf("抖动权重 = %v，期望保持 0", got.WeightJitter)
	}
}

// 只把速度权重设成 0 是「只按延迟排序」这个明确诉求，不能被静默改回 1。
//
// 兜底只在三个权重全为 0（评分会全变成 0）时才该出手。
func TestNormalizeParamsKeepsZeroSpeedWeightWhenLatencyWeighs(t *testing.T) {
	got := NormalizeParams(model.SpeedParams{
		Targets:       []model.IPRecord{target("1.1.1.1", 443)},
		WeightSpeed:   0,
		WeightLatency: 1,
	})
	if got.WeightSpeed != 0 {
		t.Errorf("速度权重 = %v，期望保持 0", got.WeightSpeed)
	}
}

// 显式给出的非零值不能被覆盖。
func TestNormalizeParamsKeepsExplicitValues(t *testing.T) {
	in := model.SpeedParams{
		Scope:             model.SpeedScopeAll,
		Targets:           []model.IPRecord{target("1.1.1.1", 443)},
		IPVersion:         6,
		URLMode:           URLModeCustom,
		CustomURL:         "example.com/down",
		UseTLS:            probe.UseTLSOn,
		Concurrency:       4,
		TargetQualified:   3,
		IntervalMS:        50,
		MinSpeed:          2,
		WeightSpeed:       2,
		WeightLatency:     0.5,
		WeightJitter:      0.25,
		PerRegionTopN:     2,
		DownloadDurationS: 5,
		Breaker429:        7,
		TimeoutMS:         800,
	}
	if got := NormalizeParams(in); !reflect.DeepEqual(got, in) {
		t.Errorf("显式参数被改写：%+v", got)
	}
}

func TestValidateParams(t *testing.T) {
	valid := baseParams(target("1.1.1.1", 443))

	tests := []struct {
		name   string
		mutate func(*model.SpeedParams)
	}{
		{"范围无法识别", func(p *model.SpeedParams) { p.Scope = "everything" }},
		{"没有目标", func(p *model.SpeedParams) { p.Targets = nil }},
		{"目标地址为空", func(p *model.SpeedParams) { p.Targets[0].IP = "" }},
		{"目标端口越界", func(p *model.SpeedParams) { p.Targets[0].Port = 70000 }},
		{"源模式无法识别", func(p *model.SpeedParams) { p.URLMode = "fastest" }},
		{"自定义源为空", func(p *model.SpeedParams) { p.URLMode = URLModeCustom }},
		{"自定义源缺主机名", func(p *model.SpeedParams) { p.URLMode = URLModeCustom; p.CustomURL = "https:///x" }},
		{"TLS 取值非法", func(p *model.SpeedParams) { p.UseTLS = "maybe" }},
		{"并发为 0", func(p *model.SpeedParams) { p.Concurrency = 0 }},
		{"并发超上限", func(p *model.SpeedParams) { p.Concurrency = maxConcurrency + 1 }},
		{"收敛目标为 0", func(p *model.SpeedParams) { p.TargetQualified = 0 }},
		{"间隔为负", func(p *model.SpeedParams) { p.IntervalMS = -1 }},
		{"目标端口为零", func(p *model.SpeedParams) { p.Targets[0].Port = 0 }},
		{"最小速度为负", func(p *model.SpeedParams) { p.MinSpeed = -1 }},
		{"权重为负", func(p *model.SpeedParams) { p.WeightLatency = -1 }},
		{"TopN 为负", func(p *model.SpeedParams) { p.PerRegionTopN = -1 }},
		{"下载时长为 0", func(p *model.SpeedParams) { p.DownloadDurationS = 0 }},
		{"熔断阈值为 0", func(p *model.SpeedParams) { p.Breaker429 = 0 }},
		{"超时为 0", func(p *model.SpeedParams) { p.TimeoutMS = 0 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := valid
			p.Targets = append([]model.IPRecord(nil), valid.Targets...)
			tc.mutate(&p)
			if err := ValidateParams(p); err == nil {
				t.Error("应当报错但通过了校验")
			}
		})
	}

	if err := ValidateParams(valid); err != nil {
		t.Errorf("合法参数被判为非法：%v", err)
	}
}

// NewRunner 自己做一遍归一化与校验，调用方漏补默认值也不会构造出坏执行器。
func TestNewRunnerRejectsInvalidParams(t *testing.T) {
	d := newDeps()
	if _, err := NewRunner(Options{Params: model.SpeedParams{}}); err == nil {
		t.Fatal("没有目标的参数应当被拒绝")
	}
	if _, err := NewRunner(d.options(t, baseParams(target("1.1.1.1", 443)))); err != nil {
		t.Fatalf("合法参数构造失败：%v", err)
	}
}

func TestNewRunnerFillsDefaultDependencies(t *testing.T) {
	r, err := NewRunner(Options{
		Params: baseParams(target("1.1.1.1", 443)),
		Logger: slog.New(slog.NewTextHandler(discard{}, nil)),
	})
	if err != nil {
		t.Fatalf("构造失败：%v", err)
	}
	if r.downloadFn == nil || r.usability == nil || r.sourceFn == nil || r.now == nil {
		t.Fatal("未注入的依赖应当补上真实实现")
	}

	// 默认下载实现要能挡住非法参数，而不是发出请求。
	if _, err := r.downloadFn(context.Background(), model.IPRecord{IP: "", Port: 443}, "example.com/x", time.Second, true); err == nil {
		t.Error("空地址应当被默认下载实现拒绝")
	}
	// 默认可用性校验同理。
	if _, err := r.usability(context.Background(), model.IPRecord{IP: "1.1.1.1", Port: 0}, time.Second); err == nil {
		t.Error("非法端口应当被默认校验实现拒绝")
	}
}

// ---------------------------------------------------------------------------
// 基本流程
// ---------------------------------------------------------------------------

// 只有达到合格线且测出速度的结果才进结果集，未测出的（0）不算。
func TestRunKeepsOnlyQualifiedResults(t *testing.T) {
	d := newDeps()
	d.downloader.fn = speedByIP(map[string]float64{
		"1.1.1.1": 10,
		"1.1.1.2": 0.2,
		"1.1.1.3": 20,
	})

	params := baseParams(target("1.1.1.1", 443), target("1.1.1.2", 443), target("1.1.1.3", 443))
	params.MinSpeed = 1
	params.WeightSpeed = 1
	params.WeightLatency = 1

	rep := newFakeReporter(context.Background())
	count, err := d.run(t, params, rep)
	if err != nil {
		t.Fatalf("测速失败：%v", err)
	}
	if count != 2 {
		t.Fatalf("合格数 = %d，期望 2", count)
	}

	got := rep.partials()
	if len(got) != 2 {
		t.Fatalf("推送结果 = %d 条，期望 2", len(got))
	}
	// 10 MB/s、50ms 延迟 → 10 / 1.05
	wantScore := 10 / 1.05
	if diff := got[0].Score - wantScore; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("评分 = %v，期望 %v", got[0].Score, wantScore)
	}
	if got[0].SpeedMBps != 10 {
		t.Errorf("速度 = %v，期望 10", got[0].SpeedMBps)
	}

	_, _, funnel := rep.snapshot()
	if funnel.Generated != 3 || funnel.Usable != 2 {
		t.Errorf("漏斗 = %+v，期望生成 3 / 可用 2", funnel)
	}
}

// 测出速度但低于合格线的结果不推送，也不计入合格数。
func TestRunDropsResultsBelowMinSpeed(t *testing.T) {
	d := newDeps()
	d.downloader.fn = speedByIP(map[string]float64{"1.1.1.1": 5})

	params := baseParams(target("1.1.1.1", 443))
	params.MinSpeed = 10

	rep := newFakeReporter(context.Background())
	count, err := d.run(t, params, rep)
	if err != nil {
		t.Fatalf("测速失败：%v", err)
	}
	if count != 0 {
		t.Errorf("合格数 = %d，期望 0", count)
	}
	if len(rep.partials()) != 0 {
		t.Errorf("不合格的结果不应推送，实际推了 %d 条", len(rep.partials()))
	}
}

func TestRunEmptyTargetsIsError(t *testing.T) {
	d := newDeps()
	params := baseParams()
	params.Targets = nil

	if _, err := NewRunner(d.options(t, params)); err == nil {
		t.Fatal("没有目标应当在构造时就报错")
	}
}

// 目标全被 TopN 或可用性校验筛掉时，要给出可读的错误而不是空结果。
func TestRunAllUnusableIsError(t *testing.T) {
	d := newDeps()
	d.usability.unusable["1.1.1.1"] = true
	d.usability.unusable["1.1.1.2"] = true

	params := baseParams(target("1.1.1.1", 443), target("1.1.1.2", 443))
	params.UsabilityCheck = true

	rep := newFakeReporter(context.Background())
	if _, err := d.run(t, params, rep); err == nil {
		t.Fatal("全部不可用时应当报错")
	}
	if d.downloader.count() != 0 {
		t.Errorf("下载次数 = %d，期望 0（没有可用目标）", d.downloader.count())
	}
}

func TestRunSourceResolutionErrorFails(t *testing.T) {
	d := newDeps()
	d.sourceErr = errors.New("选源失败")

	rep := newFakeReporter(context.Background())
	if _, err := d.run(t, baseParams(target("1.1.1.1", 443)), rep); err == nil {
		t.Fatal("选源失败应当让任务失败")
	}
	if d.downloader.count() != 0 {
		t.Errorf("选源失败后不应发起下载，实际 %d 次", d.downloader.count())
	}
}

// 出口探测失败必须回退官方源并照常测完，不能整轮中断。
func TestRunSourceFallbackKeepsRunning(t *testing.T) {
	d := newDeps()
	resolver := NewSourceResolver((&countingProbe{err: errors.New("探测不可达")}).probe, newFakeClock().get, time.Minute)
	d.sourceFn = resolver.Resolve
	d.downloader.fn = speedByIP(map[string]float64{"1.1.1.1": 10})

	params := baseParams(target("1.1.1.1", 443))
	params.URLMode = URLModeAuto

	rep := newFakeReporter(context.Background())
	count, err := d.run(t, params, rep)
	if err != nil {
		t.Fatalf("探测失败不应让任务中断：%v", err)
	}
	if count != 1 {
		t.Errorf("合格数 = %d，期望 1", count)
	}
	if got := d.downloader.lastURL(); got != officialSpeedURL {
		t.Errorf("使用的测速地址 = %q，期望回退到官方源", got)
	}
}

// ---------------------------------------------------------------------------
// TLS 推断
// ---------------------------------------------------------------------------

// use_tls = auto 时按端口推断：非 TLS 端口不再必然失败。
//
// 旧实现把 use_tls 一律当 true，80 端口上的节点永远连不上。
func TestRunInfersTLSFromPortWhenAuto(t *testing.T) {
	d := newDeps()
	d.downloader.fn = speedByIP(map[string]float64{"1.1.1.1": 1, "1.1.1.2": 1, "1.1.1.3": 1, "1.1.1.4": 1})

	params := baseParams(
		target("1.1.1.1", 443),
		target("1.1.1.2", 80),
		target("1.1.1.3", 8443),
		target("1.1.1.4", 8080),
	)
	params.UseTLS = probe.UseTLSAuto
	params.Concurrency = 1

	if _, err := d.run(t, params, newFakeReporter(context.Background())); err != nil {
		t.Fatalf("测速失败：%v", err)
	}

	// 并发为 1，调用顺序与目标顺序一致。
	want := []bool{true, false, true, false}
	if got := d.downloader.tls(); !reflect.DeepEqual(got, want) {
		t.Errorf("TLS 推断 = %v，期望 %v", got, want)
	}
}

func TestRunHonoursExplicitUseTLS(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want bool
	}{
		{probe.UseTLSOn, true},
		{probe.UseTLSOff, false},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			d := newDeps()
			d.downloader.fn = speedByIP(map[string]float64{"1.1.1.1": 1})

			params := baseParams(target("1.1.1.1", 80))
			params.UseTLS = tc.mode

			if _, err := d.run(t, params, newFakeReporter(context.Background())); err != nil {
				t.Fatalf("测速失败：%v", err)
			}
			if got := d.downloader.tls(); len(got) != 1 || got[0] != tc.want {
				t.Errorf("TLS 取值 = %v，期望 %v", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 可用性预校验
// ---------------------------------------------------------------------------

// 开关关掉时必须一次校验请求都不发——这正是「开关没生效」的高发位置。
func TestRunUsabilityCheckDisabledMakesNoRequests(t *testing.T) {
	d := newDeps()
	d.downloader.fn = speedByIP(map[string]float64{"1.1.1.1": 1})

	params := baseParams(target("1.1.1.1", 443))
	params.UsabilityCheck = false

	if _, err := d.run(t, params, newFakeReporter(context.Background())); err != nil {
		t.Fatalf("测速失败：%v", err)
	}
	if d.usability.count() != 0 {
		t.Errorf("可用性校验调用 %d 次，期望 0", d.usability.count())
	}
}

func TestRunUsabilityCheckDropsUnreachable(t *testing.T) {
	d := newDeps()
	d.usability.unusable["1.1.1.2"] = true
	d.downloader.fn = speedByIP(map[string]float64{"1.1.1.1": 1, "1.1.1.3": 1})

	params := baseParams(target("1.1.1.1", 443), target("1.1.1.2", 443), target("1.1.1.3", 443))
	params.UsabilityCheck = true

	rep := newFakeReporter(context.Background())
	if _, err := d.run(t, params, rep); err != nil {
		t.Fatalf("测速失败：%v", err)
	}
	if d.usability.count() != 3 {
		t.Errorf("可用性校验调用 %d 次，期望 3", d.usability.count())
	}
	if got := d.downloader.calledIPs(); len(got) != 2 {
		t.Errorf("下载目标 = %v，期望跳过不可用的那个", got)
	}

	_, _, funnel := rep.snapshot()
	if funnel.LatencyOK != 2 {
		t.Errorf("漏斗延迟达标 = %d，期望 2", funnel.LatencyOK)
	}
}

// 校验本身出错（而不是「不可用」）时保守保留：多测一个只是浪费一点时间，
// 误杀会让用户永远测不到这个节点。
func TestRunUsabilityErrorKeepsTarget(t *testing.T) {
	d := newDeps()
	d.usability.errs["1.1.1.1"] = errors.New("探测超时")
	d.downloader.fn = speedByIP(map[string]float64{"1.1.1.1": 1})

	params := baseParams(target("1.1.1.1", 443))
	params.UsabilityCheck = true

	if _, err := d.run(t, params, newFakeReporter(context.Background())); err != nil {
		t.Fatalf("测速失败：%v", err)
	}
	if d.downloader.count() != 1 {
		t.Errorf("下载次数 = %d，期望保留该节点并测速", d.downloader.count())
	}
}

// ---------------------------------------------------------------------------
// 进度
// ---------------------------------------------------------------------------

// 总步数按阶段累加，已完成数只增不减：进度条不能先冲到顶再退回。
func TestRunProgressIsMonotoneAndFinishes(t *testing.T) {
	d := newDeps()
	d.downloader.fn = speedByIP(map[string]float64{"1.1.1.1": 1, "1.1.1.2": 1})

	params := baseParams(target("1.1.1.1", 443), target("1.1.1.2", 443))
	params.UsabilityCheck = true

	rep := newFakeReporter(context.Background())
	if _, err := d.run(t, params, rep); err != nil {
		t.Fatalf("测速失败：%v", err)
	}

	total, done, _ := rep.snapshot()
	// 两个阶段各 2 步：可用性校验 2 + 下载 2 = 4。
	if total != 4 {
		t.Errorf("总步数 = %d，期望 4（两阶段累加）", total)
	}
	if done != 4 {
		t.Errorf("已完成 = %d，期望收尾时推满 4", done)
	}
}

// 下载时长按秒换算后传给探测层。
func TestRunPassesDownloadDuration(t *testing.T) {
	d := newDeps()
	d.downloader.fn = speedByIP(map[string]float64{"1.1.1.1": 1})

	params := baseParams(target("1.1.1.1", 443))
	params.DownloadDurationS = 3

	if _, err := d.run(t, params, newFakeReporter(context.Background())); err != nil {
		t.Fatalf("测速失败：%v", err)
	}
	if d.downloader.duration != 3*time.Second {
		t.Errorf("下载时长 = %v，期望 3s", d.downloader.duration)
	}
}

// 收敛 / 熔断 / 中止都会用到的阶段累加器，单独验一下它的边界。
func TestSleepCtx(t *testing.T) {
	if !sleepCtx(context.Background(), 0) {
		t.Error("零间隔应立即返回 true")
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if sleepCtx(cancelled, 0) {
		t.Error("已取消的 context 应返回 false")
	}
	if sleepCtx(cancelled, time.Second) {
		t.Error("已取消的 context 不应等待满时长")
	}

	// 中途取消要能立刻打断等待，否则「停止」按钮在间隔里会失灵。
	ctx, stop := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		stop()
	}()
	start := time.Now()
	if sleepCtx(ctx, 5*time.Second) {
		t.Error("等待中被取消应返回 false")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("取消后仍等了 %v，说明没有及时打断", elapsed)
	}
}

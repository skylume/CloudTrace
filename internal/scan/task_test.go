package scan

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"cloudtrace/internal/model"
	"cloudtrace/internal/probe"
	"cloudtrace/internal/source"
)

// ---------------------------------------------------------------- 替身

// fakeReporter 记录扫描流程上报的一切，供断言检查。
type fakeReporter struct {
	ctx context.Context

	mu       sync.Mutex
	total    int
	done     int
	funnel   model.Funnel
	totalSeq []int
	doneSeq  []int
	events   map[string][]any
}

func newFakeReporter(ctx context.Context) *fakeReporter {
	return &fakeReporter{ctx: ctx, events: make(map[string][]any)}
}

func (f *fakeReporter) Context() context.Context { return f.ctx }

func (f *fakeReporter) SetTotal(total int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.total = total
	f.totalSeq = append(f.totalSeq, total)
}

func (f *fakeReporter) SetDone(done int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.done = done
	f.doneSeq = append(f.doneSeq, done)
}

func (f *fakeReporter) SetFunnel(m model.Funnel) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.funnel = m
}

func (f *fakeReporter) Emit(topic string, payload any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events[topic] = append(f.events[topic], payload)
}

// SetPreset 是编排层接口的一部分，扫描流程用不到；留着只为满足接口。
func (f *fakeReporter) SetPreset(string) {}

func (f *fakeReporter) snapshot() (total, done int, funnel model.Funnel) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.total, f.done, f.funnel
}

// results 把分批推送的结果合并回一个列表。
func (f *fakeReporter) results() []model.IPRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.IPRecord
	for _, payload := range f.events[TopicResult] {
		chunk, ok := payload.([]model.IPRecord)
		if !ok {
			panic("scan/result 载荷不是 []model.IPRecord")
		}
		out = append(out, chunk...)
	}
	return out
}

// fakeProber 按 IP 返回预设结果，并记录每一次调用。
type fakeProber struct {
	mu          sync.Mutex
	calls       []probeCall
	latency     map[string]float64
	unreachable map[string]bool
	fallback    float64
	colo        map[string]string
	// after 非零时，第 after 次调用之后触发 onCall。
	after  int
	onCall func(n int)
}

type probeCall struct {
	ip    string
	port  int
	times int
}

func (f *fakeProber) probe(ctx context.Context, c Candidate, times int, _ time.Duration) (probe.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, probeCall{ip: c.IP, port: c.Port, times: times})
	n := len(f.calls)
	hook := f.onCall
	after := f.after
	f.mu.Unlock()

	if hook != nil && after > 0 && n >= after {
		hook(n)
	}
	if ctx.Err() != nil {
		return unreachableResult(times), ctx.Err()
	}

	if f.unreachable[c.IP] {
		return unreachableResult(times), nil
	}
	lat := f.fallback
	if v, ok := f.latency[c.IP]; ok {
		lat = v
	}
	return probe.Result{
		Latency: lat, LatencyAvg: lat, LatencyMax: lat,
		Sent: times, Recv: times, Colo: f.colo[c.IP],
	}, nil
}

func unreachableResult(times int) probe.Result {
	return probe.Result{
		Latency: model.Unreachable, LatencyAvg: model.Unreachable, LatencyMax: model.Unreachable,
		Loss: 1, Sent: times,
	}
}

func (f *fakeProber) all() []probeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]probeCall(nil), f.calls...)
}

func (f *fakeProber) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// timesHistogram 返回「探测次数 → 调用数」的分布。
func (f *fakeProber) timesHistogram() map[int]int {
	out := make(map[int]int)
	for _, call := range f.all() {
		out[call.times]++
	}
	return out
}

// fakeVerifier 记录校验调用并返回预设结论。
type fakeVerifier struct {
	mu      sync.Mutex
	calls   []string
	verdict map[string]probe.CFVerdict
	def     probe.CFVerdict
	err     error
	rounds  []int
}

func (f *fakeVerifier) verify(_ context.Context, c Candidate, rounds int, _ time.Duration) (probe.CFResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, c.IP)
	f.rounds = append(f.rounds, rounds)
	f.mu.Unlock()
	if f.err != nil {
		return probe.CFResult{}, f.err
	}
	verdict, ok := f.verdict[c.IP]
	if !ok {
		verdict = f.def
	}
	return probe.CFResult{Verdict: verdict, Rounds: rounds}, nil
}

func (f *fakeVerifier) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// fakeTracer 记录明细采集调用，可返回预设字段。
type fakeTracer struct {
	mu     sync.Mutex
	calls  []string
	trace  map[string]string
	byIP   map[string]map[string]string
	err    error
	after  int
	onCall func(n int)
}

func (f *fakeTracer) fetch(ctx context.Context, c Candidate, _ time.Duration) (map[string]string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, c.IP)
	n := len(f.calls)
	hook, after := f.onCall, f.after
	f.mu.Unlock()

	if hook != nil && after > 0 && n >= after {
		hook(n)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if f.err != nil {
		return nil, f.err
	}
	if trace, ok := f.byIP[c.IP]; ok {
		return trace, nil
	}
	return f.trace, nil
}

func (f *fakeTracer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// ---------------------------------------------------------------- 搭台

// scanParams 返回一份「只扫用户自定义来源」的基础参数，各用例按需覆盖。
func scanParams(source string) model.ScanParams {
	return model.ScanParams{
		Mode:             "tcping",
		Workers:          4,
		SampleMax:        100,
		LatencyThreshold: 100,
		PingTimes:        3,
		Port:             443,
		IPVersion:        4,
		SourceMode:       "custom",
		CustomSource:     source,
		TwoPhase:         true,
		VerifyNodes:      true,
		TimeoutMS:        500,
	}
}

// deps 是一次扫描用到的全部替身。
type deps struct {
	prober     *fakeProber
	verifier   *fakeVerifier
	tracer     *fakeTracer
	remote     RemoteFunc
	remoteURLs []string
	resolver   source.Resolver
	enrich     func(rec *model.IPRecord)
}

func newRunnerForTest(t *testing.T, params model.ScanParams, d *deps) *Runner {
	t.Helper()
	if d == nil {
		d = &deps{}
	}
	if d.prober == nil {
		d.prober = &fakeProber{}
	}
	if d.verifier == nil {
		d.verifier = &fakeVerifier{def: probe.CFValid}
	}
	if d.tracer == nil {
		d.tracer = &fakeTracer{}
	}
	if d.resolver == nil {
		d.resolver = &fakeResolver{}
	}

	runner, err := NewRunner(Options{
		Params:     params,
		Host:       "edge.example.com",
		Seed:       1,
		RemoteURLs: d.remoteURLs,
		Probe:      d.prober.probe,
		Trace:      d.tracer.fetch,
		Verify:     d.verifier.verify,
		Remote:     d.remote,
		Resolver:   d.resolver,
		Enrich:     d.enrich,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("NewRunner 返回错误：%v", err)
	}
	return runner
}

func runScan(t *testing.T, runner *Runner, ctx context.Context) (int, error, *fakeReporter) {
	t.Helper()
	rep := newFakeReporter(ctx)
	count, err := runner.Run(rep)
	return count, err, rep
}

func ipSet(records []model.IPRecord) map[string]bool {
	out := make(map[string]bool, len(records))
	for _, rec := range records {
		out[rec.IP] = true
	}
	return out
}

// ---------------------------------------------------------------- 参数校验

func TestNormalizeParams(t *testing.T) {
	got := NormalizeParams(model.ScanParams{})
	if got.Mode != "tcping" || got.SourceMode != "official" || got.IPVersion != 4 {
		t.Errorf("默认值补全结果 = %+v", got)
	}

	// 已经填好的值不能被覆盖。
	given := model.ScanParams{Mode: "httping", SourceMode: "custom", IPVersion: 6, PingTimes: 2}
	if got := NormalizeParams(given); !reflect.DeepEqual(got, given) {
		t.Errorf("已有值被改动：%+v → %+v", given, got)
	}
}

// 探测次数留空表示「不指定」，要补成一个真实次数。
//
// 界面上 0 显示成「自动」，而校验只接受 ≥1；不补的话「标准」档位一启动
// 就会收到「探测次数 0 必须至少为 1」，用户什么都没填错。
func TestNormalizeParamsFillsPingTimes(t *testing.T) {
	params := scanParams("1.1.1.1")
	params.PingTimes = 0

	got := NormalizeParams(params)
	if got.PingTimes != defaultPingTimes {
		t.Errorf("探测次数 = %d，期望补成 %d", got.PingTimes, defaultPingTimes)
	}
	if err := ValidateParams(got); err != nil {
		t.Errorf("补完默认值后仍不合法：%v", err)
	}
}

func TestValidateParams(t *testing.T) {
	valid := scanParams("1.1.1.1")

	tests := []struct {
		name   string
		mutate func(*model.ScanParams)
	}{
		{name: "端口为零", mutate: func(p *model.ScanParams) { p.Port = 0 }},
		{name: "端口越界", mutate: func(p *model.ScanParams) { p.Port = 70000 }},
		{name: "并发为零", mutate: func(p *model.ScanParams) { p.Workers = 0 }},
		{name: "采样上限为负", mutate: func(p *model.ScanParams) { p.SampleMax = -1 }},
		{name: "阈值为零", mutate: func(p *model.ScanParams) { p.LatencyThreshold = 0 }},
		{name: "探测次数为零", mutate: func(p *model.ScanParams) { p.PingTimes = 0 }},
		{name: "超时为零", mutate: func(p *model.ScanParams) { p.TimeoutMS = 0 }},
		{name: "重试为负", mutate: func(p *model.ScanParams) { p.Retry = -1 }},
		{name: "模式无法识别", mutate: func(p *model.ScanParams) { p.Mode = "udping" }},
		{name: "来源模式无法识别", mutate: func(p *model.ScanParams) { p.SourceMode = "whatever" }},
		{name: "地址族非法", mutate: func(p *model.ScanParams) { p.IPVersion = 5 }},
		{name: "自定义来源为空", mutate: func(p *model.ScanParams) { p.CustomSource = "   " }},
		{name: "自定义来源无法解析", mutate: func(p *model.ScanParams) { p.CustomSource = "???" }},
	}

	if err := ValidateParams(valid); err != nil {
		t.Fatalf("合法参数被判为非法：%v", err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := valid
			tt.mutate(&params)
			if err := ValidateParams(params); err == nil {
				t.Error("期望返回错误，实际为 nil")
			}
		})
	}
}

func TestValidateParamsIgnoresEmptyCustomWhenOfficialOnly(t *testing.T) {
	params := scanParams("")
	params.SourceMode = "official"
	if err := ValidateParams(params); err != nil {
		t.Errorf("仅官方来源时不应要求自定义内容，实际：%v", err)
	}
}

func TestNewRunnerRejectsInvalidParams(t *testing.T) {
	params := scanParams("1.1.1.1")
	params.LatencyThreshold = 0
	if _, err := NewRunner(Options{Params: params}); err == nil {
		t.Error("期望 NewRunner 返回错误，实际为 nil")
	}
}

// ---------------------------------------------------------------- 流水线

func TestRunTwoPhaseProbesCoarseThenFine(t *testing.T) {
	params := scanParams("1.1.1.1\n1.1.1.2\n1.1.1.3\n1.1.1.4\n1.1.1.5")
	params.VerifyNodes = false
	params.Workers = 2

	d := &deps{prober: &fakeProber{
		fallback:    10,
		latency:     map[string]float64{"1.1.1.4": 900, "1.1.1.5": 900},
		unreachable: map[string]bool{"1.1.1.5": true},
	}}
	runner := newRunnerForTest(t, params, d)

	count, err, rep := runScan(t, runner, context.Background())
	if err != nil {
		t.Fatalf("Run 返回错误：%v", err)
	}
	if count != 3 {
		t.Errorf("结果数 = %d，期望 3", count)
	}

	hist := d.prober.timesHistogram()
	if hist[1] != 5 {
		t.Errorf("粗扫（1 次）调用数 = %d，期望 5", hist[1])
	}
	if hist[3] != 3 {
		t.Errorf("精扫（3 次）调用数 = %d，期望 3", hist[3])
	}

	// 精扫只应针对粗扫达标的那三个。
	fine := map[string]bool{}
	for _, call := range d.prober.all() {
		if call.times == params.PingTimes {
			fine[call.ip] = true
		}
	}
	if !fine["1.1.1.1"] || !fine["1.1.1.2"] || !fine["1.1.1.3"] {
		t.Errorf("精扫目标不对：%v", fine)
	}
	if fine["1.1.1.4"] || fine["1.1.1.5"] {
		t.Errorf("未达标的地址不应进入精扫：%v", fine)
	}

	_, done, funnel := rep.snapshot()
	if done == 0 {
		t.Error("结束时已完成数不应为 0")
	}
	if funnel.Generated != 5 || funnel.LatencyOK != 3 || funnel.Usable != 3 {
		t.Errorf("漏斗 = %+v，期望 Generated=5 LatencyOK=3 Usable=3", funnel)
	}
	// 关闭明细采集且走 TCPing，拿不到任何地区信息，地区级为 0；
	// 但它不能把可用级一起拖下去。
	if funnel.RegionOK != 0 {
		t.Errorf("RegionOK = %d，期望 0", funnel.RegionOK)
	}
	if len(rep.results()) != 3 {
		t.Errorf("推送结果数 = %d，期望 3", len(rep.results()))
	}
}

func TestRunSinglePhaseUsesPingTimes(t *testing.T) {
	params := scanParams("1.1.1.1\n1.1.1.2")
	params.TwoPhase = false
	params.VerifyNodes = false
	params.PingTimes = 2

	d := &deps{prober: &fakeProber{fallback: 10}}
	runner := newRunnerForTest(t, params, d)

	count, err, _ := runScan(t, runner, context.Background())
	if err != nil {
		t.Fatalf("Run 返回错误：%v", err)
	}
	if count != 2 {
		t.Errorf("结果数 = %d，期望 2", count)
	}
	hist := d.prober.timesHistogram()
	if len(hist) != 1 || hist[2] != 2 {
		t.Errorf("单阶段应只以 2 次探测跑一遍，实际分布 %v", hist)
	}
}

// TestRunTwoPhaseSkipsFineWhenNothingPasses 覆盖「阶段一达标为 0 不进入阶段二」。
func TestRunTwoPhaseSkipsFineWhenNothingPasses(t *testing.T) {
	params := scanParams("1.1.1.1\n1.1.1.2\n1.1.1.3")
	params.VerifyNodes = false

	d := &deps{prober: &fakeProber{fallback: 900}}
	runner := newRunnerForTest(t, params, d)

	count, err, rep := runScan(t, runner, context.Background())
	if err != nil {
		t.Fatalf("无候选达标不该算失败，实际：%v", err)
	}
	if count != 0 {
		t.Errorf("结果数 = %d，期望 0", count)
	}
	// 只跑了粗扫那一遍，没有对任何地址做 ping_times 次探测。
	if d.prober.count() != 3 {
		t.Errorf("探测调用数 = %d，期望 3（只跑粗扫）", d.prober.count())
	}
	if hist := d.prober.timesHistogram(); hist[params.PingTimes] != 0 {
		t.Errorf("不应进入精扫，实际分布 %v", hist)
	}
	if _, _, funnel := rep.snapshot(); funnel.LatencyOK != 0 || funnel.Usable != 0 {
		t.Errorf("漏斗 = %+v，期望达标与可用都为 0", funnel)
	}
}

// TestRunFilterRunsBeforeProbing 验证前置过滤发生在任何探测之前。
//
// 顺序错了就完全失去意义：本该被过滤掉的地址照样要花一次 TCP 往返。
func TestRunFilterRunsBeforeProbing(t *testing.T) {
	params := scanParams("1.1.1.1\n1.1.1.2:8443\n1.1.1.3:2053")
	params.PreFilterPorts = []int{443}
	params.VerifyNodes = false
	params.TwoPhase = false

	d := &deps{prober: &fakeProber{fallback: 10}}
	runner := newRunnerForTest(t, params, d)

	count, err, _ := runScan(t, runner, context.Background())
	if err != nil {
		t.Fatalf("Run 返回错误：%v", err)
	}
	if count != 1 {
		t.Errorf("结果数 = %d，期望 1", count)
	}
	for _, call := range d.prober.all() {
		if call.port != 443 {
			t.Errorf("被过滤掉的端口 %d 仍被探测", call.port)
		}
	}
	if d.prober.count() != 1 {
		t.Errorf("探测调用数 = %d，期望 1", d.prober.count())
	}
}

// TestRunEmptyPoolIsError 覆盖「来源解析成功、但一条候选都没留下」的情况。
//
// 用「自定义来源写的是 IPv6，而本次要扫 IPv4」构造：文本能解析，候选
// 池却是空的。
func TestRunEmptyPoolIsError(t *testing.T) {
	params := scanParams("2606:4700::1")
	runner := newRunnerForTest(t, params, nil)

	if _, err := runner.Run(newFakeReporter(context.Background())); err == nil {
		t.Error("空候选池应返回错误，实际为 nil")
	}
}

func TestRunAllFilteredIsError(t *testing.T) {
	params := scanParams("1.1.1.1")
	params.PreFilterPorts = []int{9999}
	runner := newRunnerForTest(t, params, nil)

	if _, err := runner.Run(newFakeReporter(context.Background())); err == nil {
		t.Error("候选被过滤光应返回错误，实际为 nil")
	}
}

// TestRunVerifyNodesDisabledMakesNoTraceRequests 是 M2 的硬性验收项：
// 关闭节点明细采集时，一个 trace 请求都不许发。
func TestRunVerifyNodesDisabledMakesNoTraceRequests(t *testing.T) {
	params := scanParams("1.1.1.1\n1.1.1.2\n1.1.1.3")
	params.VerifyNodes = false
	params.TwoPhase = false

	d := &deps{
		prober:   &fakeProber{fallback: 10},
		verifier: &fakeVerifier{def: probe.CFValid},
		tracer:   &fakeTracer{trace: map[string]string{"colo": "NRT"}},
	}
	runner := newRunnerForTest(t, params, d)

	count, err, _ := runScan(t, runner, context.Background())
	if err != nil {
		t.Fatalf("Run 返回错误：%v", err)
	}
	if count != 3 {
		t.Errorf("结果数 = %d，期望 3", count)
	}
	if got := d.tracer.count(); got != 0 {
		t.Errorf("明细采集调用数 = %d，期望 0", got)
	}
	// 校验打的是同一个 trace 端点，只跳一半等于没跳。
	if got := d.verifier.count(); got != 0 {
		t.Errorf("节点校验调用数 = %d，期望 0", got)
	}
}

func TestRunVerifyDropsInvalidNodes(t *testing.T) {
	params := scanParams("1.1.1.1\n1.1.1.2\n1.1.1.3")
	params.TwoPhase = false

	d := &deps{
		prober:   &fakeProber{fallback: 10},
		verifier: &fakeVerifier{def: probe.CFValid, verdict: map[string]probe.CFVerdict{"1.1.1.2": probe.CFInvalid}},
		tracer:   &fakeTracer{trace: map[string]string{"colo": "NRT", "loc": "JP"}},
	}
	runner := newRunnerForTest(t, params, d)

	count, err, rep := runScan(t, runner, context.Background())
	if err != nil {
		t.Fatalf("Run 返回错误：%v", err)
	}
	if count != 2 {
		t.Errorf("结果数 = %d，期望 2（剔掉 1 个非 CF 节点）", count)
	}
	if got := ipSet(rep.results()); got["1.1.1.2"] {
		t.Error("被判定为非 CF 的节点不应出现在结果里")
	}
	if _, _, funnel := rep.snapshot(); funnel.Usable != 2 {
		t.Errorf("Usable = %d，期望 2", funnel.Usable)
	}
}

// TestRunVerifyUnknownKeepsNodes 验证「没能验证」不等于「不是 CF」。
func TestRunVerifyUnknownKeepsNodes(t *testing.T) {
	params := scanParams("1.1.1.1")
	params.TwoPhase = false

	d := &deps{
		prober:   &fakeProber{fallback: 10},
		verifier: &fakeVerifier{def: probe.CFUnknown},
		tracer:   &fakeTracer{trace: map[string]string{"colo": "NRT"}},
	}
	runner := newRunnerForTest(t, params, d)

	count, err, _ := runScan(t, runner, context.Background())
	if err != nil {
		t.Fatalf("Run 返回错误：%v", err)
	}
	if count != 1 {
		t.Errorf("结果数 = %d，期望 1（未能验证时保守保留）", count)
	}
}

// TestRunVerifyErrorKeepsNodes 验证校验本身出错时不会一次剔掉整批。
func TestRunVerifyErrorKeepsNodes(t *testing.T) {
	params := scanParams("1.1.1.1\n1.1.1.2")
	params.TwoPhase = false

	d := &deps{
		prober:   &fakeProber{fallback: 10},
		verifier: &fakeVerifier{err: errors.New("校验失败")},
		tracer:   &fakeTracer{trace: map[string]string{"colo": "NRT"}},
	}
	runner := newRunnerForTest(t, params, d)

	count, err, _ := runScan(t, runner, context.Background())
	if err != nil {
		t.Fatalf("Run 返回错误：%v", err)
	}
	if count != 2 {
		t.Errorf("结果数 = %d，期望 2", count)
	}
}

func TestRunFillsRegionFromTrace(t *testing.T) {
	params := scanParams("1.1.1.1\n1.1.1.2")
	params.TwoPhase = false

	d := &deps{
		prober: &fakeProber{fallback: 10},
		tracer: &fakeTracer{byIP: map[string]map[string]string{
			"1.1.1.1": {"colo": "nrt", "loc": "jp", "ip": "1.1.1.1"},
			"1.1.1.2": {"colo": "", "loc": ""},
		}},
	}
	runner := newRunnerForTest(t, params, d)

	count, err, rep := runScan(t, runner, context.Background())
	if err != nil {
		t.Fatalf("Run 返回错误：%v", err)
	}
	if count != 2 {
		t.Fatalf("结果数 = %d，期望 2", count)
	}

	byIP := map[string]model.IPRecord{}
	for _, rec := range rep.results() {
		byIP[rec.IP] = rec
	}
	if got := byIP["1.1.1.1"]; got.Colo != "NRT" || got.Loc != "JP" {
		t.Errorf("归属地未从 trace 归一化：colo=%q loc=%q", got.Colo, got.Loc)
	}
	if len(byIP["1.1.1.1"].Trace) != 3 {
		t.Errorf("trace 全字段未保存：%v", byIP["1.1.1.1"].Trace)
	}
	if _, _, funnel := rep.snapshot(); funnel.RegionOK != 1 {
		t.Errorf("RegionOK = %d，期望 1", funnel.RegionOK)
	}
}

// TestRunRegionFromHTTPingColoWhenTraceDisabled 验证关闭明细采集时，
// HTTPing 顺带拿到的 colo 仍然能填进结果。
func TestRunRegionFromHTTPingColoWhenTraceDisabled(t *testing.T) {
	params := scanParams("1.1.1.1")
	params.Mode = "httping"
	params.VerifyNodes = false
	params.TwoPhase = false

	d := &deps{
		prober: &fakeProber{fallback: 10, colo: map[string]string{"1.1.1.1": "HKG"}},
	}
	runner := newRunnerForTest(t, params, d)

	count, err, rep := runScan(t, runner, context.Background())
	if err != nil {
		t.Fatalf("Run 返回错误：%v", err)
	}
	if count != 1 {
		t.Fatalf("结果数 = %d，期望 1", count)
	}
	if got := rep.results()[0].Colo; got != "HKG" {
		t.Errorf("Colo = %q，期望 HKG", got)
	}
	if _, _, funnel := rep.snapshot(); funnel.RegionOK != 1 {
		t.Errorf("RegionOK = %d，期望 1", funnel.RegionOK)
	}
}

// TestRunHTTPingThresholdScalesWithTLS 验证 HTTPing 的阈值按 TLS 放大。
func TestRunHTTPingThresholdScalesWithTLS(t *testing.T) {
	params := scanParams("1.1.1.1:443\n1.1.1.2:8080")
	params.Mode = "httping"
	params.LatencyThreshold = 100
	params.VerifyNodes = false
	params.TwoPhase = false

	// 300ms 对无 TLS（阈值 130）不达标，对有 TLS（阈值 400）达标。
	d := &deps{prober: &fakeProber{fallback: 300}}
	runner := newRunnerForTest(t, params, d)

	count, err, rep := runScan(t, runner, context.Background())
	if err != nil {
		t.Fatalf("Run 返回错误：%v", err)
	}
	if count != 1 {
		t.Fatalf("结果数 = %d，期望 1", count)
	}
	if got := rep.results()[0].IP; got != "1.1.1.1" {
		t.Errorf("保留的应是走 TLS 的那个，实际 %q", got)
	}
	if !rep.results()[0].UseTLS {
		t.Error("443 端口的记录应标记为 TLS")
	}
}

func TestRunRetriesWhenAllProbesFail(t *testing.T) {
	params := scanParams("1.1.1.1")
	params.Retry = 2
	params.VerifyNodes = false
	params.TwoPhase = false

	d := &deps{prober: &fakeProber{unreachable: map[string]bool{"1.1.1.1": true}}}
	runner := newRunnerForTest(t, params, d)

	if _, err := runner.Run(newFakeReporter(context.Background())); err != nil {
		t.Fatalf("Run 返回错误：%v", err)
	}
	if got := d.prober.count(); got != 3 {
		t.Errorf("探测调用数 = %d，期望 3（首次 + 2 次重试）", got)
	}
}

func TestRunDoesNotRetryWhenProbeSucceeded(t *testing.T) {
	params := scanParams("1.1.1.1")
	params.Retry = 2
	params.VerifyNodes = false
	params.TwoPhase = false

	d := &deps{prober: &fakeProber{fallback: 10}}
	runner := newRunnerForTest(t, params, d)

	if _, err := runner.Run(newFakeReporter(context.Background())); err != nil {
		t.Fatalf("Run 返回错误：%v", err)
	}
	if got := d.prober.count(); got != 1 {
		t.Errorf("探测调用数 = %d，期望 1：成功过就不该重试", got)
	}
}

func TestRunProgressIsMonotoneAndFinishes(t *testing.T) {
	params := scanParams("1.1.1.1\n1.1.1.2\n1.1.1.3\n1.1.1.4")
	params.VerifyNodes = false

	d := &deps{prober: &fakeProber{fallback: 10, unreachable: map[string]bool{"1.1.1.4": true}}}
	runner := newRunnerForTest(t, params, d)

	_, err, rep := runScan(t, runner, context.Background())
	if err != nil {
		t.Fatalf("Run 返回错误：%v", err)
	}

	rep.mu.Lock()
	totalSeq := append([]int(nil), rep.totalSeq...)
	doneSeq := append([]int(nil), rep.doneSeq...)
	total, done := rep.total, rep.done
	rep.mu.Unlock()

	// 总步数随阶段推进只会变大：一次定死会让进度条先冲到顶再退回。
	for i := 1; i < len(totalSeq); i++ {
		if totalSeq[i] < totalSeq[i-1] {
			t.Fatalf("总步数回退：%v", totalSeq)
		}
	}
	// 已完成数只会变大。
	for i := 1; i < len(doneSeq); i++ {
		if doneSeq[i] < doneSeq[i-1] {
			t.Fatalf("已完成数回退：%v", doneSeq)
		}
	}
	if done != total {
		t.Errorf("结束时 done=%d total=%d，期望相等", done, total)
	}
	if done <= 0 {
		t.Error("结束时已完成数应大于 0")
	}
}

func TestRunEmitsResultsInChunks(t *testing.T) {
	const n = 120
	source := ""
	for i := 1; i <= n; i++ {
		source += "10.0.0." + strconv.Itoa(i) + "\n"
	}
	params := scanParams(source)
	params.SampleMax = n + 10
	params.VerifyNodes = false
	params.TwoPhase = false

	d := &deps{prober: &fakeProber{fallback: 10}}
	runner := newRunnerForTest(t, params, d)

	count, err, rep := runScan(t, runner, context.Background())
	if err != nil {
		t.Fatalf("Run 返回错误：%v", err)
	}
	if count != n {
		t.Fatalf("结果数 = %d，期望 %d", count, n)
	}

	rep.mu.Lock()
	chunks := len(rep.events[TopicResult])
	rep.mu.Unlock()
	want := (n + resultChunk - 1) / resultChunk
	if chunks != want {
		t.Errorf("推送批数 = %d，期望 %d", chunks, want)
	}
}

// TestRunAbortKeepsPartialResults 验证中止时已经产出的结果被保留下来。
func TestRunAbortKeepsPartialResults(t *testing.T) {
	params := scanParams("1.1.1.1\n1.1.1.2\n1.1.1.3\n1.1.1.4\n1.1.1.5")
	params.TwoPhase = false
	params.Workers = 1

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := &deps{
		prober: &fakeProber{fallback: 10},
		// 第二次明细采集后触发中止：此时前两条结果已经推送出去。
		tracer: &fakeTracer{trace: map[string]string{"colo": "NRT"}, after: 2, onCall: func(int) { cancel() }},
	}
	runner := newRunnerForTest(t, params, d)

	count, err, rep := runScan(t, runner, ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("中止时 Run 应返回 context.Canceled，实际：%v", err)
	}
	if count != 2 {
		t.Errorf("结果数 = %d，期望 2（中止前已产出的部分）", count)
	}
	if got := len(rep.results()); got != 2 {
		t.Errorf("推送结果数 = %d，期望 2", got)
	}
}

// TestRunAbortStopsBothPhases 验证两阶段共用同一个 context：
// 中止发生在粗扫阶段时，精扫一个请求都不该发。
func TestRunAbortStopsBothPhases(t *testing.T) {
	params := scanParams("1.1.1.1\n1.1.1.2\n1.1.1.3\n1.1.1.4")
	params.VerifyNodes = false
	params.Workers = 1

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := &deps{prober: &fakeProber{fallback: 10, after: 2, onCall: func(int) { cancel() }}}
	runner := newRunnerForTest(t, params, d)

	_, err, _ := runScan(t, runner, ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("中止时 Run 应返回 context.Canceled，实际：%v", err)
	}
	if hist := d.prober.timesHistogram(); hist[params.PingTimes] != 0 {
		t.Errorf("中止后不应进入精扫，实际分布 %v", hist)
	}
	if got := d.prober.count(); got > 3 {
		t.Errorf("探测调用数 = %d，期望不超过 3：取消后不应继续派发", got)
	}
}

// TestRunStopsImmediatelyWhenContextAlreadyCancelled 验证一开始就取消时
// 不会发出任何探测请求。
func TestRunStopsImmediatelyWhenContextAlreadyCancelled(t *testing.T) {
	params := scanParams("1.1.1.1\n1.1.1.2")
	params.TwoPhase = false

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	d := &deps{prober: &fakeProber{fallback: 10}}
	runner := newRunnerForTest(t, params, d)

	if _, err := runner.Run(newFakeReporter(ctx)); !errors.Is(err, context.Canceled) {
		t.Fatalf("期望 context.Canceled，实际：%v", err)
	}
	if got := d.prober.count(); got != 0 {
		t.Errorf("探测调用数 = %d，期望 0", got)
	}
}

func TestRunVerifyUsesMinimumRounds(t *testing.T) {
	params := scanParams("1.1.1.1")
	params.TwoPhase = false

	d := &deps{
		prober: &fakeProber{fallback: 10},
		tracer: &fakeTracer{trace: map[string]string{"colo": "NRT"}},
	}
	runner := newRunnerForTest(t, params, d)

	if _, err := runner.Run(newFakeReporter(context.Background())); err != nil {
		t.Fatalf("Run 返回错误：%v", err)
	}
	if len(d.verifier.rounds) != 1 {
		t.Fatalf("校验调用数 = %d，期望 1", len(d.verifier.rounds))
	}
	if d.verifier.rounds[0] < 3 {
		t.Errorf("校验轮数 = %d，期望至少 3：单轮结论太容易被抖动左右", d.verifier.rounds[0])
	}
}

func TestRunPanicInProbeDoesNotCrash(t *testing.T) {
	params := scanParams("1.1.1.1\n1.1.1.2")
	params.VerifyNodes = false
	params.TwoPhase = false

	d := &deps{prober: &fakeProber{}}
	runner := newRunnerForTest(t, params, d)
	// 让第一个地址的探测直接 panic。
	panicking := false
	runner.probeFn = func(_ context.Context, c Candidate, times int, _ time.Duration) (probe.Result, error) {
		if c.IP == "1.1.1.1" && !panicking {
			panicking = true
			panic("故意炸一下")
		}
		return probe.Result{Latency: 10, LatencyAvg: 10, LatencyMax: 10, Sent: times, Recv: times}, nil
	}

	count, err, _ := runScan(t, runner, context.Background())
	if err != nil {
		t.Fatalf("单项异常不该让整次扫描失败，实际：%v", err)
	}
	if count != 1 {
		t.Errorf("结果数 = %d，期望 1（异常那条被剔除，其余照常）", count)
	}
}

func TestEffectiveSampleMax(t *testing.T) {
	tests := []struct {
		name      string
		twoPhase  bool
		sampleMax int
		want      int
	}{
		{name: "两阶段按上限封顶", twoPhase: true, sampleMax: 5000, want: twoPhaseSampleCap},
		{name: "两阶段不限制时用封顶值", twoPhase: true, sampleMax: 0, want: twoPhaseSampleCap},
		{name: "两阶段小于封顶值时保留", twoPhase: true, sampleMax: 200, want: 200},
		{name: "单阶段不限制", twoPhase: false, sampleMax: 0, want: 0},
		{name: "单阶段用配置值", twoPhase: false, sampleMax: 5000, want: 5000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := scanParams("1.1.1.1")
			params.TwoPhase = tt.twoPhase
			params.SampleMax = tt.sampleMax
			runner := newRunnerForTest(t, params, nil)
			if got := runner.effectiveSampleMax(); got != tt.want {
				t.Errorf("effectiveSampleMax() = %d，期望 %d", got, tt.want)
			}
		})
	}
}

func TestRunUsesInjectedRemoteSource(t *testing.T) {
	// 自定义来源写的是 IPv6、本次扫 IPv4，因此池子里只剩远程源那一条。
	params := scanParams("2606:4700::1")
	params.SourceMode = "custom"
	params.VerifyNodes = false
	params.TwoPhase = false

	d := &deps{
		prober:     &fakeProber{fallback: 10},
		remoteURLs: []string{"https://list.example.com"},
		remote: func(_ context.Context, urls []string) ([]model.IPRecord, []string, error) {
			if len(urls) != 1 {
				t.Errorf("远程源地址数 = %d，期望 1", len(urls))
			}
			return []model.IPRecord{{IP: "9.9.9.9", Colo: "NRT", Loc: "JP"}}, nil, nil
		},
	}
	runner := newRunnerForTest(t, params, d)

	count, err, rep := runScan(t, runner, context.Background())
	if err != nil {
		t.Fatalf("Run 返回错误：%v", err)
	}
	if count != 1 {
		t.Fatalf("结果数 = %d，期望 1", count)
	}
	if got := rep.results()[0].Colo; got != "NRT" {
		t.Errorf("远程源自带的地区应写进结果，实际 Colo=%q", got)
	}
}

// TestRunAbortDuringFinePhase 验证精扫阶段的中止同样能立刻生效。
func TestRunAbortDuringFinePhase(t *testing.T) {
	params := scanParams("1.1.1.1\n1.1.1.2\n1.1.1.3\n1.1.1.4")
	params.VerifyNodes = false
	params.Workers = 1

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := &deps{prober: &fakeProber{fallback: 10}}
	runner := newRunnerForTest(t, params, d)

	fine := 0
	runner.probeFn = func(_ context.Context, _ Candidate, times int, _ time.Duration) (probe.Result, error) {
		if times > 1 {
			fine++
			if fine == 2 {
				cancel()
			}
		}
		return probe.Result{Latency: 10, LatencyAvg: 10, LatencyMax: 10, Sent: times, Recv: times}, nil
	}

	_, err, _ := runScan(t, runner, ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("精扫阶段中止应返回 context.Canceled，实际：%v", err)
	}
	if fine != 2 {
		t.Errorf("精扫调用数 = %d，期望 2：取消后不应继续派发", fine)
	}
}

// TestNewRunnerDefaultRemoteFetcher 覆盖默认远程源装配的接线。
//
// 打的是本地测试服务端，因此不依赖外网；同时验证「部分源失败」会被收进
// 失败清单而不是让整次拉取失败。
func TestNewRunnerDefaultRemoteFetcher(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/nodes" {
			_, _ = w.Write([]byte("9.9.9.9\n9.9.9.10:8443\n"))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	params := scanParams("1.1.1.1")
	runner, err := NewRunner(Options{Params: params})
	if err != nil {
		t.Fatalf("NewRunner 返回错误：%v", err)
	}

	records, failed, err := runner.remoteFn(context.Background(),
		[]string{server.URL + "/nodes", server.URL + "/missing"})
	if err != nil {
		t.Fatalf("部分源失败不应让整体失败，实际：%v", err)
	}
	if len(records) != 2 {
		t.Errorf("记录数 = %d，期望 2", len(records))
	}
	if len(failed) != 1 || failed[0] != server.URL+"/missing" {
		t.Errorf("失败清单 = %v，期望只含 /missing", failed)
	}
}

// TestRunPanicInVerifyDoesNotCrash 验证校验阶段单项异常不影响其余节点。
func TestRunPanicInVerifyDoesNotCrash(t *testing.T) {
	params := scanParams("1.1.1.1\n1.1.1.2")
	params.TwoPhase = false

	d := &deps{prober: &fakeProber{fallback: 10}}
	runner := newRunnerForTest(t, params, d)
	runner.traceFn = func(context.Context, Candidate, time.Duration) (map[string]string, error) {
		return map[string]string{"colo": "NRT"}, nil
	}
	panicking := false
	runner.verifyFn = func(_ context.Context, c Candidate, rounds int, _ time.Duration) (probe.CFResult, error) {
		if c.IP == "1.1.1.1" && !panicking {
			panicking = true
			panic("故意炸一下")
		}
		return probe.CFResult{Verdict: probe.CFValid, Rounds: rounds}, nil
	}

	count, err, _ := runScan(t, runner, context.Background())
	if err != nil {
		t.Fatalf("单项异常不该让整次扫描失败，实际：%v", err)
	}
	if count != 1 {
		t.Errorf("结果数 = %d，期望 1（异常那条被剔除，其余照常）", count)
	}
}

// TestRunRetryStopsWhenCancelled 验证重试循环里也会及时响应中止。
//
// 这里让探测返回「不可达但没有报错」：只有这种情况下重试循环才会走到
// 判中止那一步，否则会在进入循环前就因为出错而退出。
func TestRunRetryStopsWhenCancelled(t *testing.T) {
	params := scanParams("1.1.1.1")
	params.Retry = 3
	params.VerifyNodes = false
	params.TwoPhase = false

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := &deps{prober: &fakeProber{unreachable: map[string]bool{"1.1.1.1": true}}}
	runner := newRunnerForTest(t, params, d)

	var calls int
	runner.probeFn = func(_ context.Context, _ Candidate, times int, _ time.Duration) (probe.Result, error) {
		calls++
		if calls == 1 {
			cancel()
		}
		return unreachableResult(times), nil
	}

	if _, err := runner.Run(newFakeReporter(ctx)); !errors.Is(err, context.Canceled) {
		t.Fatalf("期望 context.Canceled，实际：%v", err)
	}
	if calls != 1 {
		t.Errorf("探测调用数 = %d，期望 1：取消后不应继续重试", calls)
	}
}

// TestRunRemoteFetchErrorFails 验证远程源整体失败时任务报错，
// 而不是静默地扫出一批缺了远程来源的结果。
func TestRunRemoteFetchErrorFails(t *testing.T) {
	params := scanParams("1.1.1.1")
	d := &deps{
		prober:     &fakeProber{fallback: 10},
		remoteURLs: []string{"https://list.example.com"},
		remote: func(context.Context, []string) ([]model.IPRecord, []string, error) {
			return nil, nil, errors.New("远程源全部不可用")
		},
	}
	runner := newRunnerForTest(t, params, d)

	if _, err := runner.Run(newFakeReporter(context.Background())); err == nil {
		t.Error("远程源整体失败应返回错误，实际为 nil")
	}
}

// TestNewRunnerDefaultDependenciesRejectBadInput 走一遍真实网络实现的分支。
//
// 用非法参数让探测层在发请求之前就返回错误：既覆盖了默认依赖的接线，
// 又不会在测试里产生任何网络流量。
func TestNewRunnerDefaultDependenciesRejectBadInput(t *testing.T) {
	ctx := context.Background()
	bad := Candidate{IP: "", Port: 443}

	for _, mode := range []string{"tcping", "httping"} {
		t.Run(mode, func(t *testing.T) {
			params := scanParams("1.1.1.1")
			params.Mode = mode
			runner, err := NewRunner(Options{Params: params})
			if err != nil {
				t.Fatalf("NewRunner 返回错误：%v", err)
			}
			if runner.host != defaultTestHost {
				t.Errorf("默认测试域名 = %q，期望 %q", runner.host, defaultTestHost)
			}
			if runner.resolver == nil {
				t.Error("默认 DNS 解析器未装配")
			}
			if _, err := runner.probeFn(ctx, bad, 1, time.Second); err == nil {
				t.Error("非法地址应让探测直接报错")
			}
			if _, err := runner.traceFn(ctx, bad, time.Second); err == nil {
				t.Error("非法地址应让明细采集直接报错")
			}
			if _, err := runner.verifyFn(ctx, bad, 3, time.Second); err == nil {
				t.Error("非法地址应让节点校验直接报错")
			}
		})
	}

	params := scanParams("1.1.1.1")
	runner, err := NewRunner(Options{Params: params})
	if err != nil {
		t.Fatalf("NewRunner 返回错误：%v", err)
	}
	if _, _, err := runner.remoteFn(ctx, nil); err == nil {
		t.Error("没有配置远程源地址时应报错")
	}
}

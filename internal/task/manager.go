package task

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"cloudtrace/internal/event"
	"cloudtrace/internal/model"
)

// 事件 topic。
const (
	// TopicState 是任务状态快照变更，载荷为 model.TaskState。
	TopicState = "state"
	// TopicProgress 是任务进度，载荷为 Progress，已按间隔节流。
	TopicProgress = "progress"
	// TopicError 是任务失败，载荷为 error。
	TopicError = "error"
)

// 终止事件的 topic 由阶段名拼出：`scan` → `scan/done` / `scan/abort` / `scan/failed`。
const (
	suffixDone   = "/done"
	suffixAbort  = "/abort"
	suffixFailed = "/failed"
)

// DoneTopic 返回某阶段完成事件的 topic（如 scan → scan/done）。
func DoneTopic(phase string) string { return phase + suffixDone }

// AbortTopic 返回某阶段中止事件的 topic（如 scan → scan/abort）。
func AbortTopic(phase string) string { return phase + suffixAbort }

// FailedTopic 返回某阶段失败事件的 topic（如 scan → scan/failed）。
//
// 失败同时还会发 TopicError，两者职责不同：TopicError 只承载「出错」与错误码
// 映射，供界面弹提示；本条承载结构化结果（阶段、耗时、告警），供界面回答
// 「卡在哪一步、跑了多久」。合并成一条会让错误提示链路被迫认识任务结构。
func FailedTopic(phase string) string { return phase + suffixFailed }

// 哨兵错误。
//
// 这里刻意不带对外错误码：码表属于传输层，编排层只表达「为什么失败」，
// 由服务端映射。同一份码表在两层各写一遍，迟早会改漏一边。
var (
	// ErrBusy 表示已有任务在执行。扫描与测速互斥，第二个任务必须被拒绝，
	// 而不是排队或并行。
	ErrBusy = errors.New("已有任务正在执行")
	// ErrInvalidParam 表示任务参数非法。
	ErrInvalidParam = errors.New("任务参数非法")
)

// Progress 是 progress 事件的载荷。
//
// 带上已耗时是必需的：运行期间编排层只发 progress、不重发 state，界面上的
// 计时器若只读 state 就会整场停在 0。
type Progress struct {
	Phase   string       `json:"phase"`
	Done    int          `json:"done"`
	Total   int          `json:"total"`
	Funnel  model.Funnel `json:"funnel"`
	ETA     float64      `json:"eta"`
	Elapsed float64      `json:"elapsed"`
	// Stages 是流水线各阶段的统计；任务体不报阶段时为空。
	Stages []model.StageStat `json:"stages,omitempty"`
}

// Outcome 是任务结束时随终止事件下发的信息。
//
// 后三个字段都是 omitempty：旧前端把它们当作未知键忽略，多出来的字段不会
// 让解析失败；没填值的场景（例如耗时不足 1 毫秒）也不必下发空值。
type Outcome struct {
	Count int    `json:"count"`
	ID    string `json:"id,omitempty"`
	Msg   string `json:"msg,omitempty"`
	// Stage 是任务结束时所处的阶段，取 model.PhaseScan / model.PhaseSpeed。
	//
	// 这里用的是**任务阶段**，不是漏斗级别：漏斗的 RegionOK 之类回答的是
	// 「筛到哪一级」，不是「任务卡在哪一步」，混用会让提示文案没法读。
	Stage string `json:"stage,omitempty"`
	// DurationMs 是从任务启动到当前阶段结束的毫秒数。
	DurationMs int64 `json:"duration_ms,omitempty"`
	// Warnings 是任务过程中可继续的局部失败，例如部分数据源不可用但整体
	// 仍然产出了结果。为空表示没有告警，不表示「没有检查」。
	Warnings []string `json:"warnings,omitempty"`
}

// Reporter 是任务向编排层汇报进度的入口。
//
// 逐项推进请走 RunBounded 的 onProgress 回调（那里已经节流），不要在
// 每个工作项里直接调用本接口——那等于绕过节流，把推送量放大几百倍。
type Reporter interface {
	// Context 返回任务级 context，任务内所有可能阻塞的调用都要带上它。
	Context() context.Context
	// SetTotal 设置总步数；候选池生成之前总数不可知。
	SetTotal(total int)
	// SetDone 设置已完成数。
	SetDone(done int)
	// SetFunnel 更新漏斗计数。
	SetFunnel(f model.Funnel)
	// Emit 发布一条业务事件（例如扫描结果增量）。
	Emit(topic string, payload any)
}

// Warner 是 Reporter 的可选扩展：能在任务过程中记录非致命告警。
//
// 设为可选接口而不是往 Reporter 里加方法：Reporter 已有多个实现，只为收集
// 告警就改接口，所有实现都得跟着改一遍，而多数实现根本不产生告警。
type Warner interface {
	// AddWarning 追加一条告警；空白串被忽略。
	AddWarning(msg string)
}

// Warn 给支持告警收集的 Reporter 追加一条告警，不支持时静默忽略。
//
// phase 遇到「可继续的局部失败」时调用它，而不是把任务判成失败：整体结果
// 仍然可用，用户需要知道的是哪一部分没拿到，而不是收到一句失败提示。
func Warn(rep Reporter, msg string) {
	if w, ok := rep.(Warner); ok {
		w.AddWarning(msg)
	}
}

// StageReporter 是 Reporter 的可选扩展：能接收流水线阶段统计。
//
// 与 Warner 同理，设为可选接口而不是往 Reporter 里加方法：多数 Reporter
// 实现并不产生阶段统计，只为它改接口会让所有实现跟着改一遍。
type StageReporter interface {
	// SetStages 用一份完整的阶段统计替换当前值。
	SetStages(stages []model.StageStat)
}

// SetStages 把阶段统计交给支持它的 Reporter，不支持时静默忽略。
func SetStages(rep Reporter, stages []model.StageStat) {
	if sr, ok := rep.(StageReporter); ok {
		sr.SetStages(stages)
	}
}

// RunFunc 是任务主体。返回的 Outcome 随终止事件下发。
type RunFunc func(Reporter) (Outcome, error)

// Options 是 Manager 的可注入依赖。
type Options struct {
	// Now 取时间，为 nil 时用 time.Now。注入是为了让耗时与 ETA 可测。
	Now func() time.Time
	// BaseContext 是任务的父 context，通常来自进程生命周期；
	// 为 nil 时用 context.Background()。
	BaseContext context.Context
	// ProgressInterval 是 progress 事件的节流间隔；<= 0 时用默认值。
	ProgressInterval time.Duration
}

// Manager 是任务编排器：单任务互斥 + 状态机 + 事件广播。
//
// 可安全并发使用。一个 Manager 只允许一个任务在跑，扫描与测速共用它，
// 因此二者天然互斥。
type Manager struct {
	bus      *event.Bus
	logger   *slog.Logger
	now      func() time.Time
	base     context.Context
	interval time.Duration

	mu      sync.RWMutex
	state   model.TaskState
	cancel  context.CancelFunc
	running bool
	started time.Time
}

// NewManager 构造任务编排器。
func NewManager(bus *event.Bus, logger *slog.Logger, opts Options) (*Manager, error) {
	if bus == nil {
		return nil, errors.New("task: 事件总线不能为空")
	}
	if logger == nil {
		logger = slog.Default()
	}

	m := &Manager{
		bus:      bus,
		logger:   logger,
		now:      opts.Now,
		base:     opts.BaseContext,
		interval: opts.ProgressInterval,
		state:    model.IdleState(),
	}
	if m.now == nil {
		m.now = time.Now
	}
	if m.base == nil {
		m.base = context.Background()
	}
	return m, nil
}

// Start 启动一个任务；已有任务在执行时返回 ErrBusy。
//
// 立即返回，不等待任务结束：任务主体在后台 goroutine 里跑。若在这里等，
// 命令处理会被任务阻塞，WebSocket 读循环随之卡住，连「停止」都收不到。
func (m *Manager) Start(phase string, runFn RunFunc) error {
	if phase == "" {
		return fmt.Errorf("%w：任务阶段不能为空", ErrInvalidParam)
	}
	if runFn == nil {
		return fmt.Errorf("%w：任务主体不能为空", ErrInvalidParam)
	}

	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return ErrBusy
	}
	ctx, cancel := context.WithCancel(m.base)
	started := m.now()
	m.running = true
	m.cancel = cancel
	m.started = started
	m.state = model.TaskState{
		Phase:     phase,
		Status:    model.StatusRunning,
		StartedAt: started.Unix(),
	}
	snapshot := m.state
	m.mu.Unlock()

	m.bus.Publish(TopicState, snapshot)

	r := &run{
		mgr:      m,
		ctx:      ctx,
		phase:    phase,
		started:  started,
		progress: newProgressTracker(m.interval),
	}
	go m.execute(r, runFn, cancel)
	return nil
}

// Abort 中止当前任务，返回是否确实中止了一个在跑的任务。
//
// 只发取消信号，不等任务退出：等待会让「停止」按钮的响应取决于任务
// 收尾速度。任务的最终状态由后台流程广播。
func (m *Manager) Abort() bool {
	m.mu.Lock()
	cancel, running := m.cancel, m.running
	m.mu.Unlock()

	if !running || cancel == nil {
		return false
	}
	cancel()
	return true
}

// Running 报告当前是否有任务在执行。
func (m *Manager) Running() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.running
}

// Snapshot 返回当前状态快照，供首连与断线重连时全量下发。
//
// 已耗时与 ETA 在这里现算，不依赖最后一次进度推送：否则重连后计时器
// 会停在旧值上不动。
func (m *Manager) Snapshot() model.TaskState {
	m.mu.RLock()
	st := m.state
	st.Stages = cloneStages(st.Stages)
	started := m.started
	running := m.running
	m.mu.RUnlock()

	if running {
		elapsed := m.now().Sub(started).Seconds()
		st.Elapsed = elapsed
		st.ETA = estimateETA(elapsed, st.Done, st.Total)
	}
	return st
}

// execute 跑完任务主体，判定终态并广播。
func (m *Manager) execute(r *run, runFn RunFunc, cancel context.CancelFunc) {
	defer cancel()

	outcome, err := invokeRun(r, runFn)
	// 阶段与耗时由编排层补：任务体只关心业务结果，而「从启动到现在过了
	// 多久」只有编排层握着起点。
	outcome = r.decorate(outcome)

	status := model.StatusDone
	errMsg := ""
	switch {
	case r.ctx.Err() != nil:
		// 先判中止：取消本身也会让任务主体返回错误，那不是任务的失败。
		// 反过来把中止当成完成，就会写出一份不完整的历史。
		status = model.StatusAborted
	case err != nil:
		status = model.StatusFailed
		errMsg = err.Error()
	}

	m.finish(r, status, errMsg)

	switch status {
	case model.StatusAborted:
		m.bus.Publish(AbortTopic(r.phase), outcome)
	case model.StatusFailed:
		// 结构化结果与错误提示是两条链路：前者回答「卡在哪、跑了多久」，
		// 后者负责错误码映射与弹窗文案。少发前者，失败时前端就只剩一句话。
		m.bus.Publish(FailedTopic(r.phase), outcome)
		m.bus.Publish(TopicError, err)
	default:
		m.bus.Publish(DoneTopic(r.phase), outcome)
	}
}

// finish 落定终态并广播最终快照。
func (m *Manager) finish(r *run, status, errMsg string) {
	done, total, funnel, stages := r.counters()

	m.mu.Lock()
	m.running = false
	m.cancel = nil
	elapsed := m.now().Sub(m.started).Seconds()
	m.state = model.TaskState{
		Phase:     r.phase,
		Status:    status,
		Done:      done,
		Total:     total,
		Elapsed:   elapsed,
		Funnel:    funnel,
		Stages:    stages,
		StartedAt: m.started.Unix(),
		ErrorMsg:  errMsg,
	}
	snapshot := m.state
	m.mu.Unlock()

	m.logger.Info("任务结束",
		"phase", r.phase, "status", status, "done", done, "total", total, "elapsed_s", elapsed)
	m.bus.Publish(TopicState, snapshot)
}

// invokeRun 调用任务主体，并兜住它抛出的 panic。
//
// 任务主体是外部传进来的函数，一个越界索引不该让整个进程退出。
func invokeRun(r *run, runFn RunFunc) (out Outcome, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("任务内部异常：%v", rec)
		}
	}()
	return runFn(r)
}

// estimateETA 按已完成数与已耗时线性外推剩余时间。
//
// 一个都没完成时返回 0：除数算不出来，硬猜只会给出一个不断跳动的
// 假数字。0 由前端显示成「估算中」。
func estimateETA(elapsed float64, done, total int) float64 {
	if done <= 0 || total <= 0 || done >= total || elapsed <= 0 {
		return 0
	}
	return elapsed / float64(done) * float64(total-done)
}

// run 是一次任务执行的进度载体，实现 Reporter。
type run struct {
	mgr      *Manager
	ctx      context.Context
	phase    string
	started  time.Time
	progress *progressTracker

	mu       sync.Mutex
	done     int
	total    int
	funnel   model.Funnel
	stages   []model.StageStat
	warnings []string
}

func (r *run) Context() context.Context { return r.ctx }

func (r *run) SetTotal(total int) {
	r.mu.Lock()
	r.total = total
	r.mu.Unlock()
	r.publish()
}

func (r *run) SetDone(done int) {
	r.mu.Lock()
	r.done = done
	r.mu.Unlock()
	r.publish()
}

func (r *run) SetFunnel(f model.Funnel) {
	r.mu.Lock()
	r.funnel = f
	r.mu.Unlock()
	r.publish()
}

func (r *run) SetStages(stages []model.StageStat) {
	r.mu.Lock()
	r.stages = cloneStages(stages)
	r.mu.Unlock()
	r.publish()
}

// cloneStages 复制阶段切片。
//
// 阶段统计会被 Snapshot、state 与 progress 三处共享，任何一处拿到原切片
// 都可能与下一次替换踩到同一个底层数组；统一在边界上拷贝。
func cloneStages(stages []model.StageStat) []model.StageStat {
	if len(stages) == 0 {
		return nil
	}
	return append([]model.StageStat(nil), stages...)
}

func (r *run) Emit(topic string, payload any) { r.mgr.bus.Publish(topic, payload) }

// AddWarning 记录一条非致命告警，随终止事件下发。
func (r *run) AddWarning(msg string) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return
	}
	r.mu.Lock()
	r.warnings = append(r.warnings, msg)
	r.mu.Unlock()
}

// decorate 补齐阶段、耗时与告警这三项编排层才知道的信息。
//
// 任务体自己返回的 Warnings 保留在前面：那是它明确要说的；编排层收集到的
// 追加在后，顺序不影响语义，但合并时不能丢任何一边。
func (r *run) decorate(out Outcome) Outcome {
	out.Stage = r.phase
	if elapsed := r.mgr.now().Sub(r.started); elapsed > 0 {
		out.DurationMs = elapsed.Milliseconds()
	}

	r.mu.Lock()
	collected := append([]string(nil), r.warnings...)
	r.mu.Unlock()

	if len(collected) > 0 {
		// 重新分配再合并：直接 append 到调用方给的切片可能写进它的底层数组，
		// 任务体若还持有那份数据就会被悄悄改掉。
		merged := make([]string, 0, len(out.Warnings)+len(collected))
		merged = append(merged, out.Warnings...)
		merged = append(merged, collected...)
		out.Warnings = merged
	}
	return out
}

func (r *run) counters() (done, total int, funnel model.Funnel, stages []model.StageStat) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.done, r.total, r.funnel, cloneStages(r.stages)
}

// publish 把进度同步进状态快照，并按节流上报 progress 事件。
//
// 快照每次都更新（重连后要能立刻看到正确进度），事件则受节流约束。
func (r *run) publish() {
	done, total, funnel, stages := r.counters()

	r.mgr.mu.Lock()
	if r.mgr.state.Status == model.StatusRunning {
		r.mgr.state.Done = done
		r.mgr.state.Total = total
		r.mgr.state.Funnel = funnel
		r.mgr.state.Stages = cloneStages(stages)
	}
	r.mgr.mu.Unlock()

	r.progress.report(done, total, func(d, t int) {
		r.mgr.bus.Publish(TopicProgress, Progress{
			Phase:   r.phase,
			Done:    d,
			Total:   t,
			Funnel:  funnel,
			ETA:     r.eta(),
			Elapsed: r.mgr.now().Sub(r.started).Seconds(),
			Stages:  cloneStages(stages),
		})
	})
}

func (r *run) eta() float64 {
	done, total, _, _ := r.counters()
	return estimateETA(r.mgr.now().Sub(r.started).Seconds(), done, total)
}

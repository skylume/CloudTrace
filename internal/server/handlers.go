package server

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"cloudtrace/internal/app"
	"cloudtrace/internal/event"
	"cloudtrace/internal/history"
	"cloudtrace/internal/model"
	"cloudtrace/internal/scan"
	"cloudtrace/internal/speed"
	"cloudtrace/internal/task"
)

// 任务相关的 WS 命令与事件名。
//
// 事件名与事件总线上的 topic 是同一套字符串，不做映射：多一层映射就多
// 一处会改漏的地方。
const (
	cmdScanStart  = "scan/start"
	cmdScanStop   = "scan/stop"
	cmdSpeedStart = "speed/start"
	cmdSpeedStop  = "speed/stop"

	eventProgress  = "progress"
	eventScanDone  = "scan/done"
	eventScanAbort = "scan/abort"
)

// phases 是需要向前端转发终止事件的阶段。
//
// 扫描与测速共用一个任务编排器，因此两者天然互斥：一个在跑时另一个会拿到
// E_BUSY，不需要另设互斥逻辑。
var phases = []string{model.PhaseScan, model.PhaseSpeed}

// taskHandlers 返回任务相关的命令表。
func (s *server) taskHandlers() map[string]commandHandler {
	return map[string]commandHandler{
		cmdScanStart:  s.handleScanStart,
		cmdScanStop:   s.handleScanStop,
		cmdSpeedStart: s.handleSpeedStart,
		cmdSpeedStop:  s.handleSpeedStop,
	}
}

// subscription 是一条待订阅的转发规则。
type subscription struct {
	topic string
	fn    event.Handler
}

// taskSubscriptions 返回需要转发给前端的「topic → 处理方式」清单。
//
// state 与 error 需要加工，其余原样转发。
func (s *server) taskSubscriptions() []subscription {
	subs := []subscription{
		{topic: app.TopicState, fn: s.onStateChanged},
		{topic: task.TopicProgress, fn: s.forward(task.TopicProgress)},
		{topic: scan.TopicResult, fn: s.forward(scan.TopicResult)},
		{topic: speed.TopicPartial, fn: s.forward(speed.TopicPartial)},
		{topic: history.TopicChanged, fn: s.forward(history.TopicChanged)},
		{topic: task.TopicError, fn: s.onTaskError},
	}
	for _, phase := range phases {
		for _, topic := range []string{task.DoneTopic(phase), task.AbortTopic(phase)} {
			subs = append(subs, subscription{topic: topic, fn: s.forward(topic)})
		}
	}
	return subs
}

// forward 把总线上的 topic 原样广播给所有连接。
func (s *server) forward(topic string) event.Handler {
	return func(payload any) { s.hub.broadcast(topic, payload) }
}

// onTaskError 把任务失败转成前端认识的错误载荷。
//
// error 对象直接序列化出来是空对象，前端拿不到任何信息，必须转成
// {code, msg} 再发。
func (s *server) onTaskError(payload any) {
	err, ok := payload.(error)
	if !ok {
		s.logger.Warn("error 事件的载荷不是错误，已忽略")
		return
	}

	// 限流熔断属于网络类问题：用户要看到的是「换个源或过会儿再试」，
	// 而不是「未分类错误」。
	code := CodeUnknown
	if errors.Is(err, speed.ErrRateLimited) {
		code = CodeNetwork
		s.broadcastBreaker(err)
	}
	s.hub.broadcast(eventError, errorPayload{Code: code, Msg: err.Error()})
}

// broadcastBreaker 在熔断时额外广播一条可操作的通知。
//
// 与错误事件分开：错误提示只说明「出错了」，而熔断有明确的下一步——降低并发
// 或者换测速源。把这两条建议随事件一起给出，界面才能把它们做成按钮，而不是
// 让用户自己去设置页里找。
//
// 带上当前配置值而不是让前端猜：建议「降到多少」得有个起点，起点只能是当前值。
func (s *server) broadcastBreaker(err error) {
	cfg := s.cfg.Get()
	s.hub.broadcast(eventSpeedBreaker, speedBreakerPayload{
		Message:     err.Error(),
		Concurrency: cfg.Speed.Concurrency,
		URLMode:     cfg.Speed.URLMode,
	})
}

// speedBreakerPayload 是熔断通知的载荷。
type speedBreakerPayload struct {
	Message string `json:"message"`
	// Concurrency 是当前配置的测速并发，「降低并发」建议以它为起点。
	Concurrency int `json:"concurrency"`
	// URLMode 是当前的测速源模式，供「换个源」建议使用。
	URLMode string `json:"url_mode"`
}

// handleScanStart 校验参数并启动扫描任务。
//
// 参数校验同步做掉：任务一旦启动就在后台 goroutine 里跑，到那时才报参数
// 错误的话，前端只能收到一条笼统的 error 事件，没法定位到具体字段。
func (s *server) handleScanStart(_ *wsConn, data json.RawMessage) error {
	var params model.ScanParams
	if len(data) > 0 {
		if err := json.Unmarshal(data, &params); err != nil {
			return fail(CodeInvalidParam, "扫描参数不是合法 JSON")
		}
	}
	params = scan.NormalizeParams(params)
	if err := scan.ValidateParams(params); err != nil {
		return fail(CodeInvalidParam, err.Error())
	}

	runner, err := scan.NewRunner(scan.Options{
		Params: params,
		// 每次扫描换一个种子：采样的意义就是每轮挑不同的地址，固定种子
		// 会让用户每次扫到同一批。
		Seed:   time.Now().UnixNano(),
		Logger: s.logger,
		// 远端源从配置现取：用户可能刚在界面上加了地址还没保存任务参数，
		// 拿配置才是他看到的那个列表。
		RemoteURLs: s.cfg.Get().Source.EnabledURLs(),
		// 归属地补齐只在本地查表与内存里算，不发请求，因此可以挂在每个
		// 节点的产出路径上。
		Enrich: s.geoEnrich(),
	})
	if err != nil {
		return fail(CodeInvalidParam, err.Error())
	}

	return s.startTask(model.PhaseScan, s.runScanTask(runner, params))
}

// runScanTask 把扫描执行器包成任务体，并在跑完后存档。
func (s *server) runScanTask(runner *scan.Runner, params model.ScanParams) task.RunFunc {
	var res model.TaskResult
	runner.SetOnDone(func(r model.TaskResult) { res = r })

	return taskRunner(runner.Run, func(duration float64) {
		s.archiveScan(params, s.currentPreset(), res, duration)
	})
}

// geoEnrich 返回注入给扫描流水线的归属地补齐；ASN 不可用时返回 nil。
func (s *server) geoEnrich() func(*model.IPRecord) {
	if s.svc.Geo == nil {
		return nil
	}
	return s.svc.Geo.Enrich
}

// handleSpeedStart 校验参数并启动测速任务。
func (s *server) handleSpeedStart(_ *wsConn, data json.RawMessage) error {
	var params model.SpeedParams
	if len(data) > 0 {
		if err := json.Unmarshal(data, &params); err != nil {
			return fail(CodeInvalidParam, "测速参数不是合法 JSON")
		}
	}
	params = speed.NormalizeParams(params)
	if err := speed.ValidateParams(params); err != nil {
		return fail(CodeInvalidParam, err.Error())
	}

	runner, err := speed.NewRunner(speed.Options{
		Params: params,
		// 选源器挂在服务端而不是每次新建：出口 ISP 探测结果要跨任务复用。
		Source: s.resolveSpeedSource,
		Logger: s.logger,
	})
	if err != nil {
		return fail(CodeInvalidParam, err.Error())
	}

	return s.startTask(model.PhaseSpeed, s.runSpeedTask(runner, params))
}

// runSpeedTask 把测速执行器包成任务体，并在跑完后存档。
func (s *server) runSpeedTask(runner *speed.Runner, params model.SpeedParams) task.RunFunc {
	var res model.TaskResult
	runner.SetOnDone(func(r model.TaskResult) { res = r })

	return taskRunner(runner.Run, func(duration float64) {
		s.archiveSpeed(params, s.currentPreset(), res, duration)
	})
}

// currentPreset 取本次任务使用的档位名。
//
// 从编排层取而不是让任务体自己记：档位是任务体通过 Reporter 上报的，
// 编排层才是它唯一的存放处。
func (s *server) currentPreset() string {
	return s.svc.Tasks.Snapshot().Preset
}

// startTask 启动任务并把编排层的失败翻译成错误码。
func (s *server) startTask(phase string, runFn task.RunFunc) error {
	err := s.svc.Tasks.Start(phase, runFn)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, task.ErrBusy):
		return fail(CodeBusy, err.Error())
	default:
		return fail(CodeInvalidParam, err.Error())
	}
}

// handleScanStop 中止当前扫描。
//
// 「没有任务在跑」不算错误：用户连点两次停止是常事，为此报错只会带来
// 困惑，而且前端本来就得处理这种竞态。
func (s *server) handleScanStop(_ *wsConn, _ json.RawMessage) error {
	s.svc.Tasks.Abort()
	return nil
}

// resolveSpeedSource 选测速源并把「为什么选它」广播出去。
//
// 自动选源对用户是个黑箱：同一份配置在不同网络下会选中不同的源，而界面上看不
// 到任何线索。把理由发出去，用户才能判断「这次测得准不准」。
//
// 包在选源外面而不是改选源器本身：选源器是纯逻辑，不该知道有事件总线这回事。
func (s *server) resolveSpeedSource(ctx context.Context, mode, customURL string) (string, error) {
	decision, err := s.speedSource.Decide(ctx, mode, customURL)
	if err != nil {
		return "", err
	}
	s.hub.broadcast(eventSpeedSource, speedSourcePayload{
		URL:    decision.URL,
		Code:   decision.Code,
		Detail: decision.Detail,
		Mode:   mode,
	})
	return decision.URL, nil
}

// speedSourcePayload 是选源说明的载荷。
type speedSourcePayload struct {
	URL string `json:"url"`
	// Code 是原因标识，前端据此选文案。
	Code string `json:"code"`
	// Detail 是补充说明，如「AS9808 中国移动」。
	Detail string `json:"detail,omitempty"`
	// Mode 是本次请求的测速源模式。
	Mode string `json:"mode"`
}

// handleSpeedStop 中止当前测速，语义与停止扫描一致。
func (s *server) handleSpeedStop(_ *wsConn, _ json.RawMessage) error {
	s.svc.Tasks.Abort()
	return nil
}

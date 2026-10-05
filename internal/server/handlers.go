package server

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"cloudtrace/internal/adaptive"
	"cloudtrace/internal/app"
	"cloudtrace/internal/config"
	"cloudtrace/internal/event"
	"cloudtrace/internal/geo"
	"cloudtrace/internal/history"
	"cloudtrace/internal/model"
	"cloudtrace/internal/netx"
	"cloudtrace/internal/scan"
	"cloudtrace/internal/source"
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
		cmdScanStart:         s.handleScanStart,
		cmdScanStop:          s.handleScanStop,
		cmdSpeedStart:        s.handleSpeedStart,
		cmdSpeedStop:         s.handleSpeedStop,
		cmdAdaptiveRecommend: s.handleAdaptiveRecommend,
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
		// 下载进度只用来触发一次重新读取：载荷是地理层的 Status，而前端要的
		// 是带提示与快捷选项的完整状态，重新组装一遍比让载荷长成两种形状干净。
		{topic: geo.TopicProgress, fn: func(any) { s.hub.broadcast(eventGeo, s.geoStatus()) }},
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

// scanOptions 组装扫描任务的选项。
//
// 单独成一个方法是为了能被直接断言：这里的每一项都是「配置到任务」的接线，
// 少传一项不会报错，只会让某个功能静默失效——远端源就曾经这样漏过一次。
// 抽出来之后，用例可以直接检查这些字段，不必真的跑一次扫描。
/**
 * remoteOptions 把来源配置翻译成远端拉取的选项。
 *
 * 单独一个函数而不是塞进 scanOptions 的构造字面量里：三处单位换算（秒→毫秒）
 * 加一个枚举校验挤在一起，读的人分不清哪个字段对应哪一项。
 *
 * 这三个开关（重试次数、重试间隔、超时）此前**一个都没被读过**——界面上它们
 * 是可用的，改了什么都不会发生。名字和探测参数里的 Retry / TimeoutMS 撞了，
 * 所以「每个配置项都得有人读」那条守卫也没发现。
 */
func remoteOptions(cfg config.Config) source.RemoteOptions {
	strategy := source.MergeUnion
	if cfg.Source.MergeStrategy == string(source.MergeIntersect) {
		strategy = source.MergeIntersect
	}
	return source.RemoteOptions{
		Timeout:  time.Duration(cfg.Source.TimeoutMS) * time.Millisecond,
		Retries:  cfg.Source.Retry,
		Interval: time.Duration(cfg.Source.RetryIntervalMS) * time.Millisecond,
		Merge:    strategy,
		// 远程源要解析域名，是自定义 DNS 的主要作用面。
		Dialer: netx.NewDialer(cfg.Net.CustomDNS, cfg.Net.DNSFallback),
	}
}

func (s *server) scanOptions(params model.ScanParams) scan.Options {
	return scan.Options{
		Params: params,
		// 每次扫描换一个种子：采样的意义就是每轮挑不同的地址，固定种子
		// 会让用户每次扫到同一批。
		Seed:   time.Now().UnixNano(),
		Logger: s.logger,
		// 远端源从配置现取：用户可能刚在界面上加了地址还没保存任务参数，
		// 拿配置才是他看到的那个列表。
		RemoteURLs: s.cfg.Get().Source.EnabledURLs(),
		// 重试、间隔、合并方式同样从配置现取，理由和地址一样。
		RemoteOptions: remoteOptions(s.cfg.Get()),
		// 归属地补齐只在本地查表与内存里算，不发请求，因此可以挂在每个
		// 节点的产出路径上。
		Enrich: s.geoEnrich(),
	}
}

/**
 * scanStartReq 是 scan/start 的载荷。
 *
 * 档位名与任务参数分开：档位名只用于历史归档，不进参数快照。参数快照要能被
 * 「用同样的参数再跑一次」直接复用，多混一个字段就多一处要过滤的地方。
 */
type scanStartReq struct {
	model.ScanParams
	// Preset 是本次使用的档位标识；空表示用户是手调的，历史里不记档位。
	Preset string `json:"preset"`
}

// handleScanStart 校验参数并启动扫描任务。
//
// 参数校验同步做掉：任务一旦启动就在后台 goroutine 里跑，到那时才报参数
// 错误的话，前端只能收到一条笼统的 error 事件，没法定位到具体字段。
func (s *server) handleScanStart(_ *wsConn, data json.RawMessage) error {
	var req scanStartReq
	if len(data) > 0 {
		if err := json.Unmarshal(data, &req); err != nil {
			return fail(CodeInvalidParam, "扫描参数不是合法 JSON")
		}
	}
	params := scan.NormalizeParams(req.ScanParams)
	if err := scan.ValidateParams(params); err != nil {
		return fail(CodeInvalidParam, err.Error())
	}

	runner, err := scan.NewRunner(s.scanOptions(params))
	if err != nil {
		return fail(CodeInvalidParam, err.Error())
	}

	return s.startTask(model.PhaseScan, s.runScanTask(runner, params, s.presetName(req.Preset)))
}

// runScanTask 把扫描执行器包成任务体，并在跑完后存档。
func (s *server) runScanTask(runner *scan.Runner, params model.ScanParams, preset string) task.RunFunc {
	var res model.TaskResult
	runner.SetOnDone(func(r model.TaskResult) { res = r })

	return taskRunner(preset, runner.Run, func(duration float64) {
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

// speedStartReq 是 speed/start 的载荷，结构与 scan/start 一致。
type speedStartReq struct {
	model.SpeedParams
	Preset string `json:"preset"`
}

// handleSpeedStart 校验参数并启动测速任务。
func (s *server) handleSpeedStart(_ *wsConn, data json.RawMessage) error {
	var req speedStartReq
	if len(data) > 0 {
		if err := json.Unmarshal(data, &req); err != nil {
			return fail(CodeInvalidParam, "测速参数不是合法 JSON")
		}
	}
	params := speed.NormalizeParams(req.SpeedParams)
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

	return s.startTask(model.PhaseSpeed, s.runSpeedTask(runner, params, s.presetName(req.Preset)))
}

// runSpeedTask 把测速执行器包成任务体，并在跑完后存档。
func (s *server) runSpeedTask(runner *speed.Runner, params model.SpeedParams, preset string) task.RunFunc {
	var res model.TaskResult
	runner.SetOnDone(func(r model.TaskResult) { res = r })

	return taskRunner(preset, runner.Run, func(duration float64) {
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

/**
 * presetName 把档位标识翻译成名字，供历史归档显示。
 *
 * 认不出来的标识原样返回：前端可能报上一个自定义的标签，丢掉它不如留着。
 * 空标识表示用户手调的参数，不属于任何档位，历史里就空着。
 */
func (s *server) presetName(id string) string {
	return presetLabel(s.svc.Presets, id)
}

// presetLabel 是 presetName 的实现，抽成独立函数以便直接断言——它没有别的
// 依赖，只认档位库。
func presetLabel(store *config.PresetStore, id string) string {
	if id == "" {
		return ""
	}
	if store == nil {
		return id
	}
	if preset, ok := store.Get(id); ok {
		return preset.Name
	}
	return id
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
	// 选源顺带知道了出口运营商，这是「移动宽带」这个自适应信号唯一的来源。
	if decision.Code == speed.ReasonMobile {
		s.applyAdaptive(adaptive.SignalMobileISP, false)
	}
	return decision.URL, nil
}

// applyAdaptive 按网络信号调整参数，或只给出建议。
//
// 判定与写入分开：`adaptive.Evaluate` 只回答「该怎么办」，而能不能拿到要写入的
// 内容由 `Decision.Patch` 决定——它只对「静默调整」返回改动。用户显式设过的值
// 因此改不动，这是结构性保证，不靠这里记得判断。
//
// 被调整的值一律留痕：发事件、写日志。规格里明确写了「不允许静默改动」，
// 而「静默」指的是用户看不见——事件驱动界面上的徽标与还原按钮，日志留给事后查。
func (s *server) applyAdaptive(signal adaptive.Signal, explicit bool) bool {
	cfg := s.cfg.Get()
	options := adaptive.Options{
		Enabled:     cfg.UI.AdaptiveEnabled,
		AllowPreset: cfg.UI.AdaptiveAllowPreset,
	}

	targets := []struct {
		key     string
		current int
	}{
		{"scan.workers", cfg.Scan.Workers},
		{"speed.concurrency", cfg.Speed.Concurrency},
	}

	applied := false
	for _, target := range targets {
		req := adaptive.Request{
			Key:     target.key,
			Current: target.current,
			Origin:  cfg.Origins[target.key],
			Signal:  signal,
			Options: options,
		}
		var decision adaptive.Decision
		if explicit {
			decision = adaptive.EvaluateExplicit(req)
		} else {
			decision = adaptive.Evaluate(req)
		}
		if decision.Action == adaptive.ActionNone {
			continue
		}

		payload := adaptivePayload{
			Key:    decision.Key,
			From:   decision.From,
			To:     decision.To,
			Reason: decision.Reason,
		}

		if patch := decision.Patch(); patch != nil {
			// 来源标记跟着值一起改：这次改动是自动做的，不是用户做的。
			if _, err := s.cfg.Patch(patch, model.ParamOrigins{decision.Key: model.OriginDefault}); err != nil {
				s.logger.Warn("自适应调整参数失败", "key", decision.Key, "err", err)
				continue
			}
			applied = true
			s.logger.Info("已按网络环境自动调整参数", "key", decision.Key, "from", decision.From, "to", decision.To, "reason", decision.Reason)
			s.hub.broadcast(eventAdaptiveApplied, payload)
			continue
		}

		s.logger.Info("自适应只给出建议，未改动参数", "key", decision.Key, "from", decision.From, "to", decision.To, "reason", decision.Reason)
		s.hub.broadcast(eventAdaptiveSuggestion, payload)
	}
	return applied
}

// cmdAdaptiveRecommend 是「智能推荐」命令。
const cmdAdaptiveRecommend = "adaptive/recommend"

// adaptiveRecommendTimeout 是一次出口探测的上限。
const adaptiveRecommendTimeout = 20 * time.Second

/**
 * handleAdaptiveRecommend 按当前网络环境给一组调整并直接应用。
 *
 * 与自动自适应的区别只有一处：**不受来源限制**。用户点了这个按钮就是明确授权，
 * 包括改他手填过的值——这正是「显式操作，不受限制」的意思。改动照样走
 * adaptive/applied 事件，因此徽标与「还原」都还在，撤销路径与自动调整完全一样。
 *
 * 没有触发条件时返回一个明确的错误，而不是静默成功：按钮点下去什么都不发生，
 * 用户只会怀疑它坏了。
 */
func (s *server) handleAdaptiveRecommend(_ *wsConn, _ json.RawMessage) error {
	ctx, cancel := context.WithTimeout(context.Background(), adaptiveRecommendTimeout)
	defer cancel()

	if !s.speedSource.MobileExit(ctx) {
		return fail(CodeInvalidParam, "当前网络环境没有可推荐的调整")
	}
	if !s.applyAdaptive(adaptive.SignalMobileISP, true) {
		return fail(CodeInvalidParam, "当前参数已经在推荐范围内")
	}
	return nil
}

// adaptivePayload 是自适应事件的载荷。
type adaptivePayload struct {
	Key  string `json:"key"`
	From int    `json:"from"`
	To   int    `json:"to"`
	// Reason 是机器可读的原因标识，文案由界面层决定。
	Reason string `json:"reason"`
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

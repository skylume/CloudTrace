package server

import (
	"encoding/json"
	"errors"
	"time"

	"cloudtrace/internal/app"
	"cloudtrace/internal/event"
	"cloudtrace/internal/model"
	"cloudtrace/internal/scan"
	"cloudtrace/internal/task"
)

// 扫描相关的 WS 命令与事件名。
//
// 事件名与事件总线上的 topic 是同一套字符串，不做映射：多一层映射就多
// 一处会改漏的地方。
const (
	cmdScanStart = "scan/start"
	cmdScanStop  = "scan/stop"

	eventProgress  = "progress"
	eventScanDone  = "scan/done"
	eventScanAbort = "scan/abort"
)

// scanHandlers 返回扫描相关的命令表。
func (s *server) scanHandlers() map[string]commandHandler {
	return map[string]commandHandler{
		cmdScanStart: s.handleScanStart,
		cmdScanStop:  s.handleScanStop,
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
	phase := model.PhaseScan
	return []subscription{
		{topic: app.TopicState, fn: s.onStateChanged},
		{topic: task.TopicProgress, fn: s.forward(task.TopicProgress)},
		{topic: scan.TopicResult, fn: s.forward(scan.TopicResult)},
		{topic: task.TopicError, fn: s.onTaskError},
		{topic: task.DoneTopic(phase), fn: s.forward(task.DoneTopic(phase))},
		{topic: task.AbortTopic(phase), fn: s.forward(task.AbortTopic(phase))},
	}
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
	s.hub.broadcast(eventError, errorPayload{Code: CodeUnknown, Msg: err.Error()})
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
	})
	if err != nil {
		return fail(CodeInvalidParam, err.Error())
	}

	err = s.svc.Tasks.Start(model.PhaseScan, func(rep task.Reporter) (task.Outcome, error) {
		count, runErr := runner.Run(rep)
		return task.Outcome{Count: count}, runErr
	})
	if err != nil {
		if errors.Is(err, task.ErrBusy) {
			return fail(CodeBusy, err.Error())
		}
		return fail(CodeInvalidParam, err.Error())
	}
	return nil
}

// handleScanStop 中止当前扫描。
//
// 「没有任务在跑」不算错误：用户连点两次停止是常事，为此报错只会带来
// 困惑，而且前端本来就得处理这种竞态。
func (s *server) handleScanStop(_ *wsConn, _ json.RawMessage) error {
	s.svc.Tasks.Abort()
	return nil
}

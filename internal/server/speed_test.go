package server

import (
	"fmt"
	"testing"
	"time"

	"cloudtrace/internal/model"
	"cloudtrace/internal/speed"
	"cloudtrace/internal/task"
)

// 一份参数合法的测速请求，用来验证「校验之后」的分支。
const validSpeedParams = `{"scope":"single","targets":[{"ip":"1.1.1.1","port":443}],` +
	`"url_mode":"official","use_tls":"auto","concurrency":1,"target_qualified":1,` +
	`"interval_ms":1,"download_duration_s":1,"breaker_429":3,"timeout_ms":200}`

func TestWSSpeedStartRejectsInvalidParams(t *testing.T) {
	st := newTestStack(t, nil)
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	// 端口越界：必须在启动任务之前同步报错，否则前端只能收到一条笼统的错误。
	send(t, conn, `{"type":"speed/start","data":{"scope":"single","targets":[{"ip":"1.1.1.1","port":70000}],`+
		`"url_mode":"official","use_tls":"auto","concurrency":1,"target_qualified":1,`+
		`"interval_ms":1,"download_duration_s":1,"breaker_429":3,"timeout_ms":200}}`)

	m := readUntil(t, conn, eventError, 3*time.Second)
	var p errorPayload
	decode(t, m, &p)
	if p.Code != CodeInvalidParam {
		t.Errorf("code = %q，期望 %q", p.Code, CodeInvalidParam)
	}
	if st.svc.Tasks.Running() {
		t.Error("参数非法时不应启动任务")
	}
}

/**
 * 一份「按配置组装好的完整参数」必须整体被接受。
 *
 * 前端发起测速时会把并发、间隔、测速源、评分权重、熔断阈值这些全部带上，
 * 服务端不替调用方读配置。这份载荷的形状一旦与校验不符，用户改过的每一项
 * 都会静默失效——所以要有一处断言把它们整体过一遍，而不是只测默认值能跑。
 *
 * 同样借「已有任务在跑」判断：E_BUSY 说明整份参数都过了校验。
 */
func TestWSSpeedStartAcceptsFullParamsFromConfig(t *testing.T) {
	st := newTestStack(t, nil)
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	release := blockTask(t, st, model.PhaseSpeed)
	readUntil(t, conn, eventState, 3*time.Second)

	// 逐项取值都刻意不取默认值，其中 timeout_ms 为 0 表示「交给后端决定」。
	const full = `{"scope":"single","targets":[{"ip":"1.1.1.1","port":443}],"ip_version":4,` +
		`"url_mode":"mobile_only","custom_url":"","use_tls":"true","concurrency":4,` +
		`"target_qualified":25,"interval_ms":800,"min_speed":6.5,"weight_speed":2,` +
		`"weight_latency":0.5,"weight_jitter":0.2,"per_region_topn":3,` +
		`"download_duration_s":15,"breaker_429":5,"usability_check":false,"timeout_ms":0}`

	send(t, conn, `{"type":"speed/start","data":`+full+`}`)

	m := readUntil(t, conn, eventError, 3*time.Second)
	var p errorPayload
	decode(t, m, &p)
	if p.Code != CodeBusy {
		t.Errorf("code = %q，期望 %q（%s）——说明这份参数没被整体接受", p.Code, CodeBusy, p.Msg)
	}
	release()
}

// 没有选中任何目标同样要在启动前拦住。
func TestWSSpeedStartRejectsEmptyTargets(t *testing.T) {
	st := newTestStack(t, nil)
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"speed/start","data":{"scope":"single","targets":[],"url_mode":"official"}}`)

	m := readUntil(t, conn, eventError, 3*time.Second)
	var p errorPayload
	decode(t, m, &p)
	if p.Code != CodeInvalidParam {
		t.Errorf("code = %q，期望 %q", p.Code, CodeInvalidParam)
	}
}

// 扫描与测速共用同一个任务编排器，因此天然互斥：一个在跑时另一个必须拿到
// E_BUSY，不能并行。
func TestWSSpeedStartReturnsBusyWhileScanning(t *testing.T) {
	st := newTestStack(t, nil)
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	release := blockTask(t, st, model.PhaseScan)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"speed/start","data":`+validSpeedParams+`}`)

	m := readUntil(t, conn, eventError, 3*time.Second)
	var p errorPayload
	decode(t, m, &p)
	if p.Code != CodeBusy {
		t.Errorf("code = %q，期望 %q", p.Code, CodeBusy)
	}
	release()
}

// 反过来同样成立：测速在跑时扫描必须拿到 E_BUSY。
func TestWSScanStartReturnsBusyWhileSpeeding(t *testing.T) {
	st := newTestStack(t, nil)
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	release := blockTask(t, st, model.PhaseSpeed)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"scan/start","data":{"port":443,"workers":8,"latency_threshold":230,`+
		`"ping_times":2,"timeout_ms":1000,"source_mode":"official","ip_version":4,"mode":"tcping"}}`)

	m := readUntil(t, conn, eventError, 3*time.Second)
	var p errorPayload
	decode(t, m, &p)
	if p.Code != CodeBusy {
		t.Errorf("code = %q，期望 %q", p.Code, CodeBusy)
	}
	release()
}

func TestWSSpeedStopWhenIdleIsNotAnError(t *testing.T) {
	st := newTestStack(t, nil)
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"speed/stop"}`)

	// 紧跟一条 ping，能正常收到 pong 就说明连接与命令循环都还健在。
	send(t, conn, `{"type":"ping"}`)
	readUntil(t, conn, eventPong, 3*time.Second)
}

// 测速结果与终止事件都要转发到前端。
func TestWSSpeedEventsAreForwarded(t *testing.T) {
	st := newTestStack(t, nil)
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	st.svc.Bus.Publish(speed.TopicPartial, []model.IPRecord{{IP: "1.1.1.1", Port: 443, SpeedMBps: 12.5}})

	m := readUntil(t, conn, speed.TopicPartial, 3*time.Second)
	var records []model.IPRecord
	decode(t, m, &records)
	if len(records) != 1 || records[0].SpeedMBps != 12.5 {
		t.Errorf("增量结果 = %+v，期望一条 12.5 MB/s 的记录", records)
	}

	// 完成与中止走各自的 topic，前端据此区分「存档」与「丢弃」。
	for _, topic := range []string{task.DoneTopic(model.PhaseSpeed), task.AbortTopic(model.PhaseSpeed)} {
		st.svc.Bus.Publish(topic, task.Outcome{Count: 3})

		done := readUntil(t, conn, topic, 3*time.Second)
		var outcome task.Outcome
		decode(t, done, &outcome)
		if outcome.Count != 3 {
			t.Errorf("%s 载荷 = %+v，期望 count 3", topic, outcome)
		}
	}
}

// 限流熔断要报成网络类错误：用户要看到的是「换个源或过会儿再试」，
// 而不是「未分类错误」。
func TestWSErrorEventMapsRateLimitToNetwork(t *testing.T) {
	st := newTestStack(t, nil)
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	st.svc.Bus.Publish(task.TopicError,
		fmt.Errorf("%w：连续 3 次遇到限速（HTTP 429），已停止测速", speed.ErrRateLimited))

	m := readUntil(t, conn, eventError, 3*time.Second)
	var p errorPayload
	decode(t, m, &p)
	if p.Code != CodeNetwork {
		t.Errorf("code = %q，期望 %q", p.Code, CodeNetwork)
	}
	if p.Msg == "" {
		t.Error("错误信息不能为空，前端要拿它给用户看")
	}
}

// 其他任务失败仍归入未分类，不要被熔断的映射带偏。
func TestWSErrorEventKeepsUnknownCodeForOtherErrors(t *testing.T) {
	st := newTestStack(t, nil)
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	st.svc.Bus.Publish(task.TopicError, fmt.Errorf("来源设置有问题"))

	m := readUntil(t, conn, eventError, 3*time.Second)
	var p errorPayload
	decode(t, m, &p)
	if p.Code != CodeUnknown {
		t.Errorf("code = %q，期望 %q", p.Code, CodeUnknown)
	}
}

// blockTask 启动一个卡住的任务，返回放行函数。
//
// 用真实任务驱动状态机，而不是直接改状态：任务状态只有一个来源，测试也走
// 生产同一条路径。
func blockTask(t *testing.T, st *testStack, phase string) func() {
	t.Helper()

	ch := make(chan struct{})
	release := func() { close(ch) }
	// 用例中途失败也要放行，否则任务一直挂着，退出时的等待会白等满上限。
	t.Cleanup(func() {
		select {
		case <-ch:
		default:
			release()
		}
	})

	if err := st.svc.Tasks.Start(phase, func(task.Reporter) (task.Outcome, error) {
		<-ch
		return task.Outcome{}, nil
	}); err != nil {
		t.Fatalf("启动任务失败：%v", err)
	}
	return release
}

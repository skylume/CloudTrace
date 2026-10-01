package scan

import (
	"context"
	"sync"

	"cloudtrace/internal/model"
	"cloudtrace/internal/probe"
	"cloudtrace/internal/task"
)

// verified 是一条记录与它是否通过校验。
//
// 用布尔标记而不是「把延迟改成哨兵值」来表示被剔除：哨兵值参与后续统计
// 时会污染计数，标记则不会。
type verified struct {
	rec  model.IPRecord
	keep bool
}

// resultSink 按批推送扫描结果。
//
// 边产边推而不是最后一次性推：中止时用户要能保留已经扫出来的结果，攒到
// 最后再发的话，中止意味着一条都不剩。分批同时把单个帧的大小压住——
// 几千条记录塞进一个 WebSocket 帧会顶到帧上限，前端也要在一帧里做完全部
// 渲染。
type resultSink struct {
	rep task.Reporter

	mu     sync.Mutex
	buffer []model.IPRecord
}

// add 收下一条结果，攒够一批就推送。
func (s *resultSink) add(rec model.IPRecord) {
	s.mu.Lock()
	s.buffer = append(s.buffer, rec)
	if len(s.buffer) < resultChunk {
		s.mu.Unlock()
		return
	}
	chunk := s.buffer
	s.buffer = nil
	s.mu.Unlock()
	s.rep.Emit(TopicResult, chunk)
}

// flush 推送最后一批不足量的结果。
func (s *resultSink) flush() {
	s.mu.Lock()
	chunk := s.buffer
	s.buffer = nil
	s.mu.Unlock()
	if len(chunk) > 0 {
		s.rep.Emit(TopicResult, chunk)
	}
}

// collect 做节点校验与明细采集，产出最终记录。
//
// 关闭明细采集时整个步骤被跳过：校验与明细打的是同一个 trace 端点，
// 只跳一半等于没跳——「开关关掉了请求照发」正是要避免的实现。
//
// 出错（含中止）时把已经产出的记录一并返回：中止保留部分结果是硬要求，
// 把计数丢成 0 会让界面显示「已中止，保留 0 条」。
func (r *Runner) collect(ctx context.Context, rep task.Reporter, st *task.Stages, sink *resultSink, items []probed) ([]model.IPRecord, error) {
	if len(items) == 0 {
		return nil, nil
	}
	if !r.params.VerifyNodes {
		return collectWithoutTrace(items, sink), nil
	}

	st.Begin(len(items))
	checked, err := task.RunBounded(ctx, items, r.params.Workers,
		func(cctx context.Context, item probed) (verified, bool) {
			out := r.verifyItem(cctx, item)
			if out.keep {
				sink.add(out.rec)
			}
			// 第二个返回值是「触发提前收敛」，这里恒为 false：校验阶段没有
			// 「够了」这回事，返回 true 会让它在第一项之后就不派发了。
			return out, false
		},
		func(done, _ int) { st.Advance(done) },
	)
	return keptRecords(checked), err
}

// keptRecords 从校验结果里挑出保留的那些，顺序与输入一致。
func keptRecords(checked []verified) []model.IPRecord {
	out := make([]model.IPRecord, 0, len(checked))
	for _, item := range checked {
		if item.keep {
			out = append(out, item.rec)
		}
	}
	return out
}

// verifyItem 校验并采集单个节点。
//
// 判定「确认不是 Cloudflare」才剔除；校验本身出错说明这次没能验证，
// 按保留处理——把一次调用错误当成「不是 CF」，会一次剔掉整批节点。
func (r *Runner) verifyItem(ctx context.Context, item probed) (out verified) {
	defer func() {
		if rec := recover(); rec != nil {
			r.logger.Warn("节点校验异常", "ip", item.cand.IP, "err", rec)
			out = verified{}
		}
	}()

	rec := recordOf(item)
	verdict, err := r.verifyFn(ctx, item.cand, cfRounds, r.timeout())
	if err != nil {
		r.logger.Debug("节点校验出错", "ip", item.cand.IP, "err", err)
	} else if !verdict.Verdict.Keep() {
		return verified{}
	}

	trace, err := r.traceFn(ctx, item.cand, r.timeout())
	if err != nil {
		r.logger.Debug("明细采集出错", "ip", item.cand.IP, "err", err)
	}
	rec.Trace = trace
	fillRegion(&rec, item.cand, item.res, trace)
	return verified{rec: rec, keep: true}
}

// collectWithoutTrace 在关闭明细采集时直接从探测结果生成记录。
//
// 这里一次网络请求都不发。HTTPing 顺带拿到的 colo 是唯一的地区信息，
// TCPing 模式下地区就是空的——这是关掉明细采集的必然代价，界面按
// 「地区未解析」显示即可，不能因此把结果丢掉。
func collectWithoutTrace(items []probed, sink *resultSink) []model.IPRecord {
	out := make([]model.IPRecord, 0, len(items))
	for _, item := range items {
		rec := recordOf(item)
		fillRegion(&rec, item.cand, item.res, nil)
		sink.add(rec)
		out = append(out, rec)
	}
	return out
}

// recordOf 把一次探测结果转成记录。
func recordOf(item probed) model.IPRecord {
	return model.IPRecord{
		IP:         item.cand.IP,
		Port:       item.cand.Port,
		UseTLS:     item.cand.UseTLS,
		Latency:    item.res.Latency,
		LatencyAvg: item.res.LatencyAvg,
		LatencyMax: item.res.LatencyMax,
		Jitter:     item.res.Jitter,
		Loss:       item.res.Loss,
		Sent:       item.res.Sent,
		Recv:       item.res.Recv,
		Colo:       item.res.Colo,
	}
}

// fillRegion 补齐记录的归属地。
//
// 不发任何请求：trace 在明细采集时已经取过，这里只是把字段搬进记录。
// 优先级是 trace > 来源自带的地区：trace 是节点当场回显的真实归属，
// 来源里写的是提供方标注的，可能有出入。
func fillRegion(rec *model.IPRecord, c Candidate, res probe.Result, trace map[string]string) {
	if colo := probe.ExtractColo(trace); colo != "" {
		rec.Colo = colo
	} else if rec.Colo == "" {
		rec.Colo = c.Colo
	}
	if loc := probe.ExtractLoc(trace); loc != "" {
		rec.Loc = loc
	} else if rec.Loc == "" {
		rec.Loc = c.Loc
	}
}

// countRegion 统计有多少条记录拿到了地区信息。
//
// 「地区解析」衡量的是地区信息的覆盖度，不是结果的可用性：TCPing 且关闭
// 明细采集时一条地区都拿不到，但结果照样可用。把两者串成一条链，会让
// 这种配置下的结果被整体抹掉。
func countRegion(records []model.IPRecord) int {
	n := 0
	for _, rec := range records {
		if rec.Colo != "" || rec.Loc != "" {
			n++
		}
	}
	return n
}

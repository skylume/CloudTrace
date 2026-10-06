package server

import (
	"encoding/json"
	"errors"
	"time"

	"cloudtrace/internal/history"
	"cloudtrace/internal/model"
	"cloudtrace/internal/task"
)

// 历史相关的 WS 命令与事件名。
//
// 响应的 type 与命令同名，与既有协议一致：多一层命名映射就多一处会改漏的
// 地方，前端也能用同一套「发什么命令等什么事件」的写法。
const (
	cmdHistoryList    = "history/list"
	cmdHistoryGet     = "history/get"
	cmdHistoryLoad    = "history/load"
	cmdHistoryDelete  = "history/delete"
	cmdHistoryUndo    = "history/undo"
	cmdHistoryTag     = "history/tag"
	cmdHistoryCompare = "history/compare"
	cmdHistoryClear   = "history/clear"
)

// historyHandlers 返回历史相关的命令表。
func (s *server) historyHandlers() map[string]commandHandler {
	return map[string]commandHandler{
		cmdHistoryList:    s.handleHistoryList,
		cmdHistoryGet:     s.handleHistoryGet,
		cmdHistoryLoad:    s.handleHistoryLoad,
		cmdHistoryDelete:  s.handleHistoryDelete,
		cmdHistoryUndo:    s.handleHistoryUndo,
		cmdHistoryTag:     s.handleHistoryTag,
		cmdHistoryCompare: s.handleHistoryCompare,
		cmdHistoryClear:   s.handleHistoryClear,
	}
}

// --- 请求与响应载荷 ---

type historyListReq struct {
	Filter history.Filter `json:"filter"`
}

type historyListResp struct {
	Entries []history.HistoryIndexEntry `json:"entries"`
	Total   int                         `json:"total"`
}

type historyIDReq struct {
	ID string `json:"id"`
}

type historyClearResp struct {
	Removed int `json:"removed"`
}

type historyGetResp struct {
	Record history.HistoryRecord `json:"record"`
}

type historyLoadReq struct {
	ID string `json:"id"`
	// Current 是前端当前的参数快照，用于比对差异。
	//
	// 由前端带过来而不是服务端从配置里取：用户可能改完参数还没保存就来
	// 加载历史，拿配置比会给出与实际不符的结论。
	Current json.RawMessage `json:"current"`
}

type historyLoadResp struct {
	Record history.HistoryRecord `json:"record"`
	// ParamDiff 是历史参数与当前参数的差异，为空表示一致。
	ParamDiff []history.ParamDiff `json:"param_diff"`
	// AgeMinutes 是这份历史距今多少分钟，供前端拼「这是 N 分钟前扫的」。
	AgeMinutes float64 `json:"age_minutes"`
}

// historyIDResp 是只回一个 ID 的通用响应。
type historyIDResp struct {
	ID string `json:"id"`
}

type historyDeleteResp struct {
	ID string `json:"id"`
	// UndoMS 是可撤销窗口的毫秒数。
	UndoMS int64 `json:"undo_ms"`
}

type historyTagReq struct {
	ID      string   `json:"id"`
	Tags    []string `json:"tags"`
	Note    string   `json:"note"`
	Starred bool     `json:"starred"`
}

type historyTagResp struct {
	ID string `json:"id"`
}

type historyCompareReq struct {
	IDA string `json:"idA"`
	IDB string `json:"idB"`
}

type historyCompareResp struct {
	Diff history.HistoryDiff `json:"diff"`
}

// --- 处理函数 ---

// handleHistoryList 返回历史列表。
//
// 走的是索引，一条记录文件都不会打开。
func (s *server) handleHistoryList(c *wsConn, data json.RawMessage) error {
	var req historyListReq
	if len(data) > 0 {
		if err := json.Unmarshal(data, &req); err != nil {
			return fail(CodeInvalidParam, "历史筛选条件不是合法 JSON")
		}
	}
	entries, err := s.history().List(req.Filter)
	if err != nil {
		return s.historyError(err)
	}
	c.sendEvent(cmdHistoryList, historyListResp{Entries: entries, Total: len(entries)})
	return nil
}

// handleHistoryGet 返回一份历史详情。
func (s *server) handleHistoryGet(c *wsConn, data json.RawMessage) error {
	var req historyIDReq
	if err := decodeReq(data, &req, "历史 ID"); err != nil {
		return err
	}
	rec, err := s.history().Load(req.ID)
	if err != nil {
		return s.historyError(err)
	}
	c.sendEvent(cmdHistoryGet, historyGetResp{Record: rec})
	return nil
}

// handleHistoryLoad 加载一份历史供复用。
//
// 服务端不保存「当前结果集」：加载只是把记录原样交回去，前端渲染完再由它
// 把选中的节点随测速请求发过来。服务端持有会话状态的话，重连、切页、重启
// 之后的行为就都不一样了。
func (s *server) handleHistoryLoad(c *wsConn, data json.RawMessage) error {
	var req historyLoadReq
	if err := decodeReq(data, &req, "历史 ID"); err != nil {
		return err
	}
	rec, err := s.history().Load(req.ID)
	if err != nil {
		return s.historyError(err)
	}

	diff, err := history.DiffParams(rec.Params, req.Current)
	if err != nil {
		// 参数对不上不影响加载本身：记录已经拿到了，差异提示降级为空。
		s.logger.Warn("参数快照比对失败", "id", rec.ID, "err", err)
		diff = nil
	}

	c.sendEvent(cmdHistoryLoad, historyLoadResp{
		Record:     rec,
		ParamDiff:  diff,
		AgeMinutes: s.history().AgeMinutes(rec.CreatedAt),
	})
	return nil
}

// handleHistoryClear 清空全部历史。
//
// 不走撤销窗口：撤销是给「点错了一个」准备的，而清空是用户明确要求把整个列表
// 抹掉，再留一个窗口只会让「到底清没清干净」变得不确定。前端负责二次确认。
func (s *server) handleHistoryClear(c *wsConn, _ json.RawMessage) error {
	removed, err := s.history().Clear()
	if err != nil {
		return s.historyError(err)
	}
	c.sendEvent(cmdHistoryClear, historyClearResp{Removed: removed})
	return nil
}

// handleHistoryDelete 软删除一份历史，返回可撤销窗口。
func (s *server) handleHistoryDelete(c *wsConn, data json.RawMessage) error {
	var req historyIDReq
	if err := decodeReq(data, &req, "历史 ID"); err != nil {
		return err
	}
	if err := s.history().Delete(req.ID); err != nil {
		return s.historyError(err)
	}
	c.sendEvent(cmdHistoryDelete, historyDeleteResp{
		ID:     req.ID,
		UndoMS: s.history().UndoWindow().Milliseconds(),
	})
	return nil
}

// handleHistoryUndo 撤销一次删除。
//
// 与删除对称：删除把记录挪进回收站并给一个撤销窗口，这条命令在窗口内把它挪
// 回来。窗口过了之后记录已经被清理，恢复会失败——这是预期行为，前端据此把
// 撤销入口收掉，不要让它一直挂在那里骗人。
func (s *server) handleHistoryUndo(c *wsConn, data json.RawMessage) error {
	var req historyIDReq
	if err := decodeReq(data, &req, "历史 ID"); err != nil {
		return err
	}
	if err := s.history().Restore(req.ID); err != nil {
		return s.historyError(err)
	}
	c.sendEvent(cmdHistoryUndo, historyIDResp{ID: req.ID})
	return nil
}

// handleHistoryTag 更新标签、备注与收藏。
func (s *server) handleHistoryTag(c *wsConn, data json.RawMessage) error {
	var req historyTagReq
	if err := decodeReq(data, &req, "历史 ID"); err != nil {
		return err
	}
	if err := s.history().UpdateMeta(req.ID, req.Tags, req.Note, req.Starred); err != nil {
		return s.historyError(err)
	}
	c.sendEvent(cmdHistoryTag, historyTagResp{ID: req.ID})
	return nil
}

// handleHistoryCompare 对比两份历史。
func (s *server) handleHistoryCompare(c *wsConn, data json.RawMessage) error {
	var req historyCompareReq
	if err := decodeReq(data, &req, "历史 ID"); err != nil {
		return err
	}
	if req.IDA == "" || req.IDB == "" {
		return fail(CodeInvalidParam, "对比需要两份历史的 ID")
	}
	a, err := s.history().Load(req.IDA)
	if err != nil {
		return s.historyError(err)
	}
	b, err := s.history().Load(req.IDB)
	if err != nil {
		return s.historyError(err)
	}
	c.sendEvent(cmdHistoryCompare, historyCompareResp{
		Diff: history.Diff(a, b, history.DefaultDiffOptions()),
	})
	return nil
}

// decodeReq 解析命令载荷。
//
// 载荷为空时不报错：多数命令的字段都可由服务端补默认值，用户漏传一个空
// 对象比收到「不是合法 JSON」更常见。
func decodeReq(data json.RawMessage, out any, what string) error {
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fail(CodeInvalidParam, what+"不是合法 JSON")
	}
	return nil
}

// historyError 把存储层的错误翻译成错误码。
func (s *server) historyError(err error) error {
	switch {
	case errors.Is(err, history.ErrNotFound):
		return fail(CodeNotFound, err.Error())
	default:
		return fail(CodeIO, err.Error())
	}
}

// history 返回历史存储。
func (s *server) history() *history.Store { return s.svc.History }

// --- 任务完成后存档 ---

// archiveScan 把一次扫描的结果存进历史。
func (s *server) archiveScan(params model.ScanParams, preset string, res model.TaskResult, duration float64) {
	s.archive(model.PhaseScan, params.IPVersion, preset, params, res, duration)
}

// archiveSpeed 把一次测速的结果存进历史。
func (s *server) archiveSpeed(params model.SpeedParams, preset string, res model.TaskResult, duration float64) {
	s.archive(model.PhaseSpeed, params.IPVersion, preset, params, res, duration)
}

// archive 写入一份历史记录。
//
// 只有任务**正常跑完**才会走到这里：中止与失败都不存档。中止的结果是残缺的，
// 存下来会让用户在历史列表里挑到一份根本没扫完的节点，而且看起来和完整结果
// 毫无区别。
func (s *server) archive(typ string, ipVersion int, preset string, params any, res model.TaskResult, duration float64) {
	store := s.history()
	if store == nil {
		return
	}
	if !s.cfg.Get().History.AutoSave {
		s.logger.Info("自动存档已关闭，本次结果不入历史", "type", typ)
		return
	}
	if ipVersion != 4 && ipVersion != 6 {
		s.logger.Warn("IP 版本异常，跳过存档", "type", typ, "ip_version", ipVersion)
		return
	}

	raw, err := json.Marshal(params)
	if err != nil {
		s.logger.Error("参数快照序列化失败，跳过存档", "type", typ, "err", err)
		return
	}

	summary := res.Summary
	// 漏斗与合格数只有编排层和测速流程知道，摘要自己算不出来。
	summary.Funnel = s.svc.Tasks.Snapshot().Funnel
	summary.Qualified = countMeasured(res.Records)

	rec, err := store.Save(history.HistoryRecord{
		Type:      typ,
		IPVersion: ipVersion,
		Duration:  duration,
		Preset:    preset,
		Params:    raw,
		Origins:   s.cfg.Get().Origins.Clone(),
		Summary:   summary,
		Results:   res.Records,
	})
	if err != nil {
		// 存档失败不能让任务看起来失败了：结果已经推给前端，用户还能导出。
		s.logger.Error("写入历史失败", "type", typ, "err", err)
		return
	}
	s.logger.Info("本次结果已存档", "id", rec.ID, "type", typ, "count", rec.Count)
}

// countMeasured 数出真正测到速度的条数。
//
// 0 表示「没测过」而不是「得了 0 分」，所以必须显式排除；扫描结果里全是 0，
// 自然得到 0。
func countMeasured(records []model.IPRecord) int {
	n := 0
	for _, r := range records {
		if r.SpeedMBps > 0 {
			n++
		}
	}
	return n
}

// taskRunner 把「跑任务 + 存档」两件事绑成一个任务体。
//
// 存档发生在任务体内部、仍在运行态时：任务体返回之后编排层就落定终态了，
// 那时再存会把一份已经结束的任务的存档算在下一个任务头上。
//
// preset 是本次使用的档位名，进来就先记到任务状态上。存档时要读它，而存档
// 发生在任务跑完之后——档位名不能等任务结束再传，那时编排层已经在准备下一
// 个任务的状态了。
func taskRunner(preset string, run func(task.Reporter) (int, error), done func(duration float64)) task.RunFunc {
	return func(rep task.Reporter) (task.Outcome, error) {
		if preset != "" {
			rep.SetPreset(preset)
		}
		started := time.Now()
		count, err := run(rep)
		if err == nil && rep.Context().Err() == nil && done != nil {
			done(time.Since(started).Seconds())
		}
		return task.Outcome{Count: count}, err
	}
}

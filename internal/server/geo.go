package server

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"cloudtrace/internal/geo"
)

// 地理信息相关的命令与事件名。
//
// 读取与更新共用同一个事件名：前端对两者的处理完全一样——拿全量状态替换
// 本地展示，不必区分是「刚读到的」还是「别人刚更新的」。
const (
	cmdGeoStatus = "geo/status"
	cmdGeoUpdate = "geo/update"
	eventGeo     = "geo"
)

// geoUpdateTimeout 是一次手动更新的上限。
//
// 库文件是几兆到十几兆，超时给得太紧会让慢网络下的更新无谓失败。
const geoUpdateTimeout = 5 * time.Minute

// geoHandlers 返回地理信息相关的命令表。
func (s *server) geoHandlers() map[string]commandHandler {
	return map[string]commandHandler{
		cmdGeoStatus: s.handleGeoStatus,
		cmdGeoUpdate: s.handleGeoUpdate,
	}
}

// geoStatusResp 是 geo 事件的数据体。
type geoStatusResp struct {
	// Status 是 ASN 库的当前状态。
	Status geo.Status `json:"status"`
	// Warning 是本机出口疑似代理时的提示；不需要提示时为空。
	Warning *geo.Warning `json:"warning,omitempty"`
	// QuickFilters 是内置的运营商快捷选项名。
	//
	// 与导出字段清单同一个理由：选项名与匹配词都由后端定义，前端各写一份
	// 必然漂移。
	QuickFilters []string `json:"quick_filters"`
}

// handleGeoStatus 下发地理信息状态。
func (s *server) handleGeoStatus(c *wsConn, _ json.RawMessage) error {
	c.sendEvent(eventGeo, s.geoStatus())
	return nil
}

// handleGeoUpdate 立即更新 ASN 库。
//
// 结果广播给所有连接：库换了之后每个面板上显示的状态与条数都变了，只回给
// 发起方会让别的面板一直停在旧数字上。
func (s *server) handleGeoUpdate(_ *wsConn, _ json.RawMessage) error {
	if s.svc.Geo == nil {
		return fail(CodeUnknown, "ASN 查询不可用")
	}

	ctx, cancel := context.WithTimeout(context.Background(), geoUpdateTimeout)
	defer cancel()

	err := s.svc.Geo.Update(ctx)
	// 失败也要广播：状态里的失败原因需要同步到所有面板。
	s.hub.broadcast(eventGeo, s.geoStatus())
	if err != nil {
		return s.geoError(err)
	}
	return nil
}

// geoStatus 组装地理信息状态。
func (s *server) geoStatus() geoStatusResp {
	resp := geoStatusResp{QuickFilters: geo.QuickFilterNames()}
	if s.svc.Geo == nil {
		return resp
	}
	resp.Status = s.svc.Geo.Status()
	if warning, ok := s.svc.Geo.ExitWarning(); ok {
		resp.Warning = &warning
	}
	return resp
}

// geoError 把地理层的错误翻成错误码。
func (s *server) geoError(err error) error {
	if errors.Is(err, geo.ErrSourceOff) {
		return fail(CodeInvalidParam, "ASN 查询已关闭，先在设置里选一个数据源")
	}
	return fail(CodeNetwork, err.Error())
}

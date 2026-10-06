package server

import (
	"encoding/json"

	"cloudtrace/assets"
)

// 内置来源相关的命令与事件名。
const (
	cmdSourceStatus = "source/status"
	eventSource     = "source"
)

// sourceStatusResp 是内置来源的现状。
//
// 段数由后端下发，不在前端硬编码：清单是随二进制固化的（`assets` 包里那份），
// 前端抄一份迟早会和它分叉——改了清单，界面还显示旧数字。这个数字以前确实是
// 硬编码在前端的 `ref(0)`，于是永远显示「共 0 段」。
type sourceStatusResp struct {
	OfficialV4 int `json:"official_v4"`
	OfficialV6 int `json:"official_v6"`
}

// sourceHandlers 返回来源相关的命令表。
func (s *server) sourceHandlers() map[string]commandHandler {
	return map[string]commandHandler{
		cmdSourceStatus: s.handleSourceStatus,
	}
}

// handleSourceStatus 回报内置来源的段数。
func (s *server) handleSourceStatus(c *wsConn, _ json.RawMessage) error {
	c.sendEvent(eventSource, sourceStatusResp{
		OfficialV4: len(assets.OfficialRanges(4)),
		OfficialV6: len(assets.OfficialRanges(6)),
	})
	return nil
}

package server

import (
	"context"
	"encoding/json"
	"time"

	"cloudtrace/internal/diag"
)

// 网络诊断相关的命令与事件名。
const (
	cmdDiagRun    = "diag/run"
	cmdDiagExport = "diag/export"
	eventDiag     = "diag"
)

// 诊断的总超时。
//
// 四项检查各自有超时，这里是兜底：网络半死不活时，前端的「诊断中」必须有个头。
// 取 20 秒——比四项超时之和略大，正常网络下永远用不到它。
const diagTimeout = 20 * time.Second

// diagHandlers 返回网络诊断相关的命令表。
func (s *server) diagHandlers() map[string]commandHandler {
	return map[string]commandHandler{
		cmdDiagRun:    s.handleDiagRun,
		cmdDiagExport: s.handleDiagExport,
	}
}

// handleDiagRun 跑一遍网络诊断并把结果回给调用方。
//
// 结果不广播：诊断是用户主动点出来的，把别人的面板也刷上一份没有意义。
func (s *server) handleDiagRun(c *wsConn, _ json.RawMessage) error {
	ctx, cancel := context.WithTimeout(context.Background(), diagTimeout)
	defer cancel()

	opts := diag.DefaultOptions()
	if s.diagOptions != nil {
		opts = s.diagOptions()
	}
	report := diag.Run(ctx, opts)
	s.logger.Info("网络诊断完成", "status", report.Status, "elapsed_ms", report.ElapsedMS)
	c.sendEvent(eventDiag, report)
	return nil
}

// diagExportResp 是诊断包的响应。
//
// 与导出结果同一套：只回下载标识与地址，内容走 `/api/download/{id}`。
// 诊断包里有日志与配置，直接塞进事件载荷会让它经过前端内存、也绕开
// 下载物的过期清理。
type diagExportResp struct {
	ID   string `json:"id"`
	URL  string `json:"url"`
	Name string `json:"name"`
}

// diagExportReq 是导出诊断包的请求。
//
// 报告由前端带回来，服务端不留上一份诊断结果：留了就要考虑「用户换了个网络
// 又跑了一次」这类同步问题，而这份数据本来就是前端手里那一份最准。
type diagExportReq struct {
	Report *diag.Report `json:"report"`
}

// handleDiagExport 组装诊断包并返回下载地址。
//
// 与导出结果一样**留内存不落盘**：诊断包里带着配置与日志，写进数据目录只会
// 多一份需要用户自己去清理的敏感文件。
func (s *server) handleDiagExport(c *wsConn, data json.RawMessage) error {
	var req diagExportReq
	// 载荷可选：没跑过诊断也能导出（包里会写「本次没有跑诊断」）。
	if len(data) > 0 {
		if err := decodeReq(data, &req, "诊断结果"); err != nil {
			return err
		}
	}

	dir, err := s.cfg.DataDir()
	if err != nil {
		// 拿不到数据目录不影响主体内容：只是带不上日志。
		s.logger.Warn("导出诊断包时拿不到数据目录", "err", err)
		dir = ""
	}

	now := time.Now()
	name := diag.BundleName(now)
	content := diag.Bundle(s.cfg.Get(), dir, s.svc.Version, req.Report, now)

	id, err := s.downloads.put(name, "text/plain; charset=utf-8", []byte(content))
	if err != nil {
		return fail(CodeIO, err.Error())
	}
	s.logger.Info("导出诊断包", "name", name, "bytes", len(content))
	c.sendEvent(cmdDiagExport, diagExportResp{ID: id, URL: downloadRoute + id, Name: name})
	return nil
}

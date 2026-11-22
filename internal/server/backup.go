package server

import (
	"errors"
	"io"
	"net/http"
	"time"

	"cloudtrace/internal/config"
)

// 配置备份与恢复的路由。
//
// 走 REST 而不是 WebSocket 命令：恢复要传一个文件，走 HTTP 请求体比把文件
// 内容塞进 WS 帧更直接；备份则直接复用浏览器的下载流程（Content-Disposition），
// 前端不需要知道文件落在哪，桌面版与浏览器版是同一套代码。
const (
	settingsBackupRoute  = "/api/settings/backup"
	settingsRestoreRoute = "/api/settings/restore"

	// restoreBodyLimit 是恢复请求体的上限。
	//
	// 配置本身只有几十 KB，留 1 MiB 是给用户手写注释、编辑器加缩进留余量；
	// 再大就不是配置文件了。
	restoreBodyLimit = 1 << 20
)

// handleSettingsBackup 输出一份可备份的配置。
func (s *server) handleSettingsBackup(w http.ResponseWriter, _ *http.Request) {
	data, err := config.ExportBytes(s.cfg.Get())
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeIO, "生成配置备份失败："+err.Error())
		return
	}
	name := "cloudtrace-settings-" + time.Now().Format("20060102-150405") + ".json"
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

// settingsRestoreResp 是恢复接口的响应。
type settingsRestoreResp struct {
	// RestartRequired 列出恢复后与启动时不同、要重启才生效的项。
	RestartRequired []string `json:"restart_required,omitempty"`
	// Ignored 列出文件里带了、但按规矩没有导入的凭据键。
	Ignored []string `json:"ignored,omitempty"`
}

// handleSettingsRestore 用上传的备份覆盖当前配置。
//
// 凭据（访问密码哈希、会话 TTL）不会被导入：备份文件会在机器之间传，
// 静默接受等于一次导入就把本机密码换成别人的。界面据 ignored 提示重设。
func (s *server) handleSettingsRestore(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, restoreBodyLimit))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, CodeInvalidParam, "配置文件过大，无法导入")
		return
	}
	cfg, ignored, err := config.ImportBytes(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidParam, err.Error())
		return
	}
	if _, err := s.cfg.Set(cfg); err != nil {
		var ve *config.ValidationError
		if errors.As(err, &ve) {
			writeError(w, http.StatusBadRequest, CodeInvalidParam, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, CodeIO, "保存配置失败："+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, settingsRestoreResp{
		RestartRequired: s.restartRequired(s.cfg.Get()),
		Ignored:         ignored,
	})
}

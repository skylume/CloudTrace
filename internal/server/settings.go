package server

import (
	"encoding/json"
	"errors"

	"cloudtrace/internal/config"
	"cloudtrace/internal/model"
)

// 设置相关的命令与事件名。
const (
	cmdSettingsGet    = "settings/get"
	cmdSettingsUpdate = "settings/update"
	cmdSettingsReset  = "settings/reset"

	// eventSettings 既是一次设置的读取结果，也是任何一次设置变更的广播。
	//
	// 读与变更共用同一个事件名：前端对两者的处理完全一样——拿全量配置替换
	// 本地状态，不做增量合并。拆成两个事件只会让前端多写一份同样的处理，
	// 而增量合并正是旧项目「两端互相覆盖」的根源。
	eventSettings = "settings"
)

// settingsHandlers 返回设置相关的命令表。
func (s *server) settingsHandlers() map[string]commandHandler {
	return map[string]commandHandler{
		cmdSettingsGet:    s.handleSettingsGet,
		cmdSettingsUpdate: s.handleSettingsUpdate,
		cmdSettingsReset:  s.handleSettingsReset,
	}
}

// settingsUpdateReq 是 settings/update 的载荷。
type settingsUpdateReq struct {
	// Patch 是嵌套的局部配置，例如 {"scan": {"workers": 100}}。
	Patch map[string]any `json:"patch"`
	// Origins 是本次改动涉及的参数来源标记（点号路径 → default/preset/user）。
	//
	// 必须由调用方带上：服务端无从判断一个值是人手改的还是档位填的，而自适应
	// 逻辑能不能覆盖它，完全取决于这个标记。
	Origins model.ParamOrigins `json:"origins"`
}

// settingsResetReq 是 settings/reset 的载荷。
type settingsResetReq struct {
	// Keys 是要恢复默认的配置项，支持整组（scan）与单个参数（scan.workers）。
	// 为空表示整份重置。
	Keys []string `json:"keys"`
}

// settingsPayload 是 settings 事件的数据体。
//
// 只包一层是为了带上「哪些改动要重启才生效」：配置本身已经含参数来源表，
// 前端拿到 values 整体替换本地状态即可。
type settingsPayload struct {
	Values config.Config `json:"values"`
	// RestartRequired 列出本次与启动时相比、改了要重启才生效的配置项。
	RestartRequired []string `json:"restart_required,omitempty"`
}

// handleSettingsGet 下发全量配置。
func (s *server) handleSettingsGet(c *wsConn, _ json.RawMessage) error {
	c.sendEvent(eventSettings, s.settingsPayload())
	return nil
}

// handleSettingsUpdate 局部更新配置。
//
// 成功时不单独回执：改动落盘后会由配置存储的回调统一广播，包括发起方在内的
// 所有连接都会收到同一份新配置。再单独回一次，发起方就会连收两份。
func (s *server) handleSettingsUpdate(_ *wsConn, data json.RawMessage) error {
	var req settingsUpdateReq
	if err := decodeReq(data, &req, "设置补丁"); err != nil {
		return err
	}
	if len(req.Patch) == 0 && len(req.Origins) == 0 {
		return fail(CodeInvalidParam, "设置补丁为空，没有可更新的内容")
	}
	if _, err := s.cfg.Patch(req.Patch, req.Origins); err != nil {
		return s.settingsError(err)
	}
	return nil
}

// handleSettingsReset 把指定项恢复为内置默认值。
func (s *server) handleSettingsReset(_ *wsConn, data json.RawMessage) error {
	var req settingsResetReq
	if err := decodeReq(data, &req, "重置项"); err != nil {
		return err
	}
	if _, err := s.cfg.ResetKeys(req.Keys); err != nil {
		return s.settingsError(err)
	}
	return nil
}

// settingsPayload 组装下发给前端的设置。
func (s *server) settingsPayload() settingsPayload {
	cur := s.cfg.Get()
	return settingsPayload{
		Values:          cur,
		RestartRequired: s.restartRequired(cur),
	}
}

// restartRequired 列出改了要重启才生效的配置项。
//
// 只报真正与启动时不同的项：用户没碰过端口却每次都看到「重启后生效」，这条
// 提示很快就会被无视，等真的改了端口时也不会有人看。
func (s *server) restartRequired(cur config.Config) []string {
	var out []string
	if s.listenPort > 0 && cur.Server.Port != s.listenPort {
		out = append(out, "server.port")
	}
	if s.startup.Server.Bind != cur.Server.Bind {
		out = append(out, "server.bind")
	}
	if s.startup.Data.Dir != cur.Data.Dir {
		out = append(out, "data.dir")
	}
	if s.startup.Data.Portable != cur.Data.Portable {
		out = append(out, "data.portable")
	}
	if s.startup.Advanced.LogLevel != cur.Advanced.LogLevel {
		out = append(out, "advanced.log_level")
	}
	return out
}

// settingsError 把配置层的错误翻成错误码。
//
// 校验失败走 E_INVALID_PARAM 并带上具体字段：前端据此定位并高亮，只说
// 「参数非法」等于什么都没说。
func (s *server) settingsError(err error) error {
	var ve *config.ValidationError
	if errors.As(err, &ve) {
		return fail(CodeInvalidParam, err.Error())
	}
	return fail(CodeIO, err.Error())
}

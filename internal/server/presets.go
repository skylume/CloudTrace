package server

import (
	"encoding/json"
	"errors"

	"cloudtrace/internal/config"
)

// 档位相关的命令与事件名。
const (
	cmdPresetsList       = "presets/list"
	cmdPresetsApply      = "presets/apply"
	cmdPresetsSave       = "presets/save"
	cmdPresetsDelete     = "presets/delete"
	cmdPresetsSetDefault = "presets/set_default"

	// eventPresets 既是档位列表的读取结果，也是任何一次档位变更的广播。
	//
	// 与 settings 同一套做法：读与变更共用一个事件名，前端对两者的处理完全
	// 一样（整体替换本地状态）。拆成两个事件只会让前端多写一份相同的处理。
	eventPresets = "presets"
)

// presetsHandlers 返回档位相关的命令表。
func (s *server) presetsHandlers() map[string]commandHandler {
	return map[string]commandHandler{
		cmdPresetsList:       s.handlePresetsList,
		cmdPresetsApply:      s.handlePresetsApply,
		cmdPresetsSave:       s.handlePresetsSave,
		cmdPresetsDelete:     s.handlePresetsDelete,
		cmdPresetsSetDefault: s.handlePresetsSetDefault,
	}
}

// presetsPayload 是 presets 事件的数据体。
type presetsPayload struct {
	Presets []config.Preset `json:"presets"`
	// Default 是启动档位的 id，前端用它决定首次进入时选中哪一档。
	Default string `json:"default"`
}

// noPresets 是档位库不可用时的错误。
//
// 档位库在启动时可能退化成不可用（数据目录解析失败），此时命令要给出明确
// 原因，而不是返回一个空列表让用户以为自己的档位都没了。
var errNoPresets = errors.New("档位库当前不可用")

// handlePresetsList 下发全部档位与启动档位。
func (s *server) handlePresetsList(c *wsConn, _ json.RawMessage) error {
	if s.svc.Presets == nil {
		return fail(CodeIO, errNoPresets.Error())
	}
	c.sendEvent(eventPresets, s.presetsPayload())
	return nil
}

// presetsApplyReq 是 presets/apply 的载荷。
type presetsApplyReq struct {
	ID string `json:"id"`
}

/**
 * handlePresetsApply 把档位的参数写进配置。
 *
 * 应用放在服务端而不是让前端自己拼配置补丁：来源标记的规则（内置档位填的
 * 值标 preset、我的档位载入的值标 user）只该有一处实现。前端再写一遍，两边
 * 迟早会分叉，而分叉的后果是自适应开始动用户明确保存过的参数。
 */
func (s *server) handlePresetsApply(_ *wsConn, data json.RawMessage) error {
	if s.svc.Presets == nil {
		return fail(CodeIO, errNoPresets.Error())
	}
	var req presetsApplyReq
	if err := decodeReq(data, &req, "档位标识"); err != nil {
		return err
	}
	patch, origins, err := s.svc.Presets.Apply(req.ID)
	if err != nil {
		return s.settingsError(err)
	}
	// 写配置成功后由配置存储的回调统一广播 settings，这里不必再发一次。
	if _, err := s.cfg.Patch(patch, origins); err != nil {
		return s.settingsError(err)
	}
	return nil
}

// handlePresetsSave 新建或覆盖保存一个自定义档位。
func (s *server) handlePresetsSave(_ *wsConn, data json.RawMessage) error {
	if s.svc.Presets == nil {
		return fail(CodeIO, errNoPresets.Error())
	}
	var preset config.Preset
	if err := decodeReq(data, &preset, "档位"); err != nil {
		return err
	}
	if err := s.svc.Presets.Save(preset); err != nil {
		return s.settingsError(err)
	}
	// 与设置同一套约定：变更由这里统一广播，发起方不再单独回执，否则它会
	// 连收两份。
	s.hub.broadcast(eventPresets, s.presetsPayload())
	return nil
}

// presetsDeleteReq 是 presets/delete 的载荷。
type presetsDeleteReq struct {
	ID string `json:"id"`
}

// presetsSetDefaultReq 是 presets/set_default 的载荷。
type presetsSetDefaultReq struct {
	ID string `json:"id"`
}

// handlePresetsDelete 删除一个自定义档位。内置档位会被拒绝。
func (s *server) handlePresetsDelete(_ *wsConn, data json.RawMessage) error {
	if s.svc.Presets == nil {
		return fail(CodeIO, errNoPresets.Error())
	}
	var req presetsDeleteReq
	if err := decodeReq(data, &req, "档位标识"); err != nil {
		return err
	}
	if err := s.svc.Presets.Delete(req.ID); err != nil {
		return s.settingsError(err)
	}
	s.hub.broadcast(eventPresets, s.presetsPayload())
	return nil
}

// handlePresetsSetDefault 把某个档位设为启动档位。
func (s *server) handlePresetsSetDefault(_ *wsConn, data json.RawMessage) error {
	if s.svc.Presets == nil {
		return fail(CodeIO, errNoPresets.Error())
	}
	var req presetsSetDefaultReq
	if err := decodeReq(data, &req, "档位标识"); err != nil {
		return err
	}
	if err := s.svc.Presets.SetDefault(req.ID); err != nil {
		return s.settingsError(err)
	}
	s.hub.broadcast(eventPresets, s.presetsPayload())
	return nil
}

// presetsPayload 组装当前档位快照。
func (s *server) presetsPayload() presetsPayload {
	return presetsPayload{
		Presets: s.svc.Presets.List(),
		Default: s.svc.Presets.Default(),
	}
}

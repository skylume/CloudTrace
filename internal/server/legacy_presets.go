package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cloudtrace/internal/config"
	"cloudtrace/internal/model"
)

// 档位下线后遗留的数据。
//
// 自定义档位是用户资产，不能因为界面不再提供档位就悄悄丢掉：扫描页上弹一条
// 横幅，点「设为默认参数」才写配置，点「导出」则把原样 JSON 交给用户保存。
// 两种动作都先备份 presets.json，原文件保持不动——用户改主意时还能翻回来。
const (
	cmdLegacyPresets       = "presets/legacy"
	cmdLegacyPresetsImport = "presets/legacy/import"

	eventLegacyPresets = "presets/legacy"

	legacyPresetsFile   = "presets.json"
	legacyPresetsBackup = "presets.json.bak"
	legacyPresetsMarker = ".presets-migrated.json"
)

// legacyPreset 是旧档位库里的一条自定义档位。
type legacyPreset struct {
	ID     string         `json:"id"`
	Name   string         `json:"name"`
	Values map[string]any `json:"values"`
}

// legacyPresetsFileShape 是旧 presets.json 的形状。
type legacyPresetsFileShape struct {
	Default string         `json:"default"`
	Presets []legacyPreset `json:"presets"`
}

// legacyPresetsStatus 是横幅需要的全部信息。
type legacyPresetsStatus struct {
	Found    bool           `json:"found"`
	Migrated bool           `json:"migrated"`
	Default  string         `json:"default,omitempty"`
	Presets  []legacyPreset `json:"presets,omitempty"`
	// Imported / Skipped 只在刚跑完一次导入时有值，供结果横幅显示。
	Imported []string `json:"imported,omitempty"`
	Skipped  []string `json:"skipped,omitempty"`
}

// legacyPresetsImportReq 是 presets/legacy/import 的载荷。
type legacyPresetsImportReq struct {
	// IDs 为空表示导入全部自定义档位。
	IDs []string `json:"ids"`
}

// legacyPresetsHandlers 返回旧档位相关的命令表。
func (s *server) legacyPresetsHandlers() map[string]commandHandler {
	return map[string]commandHandler{
		cmdLegacyPresets:       s.handleLegacyPresets,
		cmdLegacyPresetsImport: s.handleLegacyPresetsImport,
	}
}

// handleLegacyPresets 报告数据目录里是否还留着旧档位库。
func (s *server) handleLegacyPresets(c *wsConn, _ json.RawMessage) error {
	c.sendEvent(eventLegacyPresets, s.legacyPresetsStatus())
	return nil
}

// handleLegacyPresetsImport 把选中的档位值写成默认参数。
//
// 逐条写入而不是合并成一个大补丁：旧档位里可能有一两项参数新版已经删了，
// 合并会让整次导入失败，而其余几项明明可以搬。
func (s *server) handleLegacyPresetsImport(c *wsConn, data json.RawMessage) error {
	var req legacyPresetsImportReq
	if err := decodeReq(data, &req, "旧档位导入项"); err != nil {
		return err
	}

	file, err := s.readLegacyPresets()
	if err != nil {
		return fail(CodeInvalidParam, err.Error())
	}
	status := s.legacyPresetsStatus()

	wanted := map[string]bool{}
	for _, id := range req.IDs {
		wanted[id] = true
	}
	// 旧档位里的键要对着当前配置逐条核对：Patch 对不认识的键是静默忽略的，
	// 不核对就会出现「导入成功但值没变」，用户无从发现。
	shape := configShape(s.cfg.Get())
	for _, preset := range file.Presets {
		if len(wanted) > 0 && !wanted[preset.ID] {
			continue
		}
		values := map[string]any{}
		var unknown []string
		for path, value := range preset.Values {
			if !pathExists(shape, splitPath(path)) {
				unknown = append(unknown, path)
				continue
			}
			values[path] = value
		}
		if len(values) == 0 {
			status.Skipped = append(status.Skipped, preset.Name+"（"+strings.Join(unknown, "、")+"）")
			continue
		}
		origins := model.ParamOrigins{}
		for path := range values {
			origins.Set(path, model.OriginUser)
		}
		if _, err := s.cfg.Patch(nestPatch(values), origins); err != nil {
			status.Skipped = append(status.Skipped, preset.Name)
			continue
		}
		status.Imported = append(status.Imported, preset.Name)
	}

	if len(status.Imported) == 0 {
		c.sendEvent(eventLegacyPresets, status)
		return fail(CodeInvalidParam, "旧档位里的参数新版都不再支持，没有可导入的项")
	}
	if err := s.markLegacyPresetsImported(); err != nil {
		return fail(CodeIO, "写入迁移标记失败："+err.Error())
	}
	status.Migrated = true
	c.sendEvent(eventLegacyPresets, status)
	return nil
}

// legacyPresetsStatus 读旧档位库并组装横幅数据。
func (s *server) legacyPresetsStatus() legacyPresetsStatus {
	dir, err := s.svc.DataDir()
	if err != nil {
		return legacyPresetsStatus{}
	}
	if _, err := os.Stat(filepath.Join(dir, legacyPresetsMarker)); err == nil {
		return legacyPresetsStatus{Migrated: true}
	}
	file, err := s.readLegacyPresets()
	if err != nil || len(file.Presets) == 0 {
		return legacyPresetsStatus{}
	}
	return legacyPresetsStatus{Found: true, Default: file.Default, Presets: file.Presets}
}

// readLegacyPresets 读取旧档位库。文件不存在时返回空值。
func (s *server) readLegacyPresets() (legacyPresetsFileShape, error) {
	dir, err := s.svc.DataDir()
	if err != nil {
		return legacyPresetsFileShape{}, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, legacyPresetsFile))
	if errors.Is(err, fs.ErrNotExist) {
		return legacyPresetsFileShape{}, nil
	}
	if err != nil {
		return legacyPresetsFileShape{}, fmt.Errorf("读取旧档位库失败：%w", err)
	}
	var file legacyPresetsFileShape
	if err := json.Unmarshal(raw, &file); err != nil {
		return legacyPresetsFileShape{}, fmt.Errorf("旧档位库不是合法 JSON：%w", err)
	}
	return file, nil
}

// markLegacyPresetsImported 备份旧档位库并写下迁移标记。
//
// 备份在前：标记一旦写下横幅就不再出现，此时原文件必须已经有一份副本。
func (s *server) markLegacyPresetsImported() error {
	dir, err := s.svc.DataDir()
	if err != nil {
		return err
	}
	src := filepath.Join(dir, legacyPresetsFile)
	raw, err := os.ReadFile(src)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err == nil {
		if err := os.WriteFile(filepath.Join(dir, legacyPresetsBackup), raw, 0o600); err != nil {
			return err
		}
	}
	marker, err := json.Marshal(struct {
		At time.Time `json:"at"`
	}{At: time.Now()})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, legacyPresetsMarker), marker, 0o600)
}

// nestPatch 把点号路径的值表转成配置补丁的嵌套形状。
func nestPatch(values map[string]any) map[string]any {
	out := map[string]any{}
	for path, value := range values {
		node := out
		parts := splitPath(path)
		for i, part := range parts {
			if i == len(parts)-1 {
				node[part] = value
				break
			}
			next, ok := node[part].(map[string]any)
			if !ok {
				next = map[string]any{}
				node[part] = next
			}
			node = next
		}
	}
	return out
}

// configShape 把配置转成可按路径逐层查找的 JSON 对象。
func configShape(cfg config.Config) map[string]any {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	return out
}

// pathExists 报告配置里是否真有这条路径。
func pathExists(root map[string]any, parts []string) bool {
	var node any = root
	for _, part := range parts {
		obj, ok := node.(map[string]any)
		if !ok {
			return false
		}
		node, ok = obj[part]
		if !ok {
			return false
		}
	}
	return true
}

// splitPath 按点号切分配置路径。
func splitPath(path string) []string {
	var out []string
	start := 0
	for i := 0; i < len(path); i++ {
		if path[i] == '.' {
			out = append(out, path[start:i])
			start = i + 1
		}
	}
	return append(out, path[start:])
}

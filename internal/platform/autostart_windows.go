//go:build windows

package platform

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// set 在 Windows 上写当前用户的 Run 键。
//
// 用 CreateKey 而不是 OpenKey：OpenKey 在项不存在时直接失败，而「项不存在」
// 在这里是完全正常的起点——新建的用户配置、或者用户手动清过 Run 键。创建它
// 本来就是写自启项该做的事。
func (a Autostart) set(exePath string, enabled bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, a.KeyPath, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return fmt.Errorf("platform: 打开自启注册表项失败：%w", err)
	}
	defer func() { _ = key.Close() }()

	if !enabled {
		// 值本来就不存在时删除会报错，那不算失败——目标状态已经达成。
		if err := key.DeleteValue(a.Value); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return fmt.Errorf("platform: 关闭自启失败：%w", err)
		}
		return nil
	}

	exe, err := resolveExe(exePath)
	if err != nil {
		return err
	}
	if err := key.SetStringValue(a.Value, quote(exe)); err != nil {
		return fmt.Errorf("platform: 写入自启项失败：%w", err)
	}
	return nil
}

// enabled 读出当前值并与目标可执行文件比对。
func (a Autostart) enabled(exePath string) (bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, a.KeyPath, registry.QUERY_VALUE)
	if err != nil {
		// 项不存在等于没设过自启，不是错误。
		if errors.Is(err, registry.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("platform: 读取自启注册表项失败：%w", err)
	}
	defer func() { _ = key.Close() }()

	stored, _, err := key.GetStringValue(a.Value)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("platform: 读取自启项失败：%w", err)
	}

	exe, err := resolveExe(exePath)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(unquote(stored), exe), nil
}

// resolveExe 取出要写进注册表的可执行文件路径。
func resolveExe(exePath string) (string, error) {
	if strings.TrimSpace(exePath) != "" {
		return exePath, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("platform: 取当前可执行文件失败：%w", err)
	}
	return exe, nil
}

// quote 给路径加引号；已经有引号就不重复加。
func quote(path string) string {
	if strings.HasPrefix(path, `"`) {
		return path
	}
	return `"` + path + `"`
}

// unquote 去掉一层引号，便于与不带引号的路径比对。
func unquote(value string) string {
	return strings.Trim(value, `"`)
}

//go:build !windows

package platform

// set 在非 Windows 平台上不做任何事。
//
// 这个产品只发布 Windows 版本，其它平台上的构建是为了让开发与 CI 能跑起来。
// 悄悄成功而不是报错：调用方（启动时对齐自启项）不该因为一个辅助能力失败
// 就在控制台上刷错误——那会让真正的错误被淹掉。
func (a Autostart) set(string, bool) error { return nil }

// enabled 在非 Windows 平台上始终返回「没开」。
func (a Autostart) enabled(string) (bool, error) { return false, nil }

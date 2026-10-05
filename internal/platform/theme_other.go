//go:build !windows

package platform

// PrefersDarkTheme 在非 Windows 平台上按浅色处理。
//
// 这个产品只发布 Windows 版本，其它平台上的构建是为了让开发与 CI 能跑起来。
func PrefersDarkTheme() bool { return false }

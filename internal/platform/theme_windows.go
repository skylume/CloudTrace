//go:build windows

package platform

import "golang.org/x/sys/windows/registry"

// prefersDarkThemeKey 是 Windows 记录「应用使用浅色还是深色」的地方。
//
// 只读当前用户（HKCU）：这是每用户的显示偏好，机器级的那个值不是它的替代品。
const prefersDarkThemeKey = `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`

// PrefersDarkTheme 报告系统当前是否用深色主题。
//
// 桌面版要在建窗口时铺一层底色，而那时前端脚本还没跑起来，JS 层那套
// 「跟随系统」的解析帮不上忙，只能自己问系统。
//
// 读不到就按浅色处理：Win7 与更早的系统没有这个键，而那些机器上的
// 默认观感是浅色。窗口底色只在内容画出来之前露一下，猜错的代价是闪一下。
func PrefersDarkTheme() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, prefersDarkThemeKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer func() { _ = key.Close() }()

	value, _, err := key.GetIntegerValue("AppsUseLightTheme")
	if err != nil {
		return false
	}
	return value == 0
}

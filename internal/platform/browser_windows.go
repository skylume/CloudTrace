//go:build windows

package platform

import "os/exec"

// OpenBrowser 用系统默认浏览器打开 url。
//
// 失败只返回错误、不 panic：自动打开浏览器属于便利功能，
// 打不开时用户手动访问面板地址即可，不能因此阻断启动。
func OpenBrowser(url string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

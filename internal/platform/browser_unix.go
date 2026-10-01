//go:build !windows

package platform

import (
	"fmt"
	"os/exec"
	"runtime"
)

// OpenBrowser 用系统默认浏览器打开 url。
//
// 失败只返回错误、不 panic：自动打开浏览器属于便利功能，
// 打不开时用户手动访问面板地址即可，不能因此阻断启动。
func OpenBrowser(url string) error {
	cmd := "xdg-open"
	if runtime.GOOS == "darwin" {
		cmd = "open"
	}
	if _, err := exec.LookPath(cmd); err != nil {
		return fmt.Errorf("platform: 找不到 %s：%w", cmd, err)
	}
	return exec.Command(cmd, url).Start()
}

//go:build windows

package platform

import "golang.org/x/sys/windows"

// dpiAwarenessPerMonitorV2 是 DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 的值。
//
// 它是一个伪句柄 -4，而不是枚举值；x/sys 没有导出，这里按定义写出来。
const dpiAwarenessPerMonitorV2 = ^uintptr(3)

// SetDPIAware 让进程按显示器适配 DPI，高分屏上文字才不糊。
//
// 通常做法是在 exe 的清单文件里声明，而清单要靠 wails CLI 生成——本项目的构建
// 刻意不依赖那个 CLI：它会额外要求装一个工具，而构建流程越少依赖越不容易在别人
// 机器上失败。运行时调用效果相同。
//
// **必须在创建任何窗口之前调用**：窗口一旦建好，DPI 感知方式就定下来了。
//
// 失败不报错：老系统没有这个接口，退回系统默认缩放只是显示略糊，不该让程序
// 起不来。
func SetDPIAware() {
	proc := windows.NewLazySystemDLL("user32.dll").NewProc("SetProcessDpiAwarenessContext")
	if err := proc.Find(); err != nil {
		return
	}
	_, _, _ = proc.Call(dpiAwarenessPerMonitorV2)
}

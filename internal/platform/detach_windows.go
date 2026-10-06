//go:build windows

package platform

import (
	"os/exec"
	"syscall"
)

// detachProcess 让子进程在父进程退出后继续活着，并且不弹出控制台窗口。
//
// 三个标志各有用途：
//   - DETACHED_PROCESS：子进程不继承父进程的控制台。父进程退出时不会连它一起
//     带走，也不会在任务栏里多出一个黑窗口。
//   - CREATE_NEW_PROCESS_GROUP：让它自成一个进程组，父进程收到的 Ctrl+C 不会
//     顺带把它也停掉。
//   - CREATE_NO_WINDOW：桌面版用 -H=windowsgui 构建、本来就没有控制台，但面板版
//     有；重启面板版时子进程会短暂闪一个黑窗口，这个标志挡掉它。
const (
	detachedProcess       = 0x00000008
	createNewProcessGroup = 0x00000200
	createNoWindow        = 0x08000000
)

func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: detachedProcess | createNewProcessGroup | createNoWindow,
	}
}

// DetachProcess 让 cmd 起的进程脱离当前进程。
func DetachProcess(cmd *exec.Cmd) { detachProcess(cmd) }

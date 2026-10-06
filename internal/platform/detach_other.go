//go:build !windows

package platform

import (
	"os/exec"
	"syscall"
)

// detachProcess 让子进程脱离当前会话，父进程退出后它继续活着。
//
// 新开一个会话（Setsid）就够：没有控制终端，父进程退出时收到的挂断信号传不到它。
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// DetachProcess 让 cmd 起的进程脱离当前进程。
func DetachProcess(cmd *exec.Cmd) { detachProcess(cmd) }

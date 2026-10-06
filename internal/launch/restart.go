package launch

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"cloudtrace/internal/platform"
)

// RestartEnv 是「这一份是被重启拉起来的」的标记。
//
// 子进程靠它决定要不要在端口被占用时等一会儿——重启的那一瞬间，父进程可能
// 还没把监听套接字释放掉（见 App.Listen 里的说明）。
const RestartEnv = "CLOUDTRACE_RESTART"

// RestartSelf 拉起一份参数完全相同的新进程。
//
// 用「拉新进程再退出旧的」而不是原地重新初始化：配置、监听地址、日志级别这些
// 东西是在装配阶段读进去的，分散在配置存储、HTTP 服务器、日志器里，就地重来
// 要挨个重置，漏一个就变成「一半是旧的一半是新的」。换一个进程反而干净。
//
// 调用方负责在新进程起来之后退出自己。
func RestartSelf() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("找不到自身可执行文件：%w", err)
	}

	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Dir = workingDir()
	cmd.Env = append(os.Environ(), RestartEnv+"=1")
	// 子进程的日志走它自己新建的日志文件；标准输出接回原进程的，面板版从终端
	// 启动时新进程的输出仍然出现在同一个终端里。
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	platform.DetachProcess(cmd)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("拉起新进程失败：%w", err)
	}
	// 不 Wait：子进程是独立的一份，父进程马上就会退出。
	return nil
}

// workingDir 返回新进程的工作目录。
//
// 取当前进程的工作目录而不是 exe 所在目录：用户可能是在某个目录里用相对路径
// 启动的（`./cloudtrace.exe -data-dir ./data`），换到 exe 目录会让那些相对路径
// 指向别处。
func workingDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}

// restartBindWait 是重启后等待旧进程释放端口的时长。
//
// 5 秒远大于实际需要（旧进程退出是毫秒级的），留宽一点是为了覆盖磁盘慢、
// 杀进程被系统延迟这类情况。
const restartBindWait = 5 * time.Second

// Restarting 报告这一份是不是被重启拉起来的。
func Restarting() bool {
	return os.Getenv(RestartEnv) == "1"
}

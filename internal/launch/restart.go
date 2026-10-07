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

// 重启后等待旧进程让出资源的上限。
//
// 取 15 秒而不是「够用就好」：旧进程走的是**优雅关闭**，里面有一个 5 秒的
// HTTP Shutdown 超时（等正在跑的请求收尾）。实测一次重启里旧进程从收到请求到
// 真正退出约 5.7 秒——用 5 秒做上限就是擦边，一旦旧进程关得慢一点，新进程会
// 等到超时、把自己当成「第二个实例」然后退出，用户看到的还是「点了重启程序
// 就没了」。
//
// 代价只是「旧进程真的卡死时，新进程要多等十几秒才走兜底路径」，
// 而那条路径本来就不是正常情况。
const (
	restartBindWait = 15 * time.Second
	restartLockWait = 15 * time.Second
)

// AcquireSingleInstance 取单实例锁。
//
// 被重启拉起时**必须等一会儿再放弃**：重启的做法是先拉新进程、旧进程再退出，
// 那几十毫秒里锁还在旧进程手上。不等的话新进程会把自己当成「第二个实例」——
// 打开旧面板然后退出，而旧进程随后也退了，结果是用户点了重启之后什么都不剩。
//
// 等锁而不是「重启时跳过检查」：跳过的话新旧两份会短暂同时持有配置与历史，
// 而那正是单实例锁要防的事。
func AcquireSingleInstance() (*platform.Lock, bool, error) {
	if !Restarting() {
		return platform.AcquireLock(platform.SingleInstanceName)
	}

	deadline := time.Now().Add(restartLockWait)
	for {
		lock, first, err := platform.AcquireLock(platform.SingleInstanceName)
		if err != nil || first {
			return lock, first, err
		}
		if time.Now().After(deadline) {
			// 等超了还拿不到，说明确实还有另一个实例在跑。按「不是第一个」
			// 返回，让调用方走「打开已有面板」那条路——总比硬起第二份去写
			// 同一份配置安全。
			return lock, false, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Restarting 报告这一份是不是被重启拉起来的。
func Restarting() bool {
	return os.Getenv(RestartEnv) == "1"
}

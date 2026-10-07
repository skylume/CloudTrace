// Command panel 是面板版入口：纯 Go 单二进制，浏览器访问。
//
// 启动顺序：单实例检查 → 共用装配（数据目录 → 配置 → 服务 → 唯一 handler）
// → 监听端口 → 自动打开浏览器 → 等待退出信号。
//
// 本入口不含任何 Wails / GUI 依赖，因此二进制更小，且对系统无额外要求。
// 装配部分与桌面版共用 `internal/launch`——两个发行版行为必须完全一致，
// 而启动流程各写一遍是「一致」最先被破坏的地方。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"cloudtrace/internal/launch"
	"cloudtrace/internal/platform"
)

// version 由构建时通过 -ldflags 注入；未注入时为 dev。
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "启动失败："+err.Error())
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("cloudtrace-panel", flag.ExitOnError)
	var flags launch.Flags
	flags.Register(fs)
	_ = fs.Parse(os.Args[1:])

	if flags.ShowVersion {
		fmt.Println("cloudtrace " + version)
		return nil
	}

	// 单实例：第二个实例只把已有面板打开，不真的再起一份。
	//
	// 用系统级锁而不是靠「端口被占用」来判断：两个实例用不同端口时端口判断
	// 会漏，而它们会同时写同一份配置与历史——那是数据损坏，不是多开一个窗口。
	//
	// **必须在 Prepare 之前**：那个函数会生成访问 Token 并把配置写回磁盘。
	// 第二个实例在退出前重写一遍配置，正在运行的那份内存里的 Token 就与磁盘
	// 对不上了——控制台打印新的、登录校验用旧的，用户怎么输都进不去。
	lock, first, lockErr := launch.AcquireSingleInstance()
	if lockErr != nil {
		// 拿不到锁不算致命：宁可多开一个实例，也不要因为一个辅助能力让程序起不来。
		fmt.Fprintln(os.Stderr, "警告：单实例检查失败，继续启动："+lockErr.Error())
	} else {
		defer lock.Release()
		if !first {
			url := launch.PeekPanelURL(flags)
			fmt.Fprintln(os.Stderr, "已有实例在运行，打开它的面板后退出："+url)
			if !flags.NoBrowser {
				_ = platform.OpenBrowser(url)
			}
			return nil
		}
	}

	app, err := launch.Prepare(launch.Options{Flags: flags, Version: version})
	if err != nil {
		return err
	}

	// 开机自启以配置为准：用户可能把 exe 挪到了别处，注册表里那条旧记录
	// 既不会自己消失，也不会再起作用。
	if err := platform.SyncAutostart(app.Store.Get().Server.Autostart); err != nil {
		app.Logger.Warn("同步开机自启失败", "err", err)
	}

	// 重启：拉起新进程，然后走与收到退出信号同一条清理路径。
	//
	// 不复用「直接 os.Exit」：那样会跳过日志落盘与服务的优雅关闭，而重启恰恰
	// 是最不该丢数据的时候——用户刚改完配置。
	restarting := make(chan struct{}, 1)
	app.Services.Restart = func() error {
		if err := launch.RestartSelf(); err != nil {
			return err
		}
		select {
		case restarting <- struct{}{}:
		default:
		}
		return nil
	}

	listener, err := app.Listen()
	if err != nil {
		var inUse launch.ErrAddrInUse
		if errors.As(err, &inUse) {
			app.Logger.Warn(inUse.Error(), "url", inUse.URL)
			if !flags.NoBrowser {
				_ = platform.OpenBrowser(inUse.URL)
			}
			return nil
		}
		return err
	}

	httpServer := app.HTTPServer()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.Services.Startup(ctx); err != nil {
		return err
	}

	app.LogAccess()

	serveErr := make(chan error, 1)
	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	if !flags.NoBrowser && app.Store.Get().Server.OpenBrowser {
		go func() {
			if err := platform.OpenBrowser(app.URL); err != nil {
				app.Logger.Warn("自动打开浏览器失败，请手动访问", "url", app.URL, "err", err)
			}
		}()
	}

	select {
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("HTTP 服务异常退出：%w", err)
		}
		return nil
	case <-restarting:
		app.Logger.Info("正在重启…")
	case <-ctx.Done():
		app.Logger.Info("收到退出信号，正在关闭…")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	// 释放日志文件：Windows 上被占用的文件连删除都会失败。
	app.Close()
	if err := app.Services.Shutdown(shutdownCtx); err != nil {
		app.Logger.Warn("关闭服务时出错", "err", err)
	}
	return nil
}

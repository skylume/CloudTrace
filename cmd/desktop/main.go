//go:build desktop

// Command desktop 是桌面版入口：原生窗口 + 同一份面板。
//
// 与面板版共用 `internal/launch` 的全部装配，因此两者拿到的是**同一个 handler**。
// 这正是「浏览器也能访问同一面板」的实现方式：窗口和浏览器命中的是同一个东西，
// 前端不需要判断自己在不在 Wails 里，也就不会长出两套行为。
//
// 构建：`go build -tags desktop ./cmd/desktop`（本机可编译，产物由 CI 打包）。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"cloudtrace/internal/config"
	"cloudtrace/internal/launch"
	"cloudtrace/internal/platform"
)

// version 由构建时通过 -ldflags 注入；未注入时为 dev。
var version = "dev"

// windowStateFile 是窗口位置与尺寸的记忆文件，放在数据目录下。
const windowStateFile = "window.json"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "启动失败："+err.Error())
		os.Exit(1)
	}
}

func run() error {
	// 必须在建窗口之前：窗口一旦建好，DPI 感知方式就定下来了。
	platform.SetDPIAware()

	fs := flag.NewFlagSet("cloudtrace-desktop", flag.ExitOnError)
	var flags launch.Flags
	flags.Register(fs)
	_ = fs.Parse(os.Args[1:])

	if flags.ShowVersion {
		fmt.Println("cloudtrace " + version)
		return nil
	}

	app, err := launch.Prepare(launch.Options{Flags: flags, Version: version})
	if err != nil {
		return err
	}

	// 单实例：第二个实例不再开一个窗口，而是把已有面板打开。
	//
	// 桌面版尤其需要它——双击两次图标就出现两个窗口、两份后台任务，而它们
	// 写的是同一份配置与历史。
	lock, first, lockErr := platform.AcquireLock(platform.SingleInstanceName)
	if lockErr != nil {
		app.Logger.Warn("单实例检查失败，继续启动", "err", lockErr)
	} else {
		defer lock.Release()
		if !first {
			app.Logger.Info("已有实例在运行，打开它的面板后退出", "url", app.URL)
			_ = platform.OpenBrowser(app.URL)
			return nil
		}
	}

	if err := platform.SyncAutostart(app.Store.Get().Server.Autostart); err != nil {
		app.Logger.Warn("同步开机自启失败", "err", err)
	}

	// 本地监听：桌面窗口与浏览器命中同一个 handler。少这一步，桌面版就失去了
	// 「浏览器也能访问同一面板」的能力。
	listener, err := app.Listen()
	if err != nil {
		var inUse launch.ErrAddrInUse
		if errors.As(err, &inUse) {
			app.Logger.Warn(inUse.Error(), "url", inUse.URL)
			_ = platform.OpenBrowser(inUse.URL)
			return nil
		}
		return err
	}

	httpServer := app.HTTPServer()
	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			app.Logger.Warn("本地监听异常退出，浏览器将无法访问面板", "err", err)
		}
	}()
	app.Logger.Info("面板已启动", "url", app.URL, "data_dir", app.DataDir)

	statePath := filepath.Join(app.DataDir, windowStateFile)
	state := platform.LoadWindowState(statePath)

	return wails.Run(&options.App{
		Title:     "CloudTrace",
		Width:     state.Width,
		Height:    state.Height,
		MinWidth:  960,
		MinHeight: 640,
		// 窗口与浏览器共用同一个 handler，前端因此不必区分运行环境。
		AssetServer: &assetserver.Options{Handler: app.Handler},
		OnStartup: func(ctx context.Context) {
			// 后台任务（ASN 库更新等）在窗口起来之后启动：它们不阻塞界面，
			// 但也没必要抢在窗口之前。
			if err := app.Services.Startup(ctx); err != nil {
				app.Logger.Warn("启动后台任务失败", "err", err)
			}
		},
		OnDomReady: func(ctx context.Context) {
			restoreWindow(ctx, state)
		},
		// 关闭窗口就是退出：这个版本没有托盘（见 README 的说明），窗口关掉之后
		// 没有任何入口能把它叫回来，留在后台只会让用户以为程序没退干净。
		OnBeforeClose: func(ctx context.Context) (prevent bool) {
			saveWindow(ctx, statePath, state)
			return false
		},
		OnShutdown: func(ctx context.Context) {
			saveWindow(ctx, statePath, state)

			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = httpServer.Shutdown(shutdownCtx)
			if err := app.Services.Shutdown(shutdownCtx); err != nil {
				app.Logger.Warn("关闭服务时出错", "err", err)
			}
		},
		Windows: &windows.Options{
			// Win7 需要固定版本的 WebView2 运行时。只有确实带了那个目录才设置，
			// 否则 Wails 会去找一个不存在的路径——而系统自带的 WebView2 明明能用。
			WebviewBrowserPath: webviewBrowserPath(app.DataDir),
		},
	})
}

// restoreWindow 把窗口挪回上次的位置与尺寸。
//
// 放在 DomReady 而不是启动参数里：Wails 的启动参数只能给尺寸，位置要靠运行时
// 接口设置，而运行时接口在窗口就绪之前不可用。
func restoreWindow(ctx context.Context, state platform.WindowState) {
	if state.X != 0 || state.Y != 0 {
		wailsruntime.WindowSetPosition(ctx, state.X, state.Y)
	}
	if state.Maximised {
		wailsruntime.WindowMaximise(ctx)
	}
}

// saveWindow 记下当前窗口状态。
//
// 位置与尺寸分别取：最大化时 `WindowGetPosition` 给的是最大化之后的位置，
// 记下来会导致下次以「最大化尺寸 + 还原位置」这个奇怪的组合打开，因此最大化
// 时只记标记，尺寸位置沿用上一次。
func saveWindow(ctx context.Context, path string, state platform.WindowState) {
	if wailsruntime.WindowIsMaximised(ctx) {
		state.Maximised = true
	} else {
		x, y := wailsruntime.WindowGetPosition(ctx)
		w, h := wailsruntime.WindowGetSize(ctx)
		state = platform.WindowState{X: x, Y: y, Width: w, Height: h}
	}
	if err := platform.SaveWindowState(path, state); err != nil {
		// 记不住窗口位置不是用户要关心的问题，不值得打断退出流程。
		fmt.Fprintln(os.Stderr, "警告：保存窗口位置失败："+err.Error())
	}
}

// webviewBrowserPath 返回随包携带的 WebView2 运行时目录。
//
// 只有那个目录真的存在才返回：Win10+ 系统自带 WebView2，此时指定一个不存在的
// 路径会让窗口起不来，而用户看到的是「双击没反应」。
func webviewBrowserPath(dataDir string) string {
	exeDir, err := config.ExecutableDir()
	if err != nil {
		return ""
	}
	// 优先看 exe 同级（打包时的布局），再看数据目录（用户手动放进去的情况）。
	for _, dir := range []string{
		filepath.Join(exeDir, "webview2"),
		filepath.Join(dataDir, "webview2"),
	} {
		if info, statErr := os.Stat(dir); statErr == nil && info.IsDir() {
			return dir
		}
	}
	return ""
}

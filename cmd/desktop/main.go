//go:build desktop

// Command desktop 是桌面版入口：原生窗口 + 系统托盘 + 同一份面板。
//
// 与面板版共用 `internal/launch` 的全部装配，因此两者拿到的是**同一个 handler**。
// 这正是「浏览器也能访问同一面板」的实现方式：窗口和浏览器命中的是同一个东西，
// 前端不需要判断自己在不在原生壳里，也就不会长出两套行为。
//
// 构建：`go build -tags desktop ./cmd/desktop`（本机可编译，产物由 CI 打包）。
// 不需要 wails CLI，也不需要 C 编译器。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"cloudtrace/internal/config"
	"cloudtrace/internal/launch"
	"cloudtrace/internal/model"
	"cloudtrace/internal/platform"
	"cloudtrace/internal/task"
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

	return runUI(app, httpServer)
}

// runUI 起原生窗口与托盘，并阻塞到退出。
func runUI(app *launch.App, httpServer *http.Server) error {
	statePath := filepath.Join(app.DataDir, windowStateFile)
	state := platform.LoadWindowState(statePath)

	// quitting 标记「这次是真的要退」。
	//
	// 关闭窗口默认只是收进托盘，因此不能靠「窗口没了」来判断该不该退出——
	// 得有一个明确的意图。托盘菜单的「退出」会把它置上。
	var quitting atomic.Bool

	native := application.New(application.Options{
		Name:        "CloudTrace",
		Description: "Cloudflare IP 扫描与测速",
		Logger:      app.Logger,
		// 窗口与浏览器共用同一个 handler，前端因此不必区分运行环境。
		Assets: application.AssetOptions{Handler: app.Handler},
		// 只有明确要求退出时才真的退：否则关掉窗口会把后台任务一起带走。
		ShouldQuit: func() bool { return quitting.Load() },
		Windows: application.WindowsOptions{
			// Win7 需要固定版本的 WebView2 运行时。只有确实带了那个目录才设置，
			// 否则会去找一个不存在的路径——而系统自带的 WebView2 明明能用，
			// 用户看到的却是「双击没反应」。
			WebviewBrowserPath: webviewBrowserPath(app.DataDir),
		},
		OnShutdown: func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = httpServer.Shutdown(shutdownCtx)
			if err := app.Services.Shutdown(shutdownCtx); err != nil {
				app.Logger.Warn("关闭服务时出错", "err", err)
			}
		},
	})

	window := native.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "CloudTrace",
		Width:     state.Width,
		Height:    state.Height,
		MinWidth:  960,
		MinHeight: 640,
		X:         state.X,
		Y:         state.Y,
	})
	if state.Maximised {
		window.Maximise()
	}

	// 关闭窗口：收进托盘还是退出，由配置说了算。
	//
	// 用 RegisterHook 而不是 OnWindowEvent：hook 先同步执行，取消之后 Wails
	// 自己的关闭流程根本不会跑；监听者则是并行触发的，拦不住。
	window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		saveWindow(window, statePath)
		if !app.Store.Get().UI.CloseToTray {
			quitting.Store(true)
			return
		}
		event.Cancel()
		window.Hide()
	})

	setupTray(native, window, app, &quitting)

	if err := app.Services.Startup(native.Context()); err != nil {
		// 后台任务起不来不该让窗口开不出来：面板本身还能用。
		app.Logger.Warn("启动后台任务失败", "err", err)
	}

	return native.Run()
}

// setupTray 装上系统托盘。
//
// 托盘是这个壳最要紧的一块：扫描要跑几分钟，用户关掉窗口多半是想让它去后台
// 跑——而收进托盘之后，他需要一个地方能看进度、能停、能退。
func setupTray(
	native *application.App,
	window *application.WebviewWindow,
	app *launch.App,
	quitting *atomic.Bool,
) {
	labels := platform.TrayLabelsFor(app.Store.Get().UI.Lang)

	deps := platform.TrayDeps{
		Running: func() bool { return app.Services.Tasks.Running() },
		Show: func() {
			window.Show()
			window.Focus()
		},
		Stop: func() {
			if app.Services.Tasks.Abort() {
				app.Logger.Info("已从托盘中止当前任务")
			}
		},
		Quit: func() {
			quitting.Store(true)
			native.Quit()
		},
	}

	tray := native.SystemTray.New()
	tray.SetLabel(platform.TrayTooltip(labels, model.StatusIdle, 0))

	menu := application.NewMenu()
	items := map[string]*application.MenuItem{}
	for _, item := range platform.TrayMenu(deps, labels) {
		menuItem := menu.Add(item.Label)
		menuItem.OnClick(func(*application.Context) {
			if item.Clickable() {
				item.Run()
			}
		})
		items[item.ID] = menuItem
	}
	menu.AddSeparator()
	tray.SetMenu(menu)

	// 托盘提示跟随任务状态——这就是「托盘通知」的落地：后台跑着的时候把鼠标
	// 移到图标上就能看到进度，不必把窗口翻出来。
	//
	// 订阅本身**不受开关影响**：下面「停止」项的可点状态也靠它，而那属于菜单
	// 本身的功能，不是通知。受 `notify.tray` 控制的只有提示文案。
	//
	// 订阅失败不报错：那只是少了一个便利，任务本身照跑。
	trayNotify := app.Store.Get().Notify.Tray
	if _, err := app.Services.Bus.Subscribe(task.TopicState, func(payload any) {
		state, ok := payload.(model.TaskState)
		if !ok {
			return
		}
		if trayNotify {
			tray.SetLabel(platform.TrayTooltip(labels, state.Status, percentOf(state)))
		}

		// 「停止」只在有任务可停时才是可点的：一个点了没反应的菜单项，用户
		// 会以为是程序卡住了。
		if stop, ok := items[platform.TrayStop]; ok {
			stop.SetEnabled(state.Status == model.StatusRunning)
		}
	}); err != nil {
		app.Logger.Warn("订阅任务状态失败，托盘提示不会更新", "err", err)
	}
}

// percentOf 把任务状态换算成 0–100 的进度。
func percentOf(state model.TaskState) int {
	if state.Total <= 0 {
		return 0
	}
	percent := state.Done * 100 / state.Total
	if percent > 100 {
		return 100
	}
	return percent
}

// saveWindow 记下当前窗口状态。
//
// 位置与尺寸分别取：最大化时拿到的位置是最大化之后的位置，记下来会导致下次以
// 「最大化尺寸 + 还原位置」这个奇怪的组合打开，因此最大化时只记标记。
func saveWindow(window *application.WebviewWindow, path string) {
	state := platform.WindowState{Maximised: window.IsMaximised()}
	if !state.Maximised {
		state.X, state.Y = window.Position()
		state.Width, state.Height = window.Size()
	}
	if err := platform.SaveWindowState(path, state); err != nil {
		// 记不住窗口位置不是用户要关心的问题，不值得打断退出流程。
		fmt.Fprintln(os.Stderr, "警告：保存窗口位置失败："+err.Error())
	}
}

// webviewBrowserPath 返回随包携带的 WebView2 运行时目录。
//
// 只有那个目录真的存在才返回：Win10+ 系统自带 WebView2，此时指定一个不存在的
// 路径会让窗口起不来。
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

// Package launch 是两个入口（面板版与桌面版）共用的启动装配。
//
// 两个发行版必须行为完全一致，而「一致」最容易在启动流程上被破坏：数据目录怎么
// 解析、访问 Token 什么时候生成、端口被占用怎么办、日志级别从哪儿取——这些各写
// 一遍，迟早会有一边漏掉其中一步，而表现是「桌面版好好的，面板版少了个功能」。
//
// 装配收在这里之后，两个入口只剩一个区别：**怎么把同一个 handler 暴露出去**。
// 面板版交给 http.ListenAndServe，桌面版同时交给原生窗口与本地监听——后者正是
// 「浏览器也能访问同一面板」这条硬性要求的实现方式。
package launch

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"cloudtrace/internal/app"
	"cloudtrace/internal/config"
	"cloudtrace/internal/model"
	"cloudtrace/internal/server"
	"cloudtrace/internal/update"
)

// Flags 是两个入口共用的命令行参数。
//
// 共用一份定义而不是各写各的：桌面版少一个参数就少一个能力，而用户分不清
// 「这个参数在哪个版本里有」。
type Flags struct {
	DataDir     string
	Port        int
	Bind        string
	NoBrowser   bool
	LogLevel    string
	ShowVersion bool
}

// Register 把参数注册到一个 FlagSet 上。
func (f *Flags) Register(fs *flag.FlagSet) {
	fs.StringVar(&f.DataDir, "data-dir", "", "数据根目录（默认：便携模式为 exe 同级 data/）")
	fs.IntVar(&f.Port, "port", 0, "面板端口（0 = 使用配置值）")
	fs.StringVar(&f.Bind, "bind", "", "监听地址：127.0.0.1（默认）或 0.0.0.0")
	fs.BoolVar(&f.NoBrowser, "no-browser", false, "启动后不自动打开浏览器")
	fs.StringVar(&f.LogLevel, "log-level", "", "日志级别：debug/info/warn/error")
	fs.BoolVar(&f.ShowVersion, "version", false, "打印版本后退出")
}

// Options 是 Prepare 的输入。
type Options struct {
	Flags   Flags
	Version string
	// LogWriter 是日志输出目标；为空时用标准输出。
	LogWriter io.Writer
}

// App 是装配完成、但还没有开始对外服务的一套依赖。
type App struct {
	Store    *config.Store
	Services *app.Services
	// Handler 是唯一的前后端入口：静态资源 + REST + WebSocket。
	//
	// 面板版与桌面版拿到的是同一个东西，因此两版行为必然一致。
	Handler http.Handler
	// URL 是本机访问地址。绑定 0.0.0.0 时也用回环地址——浏览器打不开 0.0.0.0。
	URL string
	// DataDir 是最终生效的数据目录。
	DataDir string
	Logger  *slog.Logger
	// LogPath 是本次运行的日志文件路径；没有落盘时为空。
	LogPath string
	// logFile 是日志文件句柄，由 Close 释放。
	logFile *os.File
	// Version 是构建时注入的版本号。
	Version string
}

// Prepare 完成从数据目录到 handler 的全部装配。
//
// 不监听、不启动后台任务：那两件事两个入口的时机不同（桌面版要先有窗口，
// 面板版要先确认端口可用）。这里只保证「该准备的都准备好了」。
func Prepare(opts Options) (*App, error) {
	exeDir, err := config.ExecutableDir()
	if err != nil {
		return nil, fmt.Errorf("无法定位程序目录：%w", err)
	}

	rootDir, err := resolveRootDir(opts.Flags.DataDir, exeDir)
	if err != nil {
		return nil, err
	}
	if err := config.EnsureDataDirs(rootDir); err != nil {
		return nil, fmt.Errorf("无法创建数据目录 %s：%w", rootDir, err)
	}

	store, err := config.OpenStore(config.ConfigPath(rootDir), exeDir)
	if err != nil {
		return nil, fmt.Errorf("加载配置失败：%w", err)
	}

	// 日志同时进控制台与文件。
	//
	// 控制台是给「现在正看着的人」的，文件是给「事后要查的人」的——排查一个
	// 跑了几分钟的任务时，控制台早就被刷掉了。
	logWriters := []io.Writer{opts.LogWriter}
	if opts.LogWriter == nil {
		logWriters[0] = os.Stdout
	}

	logPath := ""
	var logFile *os.File
	if keep := store.Get().Advanced.LogKeepDays; keep > 0 {
		file, ferr := openLogFile(rootDir, time.Now())
		if ferr != nil {
			// 写不了文件不该让程序起不来：日志只是排查用的辅助。
			fmt.Fprintf(os.Stderr, "警告：打开日志文件失败：%v\n", ferr)
		} else {
			// 进程存活期间一直开着：日志是逐行写的，关掉再开没有意义。
			// 退出时由 Close 释放。
			logWriters = append(logWriters, file)
			logPath = file.Name()
			logFile = file
		}
	}

	logger := newLogger(opts.Flags.LogLevel, store.Get().Advanced.LogLevel, io.MultiWriter(logWriters...))
	if logPath != "" {
		logger.Info("日志写入文件", "path", logPath)
		if removed, perr := pruneLogs(rootDir, store.Get().Advanced.LogKeepDays, time.Now()); perr != nil {
			logger.Warn("清理过期日志失败", "err", perr)
		} else if removed > 0 {
			logger.Info("已清理过期日志", "removed", removed, "keep_days", store.Get().Advanced.LogKeepDays)
		}
	}

	// 更新检查放在后台：它要联网，而联网可能很慢甚至不通——那不该让程序
	// 晚几秒才起来。开关关掉时一次请求都不发。
	//
	// 必须等 logger 建好之后再起：结果要写进那份带文件输出的日志，
	// 而不是默认 logger。
	if store.Get().Advanced.CheckUpdate {
		go checkUpdate(logger, opts.Version)
	}
	for _, w := range store.Warnings() {
		logger.Warn(w)
	}

	// 首次运行自动生成访问 Token（局域网访问时使用）。
	cfg := store.Get()
	if generated, gerr := (&cfg.Server).EnsureToken(); gerr != nil {
		logger.Warn("生成访问 Token 失败，局域网访问将不可用", "err", gerr)
	} else if generated {
		if _, serr := store.Set(cfg); serr != nil {
			return nil, fmt.Errorf("保存配置失败：%w", serr)
		}
	}

	if patch, origins := flagOverrides(opts.Flags, rootDir); len(patch) > 0 {
		if _, perr := store.Patch(patch, origins); perr != nil {
			return nil, fmt.Errorf("应用命令行参数失败：%w", perr)
		}
	}
	cfg = store.Get()

	// 配置、历史、缓存、日志统一落在同一个数据目录下。必须走 store.DataDir()
	// 而不是直接用 rootDir：否则 data.dir 一旦被用户改过，就会出现「配置文件在
	// 这里、历史记录写去了别处」。
	dataDir, err := store.DataDir()
	if err != nil {
		return nil, fmt.Errorf("解析数据目录失败：%w", err)
	}
	if err := config.EnsureDataDirs(dataDir); err != nil {
		return nil, fmt.Errorf("无法创建数据目录 %s：%w", dataDir, err)
	}

	svc, err := app.New(store, opts.Version, logger)
	if err != nil {
		return nil, err
	}
	handler, err := server.New(store, svc, cfg.Server.Port)
	if err != nil {
		return nil, err
	}

	return &App{
		Store:    store,
		Services: svc,
		Handler:  handler,
		URL:      PanelURL(cfg.Server.Bind, cfg.Server.Port),
		DataDir:  dataDir,
		Logger:   logger,
		LogPath:  logPath,
		logFile:  logFile,
		Version:  opts.Version,
	}, nil
}

// Listen 在配置的地址上监听。
//
// 端口被占用时返回 ErrAddrInUse：那几乎总是「已经有一个实例在跑」，调用方应当
// 打开已有面板然后退出，而不是抛一堆系统调用栈让用户困惑。
func (a *App) Listen() (net.Listener, error) {
	cfg := a.Store.Get()
	addr := net.JoinHostPort(cfg.Server.Bind, strconv.Itoa(cfg.Server.Port))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		if IsAddrInUse(err) {
			return nil, ErrAddrInUse{Addr: addr, URL: a.URL}
		}
		return nil, fmt.Errorf("监听 %s 失败：%w", addr, err)
	}
	return listener, nil
}

// HTTPServer 组装一个带合理超时的 http.Server。
//
// ReadHeaderTimeout 必须设：不设的话一个只连不发数据的客户端就能一直占着连接，
// 而这类客户端在公网上到处都是。
func (a *App) HTTPServer() *http.Server {
	return &http.Server{
		Handler:           a.Handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

// LogAccess 输出「面板起在哪儿、怎么进去」。
//
// 访问 Token 必须把值打出来：登录页让用户「输入启动时控制台打印的访问 Token」，
// 那就得真的打印。只写一句「已生成访问 Token」等于什么也没给——用户只能去翻
// 配置文件，而配置文件在哪个目录取决于数据目录，那正是他不知道的东西。
//
// 两个入口共用一份，不在各自的 main 里各写一遍：只绑回环时该不该提示、
// 局域网时该说什么，两版必须一致，而各写一遍正是一致性最先被破坏的地方。
func (a *App) LogAccess() {
	cfg := a.Store.Get()
	a.Logger.Info("面板已启动", "url", a.URL, "data_dir", a.DataDir)
	if cfg.Server.Token != "" {
		a.Logger.Info("访问 Token（局域网访问面板时需要）", "token", cfg.Server.Token)
	}
	if cfg.Server.Bind != "127.0.0.1" {
		a.Logger.Warn("面板已开放局域网访问，同一网络下的设备都能打开它")
	}
}

// ErrAddrInUse 表示端口已经被占用。
//
// 单独一个类型而不是一句字符串：调用方要据此决定「打开已有面板并退出」，
// 而字符串比对会随系统语言变。
type ErrAddrInUse struct {
	Addr string
	URL  string
}

func (e ErrAddrInUse) Error() string {
	return "端口 " + e.Addr + " 已被占用，可能已有实例在运行"
}

// IsAddrInUse 判断监听失败是否因为端口被占用。
func IsAddrInUse(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	// 不同 Windows 版本的措辞不一样，退回字符串判断；只有确实拿不到
	// 错误码时才会走到这里。
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "address already in use") ||
		strings.Contains(msg, "only one usage of each socket address")
}

// resolveRootDir 决定数据根目录。
//
// 优先级：命令行 > 便携模式（exe 同级 data/）> 标准模式（用户配置目录）。
func resolveRootDir(flagValue, exeDir string) (string, error) {
	if flagValue != "" {
		return filepath.Abs(flagValue)
	}
	return config.ResolveDataDir(config.Default(), exeDir)
}

// flagOverrides 把显式传入的命令行参数转成配置补丁。
//
// 这些值来自用户本次显式指定，因此标记为 user，自适应逻辑不得改动。
// dataDir 为空表示用户没传 -data-dir，此时不动 data.dir。
func flagOverrides(f Flags, dataDir string) (map[string]any, model.ParamOrigins) {
	patch := map[string]any{}
	origins := model.ParamOrigins{}
	serverPatch := map[string]any{}

	if f.Port > 0 {
		serverPatch["port"] = f.Port
		origins.Set("server.port", model.OriginUser)
	}
	if f.Bind != "" {
		serverPatch["bind"] = f.Bind
		origins.Set("server.bind", model.OriginUser)
	}
	if len(serverPatch) > 0 {
		patch["server"] = serverPatch
	}

	// -data-dir 必须落到 data.dir：否则配置文件写在指定目录，而历史、缓存、
	// 日志会按便携模式写到 exe 同级，两处不一致。
	if f.DataDir != "" {
		patch["data"] = map[string]any{"dir": dataDir}
		origins.Set("data.dir", model.OriginUser)
	}
	return patch, origins
}

func newLogger(flagLevel, cfgLevel string, w io.Writer) *slog.Logger {
	if w == nil {
		w = os.Stdout
	}
	level := cfgLevel
	if flagLevel != "" {
		level = flagLevel
	}
	var lv slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: lv}))
}

// PanelURL 拼出面板地址。
//
// 监听 0.0.0.0 时浏览器打不开 0.0.0.0，因此本机访问一律用回环地址。
func PanelURL(bind string, port int) string {
	host := bind
	if host == "0.0.0.0" || host == "" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port))
}

// checkUpdate 在后台问一次有没有新版本，并把结果写进日志。
//
// 结果只写日志：日志面板在界面上就能看到，为此再加一套通知不划算。查不动
// （网络不通、当前是开发版）都只记 debug——用户没主动问，不该被这些打扰。
//
// logger 必须传进来，不能用包级的 slog：这个包从不调 slog.SetDefault，包级
// 调用会走到默认 logger——Debug 被它的级别挡掉、Info 只进 stderr 不进日志文件，
// 等于这个功能的结果谁也看不到。
func checkUpdate(logger *slog.Logger, current string) {
	ctx, cancel := context.WithTimeout(context.Background(), updateTimeout)
	defer cancel()

	res, err := update.Check(ctx, current, nil)
	switch {
	case err != nil:
		logger.Debug("检查更新失败", "err", err)
	case res.Skipped != "":
		logger.Debug("跳过更新检查", "reason", res.Skipped)
	case res.HasUpdate:
		logger.Info("有新版本可用", "current", res.Current, "latest", res.Latest, "url", res.URL)
	default:
		logger.Debug("已是最新版本", "version", res.Current)
	}
}

// updateTimeout 是一次更新检查的上限。
const updateTimeout = 15 * time.Second

// Close 释放启动时占用的资源。
//
// 目前只有日志文件。它在进程存活期间一直开着，而 Windows 上被占用的文件连
// 删除都会失败——数据目录因此删不掉，用户会以为程序还在跑。
//
// 由入口在退出路径上调用一次；不保证并发安全，也不需要。
func (a *App) Close() {
	if a.logFile == nil {
		return
	}
	_ = a.logFile.Close()
	a.logFile = nil
}

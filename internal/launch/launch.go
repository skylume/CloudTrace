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

	logger := newLogger(opts.Flags.LogLevel, store.Get().Advanced.LogLevel, opts.LogWriter)
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
		logger.Info("已生成访问 Token")
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

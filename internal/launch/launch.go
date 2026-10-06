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

	// 访问密码由用户自己在设置页设，这里不再自动生成。
	//
	// 自动生成的那串 64 位十六进制，用户只能从控制台抄——而桌面版用
	// -H=windowsgui 构建，压根没有控制台。默认只绑回环时本机访问本来就不需要
	// 密码，自动生成一个只会在用户想开局域网时逼他去找一串他永远记不住的东西。
	//
	// 绑定到局域网却还没设密码会在配置校验那一关被拒（见 config.Validate），
	// 设置页也会在开这个开关之前先把密码要出来。
	cfg := store.Get()

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

	// 被重启拉起时要等一会儿再放弃。
	//
	// 重启的流程是「先拉新进程、旧进程再退出」，中间那几十毫秒里监听套接字
	// 还在旧进程手上。不等的话新进程会判定「端口被占用」，然后走成「打开已有
	// 面板后退出」——用户点了重启，看到的是窗口闪一下又回到原来那个。
	deadline := time.Now().Add(restartBindWait)
	for {
		listener, err := net.Listen("tcp", addr)
		if err == nil {
			return listener, nil
		}
		if !IsAddrInUse(err) {
			return nil, fmt.Errorf("监听 %s 失败：%w", addr, err)
		}
		if !Restarting() || time.Now().After(deadline) {
			return nil, ErrAddrInUse{Addr: addr, URL: a.URL}
		}
		time.Sleep(100 * time.Millisecond)
	}
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

// PeekPanelURL 只读地算出面板地址。
//
// 给「已经有实例在跑」那条路用：第二个实例要把已有面板打开然后退出。
//
// 刻意不经过 Prepare——那个函数会生成访问 Token、把命令行参数写进配置、建日志
// 文件，每一件都只有真正要启动的那一份才该做。第二个实例在退出前把配置重写
// 一遍，正在运行的那份内存里的 Token 就和磁盘上对不上了：控制台打印的是新的，
// 而登录校验用的还是旧的，用户怎么输都进不去。
//
// 读不到配置就退回默认值：这条路只为了打开一个网址，不值得因为读不到配置
// 而让用户什么也看不到。
func PeekPanelURL(flags Flags) string {
	bind := flags.Bind
	port := flags.Port

	exeDir, err := config.ExecutableDir()
	if err == nil {
		if rootDir, rerr := resolveRootDir(flags.DataDir, exeDir); rerr == nil {
			// LoadFile 在文件不存在时返回默认值，不会创建任何东西。
			if cfg, lerr := config.LoadFile(config.ConfigPath(rootDir)); lerr == nil {
				if bind == "" {
					bind = cfg.Server.Bind
				}
				if port == 0 {
					port = cfg.Server.Port
				}
			}
		}
	}

	d := config.Default()
	if bind == "" {
		bind = d.Server.Bind
	}
	if port == 0 {
		port = d.Server.Port
	}
	return PanelURL(bind, port)
}

// LogAccess 输出「面板起在哪儿、怎么进去」。
//
// 本机地址与局域网地址都要打出来：用户开了局域网访问之后，最想知道的就是
// 「手机该输什么地址」，而那个地址取决于本机网卡，他自己查不出来。
//
// 密码只在**还是明文**的时候打出来。用户自己设的密码落盘是加盐哈希，打一串
// 哈希给他看毫无意义（他也输入不进去）；而升级前那版自动生成的明文 Token
// 仍然要打——那批用户没有别的途径知道它。
//
// 两个入口共用一份，不在各自的 main 里各写一遍：只绑回环时该不该提示、
// 局域网时该说什么，两版必须一致，而各写一遍正是一致性最先被破坏的地方。
func (a *App) LogAccess() {
	cfg := a.Store.Get()
	a.Logger.Info("面板已启动", "url", a.URL, "data_dir", a.DataDir)

	for _, url := range LANURLs(cfg.Server.Bind, cfg.Server.Port) {
		a.Logger.Info("局域网访问地址（同一网络下的设备用这个）", "url", url)
	}

	switch {
	case cfg.Server.Token == "":
		// 只绑回环时这是正常状态，不必提示。
		if cfg.Server.Bind != "127.0.0.1" && cfg.Server.Bind != "" {
			a.Logger.Warn("面板绑定了非回环地址却还没有访问密码，请在设置页设置后再重启")
		}
	case config.IsHashedPassword(cfg.Server.Token):
		a.Logger.Info("已设置访问密码（局域网访问时需要，忘记可在设置页重设）")
	default:
		// 升级前那版自动生成的明文 Token。
		a.Logger.Info("访问 Token（局域网访问面板时需要）", "token", cfg.Server.Token)
	}

	if cfg.Server.Bind != "127.0.0.1" && cfg.Server.Bind != "" {
		a.Logger.Warn("面板已开放局域网访问，同一网络下的设备都能打开它")
	}
}

// LANURLs 列出局域网内其他设备可用的访问地址；只绑回环时为空。
//
// 与 internal/server 里那份是同一套规则。放在这里是因为启动日志要用它，而
// server 包反过来依赖 launch（PanelURL），不能互相引。
func LANURLs(bind string, port int) []string {
	if bind == "" || bind == "127.0.0.1" || bind == "localhost" || bind == "::1" {
		return nil
	}
	if bind != "0.0.0.0" && bind != "::" {
		return []string{"http://" + net.JoinHostPort(bind, strconv.Itoa(port))}
	}

	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	out := make([]string, 0, 4)
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		ip4 := ipNet.IP.To4()
		if ip4 == nil || ip4.IsLinkLocalUnicast() {
			continue
		}
		out = append(out, "http://"+net.JoinHostPort(ip4.String(), strconv.Itoa(port)))
	}
	return out
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

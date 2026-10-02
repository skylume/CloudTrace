// Command panel 是面板版入口：纯 Go 单二进制，浏览器访问。
//
// 启动顺序：定位数据目录 → 加载配置 → 装配服务 → 构造唯一 handler
// → 监听端口 → 自动打开浏览器 → 等待退出信号。
//
// 本入口不含任何 Wails / GUI 依赖，因此二进制更小，且对系统无额外要求。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"cloudtrace/internal/app"
	"cloudtrace/internal/config"
	"cloudtrace/internal/model"
	"cloudtrace/internal/platform"
	"cloudtrace/internal/server"
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
	var (
		dataDirFlag = flag.String("data-dir", "", "数据根目录（默认：便携模式为 exe 同级 data/）")
		portFlag    = flag.Int("port", 0, "面板端口（0 = 使用配置值）")
		bindFlag    = flag.String("bind", "", "监听地址：127.0.0.1（默认）或 0.0.0.0")
		noBrowser   = flag.Bool("no-browser", false, "启动后不自动打开浏览器")
		logLevel    = flag.String("log-level", "", "日志级别：debug/info/warn/error")
		showVersion = flag.Bool("version", false, "打印版本后退出")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("cloudtrace " + version)
		return nil
	}

	exeDir, err := config.ExecutableDir()
	if err != nil {
		return fmt.Errorf("无法定位程序目录：%w", err)
	}

	// ---- 1. 定位数据目录 ----
	rootDir, err := resolveRootDir(*dataDirFlag, exeDir)
	if err != nil {
		return err
	}
	if err := config.EnsureDataDirs(rootDir); err != nil {
		return fmt.Errorf("无法创建数据目录 %s：%w", rootDir, err)
	}

	// ---- 2. 加载配置 ----
	store, err := config.OpenStore(config.ConfigPath(rootDir), exeDir)
	if err != nil {
		return fmt.Errorf("加载配置失败：%w", err)
	}

	logger := newLogger(*logLevel, store.Get().Advanced.LogLevel)
	for _, w := range store.Warnings() {
		logger.Warn(w)
	}

	// 首次运行自动生成访问 Token（局域网访问时使用）。
	cfg := store.Get()
	if generated, gerr := (&cfg.Server).EnsureToken(); gerr != nil {
		logger.Warn("生成访问 Token 失败，局域网访问将不可用", "err", gerr)
	} else if generated {
		if _, serr := store.Set(cfg); serr != nil {
			return fmt.Errorf("保存配置失败：%w", serr)
		}
		logger.Info("已生成访问 Token")
	}

	// 命令行覆盖项：只覆盖显式传入的部分。
	// 只有用户显式传了 -data-dir 才固定 data.dir；否则保持为空，
	// 让便携 / 标准模式在每次启动时重新解析，避免把绝对路径写死进配置。
	explicitDataDir := ""
	if *dataDirFlag != "" {
		explicitDataDir = rootDir
	}
	if patch, origins := flagOverrides(*portFlag, *bindFlag, explicitDataDir); len(patch) > 0 {
		if _, perr := store.Patch(patch, origins); perr != nil {
			return fmt.Errorf("应用命令行参数失败：%w", perr)
		}
	}

	cfg = store.Get()

	// 配置、历史、缓存、日志统一落在同一个数据目录下。
	// 必须走 store.DataDir() 而不是直接用 rootDir：否则 data.dir 一旦被
	// 用户改过，就会出现「配置文件在这里、历史记录写去了别处」。
	dataDir, err := store.DataDir()
	if err != nil {
		return fmt.Errorf("解析数据目录失败：%w", err)
	}
	if err := config.EnsureDataDirs(dataDir); err != nil {
		return fmt.Errorf("无法创建数据目录 %s：%w", dataDir, err)
	}

	// ---- 3. 装配 ----
	svc, err := app.New(store, version, logger)
	if err != nil {
		return err
	}
	handler, err := server.New(store, svc, cfg.Server.Port)
	if err != nil {
		return err
	}

	addr := net.JoinHostPort(cfg.Server.Bind, fmt.Sprint(cfg.Server.Port))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		// 端口被占用通常意味着已经有一个实例在跑：直接打开已有面板，
		// 而不是抛一堆栈让用户困惑。
		if isAddrInUse(err) {
			url := panelURL(cfg.Server.Bind, cfg.Server.Port)
			logger.Warn("端口已被占用，可能已有实例在运行", "addr", addr, "url", url)
			if !*noBrowser && cfg.Server.OpenBrowser {
				_ = platform.OpenBrowser(url)
			}
			return nil
		}
		return fmt.Errorf("监听 %s 失败：%w", addr, err)
	}

	httpServer := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := svc.Startup(ctx); err != nil {
		return err
	}

	url := panelURL(cfg.Server.Bind, cfg.Server.Port)
	logger.Info("面板已启动", "url", url, "data_dir", dataDir)
	if cfg.Server.Bind != "127.0.0.1" {
		logger.Warn("面板已开放局域网访问，登录需要访问 Token", "token", cfg.Server.Token)
	}

	serveErr := make(chan error, 1)
	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	if !*noBrowser && cfg.Server.OpenBrowser {
		go func() {
			if err := platform.OpenBrowser(url); err != nil {
				logger.Warn("自动打开浏览器失败，请手动访问", "url", url, "err", err)
			}
		}()
	}

	select {
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("HTTP 服务异常退出：%w", err)
		}
		return nil
	case <-ctx.Done():
		logger.Info("收到退出信号，正在关闭…")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	if err := svc.Shutdown(shutdownCtx); err != nil {
		logger.Warn("关闭服务时出错", "err", err)
	}
	return nil
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
func flagOverrides(port int, bind, dataDir string) (map[string]any, model.ParamOrigins) {
	patch := map[string]any{}
	origins := model.ParamOrigins{}
	server := map[string]any{}

	if port > 0 {
		server["port"] = port
		origins.Set("server.port", model.OriginUser)
	}
	if bind != "" {
		server["bind"] = bind
		origins.Set("server.bind", model.OriginUser)
	}
	if len(server) > 0 {
		patch["server"] = server
	}

	// -data-dir 必须落到 data.dir：否则配置文件写在指定目录，
	// 而历史、缓存、日志会按便携模式写到 exe 同级，两处不一致。
	if dataDir != "" {
		patch["data"] = map[string]any{"dir": dataDir}
		origins.Set("data.dir", model.OriginUser)
	}
	return patch, origins
}

func newLogger(flagLevel, cfgLevel string) *slog.Logger {
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
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lv}))
}

// panelURL 拼出面板地址。
//
// 监听 0.0.0.0 时浏览器打不开 0.0.0.0，因此本机访问一律用回环地址。
func panelURL(bind string, port int) string {
	host := bind
	if host == "0.0.0.0" || host == "" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s:%d", host, port)
}

// isAddrInUse 判断监听失败是否因为端口被占用。
func isAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE) || strings.Contains(strings.ToLower(err.Error()), "address already in use") ||
		strings.Contains(strings.ToLower(err.Error()), "only one usage of each socket address")
}

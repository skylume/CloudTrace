package launch

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cloudtrace/internal/config"
)

// newTestStore 建一份指向临时目录的配置存储，可按需改默认值。
func newTestStore(t *testing.T, mutate func(*config.Config)) *config.Store {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Data.Dir = dir
	if mutate != nil {
		mutate(&cfg)
	}
	if err := config.SaveFile(config.ConfigPath(dir), cfg); err != nil {
		t.Fatalf("保存测试配置失败：%v", err)
	}
	store, err := config.OpenStore(config.ConfigPath(dir), "")
	if err != nil {
		t.Fatalf("打开测试配置失败：%v", err)
	}
	return store
}

func TestPanelURLUsesLoopbackForWildcardBind(t *testing.T) {
	// 浏览器打不开 0.0.0.0：照搬给用户的是一个点不开的地址。
	cases := map[string]string{
		"127.0.0.1":   "http://127.0.0.1:17443",
		"0.0.0.0":     "http://127.0.0.1:17443",
		"":            "http://127.0.0.1:17443",
		"192.168.1.5": "http://192.168.1.5:17443",
	}
	for bind, want := range cases {
		if got := PanelURL(bind, 17443); got != want {
			t.Errorf("PanelURL(%q) = %q，期望 %q", bind, got, want)
		}
	}
}

func TestIsAddrInUse(t *testing.T) {
	if IsAddrInUse(nil) {
		t.Error("nil 不该被当成端口占用")
	}
	if !IsAddrInUse(errors.New("listen tcp 127.0.0.1:17443: bind: address already in use")) {
		t.Error("常见的占用措辞没被识别")
	}
	if !IsAddrInUse(errors.New("Only one usage of each socket address is normally permitted")) {
		t.Error("Windows 的措辞没被识别")
	}
	if IsAddrInUse(errors.New("connection refused")) {
		t.Error("不相干的错误被当成了端口占用")
	}
}

// ErrAddrInUse 要带上用户能直接用的地址：报错只说「端口被占用」没用，
// 他需要的是「打开哪个地址」。
func TestErrAddrInUseCarriesURL(t *testing.T) {
	err := error(ErrAddrInUse{Addr: "127.0.0.1:17443", URL: "http://127.0.0.1:17443"})

	var target ErrAddrInUse
	if !errors.As(err, &target) {
		t.Fatal("类型断言失败")
	}
	if target.URL == "" {
		t.Error("没带上可访问的地址")
	}
}

// 命令行覆盖项必须标成 user：那是用户本次显式指定的，自适应不得改动。
func TestFlagOverridesMarksUserOrigin(t *testing.T) {
	patch, origins := flagOverrides(Flags{Port: 8080, Bind: "0.0.0.0"}, "")

	server, ok := patch["server"].(map[string]any)
	if !ok {
		t.Fatalf("补丁里没有 server 分组：%v", patch)
	}
	if server["port"] != 8080 || server["bind"] != "0.0.0.0" {
		t.Errorf("覆盖项没进补丁：%v", server)
	}
	if origins["server.port"] != "user" || origins["server.bind"] != "user" {
		t.Errorf("来源标记不对：%v", origins)
	}
}

// 没传 -data-dir 时不能动 data.dir：写了就会把绝对路径钉死进配置，
// 之后用户把整个目录挪个地方，程序就找不回数据了。
func TestFlagOverridesLeavesDataDirAlone(t *testing.T) {
	patch, origins := flagOverrides(Flags{Port: 8080}, "")

	if _, ok := patch["data"]; ok {
		t.Errorf("没传 -data-dir 却改了 data.dir：%v", patch)
	}
	if _, ok := origins["data.dir"]; ok {
		t.Errorf("没传 -data-dir 却标了来源：%v", origins)
	}
}

// 传了 -data-dir 时它要落到 data.dir，否则配置文件写在指定目录、而历史与缓存
// 按便携模式写到 exe 同级，两处不一致。
func TestFlagOverridesAppliesDataDir(t *testing.T) {
	patch, origins := flagOverrides(Flags{DataDir: "./custom"}, "/abs/custom")

	data, ok := patch["data"].(map[string]any)
	if !ok {
		t.Fatalf("补丁里没有 data 分组：%v", patch)
	}
	if data["dir"] != "/abs/custom" {
		t.Errorf("data.dir = %v，期望绝对路径", data["dir"])
	}
	if origins["data.dir"] != "user" {
		t.Errorf("来源标记不对：%v", origins)
	}
}

func TestFlagOverridesEmptyWhenNothingGiven(t *testing.T) {
	patch, origins := flagOverrides(Flags{}, "")
	if len(patch) != 0 || len(origins) != 0 {
		t.Errorf("什么都没传却生成了补丁：%v %v", patch, origins)
	}
}

/**
 * 装配要真的能跑起来。
 *
 * 这条覆盖两个入口共用的整条路径：定位数据目录 → 建目录 → 开配置 → 生成 Token
 * → 装配服务 → 构造 handler。它比逐段测更值——两个发行版行为一致靠的就是这条
 * 路径是同一份代码。
 */
func TestPrepareAssemblesUsableApp(t *testing.T) {
	dir := t.TempDir()

	app, err := Prepare(Options{Flags: Flags{DataDir: dir}, Version: "test"})
	if err == nil {
		t.Cleanup(app.Close)
	}
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	t.Cleanup(func() {
		_ = app.Services.Shutdown(t.Context())
	})

	if app.Handler == nil {
		t.Error("没有拿到 handler")
	}
	if app.URL == "" {
		t.Error("没有算出面板地址")
	}
	if app.Version != "test" {
		t.Errorf("版本 = %q", app.Version)
	}
	// 数据目录必须落在指定位置，且骨架已经建好。
	for _, sub := range []string{"", "history", "cache", "logs"} {
		if _, err := os.Stat(filepath.Join(dir, sub)); err != nil {
			t.Errorf("数据目录缺少 %q：%v", sub, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); err != nil {
		t.Errorf("配置文件没有落盘：%v", err)
	}
}

// 首次运行**不**自动生成访问密码。
//
// 生成的那串 64 位十六进制用户只能从控制台抄，而桌面版没有控制台；默认只绑
// 回环时本机访问本来就不需要密码。密码由用户在设置页自己设，绑定局域网却没
// 设密码会在配置校验那一关被拒。
func TestPrepareLeavesPasswordUnset(t *testing.T) {
	app, err := Prepare(Options{Flags: Flags{DataDir: t.TempDir()}, Version: "test"})
	if err == nil {
		t.Cleanup(app.Close)
	}
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	t.Cleanup(func() { _ = app.Services.Shutdown(t.Context()) })

	if got := app.Store.Get().Server.Token; got != "" {
		t.Errorf("不该自动生成访问密码，实际 %q", got)
	}
}

// 只绑回环时不给局域网地址：那种情况下局域网根本连不上，给出地址等于骗人。
func TestLANURLsEmptyWhenBoundToLoopback(t *testing.T) {
	if urls := LANURLs("127.0.0.1", 17443); len(urls) != 0 {
		t.Errorf("绑回环时不该有局域网地址，实际 %v", urls)
	}
	if urls := LANURLs("", 17443); len(urls) != 0 {
		t.Errorf("空绑定等于回环，不该有局域网地址，实际 %v", urls)
	}
}

// 指定了具体网卡就只给那一个地址：枚举出来的其他地址用户访问不到。
func TestLANURLsFollowExplicitBind(t *testing.T) {
	urls := LANURLs("192.168.5.246", 17443)
	if len(urls) != 1 || urls[0] != "http://192.168.5.246:17443" {
		t.Errorf("指定绑定时的局域网地址 = %v", urls)
	}
}

// 绑 0.0.0.0 时枚举出真实网卡地址，且不带回环。
func TestLANURLsEnumerateInterfaces(t *testing.T) {
	for _, url := range LANURLs("0.0.0.0", 17443) {
		if strings.Contains(url, "127.0.0.1") || strings.Contains(url, "[::1]") {
			t.Errorf("局域网地址里出现了回环：%s", url)
		}
		if !strings.HasPrefix(url, "http://") || !strings.HasSuffix(url, ":17443") {
			t.Errorf("地址形状不对：%s", url)
		}
	}
}

// 端口占用时给出的是可用的地址，而不是一句「端口被占用」。
func TestListenReportsAddrInUse(t *testing.T) {
	app, err := Prepare(Options{Flags: Flags{DataDir: t.TempDir()}, Version: "test"})
	if err == nil {
		t.Cleanup(app.Close)
	}
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	t.Cleanup(func() { _ = app.Services.Shutdown(t.Context()) })

	first, err := app.Listen()
	if err != nil {
		t.Fatalf("第一次监听失败：%v", err)
	}
	defer func() { _ = first.Close() }()

	// 同一个地址再监听一次：正是「已经有一个实例在跑」的场景。
	if _, err := app.Listen(); err == nil {
		t.Fatal("重复监听同一个地址应当失败")
	} else {
		var inUse ErrAddrInUse
		if !errors.As(err, &inUse) {
			t.Fatalf("错误类型 = %T，期望 ErrAddrInUse：%v", err, err)
		}
		if inUse.URL == "" {
			t.Error("没带上可访问的地址")
		}
	}
}

// PeekPanelURL 供「已有实例在跑」那条路使用，因此有一条硬性要求：
// **只读**。它一旦顺手建了配置或写了 Token，正在运行的那份实例内存里的
// Token 就与磁盘对不上了——控制台打印新的、登录校验用旧的，用户怎么输都
// 进不去。
func TestPeekPanelURLDoesNotCreateAnything(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	got := PeekPanelURL(Flags{DataDir: dir})
	if want := PanelURL(config.Default().Server.Bind, config.Default().Server.Port); got != want {
		t.Errorf("没有配置时 = %q，期望默认值 %q", got, want)
	}
	if _, err := os.Stat(cfgPath); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("只读地看一眼却创建了配置文件：%v", err)
	}
}

func TestPeekPanelURLReadsConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Server.Bind = "0.0.0.0"
	cfg.Server.Port = 18999
	if err := config.SaveFile(filepath.Join(dir, "config.json"), cfg); err != nil {
		t.Fatalf("写入测试配置失败：%v", err)
	}

	// 绑了通配地址也要给回环：用户要打开的是一个点得开的地址。
	if got, want := PeekPanelURL(Flags{DataDir: dir}), "http://127.0.0.1:18999"; got != want {
		t.Errorf("= %q，期望 %q", got, want)
	}
}

func TestPeekPanelURLPrefersFlags(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Server.Port = 18999
	if err := config.SaveFile(filepath.Join(dir, "config.json"), cfg); err != nil {
		t.Fatalf("写入测试配置失败：%v", err)
	}

	// 本次显式指定的端口就是实际监听的那个，配置里的旧值不作数。
	got := PeekPanelURL(Flags{DataDir: dir, Port: 18888})
	if want := "http://127.0.0.1:18888"; got != want {
		t.Errorf("= %q，期望 %q", got, want)
	}
}

// Register 把六个参数全部挂到 FlagSet 上，并且都能被解析回来。
func TestRegisterExposesAllFlags(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	var f Flags
	f.Register(fs)

	if err := fs.Parse([]string{
		"-data-dir", "/tmp/data",
		"-port", "8080",
		"-bind", "0.0.0.0",
		"-no-browser",
		"-log-level", "debug",
		"-version",
	}); err != nil {
		t.Fatalf("解析参数失败：%v", err)
	}
	if f.DataDir != "/tmp/data" || f.Port != 8080 || f.Bind != "0.0.0.0" {
		t.Errorf("标量参数没解析对：%+v", f)
	}
	if !f.NoBrowser || !f.ShowVersion || f.LogLevel != "debug" {
		t.Errorf("布尔与枚举参数没解析对：%+v", f)
	}
}

// ErrAddrInUse 的文案要带上地址，用户才知道是哪个端口被占了。
func TestErrAddrInUseMessage(t *testing.T) {
	err := error(ErrAddrInUse{Addr: "127.0.0.1:17443"})
	msg := err.Error()
	if !strings.Contains(msg, "17443") {
		t.Errorf("文案没带端口：%q", msg)
	}
}

// newLogger 把级别字符串映射到 slog 级别；flag 级别优先于配置级别。
func TestNewLoggerMapsLevels(t *testing.T) {
	cases := map[string]slog.Level{
		"debug": slog.LevelDebug,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
		"info":  slog.LevelInfo,
		"":      slog.LevelInfo,
		"bogus": slog.LevelInfo,
	}
	for in, want := range cases {
		l := newLogger(in, "", io.Discard)
		got := l.Enabled(context.Background(), want)
		if !got {
			t.Errorf("newLogger(%q) 应启用级别 %v", in, want)
		}
	}
}

// flag 级别压过配置级别：用户显式 -log-level 说了算。
func TestNewLoggerFlagLevelWins(t *testing.T) {
	l := newLogger("debug", "error", io.Discard)
	if !l.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("flag=debug 时应启用 debug，配置的 error 不应压过它")
	}
}

// HTTPServer 组装出带头部读取超时、绑定已装配 handler 的服务器。
func TestHTTPServerBuildsConfiguredServer(t *testing.T) {
	fs := flag.NewFlagSet("h", flag.ContinueOnError)
	var f Flags
	f.Register(fs)
	app := &App{Handler: http.NotFoundHandler(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	srv := app.HTTPServer()
	if srv.ReadHeaderTimeout != 10*time.Second {
		t.Errorf("ReadHeaderTimeout = %v", srv.ReadHeaderTimeout)
	}
	if srv.Handler == nil {
		t.Error("handler 没有挂上去")
	}
}

// Close 对没有日志文件的 App 是无害空操作。
func TestCloseWithoutLogFileIsNoop(t *testing.T) {
	a := &App{}
	a.Close() // 不应 panic
}

// LogAccess 无 Token 且绑回环：只打基础信息，不打任何密码类提示。
func TestLogAccessLoopbackNoToken(t *testing.T) {
	var buf bytes.Buffer
	a := &App{
		Store:   newTestStore(t, func(cfg *config.Config) { cfg.Server.Bind = "127.0.0.1" }),
		Logger:  slog.New(slog.NewTextHandler(&buf, nil)),
		URL:     "http://127.0.0.1:17443",
		DataDir: t.TempDir(),
	}
	a.LogAccess()
	msg := buf.String()
	if !strings.Contains(msg, "http://127.0.0.1:17443") {
		t.Errorf("没打面板地址：%s", msg)
	}
	// 回环绑定时不应出现两类密码/局域网提示（注意：t.TempDir 路径可能含测试名片段，
	// 所以只断言具体提示文本，不断言裸的 token/密码 子串）。
	for _, bad := range []string{"没有访问密码", "已设置访问密码", "访问 Token", "开放局域网访问"} {
		if strings.Contains(msg, bad) {
			t.Errorf("回环无 Token 时不该提示 %q：%s", bad, msg)
		}
	}
}

// LogAccess 有哈希密码：提示已设置密码。
func TestLogAccessHashedPassword(t *testing.T) {
	var buf bytes.Buffer
	a := &App{
		Store:   newTestStore(t, func(cfg *config.Config) { cfg.Server.Bind = "0.0.0.0" }),
		Logger:  slog.New(slog.NewTextHandler(&buf, nil)),
		URL:     "http://127.0.0.1:17443",
		DataDir: t.TempDir(),
	}
	hash, err := config.HashPassword("secret")
	if err != nil {
		t.Fatalf("生成测试哈希失败：%v", err)
	}
	a.Store.Patch(map[string]any{"server": map[string]any{"token": hash}}, nil)
	a.LogAccess()
	msg := buf.String()
	if !strings.Contains(msg, "已设置访问密码") {
		t.Errorf("有哈希密码时该提示已设置：%s", msg)
	}
	if strings.Contains(msg, "没有访问密码") {
		t.Errorf("有密码还提示没有：%s", msg)
	}
}

// LogAccess 明文旧版 Token：把 Token 打印出来供用户抄录。
func TestLogAccessLegacyToken(t *testing.T) {
	var buf bytes.Buffer
	a := &App{
		Store:   newTestStore(t, func(cfg *config.Config) { cfg.Server.Bind = "0.0.0.0" }),
		Logger:  slog.New(slog.NewTextHandler(&buf, nil)),
		URL:     "http://127.0.0.1:17443",
		DataDir: t.TempDir(),
	}
	a.Store.Patch(map[string]any{"server": map[string]any{"token": "deadbeef"}}, nil)
	a.LogAccess()
	msg := buf.String()
	if !strings.Contains(msg, "deadbeef") {
		t.Errorf("明文 Token 应当打出来供抄录：%s", msg)
	}
}

// resolveRootDir 空命令行值时走默认解析（这里 exeDir 非空 → 便携模式）。
func TestResolveRootDirDefaultsToPortable(t *testing.T) {
	got, err := resolveRootDir("", "/exe/dir")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	want := filepath.Join("/exe/dir", "data")
	if got != want {
		t.Errorf("resolveRootDir(空, /exe/dir) = %q，期望 %q", got, want)
	}
}

// AcquireSingleInstance 非重启路径：我这份是第一个实例。
func TestAcquireSingleInstanceFirstRun(t *testing.T) {
	t.Setenv(RestartEnv, "")
	lock, first, err := AcquireSingleInstance()
	if err != nil {
		t.Fatalf("取单实例锁失败：%v", err)
	}
	defer lock.Release()
	if !first {
		t.Error("首次运行应当拿到「第一个实例」标记")
	}
}

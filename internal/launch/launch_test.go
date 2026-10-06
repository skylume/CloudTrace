package launch

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cloudtrace/internal/config"
)

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

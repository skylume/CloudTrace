package launch

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
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

// 首次运行要自动生成访问 Token：局域网访问靠它，缺了用户根本登不进去。
func TestPrepareGeneratesToken(t *testing.T) {
	app, err := Prepare(Options{Flags: Flags{DataDir: t.TempDir()}, Version: "test"})
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	t.Cleanup(func() { _ = app.Services.Shutdown(t.Context()) })

	if app.Store.Get().Server.Token == "" {
		t.Error("没有生成访问 Token")
	}
}

// 端口占用时给出的是可用的地址，而不是一句「端口被占用」。
func TestListenReportsAddrInUse(t *testing.T) {
	app, err := Prepare(Options{Flags: Flags{DataDir: t.TempDir()}, Version: "test"})
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

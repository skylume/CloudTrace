package server

import (
	"io"
	"log/slog"
	"testing"

	"cloudtrace/internal/config"
	"cloudtrace/internal/model"
)

// testServer 从测试脚手架里造一个可用的 server。
//
// 只用来调那些「组装选项」的纯方法，不启动任何任务，因此不需要完整的运行时。
func testServer(st *testStack) *server {
	return &server{
		cfg:    st.store,
		svc:    st.svc,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// 启用的远端源要接进扫描任务。
//
// 这条接线曾经漏过：配置里有远端列表、拉取逻辑也写好了，但组装扫描选项时
// 没把它传进去——**用户加再多远端地址都不会去拉**，而且不报任何错。这类
// 「少传一项就静默失效」的接线只能靠直接断言挡住。
func TestScanOptionsWireRemoteSources(t *testing.T) {
	st := newTestStack(t, func(c *config.Config) {
		// 注意不能放「已启用但地址为空」：配置校验本身就会拒绝它，那是另一层
		// 保护。这里只验证「停用的不会被带进任务」。
		c.Source.RemoteURLs = []config.RemoteSource{
			{URL: "https://a.example.com/list.txt", Enabled: true, Note: "生产列表"},
			{URL: "https://b.example.com/list.txt", Enabled: false, Note: "暂不启用"},
		}
	})

	opts := testServer(st).scanOptions(model.ScanParams{})

	if len(opts.RemoteURLs) != 1 {
		t.Fatalf("远端源 = %v，期望只带启用的那一个", opts.RemoteURLs)
	}
	if opts.RemoteURLs[0] != "https://a.example.com/list.txt" {
		t.Errorf("远端源 = %q", opts.RemoteURLs[0])
	}
}

// 没配置远端源时不能凭空多出请求。
func TestScanOptionsWithoutRemoteSources(t *testing.T) {
	st := newTestStack(t, nil)

	opts := testServer(st).scanOptions(model.ScanParams{})

	if len(opts.RemoteURLs) != 0 {
		t.Fatalf("没有配置远端源却带上了 %v", opts.RemoteURLs)
	}
}

// 归属地补齐要接上：漏了不会报错，只是结果里永远没有地区与运营商。
func TestScanOptionsWireEnrich(t *testing.T) {
	st := newTestStack(t, nil)

	opts := testServer(st).scanOptions(model.ScanParams{})

	if opts.Enrich == nil {
		t.Error("归属地补齐没有接上——结果里会缺少地区与运营商")
	}
	if opts.Logger == nil {
		t.Error("日志没有接上——任务出问题时查不到任何记录")
	}
	if opts.Seed == 0 {
		t.Error("种子没有设置——每轮会扫到同一批地址")
	}
}

// 扫描参数要原样带下去，不能在中途被改掉。
func TestScanOptionsKeepsParams(t *testing.T) {
	st := newTestStack(t, nil)

	params := model.ScanParams{Workers: 42, SampleMax: 1234, Port: 2053}
	opts := testServer(st).scanOptions(params)

	if opts.Params.Workers != 42 || opts.Params.SampleMax != 1234 || opts.Params.Port != 2053 {
		t.Fatalf("参数被改动了：%+v", opts.Params)
	}
}

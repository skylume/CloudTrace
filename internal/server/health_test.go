package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"cloudtrace/internal/config"
	"cloudtrace/internal/health"
	"cloudtrace/internal/speed"
)

// testConn 造一个只往内存队列发报文的连接，便于直接调用命令处理函数。
//
// 命令处理函数只依赖 sendEvent，不需要真的握一次手；直接调用能省掉一整条
// 「起 HTTP → 连 WS」的链路，探测结果也因此完全可控。
func testConn() *wsConn {
	return &wsConn{
		hub:  newWSHub(slog.New(slog.NewTextHandler(io.Discard, nil))),
		send: make(chan []byte, 16),
	}
}

// nextEvent 取出该连接发出的下一条报文。
func nextEvent(t *testing.T, c *wsConn) message {
	t.Helper()
	select {
	case b := <-c.send:
		var m message
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("报文不是合法 JSON：%v", err)
		}
		return m
	case <-time.After(3 * time.Second):
		t.Fatal("3 秒内没有收到任何事件")
		return message{}
	}
}

// probeServer 造一个体检探测结果完全可控的 server。
func probeServer(t *testing.T, st *testStack, portInUse bool, dirErr, srcErr error) *server {
	t.Helper()
	cfg := st.store.Get()
	return &server{
		cfg:        st.store,
		svc:        st.svc,
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		listenPort: cfg.Server.Port,
		startup:    cfg,
		portInUse:  func(int) bool { return portInUse },
		dirWritable: func(string) error {
			return dirErr
		},
		sourceReachable: func(context.Context) error { return srcErr },
	}
}

// issuesOf 把报告里的检查项收成集合，便于断言「有哪几项」。
func issuesOf(rep health.Report) map[string]health.Issue {
	out := make(map[string]health.Issue, len(rep.Issues))
	for _, i := range rep.Issues {
		out[i.Key] = i
	}
	return out
}

// 体检必须能把配置问题翻成一句人话 + 一条建议。
func TestHealthCheckReportsEveryProblem(t *testing.T) {
	st := newTestStack(t, func(c *config.Config) {
		c.Scan.Workers = 500
		c.Scan.LatencyThreshold = 50
	})
	s := probeServer(t, st, true, errors.New("只读"), errors.New("连不上"))
	// 让「本进程占用的端口」与配置里的端口不同：否则这一项会被当成自己占用
	// 而正确地跳过，也就测不到端口冲突了。
	s.listenPort = st.store.Get().Server.Port + 1

	conn := testConn()
	if err := s.handleHealthCheck(conn, nil); err != nil {
		t.Fatalf("体检命令失败：%v", err)
	}

	m := nextEvent(t, conn)
	if m.Type != eventHealth {
		t.Fatalf("事件类型 = %q，期望 %q", m.Type, eventHealth)
	}
	var rep health.Report
	decode(t, m, &rep)

	issues := issuesOf(rep)
	for _, key := range []string{
		health.KeyPort,
		health.KeyWorkers,
		health.KeyLatency,
		health.KeyDataDir,
		health.KeyASNDB,
		health.KeySpeedSource,
	} {
		if _, ok := issues[key]; !ok {
			t.Errorf("缺少 %s 这一项：%+v", key, rep.Issues)
		}
	}
	if rep.Checked < 6 {
		t.Errorf("已查项数 = %d，期望至少 6", rep.Checked)
	}
	for _, i := range rep.Issues {
		if i.Problem == "" || i.Suggestion == "" {
			t.Errorf("检查项 %s 缺少问题描述或建议：%+v", i.Key, i)
		}
	}

	// 错误级要排在警告前面，前端按顺序展示时最先看到最要紧的。
	if len(rep.Issues) > 1 && rep.Issues[0].Level != health.LevelError {
		t.Errorf("首位等级 = %q，期望错误级在前", rep.Issues[0].Level)
	}

	// 并发过高与阈值过低都要给一键修复。
	for _, key := range []string{health.KeyWorkers, health.KeyLatency} {
		if !issues[key].Fixable || issues[key].Fix == nil {
			t.Errorf("%s 应当提供一键修复", key)
		}
	}
}

// 配置健康时不该报任何问题——天天喊狼来了的体检等于没有体检。
func TestHealthCheckHealthyConfig(t *testing.T) {
	st := newTestStack(t, func(c *config.Config) {
		// ASN 库文件在测试环境里必然不存在，关掉这一项才能测「全通过」。
		c.Geo.ASNSource = "off"
	})
	s := probeServer(t, st, false, nil, nil)

	conn := testConn()
	if err := s.handleHealthCheck(conn, nil); err != nil {
		t.Fatalf("体检命令失败：%v", err)
	}

	var rep health.Report
	decode(t, nextEvent(t, conn), &rep)
	if len(rep.Issues) != 0 {
		t.Fatalf("健康配置报了问题：%+v", rep.Issues)
	}
	// 出口地区与代理没配好时那两项查不了，不计入已查数量。
	if rep.Checked != 5 {
		t.Errorf("已查项数 = %d，期望 5（出口地区与测速源跳过了两项）", rep.Checked)
	}
}

// 面板自己占着的端口不能算冲突，否则每次体检都报一条假故障。
func TestHealthCheckOwnPortIsNotAConflict(t *testing.T) {
	st := newTestStack(t, func(c *config.Config) {
		c.Geo.ASNSource = "off"
	})
	// portInUse 一律返回 true：模拟「端口确实被占着，但占用者就是自己」。
	s := probeServer(t, st, true, nil, nil)

	conn := testConn()
	if err := s.handleHealthCheck(conn, nil); err != nil {
		t.Fatalf("体检命令失败：%v", err)
	}

	var rep health.Report
	decode(t, nextEvent(t, conn), &rep)
	if _, ok := issuesOf(rep)[health.KeyPort]; ok {
		t.Error("把自己占着的端口报成了冲突")
	}
}

// 测速源探测：任何 HTTP 响应都算可达，连不上才算不可达。
func TestProbeSpeedSource(t *testing.T) {
	// 一个不接受 HEAD 的服务：用状态码判定会误报成不可达。
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer ts.Close()

	st := newTestStack(t, func(c *config.Config) {
		c.Speed.URLMode = "custom"
		c.Speed.CustomURL = ts.URL
	})
	s := &server{
		cfg:         st.store,
		svc:         st.svc,
		speedSource: speed.NewSourceResolver(nil, time.Now, speed.DefaultSourceTTL),
	}

	if err := s.probeSpeedSource(context.Background()); err != nil {
		t.Fatalf("本机服务应当算可达，实际报错：%v", err)
	}
}

func TestProbeSpeedSourceFailure(t *testing.T) {
	st := newTestStack(t, func(c *config.Config) {
		c.Speed.URLMode = "custom"
		// 1 号端口不会有人监听，连接会立刻被拒绝。
		c.Speed.CustomURL = "http://127.0.0.1:1/"
	})
	s := &server{
		cfg:         st.store,
		svc:         st.svc,
		speedSource: speed.NewSourceResolver(nil, time.Now, speed.DefaultSourceTTL),
	}

	if err := s.probeSpeedSource(context.Background()); err == nil {
		t.Fatal("连不上的地址应当报错")
	}
}

// 走代理时要真的用上配置里的代理，否则开了代理的用户会看到假的「源不可达」。
func TestHealthTransportHonoursProxy(t *testing.T) {
	cfg := config.Default()

	direct := healthTransport(cfg)
	if direct.Proxy != nil {
		t.Error("默认强制直连时不该设置代理")
	}

	cfg.Net.Proxy = "http://127.0.0.1:7890"
	cfg.Net.ForceDirect = false
	withProxy := healthTransport(cfg)
	if withProxy.Proxy == nil {
		t.Fatal("配了代理却没有生效")
	}
	if _, err := withProxy.Proxy(&http.Request{}); err != nil {
		t.Fatalf("代理地址无法解析：%v", err)
	}

	// 强制直连优先：代理配了也不用。
	cfg.Net.ForceDirect = true
	if forced := healthTransport(cfg); forced.Proxy != nil {
		t.Error("强制直连时不该设置代理")
	}

	// 地址写错时退回直连，而不是让体检整个失败。
	cfg.Net.Proxy = "://bad"
	cfg.Net.ForceDirect = false
	if bad := healthTransport(cfg); bad.Proxy != nil {
		t.Error("代理地址无法解析时应当退回直连")
	}
}

func TestResolveASNPath(t *testing.T) {
	dataDir := filepath.Join("root", "data")
	cases := []struct {
		in   string
		want string
	}{
		// 默认值自带 data/ 前缀，不能拼成 data/data/。
		{"data/cache/asn", filepath.Join(dataDir, "cache", "asn")},
		{"cache/asn", filepath.Join(dataDir, "cache", "asn")},
		{"./cache/asn", filepath.Join(dataDir, "cache", "asn")},
		{"  cache/asn  ", filepath.Join(dataDir, "cache", "asn")},
		{"", ""},
	}
	for _, c := range cases {
		if got := resolveASNPath(dataDir, c.in); got != c.want {
			t.Errorf("resolveASNPath(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}

	abs := filepath.Join(t.TempDir(), "asn.mmdb")
	if got := resolveASNPath(dataDir, abs); got != abs {
		t.Errorf("绝对路径被改写了：%q", got)
	}
}

// 体检结果只回给发起方，不广播给别的连接。
func TestHealthCheckIsNotBroadcast(t *testing.T) {
	// 用打向本机被拒端口的速度源：真实探测在单测里又慢又不可控。
	st := fastSourceStack(t, nil)

	first := st.mustDial(t)
	second := st.mustDial(t)
	readUntil(t, first, eventState, 3*time.Second)
	readUntil(t, second, eventState, 3*time.Second)

	send(t, first, `{"type":"health/check"}`)
	if m := readUntil(t, first, eventHealth, 5*time.Second); m.Type != eventHealth {
		t.Fatalf("发起方收到的事件 = %q", m.Type)
	}

	// 第二个连接只该收到心跳之外什么都没有；用一段短读确认没有 health 事件。
	_ = second.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	for {
		_, data, err := second.ReadMessage()
		if err != nil {
			break
		}
		var m message
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("报文不是合法 JSON：%v", err)
		}
		if m.Type == eventHealth {
			t.Fatal("体检结果被广播给了其它连接")
		}
	}
}

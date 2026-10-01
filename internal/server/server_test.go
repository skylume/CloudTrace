package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"cloudtrace/internal/app"
	"cloudtrace/internal/config"
	"cloudtrace/internal/model"
)

// ---------------------------------------------------------------------------
// 测试脚手架
// ---------------------------------------------------------------------------

// testStack 是一次完整的装配结果，供各用例断言。
type testStack struct {
	ts    *httptest.Server
	store *config.Store
	svc   *app.Services
}

// newTestStack 装配一套真实的服务端（配置 → 服务容器 → handler → httptest）。
//
// mutate 用于在装配前调整配置；传入 nil 表示使用默认配置。
func newTestStack(t *testing.T, mutate func(*config.Config)) *testStack {
	t.Helper()

	dir := t.TempDir()
	store, err := config.OpenStore(filepath.Join(dir, "settings.json"), dir)
	if err != nil {
		t.Fatalf("打开配置失败：%v", err)
	}
	if mutate != nil {
		cfg := store.Get()
		mutate(&cfg)
		if _, err := store.Set(cfg); err != nil {
			t.Fatalf("写入测试配置失败：%v", err)
		}
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc, err := app.New(store, "test-version", logger)
	if err != nil {
		t.Fatalf("装配服务失败：%v", err)
	}
	if err := svc.Startup(context.Background()); err != nil {
		t.Fatalf("启动服务失败：%v", err)
	}

	handler, err := New(store, svc)
	if err != nil {
		t.Fatalf("构造 handler 失败：%v", err)
	}

	ts := httptest.NewServer(handler)
	t.Cleanup(func() {
		ts.Close()
		_ = svc.Shutdown(context.Background())
	})

	return &testStack{ts: ts, store: store, svc: svc}
}

// wsURL 把 httptest 的 http 地址转换成 WebSocket 地址。
func (s *testStack) wsURL() string {
	return "ws" + strings.TrimPrefix(s.ts.URL, "http") + "/ws"
}

// dial 建立 WS 连接；cookies 为空表示不带会话。
func (s *testStack) dial(t *testing.T, cookies ...*http.Cookie) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	hdr := http.Header{}
	if len(cookies) > 0 {
		req := httptest.NewRequest(http.MethodGet, "/ws", nil)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		hdr.Set("Cookie", req.Header.Get("Cookie"))
	}
	d := &websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	return d.Dial(s.wsURL(), hdr)
}

// mustDial 建立 WS 连接，失败即终止用例。
func (s *testStack) mustDial(t *testing.T) *websocket.Conn {
	t.Helper()
	conn, resp, err := s.dial(t)
	if err != nil {
		code := 0
		if resp != nil {
			code = resp.StatusCode
		}
		t.Fatalf("建立 WS 连接失败（status=%d）：%v", code, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// readUntil 读取消息直到出现目标类型，期间忽略其他类型。
func readUntil(t *testing.T, conn *websocket.Conn, wantType string, timeout time.Duration) message {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("设置读超时失败：%v", err)
		}
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("等待 %q 事件时读取失败：%v", wantType, err)
		}
		var m message
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("报文不是合法 JSON：%v（原文 %s）", err, data)
		}
		if m.Type == wantType {
			return m
		}
		if time.Now().After(deadline) {
			t.Fatalf("超时未收到 %q 事件", wantType)
		}
	}
}

// send 发送一条客户端命令。
func send(t *testing.T, conn *websocket.Conn, payload string) {
	t.Helper()
	if err := conn.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
		t.Fatalf("发送命令失败：%v", err)
	}
}

// get 发起一次 GET 请求。
func get(t *testing.T, url string, cookies ...*http.Cookie) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("构造请求失败：%v", err)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败：%v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// bodyOf 读取响应体文本。
func bodyOf(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应体失败：%v", err)
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// 探活接口
// ---------------------------------------------------------------------------

func TestHealthEndpoint(t *testing.T) {
	st := newTestStack(t, nil)

	resp := get(t, st.ts.URL+"/api/health")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q，期望 application/json", ct)
	}

	var body healthResponse
	if err := json.Unmarshal([]byte(bodyOf(t, resp)), &body); err != nil {
		t.Fatalf("响应不是合法 JSON：%v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q，期望 ok", body.Status)
	}
	if body.Version != "test-version" {
		t.Errorf("version = %q，期望 test-version", body.Version)
	}
	if body.ProtocolVersion != ProtocolVersion {
		t.Errorf("protocol_version = %d，期望 %d", body.ProtocolVersion, ProtocolVersion)
	}
	if body.DataDir == "" {
		t.Error("data_dir 不应为空")
	}
}

func TestHealthRejectsNonGet(t *testing.T) {
	st := newTestStack(t, nil)

	req, err := http.NewRequest(http.MethodPost, st.ts.URL+"/api/health", nil)
	if err != nil {
		t.Fatalf("构造请求失败：%v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败：%v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("状态码 = %d，期望 405", resp.StatusCode)
	}
	if allow := resp.Header.Get("Allow"); !strings.Contains(allow, "GET") {
		t.Errorf("Allow = %q，期望包含 GET", allow)
	}
}

// 配置文件损坏时不应启动失败，而是回退默认值并通过 health 暴露原因。
func TestHealthSurfacesStartupWarnings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte("{ 这不是 JSON"), 0o644); err != nil {
		t.Fatalf("写入损坏配置失败：%v", err)
	}

	store, err := config.OpenStore(path, dir)
	if err != nil {
		t.Fatalf("损坏配置不应导致打开失败：%v", err)
	}
	if len(store.Warnings()) == 0 {
		t.Fatal("期望记录启动警告")
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc, err := app.New(store, "test-version", logger)
	if err != nil {
		t.Fatalf("装配服务失败：%v", err)
	}
	if err := svc.Startup(context.Background()); err != nil {
		t.Fatalf("启动服务失败：%v", err)
	}
	t.Cleanup(func() { _ = svc.Shutdown(context.Background()) })

	handler, err := New(store, svc)
	if err != nil {
		t.Fatalf("构造 handler 失败：%v", err)
	}
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	resp := get(t, ts.URL+"/api/health")
	var body healthResponse
	if err := json.Unmarshal([]byte(bodyOf(t, resp)), &body); err != nil {
		t.Fatalf("响应不是合法 JSON：%v", err)
	}
	if len(body.Warnings) == 0 {
		t.Error("health 响应应包含启动警告")
	}
}

// ---------------------------------------------------------------------------
// 静态资源与鉴权
// ---------------------------------------------------------------------------

func TestStaticServesIndexWhenLoopbackOnly(t *testing.T) {
	st := newTestStack(t, nil)

	resp := get(t, st.ts.URL+"/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", resp.StatusCode)
	}
	if body := bodyOf(t, resp); !strings.Contains(body, "CloudTrace") {
		t.Errorf("首页内容不符合预期：%s", truncate(body, 120))
	}
}

// 前端路由不对应真实文件，刷新时必须回退到 index.html。
func TestStaticFallsBackToIndex(t *testing.T) {
	st := newTestStack(t, nil)

	resp := get(t, st.ts.URL+"/history/detail/42")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", resp.StatusCode)
	}
	if body := bodyOf(t, resp); !strings.Contains(body, "CloudTrace") {
		t.Errorf("未回退到 index.html：%s", truncate(body, 120))
	}
}

// 绑定 0.0.0.0 后，未登录访问页面应拿到登录页而不是空白。
func TestUnauthenticatedGetsLoginPage(t *testing.T) {
	st := newTestStack(t, func(c *config.Config) {
		c.Server.Bind = "0.0.0.0"
		c.Server.Token = "secret-token"
	})

	resp := get(t, st.ts.URL+"/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", resp.StatusCode)
	}
	if body := bodyOf(t, resp); !strings.Contains(body, "访问 Token") {
		t.Errorf("未返回登录页：%s", truncate(body, 120))
	}
}

// 绑定 0.0.0.0 后，接口类请求未登录应返回 401 + E_UNAUTHORIZED。
func TestAPIRequiresAuthWhenBoundPublicly(t *testing.T) {
	st := newTestStack(t, func(c *config.Config) {
		c.Server.Bind = "0.0.0.0"
		c.Server.Token = "secret-token"
	})

	conn, resp, err := st.dial(t)
	if err == nil {
		_ = conn.Close()
		t.Fatal("未登录时不应建立 WS 连接")
	}
	if resp == nil {
		t.Fatalf("期望拿到 HTTP 响应，实际为 nil（err=%v）", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("状态码 = %d，期望 401", resp.StatusCode)
	}
}

func TestLoginRejectsWrongToken(t *testing.T) {
	st := newTestStack(t, func(c *config.Config) {
		c.Server.Bind = "0.0.0.0"
		c.Server.Token = "secret-token"
	})

	resp := postJSON(t, st.ts.URL+"/auth/login", `{"token":"wrong"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("状态码 = %d，期望 401", resp.StatusCode)
	}
	var body errorPayload
	if err := json.Unmarshal([]byte(bodyOf(t, resp)), &body); err != nil {
		t.Fatalf("响应不是合法 JSON：%v", err)
	}
	if body.Code != CodeUnauthorized {
		t.Errorf("code = %q，期望 %q", body.Code, CodeUnauthorized)
	}
}

// 登录成功 → 拿到会话 Cookie → 凭 Cookie 可访问页面与 WS。
func TestLoginFlowGrantsAccess(t *testing.T) {
	st := newTestStack(t, func(c *config.Config) {
		c.Server.Bind = "0.0.0.0"
		c.Server.Token = "secret-token"
	})

	resp := postJSON(t, st.ts.URL+"/auth/login", `{"token":"secret-token"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", resp.StatusCode)
	}

	var session *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			session = c
		}
	}
	if session == nil || session.Value == "" {
		t.Fatal("登录成功但未签发会话 Cookie")
	}
	if !session.HttpOnly {
		t.Error("会话 Cookie 应为 HttpOnly")
	}

	page := get(t, st.ts.URL+"/", session)
	if body := bodyOf(t, page); !strings.Contains(body, "发送 ping") {
		t.Errorf("登录后未返回主页面：%s", truncate(body, 120))
	}

	conn, _, err := st.dial(t, session)
	if err != nil {
		t.Fatalf("携带会话 Cookie 仍无法建立 WS：%v", err)
	}
	_ = conn.Close()
}

func TestLogoutRevokesSession(t *testing.T) {
	st := newTestStack(t, func(c *config.Config) {
		c.Server.Bind = "0.0.0.0"
		c.Server.Token = "secret-token"
	})

	resp := postJSON(t, st.ts.URL+"/auth/login", `{"token":"secret-token"}`)
	var session *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			session = c
		}
	}
	if session == nil {
		t.Fatal("未拿到会话 Cookie")
	}

	if got := st.svc.Config.Get(); got.Server.Token != "secret-token" {
		t.Fatalf("配置未生效：token = %q", got.Server.Token)
	}
	if n := st.svc.Bus.Subscribers(app.TopicState); n != 1 {
		t.Fatalf("state 订阅者数量 = %d，期望 1", n)
	}

	// 登出必须携带会话 Cookie —— 服务端据此知道要吊销哪一个会话。
	out := postJSON(t, st.ts.URL+"/auth/logout", ``, session)
	if out.StatusCode != http.StatusOK {
		t.Fatalf("登出状态码 = %d，期望 200", out.StatusCode)
	}

	if _, _, err := st.dial(t, session); err == nil {
		t.Fatal("登出后旧会话不应再能建立 WS")
	}
}

// ---------------------------------------------------------------------------
// WebSocket 协议
// ---------------------------------------------------------------------------

// 首连即收到全量状态快照，前端无需额外请求即可渲染。
func TestWSReceivesInitialState(t *testing.T) {
	st := newTestStack(t, nil)
	conn := st.mustDial(t)

	m := readUntil(t, conn, eventState, 3*time.Second)
	var state model.TaskState
	if err := json.Unmarshal(m.Data, &state); err != nil {
		t.Fatalf("state 载荷不是合法 JSON：%v", err)
	}
	if state.Phase != model.PhaseIdle {
		t.Errorf("phase = %q，期望 %q", state.Phase, model.PhaseIdle)
	}
	if state.Status != model.StatusIdle {
		t.Errorf("status = %q，期望 %q", state.Status, model.StatusIdle)
	}
}

// 状态变更必须经事件总线广播到已连接的前端。
func TestWSReceivesStateBroadcast(t *testing.T) {
	st := newTestStack(t, nil)
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	st.svc.SetState(model.TaskState{
		Phase:  model.PhaseScan,
		Status: model.StatusRunning,
		Done:   3,
		Total:  10,
		Funnel: model.Funnel{Generated: 100, LatencyOK: 40},
	})

	m := readUntil(t, conn, eventState, 3*time.Second)
	var state model.TaskState
	if err := json.Unmarshal(m.Data, &state); err != nil {
		t.Fatalf("state 载荷不是合法 JSON：%v", err)
	}
	if state.Phase != model.PhaseScan || state.Status != model.StatusRunning {
		t.Errorf("phase/status = %q/%q，期望 scan/running", state.Phase, state.Status)
	}
	if state.Done != 3 || state.Total != 10 {
		t.Errorf("done/total = %d/%d，期望 3/10", state.Done, state.Total)
	}
	if state.Funnel.Generated != 100 || state.Funnel.LatencyOK != 40 {
		t.Errorf("漏斗数据未透传：%+v", state.Funnel)
	}
}

func TestWSPingPong(t *testing.T) {
	st := newTestStack(t, nil)
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"ping"}`)
	readUntil(t, conn, eventPong, 3*time.Second)
}

// 未注册的命令必须回 error，不得静默丢弃。
func TestWSUnknownCommandReturnsError(t *testing.T) {
	st := newTestStack(t, nil)
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"definitely-not-a-command"}`)

	m := readUntil(t, conn, eventError, 3*time.Second)
	var p errorPayload
	if err := json.Unmarshal(m.Data, &p); err != nil {
		t.Fatalf("error 载荷不是合法 JSON：%v", err)
	}
	if p.Code != CodeUnknown {
		t.Errorf("code = %q，期望 %q", p.Code, CodeUnknown)
	}
	if !strings.Contains(p.Msg, "definitely-not-a-command") {
		t.Errorf("错误信息应指明命令名，实际为 %q", p.Msg)
	}
}

func TestWSInvalidJSONReturnsError(t *testing.T) {
	st := newTestStack(t, nil)
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{oops`)

	m := readUntil(t, conn, eventError, 3*time.Second)
	var p errorPayload
	if err := json.Unmarshal(m.Data, &p); err != nil {
		t.Fatalf("error 载荷不是合法 JSON：%v", err)
	}
	if p.Code != CodeInvalidParam {
		t.Errorf("code = %q，期望 %q", p.Code, CodeInvalidParam)
	}
}

// 一条连接出错不应影响其他连接。
func TestWSConnectionsAreIsolated(t *testing.T) {
	st := newTestStack(t, nil)
	a := st.mustDial(t)
	b := st.mustDial(t)
	readUntil(t, a, eventState, 3*time.Second)
	readUntil(t, b, eventState, 3*time.Second)

	send(t, a, `{"type":"nope"}`)
	readUntil(t, a, eventError, 3*time.Second)

	// b 仍能正常收发。
	send(t, b, `{"type":"ping"}`)
	readUntil(t, b, eventPong, 3*time.Second)
}

// ---------------------------------------------------------------------------
// 单元测试：鉴权辅助函数
// ---------------------------------------------------------------------------

func TestMatchSecret(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
		ok   bool
	}{
		{"相等", "abc123", "abc123", true},
		{"不等", "abc123", "abc124", false},
		{"长度不同", "abc", "abc123", false},
		{"双方为空", "", "", false},
		{"请求为空", "", "abc", false},
		{"配置为空", "abc", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchSecret(tc.got, tc.want); got != tc.ok {
				t.Errorf("matchSecret(%q, %q) = %v，期望 %v", tc.got, tc.want, got, tc.ok)
			}
		})
	}
}

func TestIsLoopbackIP(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1":   true,
		"127.0.0.5":   true,
		"::1":         true,
		"192.168.1.5": false,
		"8.8.8.8":     false,
		"not-an-ip":   false,
		"":            false,
	}
	for host, want := range cases {
		if got := isLoopbackIP(host); got != want {
			t.Errorf("isLoopbackIP(%q) = %v，期望 %v", host, got, want)
		}
	}
}

// 不信任 X-Forwarded-For，否则「本机免鉴权」可被远程伪造绕过。
func TestClientIPIgnoresForwardedHeaders(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "8.8.8.8:12345"
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	r.Header.Set("X-Real-IP", "127.0.0.1")

	if got := clientIP(r); got != "8.8.8.8" {
		t.Errorf("clientIP = %q，期望 8.8.8.8", got)
	}
}

func TestClientIPWithoutPort(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.7"
	if got := clientIP(r); got != "10.0.0.7" {
		t.Errorf("clientIP = %q，期望 10.0.0.7", got)
	}
}

func TestSessionTokenPrefersCookie(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "from-cookie"})
	r.Header.Set("Authorization", "Bearer from-header")

	if got := sessionToken(r); got != "from-cookie" {
		t.Errorf("sessionToken = %q，期望 from-cookie", got)
	}
}

func TestSessionTokenFallsBackToBearer(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer from-header")

	if got := sessionToken(r); got != "from-header" {
		t.Errorf("sessionToken = %q，期望 from-header", got)
	}
}

func TestSessionTokenEmpty(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := sessionToken(r); got != "" {
		t.Errorf("sessionToken = %q，期望空串", got)
	}
}

func TestSameOrigin(t *testing.T) {
	cases := []struct {
		name   string
		origin string
		host   string
		ok     bool
	}{
		{"无 Origin（脚本客户端）", "", "127.0.0.1:17443", true},
		{"完全同源", "http://127.0.0.1:17443", "127.0.0.1:17443", true},
		{"同主机不同端口（开发态）", "http://127.0.0.1:5173", "127.0.0.1:17443", true},
		{"桌面壳私有 scheme", "wails://wails.localhost", "127.0.0.1:17443", true},
		{"跨站来源", "http://evil.example", "127.0.0.1:17443", false},
		{"非法来源", "://bad", "127.0.0.1:17443", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/ws", nil)
			r.Host = tc.host
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if got := sameOrigin(r); got != tc.ok {
				t.Errorf("sameOrigin = %v，期望 %v", got, tc.ok)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 单元测试：会话表
// ---------------------------------------------------------------------------

func TestAuthStoreIssueValidRevoke(t *testing.T) {
	a := newAuthStore(time.Minute)

	token, expires, err := a.issue()
	if err != nil {
		t.Fatalf("签发会话失败：%v", err)
	}
	if len(token) != 64 {
		t.Errorf("会话令牌长度 = %d，期望 64（32 字节 hex）", len(token))
	}
	if !expires.After(time.Now()) {
		t.Error("过期时间应晚于当前时间")
	}
	if !a.valid(token) {
		t.Error("刚签发的会话应有效")
	}
	if a.valid("") {
		t.Error("空令牌不应有效")
	}
	if a.valid("nonexistent") {
		t.Error("不存在的令牌不应有效")
	}

	a.revoke(token)
	if a.valid(token) {
		t.Error("吊销后会话不应有效")
	}
}

func TestAuthStoreExpires(t *testing.T) {
	base := time.Now()
	a := newAuthStore(time.Minute)
	a.now = func() time.Time { return base }

	token, _, err := a.issue()
	if err != nil {
		t.Fatalf("签发会话失败：%v", err)
	}
	if !a.valid(token) {
		t.Fatal("会话应有效")
	}

	a.now = func() time.Time { return base.Add(2 * time.Minute) }
	if a.valid(token) {
		t.Error("过期会话不应有效")
	}
	if n := a.count(); n != 0 {
		t.Errorf("过期会话应被清理，剩余 %d", n)
	}
}

func TestAuthStoreDefaultTTL(t *testing.T) {
	a := newAuthStore(0)
	if a.ttl != 12*time.Hour {
		t.Errorf("ttl = %v，期望 12h", a.ttl)
	}
}

func TestSessionCookieRoundTrip(t *testing.T) {
	rec := httptest.NewRecorder()
	setSessionCookie(rec, "abc", time.Now().Add(time.Hour))

	res := rec.Result()
	defer res.Body.Close()

	cookies := res.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("Set-Cookie 数量 = %d，期望 1", len(cookies))
	}
	c := cookies[0]
	if c.Name != sessionCookieName || c.Value != "abc" {
		t.Errorf("Cookie = %s=%s，期望 %s=abc", c.Name, c.Value, sessionCookieName)
	}
	if !c.HttpOnly {
		t.Error("会话 Cookie 应为 HttpOnly")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v，期望 Lax", c.SameSite)
	}

	rec2 := httptest.NewRecorder()
	clearSessionCookie(rec2)
	res2 := rec2.Result()
	defer res2.Body.Close()
	c2 := res2.Cookies()[0]
	if c2.MaxAge != -1 {
		t.Errorf("清除 Cookie 的 MaxAge = %d，期望 -1", c2.MaxAge)
	}
}

// ---------------------------------------------------------------------------
// 工具
// ---------------------------------------------------------------------------

// postJSON 发起一次 JSON POST 请求，可附带会话 Cookie。
func postJSON(t *testing.T, url, body string, cookies ...*http.Cookie) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("构造请求失败：%v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败：%v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// truncate 截断长文本，便于断言失败时输出。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

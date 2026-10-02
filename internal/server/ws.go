package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WebSocket 连接参数。
const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 1 << 20 // 1 MiB
	sendQueueSize  = 256
)

// 错误码。前端据 code 决定表现（Toast / 跳登录页 / 字段高亮）。
const (
	CodeBusy           = "E_BUSY"
	CodeInvalidParam   = "E_INVALID_PARAM"
	CodeNotFound       = "E_NOT_FOUND"
	CodeUnauthorized   = "E_UNAUTHORIZED"
	CodeIO             = "E_IO"
	CodeNetwork        = "E_NETWORK"
	CodeASNUnavailable = "E_ASN_UNAVAILABLE"
	CodeUnknown        = "E_UNKNOWN"
)

// 事件名。
const (
	eventState = "state"
	eventPong  = "pong"
	eventError = "error"
)

// message 是前后端统一的 WS 报文：{"type": ..., "data": ...}。
//
// 客户端发来的 type 是命令，服务端发出的 type 是事件。
type message struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// errorPayload 是 error 事件的数据体。
type errorPayload struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
}

// cmdError 是带错误码的命令处理失败。
type cmdError struct {
	Code string
	Msg  string
}

func (e *cmdError) Error() string { return e.Msg }

// fail 构造一个带错误码的失败。
func fail(code, msg string) error { return &cmdError{Code: code, Msg: msg} }

// commandHandler 处理一条客户端命令。
type commandHandler func(c *wsConn, data json.RawMessage) error

// wsHub 管理所有 WS 连接并负责广播。
type wsHub struct {
	mu      sync.RWMutex
	conns   map[*wsConn]struct{}
	logger  *slog.Logger
	upgrade websocket.Upgrader
}

func newWSHub(logger *slog.Logger) *wsHub {
	return &wsHub{
		conns:  make(map[*wsConn]struct{}),
		logger: logger,
		upgrade: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin:     sameOrigin,
		},
	}
}

// sameOrigin 校验握手来源，避免任意网页借用本机会话发起 WS 连接。
//
// 放行规则：
//   - 无 Origin 头（脚本 / 命令行客户端）——浏览器一定带，因此不会削弱防护；
//   - Origin 与请求 Host 完全一致（生产同源）；
//   - 主机名一致、仅端口不同（开发态前端 dev server 与后端不同端口）；
//   - 桌面壳的私有 scheme（原生窗口加载前端时不带 http 主机名）。
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	if u.Hostname() != "" && strings.EqualFold(u.Hostname(), hostOnly(r.Host)) {
		return true
	}
	return u.Scheme == "wails"
}

func hostOnly(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

func (h *wsHub) add(conn *websocket.Conn) *wsConn {
	c := &wsConn{
		hub:  h,
		ws:   conn,
		send: make(chan []byte, sendQueueSize),
	}
	h.mu.Lock()
	h.conns[c] = struct{}{}
	h.mu.Unlock()
	return c
}

func (h *wsHub) remove(c *wsConn) {
	h.mu.Lock()
	delete(h.conns, c)
	h.mu.Unlock()
}

// Count 返回当前连接数。
func (h *wsHub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.conns)
}

// broadcast 向所有连接推送一个事件。
func (h *wsHub) broadcast(eventType string, data any) {
	h.mu.RLock()
	conns := make([]*wsConn, 0, len(h.conns))
	for c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.RUnlock()

	for _, c := range conns {
		c.sendEvent(eventType, data)
	}
}

// wsConn 是单条连接。
//
// 读、写各由一个 goroutine 驱动：读负责分发命令，写负责串行化发送
// （gorilla/websocket 不允许并发写）。
type wsConn struct {
	hub *wsHub
	ws  *websocket.Conn

	mu     sync.Mutex
	send   chan []byte
	closed bool
}

// trySend 非阻塞投递一条已序列化的报文。
//
// 队列满说明客户端消费不过来，返回 false 由调用方断开连接，
// 避免慢客户端拖垮服务端内存。
func (c *wsConn) trySend(b []byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	select {
	case c.send <- b:
		return true
	default:
		return false
	}
}

// close 关闭连接，可安全重复调用。
func (c *wsConn) close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	close(c.send)
	c.mu.Unlock()

	c.hub.remove(c)
	_ = c.ws.Close()
}

// sendEvent 向本连接推送一个事件。
func (c *wsConn) sendEvent(eventType string, data any) {
	m := message{Type: eventType}
	if data != nil {
		raw, err := json.Marshal(data)
		if err != nil {
			c.hub.logger.Error("事件序列化失败", "type", eventType, "err", err)
			return
		}
		m.Data = raw
	}
	b, err := json.Marshal(m)
	if err != nil {
		c.hub.logger.Error("报文序列化失败", "type", eventType, "err", err)
		return
	}
	if !c.trySend(b) {
		c.hub.logger.Warn("发送队列已满，断开连接", "type", eventType)
		c.close()
	}
}

// readPump 读取并分发客户端命令；返回即表示连接结束。
func (c *wsConn) readPump(handlers map[string]commandHandler) {
	defer c.close()

	c.ws.SetReadLimit(maxMessageSize)
	_ = c.ws.SetReadDeadline(time.Now().Add(pongWait))
	c.ws.SetPongHandler(func(string) error {
		return c.ws.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		_, data, err := c.ws.ReadMessage()
		if err != nil {
			return
		}

		var m message
		if err := json.Unmarshal(data, &m); err != nil {
			c.sendEvent(eventError, errorPayload{Code: CodeInvalidParam, Msg: "报文不是合法 JSON"})
			continue
		}

		h, ok := handlers[m.Type]
		if !ok {
			// 未注册的命令必须返回 error，**不得静默丢弃**。
			c.sendEvent(eventError, errorPayload{
				Code: CodeUnknown,
				Msg:  "未注册的命令：" + m.Type,
			})
			continue
		}

		if err := h(c, m.Data); err != nil {
			code := CodeUnknown
			var ce *cmdError
			if errors.As(err, &ce) {
				code = ce.Code
			}
			c.sendEvent(eventError, errorPayload{Code: code, Msg: err.Error()})
		}
	}
}

// writePump 串行化写：所有出站报文都经由本 goroutine 写入。
func (c *wsConn) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.close()
	}()

	for {
		select {
		case b, ok := <-c.send:
			_ = c.ws.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.ws.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return
			}
			if err := c.ws.WriteMessage(websocket.TextMessage, b); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.ws.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// handleWS 处理 WS 握手，并在连接建立后立即下发全量状态快照。
func (s *server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := s.hub.upgrade.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade 失败时已写出响应，这里只记日志。
		s.logger.Warn("WebSocket 握手失败", "err", err)
		return
	}

	c := s.hub.add(conn)

	// 首连即下发全量快照，前端无需额外请求即可恢复视图。
	c.sendEvent(eventState, s.svc.Snapshot())

	go c.writePump()
	c.readPump(s.commands())
}

// commands 返回当前已实现的命令表。
//
// 未出现在这里的命令一律由 readPump 回复 E_UNKNOWN。
func (s *server) commands() map[string]commandHandler {
	handlers := map[string]commandHandler{
		"ping": func(c *wsConn, _ json.RawMessage) error {
			c.sendEvent(eventPong, nil)
			return nil
		},
	}
	// 各功能域各交一份命令表再合并：新增命令只需要在自己那个文件里登记，
	// 不必回到这里改一处长长的清单。
	for _, group := range []map[string]commandHandler{
		s.taskHandlers(),
		s.historyHandlers(),
		s.settingsHandlers(),
		s.healthHandlers(),
		s.geoHandlers(),
		{cmdExport: s.handleExport},
	} {
		for name, handler := range group {
			handlers[name] = handler
		}
	}
	return handlers
}

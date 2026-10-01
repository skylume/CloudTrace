package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

// routes 组装全部路由与中间件。
func (s *server) routes() http.Handler {
	mux := http.NewServeMux()

	// 探活接口：不鉴权，供外部脚本与启动自检使用。
	mux.HandleFunc("/api/health", s.handleHealth)

	// 登录相关：登录页与登录接口本身不能要求已登录。
	mux.HandleFunc("/auth/login", s.handleLogin)
	mux.HandleFunc("/auth/logout", s.handleLogout)

	// WebSocket：鉴权后建立。
	mux.Handle("/ws", s.requireAuth(http.HandlerFunc(s.handleWS)))

	// 静态资源与单页回退：未登录时返回登录页而不是 401，
	// 否则局域网用户会看到一片空白而不知道该做什么。
	mux.Handle("/", s.requireAuthHTML(s.static))

	return securityHeaders(s.withRecover(mux))
}

// requireAuth 对接口类请求强制鉴权，失败返回 401 + E_UNAUTHORIZED。
func (s *server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			writeError(w, http.StatusUnauthorized, CodeUnauthorized, "未登录或会话已失效")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireAuthHTML 对页面类请求鉴权，未通过时返回登录页。
func (s *server) requireAuthHTML(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			if !s.static.serveNamed(w, "login.html") {
				http.Error(w, "登录页缺失", http.StatusInternalServerError)
			}
			return
		}
		next.ServeHTTP(w, r)
	})
}

// withRecover 兜住处理过程中的 panic，避免单次请求异常拖垮整个服务。
func (s *server) withRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.logger.Error("请求处理 panic", "path", r.URL.Path, "panic", rec)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// securityHeaders 附加基础安全响应头。
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// healthResponse 是探活接口的响应体。
type healthResponse struct {
	Status          string   `json:"status"`
	Version         string   `json:"version"`
	ProtocolVersion int      `json:"protocol_version"`
	UptimeS         float64  `json:"uptime_s"`
	DataDir         string   `json:"data_dir"`
	Warnings        []string `json:"warnings,omitempty"`
}

// handleHealth 处理 GET /api/health。
func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, CodeInvalidParam, "只支持 GET")
		return
	}

	dataDir, err := s.svc.DataDir()
	if err != nil {
		dataDir = ""
	}

	writeJSON(w, http.StatusOK, healthResponse{
		Status:          "ok",
		Version:         s.svc.Version,
		ProtocolVersion: ProtocolVersion,
		UptimeS:         s.svc.Uptime().Seconds(),
		DataDir:         dataDir,
		Warnings:        s.cfg.Warnings(),
	})
}

// loginRequest 是登录请求体。
type loginRequest struct {
	Token string `json:"token"`
}

// handleLogin 处理 /auth/login：GET 返回登录页，POST 校验访问 Token 并签发会话。
func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		if !s.static.serveNamed(w, "login.html") {
			http.Error(w, "登录页缺失", http.StatusInternalServerError)
		}

	case http.MethodPost:
		var body loginRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		if err := dec.Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, CodeInvalidParam, "请求体不是合法 JSON")
			return
		}

		cfg := s.cfg.Get()
		if !matchSecret(strings.TrimSpace(body.Token), cfg.Server.Token) {
			writeError(w, http.StatusUnauthorized, CodeUnauthorized, "访问 Token 不正确")
			return
		}

		token, expires, err := s.auth.issue()
		if err != nil {
			writeError(w, http.StatusInternalServerError, CodeUnknown, "生成会话失败")
			return
		}
		setSessionCookie(w, token, expires)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":         true,
			"expires_at": expires.Unix(),
		})

	default:
		w.Header().Set("Allow", "GET, HEAD, POST")
		writeError(w, http.StatusMethodNotAllowed, CodeInvalidParam, "不支持的请求方法")
	}
}

// handleLogout 处理 /auth/logout：吊销当前会话并清除 Cookie。
func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, POST")
		writeError(w, http.StatusMethodNotAllowed, CodeInvalidParam, "不支持的请求方法")
		return
	}
	s.auth.revoke(sessionToken(r))
	clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

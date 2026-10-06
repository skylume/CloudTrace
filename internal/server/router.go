package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"cloudtrace/internal/config"
)

// routes 组装全部路由与中间件。
func (s *server) routes() http.Handler {
	mux := http.NewServeMux()

	// 探活接口：不鉴权，供外部脚本与启动自检使用。
	mux.HandleFunc("/api/health", s.handleHealth)

	// 登录相关：登录页与登录接口本身不能要求已登录。
	mux.HandleFunc("/auth/login", s.handleLogin)
	mux.HandleFunc("/auth/login-info", s.handleLoginInfo)
	mux.HandleFunc("/auth/logout", s.handleLogout)

	// 导出字段清单、导出文件下载与本地结果地址。
	//
	// 三者都要鉴权：导出的是用户扫出来的 IP 列表，本地结果地址的用途正是
	// 让别的程序拉取，一旦不设防，同一局域网里谁都能拿到。默认只监听回环
	// 地址时本机访问仍然免鉴权（见 authorized），脚本照常可用。
	mux.Handle(exportFieldsRoute, s.requireAuth(http.HandlerFunc(s.handleExportFields)))
	mux.Handle(downloadRoute, s.requireAuth(http.HandlerFunc(s.handleDownload)))
	mux.Handle(latestRoute, s.requireAuth(http.HandlerFunc(s.handleLatest)))
	mux.Handle(latestJSONRoute, s.requireAuth(http.HandlerFunc(s.handleLatestJSON)))

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
//
// 字段名沿用 token 而不是 password：这是接口契约的一部分，外部脚本按它写。
// 界面上它叫「访问密码」，两边说的是同一件东西。
type loginRequest struct {
	Token string `json:"token"`
}

// handleLogin 处理 /auth/login：GET 返回登录页，POST 校验访问密码并签发会话。
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
		// 存下来的可能是用户设的加盐哈希，也可能是升级前那版自动生成的明文
		// Token——VerifyPassword 两种都认。
		if !config.VerifyPassword(cfg.Server.Token, strings.TrimSpace(body.Token)) {
			writeError(w, http.StatusUnauthorized, CodeUnauthorized, "访问密码不正确")
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

// loginInfoResponse 是登录页要的那点上下文。
type loginInfoResponse struct {
	// LAN 表示面板绑定的不止回环地址，也就是「同一个网络里的设备都能打开」。
	//
	// 登录页原来把「已开放局域网访问」写死在文案里，只绑回环时那句话是错的。
	LAN bool `json:"lan"`
	// TokenPath 是配置文件的绝对路径，用户照着它就能翻到密码。
	//
	// 只对本机请求给出：把服务端的目录结构发给整个网段没有道理，而站在
	// 这台机器上的人本来就能自己打开那个文件。
	TokenPath string `json:"token_path,omitempty"`
	// PasswordSet 表示已经设过访问密码。
	PasswordSet bool `json:"password_set"`
	// LegacyToken 表示存下来的是升级前那版自动生成的明文 Token。
	//
	// 登录页据此换一句话：那批用户不知道该输什么，得告诉他去哪儿找；而自己
	// 设过密码的人只需要一个输入框。
	LegacyToken bool `json:"legacy_token"`
}

// handleLoginInfo 处理 GET /auth/login-info。
//
// 登录页是静态产物，而「绑没绑局域网」「配置文件在哪」只有服务端知道，
// 所以单开一个不需要鉴权的只读接口把这两件事告诉它。
func (s *server) handleLoginInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, CodeInvalidParam, "只支持 GET")
		return
	}

	cfg := s.cfg.Get()
	out := loginInfoResponse{
		LAN:         !isLoopbackBind(cfg.Server.Bind),
		PasswordSet: cfg.Server.Token != "",
		LegacyToken: cfg.Server.Token != "" && !config.IsHashedPassword(cfg.Server.Token),
	}
	if isLoopbackIP(clientIP(r)) {
		out.TokenPath = s.cfg.Path()
	}
	writeJSON(w, http.StatusOK, out)
}

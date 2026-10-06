package server

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// sessionCookieName 是会话 Cookie 名。
const sessionCookieName = "session_id"

// authStore 是内存会话表。
//
// 会话只存在于内存、不落盘：进程重启即全部失效，用户重新登录即可。
// 会话令牌与「访问 Token」是两回事——后者用于登录，前者用于后续请求。
type authStore struct {
	mu     sync.Mutex
	ttl    time.Duration
	tokens map[string]time.Time // 会话令牌 → 过期时间
	now    func() time.Time
}

func newAuthStore(ttl time.Duration) *authStore {
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	return &authStore{
		ttl:    ttl,
		tokens: make(map[string]time.Time),
		now:    time.Now,
	}
}

// issue 生成新的会话令牌（32 字节随机 hex）。
func (a *authStore) issue() (string, time.Time, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", time.Time{}, err
	}
	token := hex.EncodeToString(buf)
	expires := a.now().Add(a.ttl)

	a.mu.Lock()
	a.gcLocked()
	a.tokens[token] = expires
	a.mu.Unlock()

	return token, expires, nil
}

// valid 判断会话令牌是否存在且未过期。
func (a *authStore) valid(token string) bool {
	if token == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	expires, ok := a.tokens[token]
	if !ok {
		return false
	}
	if a.now().After(expires) {
		delete(a.tokens, token)
		return false
	}
	return true
}

// revoke 使会话令牌立即失效。
func (a *authStore) revoke(token string) {
	if token == "" {
		return
	}
	a.mu.Lock()
	delete(a.tokens, token)
	a.mu.Unlock()
}

// revokeAll 作废全部会话。
//
// 改访问密码时用：密码换了，之前用旧密码换来的通行证就不该继续有效。
func (a *authStore) revokeAll() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := len(a.tokens)
	a.tokens = make(map[string]time.Time)
	return n
}

// count 返回当前会话数（诊断用）。
func (a *authStore) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.tokens)
}

func (a *authStore) gcLocked() {
	now := a.now()
	for token, expires := range a.tokens {
		if now.After(expires) {
			delete(a.tokens, token)
		}
	}
}

// setSessionCookie 写入会话 Cookie。
//
// 本产品只监听本机或局域网 HTTP，没有 HTTPS，因此 Secure 保持关闭，
// 否则浏览器不会回传 Cookie。
func setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// clearSessionCookie 让浏览器丢弃会话 Cookie。
func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// sessionToken 提取请求携带的会话令牌：优先 Cookie，其次 Bearer 头。
func sessionToken(r *http.Request) string {
	if c, err := r.Cookie(sessionCookieName); err == nil && c.Value != "" {
		return c.Value
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	return ""
}

// clientIP 提取客户端 IP。
//
// 只信任 RemoteAddr，**不解析 X-Forwarded-For**：本产品不假设前面存在
// 可信代理，若信任转发头，「本机免鉴权」就可以被远程伪造绕过。
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// isLoopbackIP 报告给定地址是否为回环地址。
func isLoopbackIP(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// localExempt 报告该请求是否享受「本机免鉴权」。
//
// 只看**来源地址**，不看绑定地址。曾经这里还要求 `server.bind` 正好是
// 127.0.0.1，于是把绑定改成 0.0.0.0（为了局域网访问）之后，连本机窗口自己
// 都被要求登录——而桌面版的窗口就是一个普通浏览器，走的是回环。用户看到的是
// 「改完设置重启一次，就再也进不去了」。
//
// 绑定 0.0.0.0 只是把面板暴露给局域网，并不改变「来自回环的请求只能由本机
// 发出」这件事：TCP 握手完不成源地址伪造，所以回环来源始终可信。局域网来的
// 请求照旧一律鉴权（下面 authorized 的第二个分支）。
func (s *server) localExempt(r *http.Request) bool {
	return isLoopbackIP(clientIP(r))
}

// authorized 报告请求是否已通过鉴权。
func (s *server) authorized(r *http.Request) bool {
	if s.localExempt(r) {
		return true
	}
	return s.auth.valid(sessionToken(r))
}

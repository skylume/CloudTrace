package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cloudtrace/internal/config"
)

// 登录页要知道「面板绑在哪儿」与「配置文件在哪」，而两者都只有服务端知道。
// 文案写死在页面里只能二选一：只绑回环时那句「已开放局域网访问」是错的。
func TestLoginInfoReportsBinding(t *testing.T) {
	cases := []struct {
		name string
		bind string
		lan  bool
	}{
		{"只绑回环", "127.0.0.1", false},
		{"绑了 0.0.0.0", "0.0.0.0", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStack(t, func(c *config.Config) {
				c.Server.Bind = tc.bind
				c.Server.Token = "secret-token"
			})

			resp := get(t, st.ts.URL+"/auth/login-info")
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("状态码 = %d，期望 200", resp.StatusCode)
			}

			var info loginInfoResponse
			if err := json.Unmarshal([]byte(bodyOf(t, resp)), &info); err != nil {
				t.Fatalf("响应不是合法 JSON：%v", err)
			}
			if info.LAN != tc.lan {
				t.Errorf("lan = %v，期望 %v", info.LAN, tc.lan)
			}
		})
	}
}

// 本机请求要给出配置文件路径——用户翻到那一项才能拿到 Token。
func TestLoginInfoGivesTokenPathToLocalClients(t *testing.T) {
	st := newTestStack(t, func(c *config.Config) {
		c.Server.Bind = "0.0.0.0"
		c.Server.Token = "secret-token"
	})

	resp := get(t, st.ts.URL+"/auth/login-info")
	var info loginInfoResponse
	if err := json.Unmarshal([]byte(bodyOf(t, resp)), &info); err != nil {
		t.Fatalf("响应不是合法 JSON：%v", err)
	}
	// httptest 的请求来自回环地址，因此属于「本机」。
	if info.TokenPath != st.store.Path() {
		t.Errorf("token_path = %q，期望 %q", info.TokenPath, st.store.Path())
	}
}

// 局域网来的请求不给路径：把服务端的目录结构发给整个网段没有道理，
// 而站在那台机器上的人本来就能自己打开那个文件。
func TestLoginInfoHidesTokenPathFromRemoteClients(t *testing.T) {
	st := newTestStack(t, func(c *config.Config) {
		c.Server.Bind = "0.0.0.0"
		c.Server.Token = "secret-token"
	})

	req := httptest.NewRequest(http.MethodGet, "/auth/login-info", nil)
	req.RemoteAddr = "192.168.1.9:51324"
	rec := httptest.NewRecorder()
	st.srv.handleLoginInfo(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}
	var info loginInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatalf("响应不是合法 JSON：%v", err)
	}
	if !info.LAN {
		t.Error("绑定 0.0.0.0 时 LAN 应为 true")
	}
	if info.TokenPath != "" {
		t.Errorf("远程请求不应拿到路径，实际 %q", info.TokenPath)
	}
}

// 这个接口不需要鉴权（登录页正是在未登录时用它），但也不该接受写请求。
func TestLoginInfoRejectsNonGET(t *testing.T) {
	st := newTestStack(t, func(c *config.Config) {
		c.Server.Bind = "0.0.0.0"
		c.Server.Token = "secret-token"
	})

	resp := postJSON(t, st.ts.URL+"/auth/login-info", `{}`)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("状态码 = %d，期望 405", resp.StatusCode)
	}
	if allow := resp.Header.Get("Allow"); !strings.Contains(allow, http.MethodGet) {
		t.Errorf("Allow = %q，期望包含 GET", allow)
	}
}

// 连续失败到阈值后按来源地址锁定，正确密码在锁定期内也进不来。
//
// 凭据从 256 位随机 Token 换成用户自己设的密码之后，暴力破解的成本低了几个
// 数量级，而登录接口此前完全不限速——局域网里跑字典是分钟级的事。
func TestLoginLocksOutAfterRepeatedFailures(t *testing.T) {
	st := newTestStack(t, func(c *config.Config) {
		c.Server.Bind = "0.0.0.0"
		c.Server.Token = "secret-token"
	})

	// 换一个不是「自己」的地址，才能确定锁的是这个来源而不是本机。
	attempt := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/auth/login",
			strings.NewReader(`{"token":"`+token+`"}`))
		req.RemoteAddr = "192.168.1.9:51324"
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		st.srv.handleLogin(rec, req)
		return rec
	}

	for i := 0; i < loginMaxFailures; i++ {
		if rec := attempt("wrong"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次失败的状态码 = %d，期望 401", i+1, rec.Code)
		}
	}

	// 到了阈值就该被锁：连正确密码也不行，否则限流形同虚设。
	rec := attempt("secret-token")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("锁定期内的状态码 = %d，期望 429", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "次数过多") {
		t.Errorf("锁定提示不明确：%s", truncate(body, 120))
	}
}

// 登录成功清掉失败计数：偶尔输错一次不该累积到锁定。
func TestLoginSuccessResetsFailures(t *testing.T) {
	st := newTestStack(t, func(c *config.Config) {
		c.Server.Bind = "0.0.0.0"
		c.Server.Token = "secret-token"
	})

	ip := "192.168.1.9:51324"
	attempt := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/auth/login",
			strings.NewReader(`{"token":"`+token+`"}`))
		req.RemoteAddr = ip
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		st.srv.handleLogin(rec, req)
		return rec
	}

	for i := 0; i < loginMaxFailures-1; i++ {
		attempt("wrong")
	}
	if rec := attempt("secret-token"); rec.Code != http.StatusOK {
		t.Fatalf("未到阈值时正确密码的状态码 = %d，期望 200", rec.Code)
	}

	// 计数已清零，再错满一轮也还是 401 而不是一上来就 429。
	for i := 0; i < loginMaxFailures-1; i++ {
		if rec := attempt("wrong"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("重置后第 %d 次失败的状态码 = %d，期望 401", i+1, rec.Code)
		}
	}
}

// 锁定只针对那一个来源，别人不受影响。
func TestLoginLockoutIsPerSource(t *testing.T) {
	st := newTestStack(t, func(c *config.Config) {
		c.Server.Bind = "0.0.0.0"
		c.Server.Token = "secret-token"
	})

	attemptFrom := func(remote, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/auth/login",
			strings.NewReader(`{"token":"`+token+`"}`))
		req.RemoteAddr = remote
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		st.srv.handleLogin(rec, req)
		return rec
	}

	for i := 0; i < loginMaxFailures; i++ {
		attemptFrom("192.168.1.9:51324", "wrong")
	}
	if rec := attemptFrom("192.168.1.9:51324", "secret-token"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("被锁来源的状态码 = %d，期望 429", rec.Code)
	}
	if rec := attemptFrom("192.168.1.20:51324", "secret-token"); rec.Code != http.StatusOK {
		t.Fatalf("另一个来源的状态码 = %d，期望 200", rec.Code)
	}
}

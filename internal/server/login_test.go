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

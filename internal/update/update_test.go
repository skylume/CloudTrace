package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		current string
		latest  string
		want    bool
	}{
		{"v1.2.3", "v1.2.4", true},
		{"v1.2.3", "v1.3.0", true},
		{"v1.2.3", "v2.0.0", true},
		{"v1.2.3", "v1.2.3", false},
		{"v1.2.3", "v1.2.2", false},
		// 不带 v 前缀、位数不同都要能比。
		{"1.2", "1.2.1", true},
		{"v1.2", "v1.2.0", false},
		{"v1.10.0", "v1.9.0", false},
		// 预发布后缀只比主版本段。
		{"v1.2.3", "v1.3.0-beta.1", true},
		// 认不出的一律当作「没有更新」：宁可漏报，也不要天天提示用户升级。
		{"dev", "v1.2.4", false},
		{"", "v1.2.4", false},
		{"v1.2.3", "latest", false},
	}
	for _, tt := range cases {
		if got := Newer(tt.current, tt.latest); got != tt.want {
			t.Errorf("Newer(%q, %q) = %v，期望 %v", tt.current, tt.latest, got, tt.want)
		}
	}
}

func TestComparable(t *testing.T) {
	if comparable("dev") {
		t.Error("dev 不该被当成可比较的版本")
	}
	if !comparable("v1.0.0") {
		t.Error("v1.0.0 应当可比较")
	}
}

// 开发版直接跳过，不去打扰网络。
func TestCheckSkipsNonReleaseVersion(t *testing.T) {
	res, err := Check(context.Background(), "dev", nil)
	if err != nil {
		t.Fatalf("跳过不该报错：%v", err)
	}
	if res.Skipped == "" {
		t.Error("应当说明为什么没查")
	}
	if res.HasUpdate {
		t.Error("跳过时不该报有更新")
	}
}

func TestCheckFindsNewerRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// GitHub 对没有 UA 的请求直接 403，这里把这条约定也测住。
		if r.Header.Get("User-Agent") == "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"tag_name": "v9.9.9",
			"html_url": "https://example.com/releases/v9.9.9",
			"body":     "修了几个问题",
		})
	}))
	defer server.Close()

	res, err := checkFrom(context.Background(), "v1.0.0", server.Client(), server.URL)
	if err != nil {
		t.Fatalf("检查失败：%v", err)
	}
	if !res.HasUpdate {
		t.Fatal("应当发现新版本")
	}
	if res.Latest != "v9.9.9" || res.URL == "" || res.Notes == "" {
		t.Errorf("结果不完整：%+v", res)
	}
}

func TestCheckNoUpdateWhenSameVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v1.0.0"})
	}))
	defer server.Close()

	res, err := checkFrom(context.Background(), "v1.0.0", server.Client(), server.URL)
	if err != nil {
		t.Fatalf("检查失败：%v", err)
	}
	if res.HasUpdate {
		t.Error("版本相同不该报有更新")
	}
}

// 服务端出错时返回错误，而不是悄悄当成「没有更新」。
func TestCheckReportsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	if _, err := checkFrom(context.Background(), "v1.0.0", server.Client(), server.URL); err == nil {
		t.Error("服务端出错时应当返回错误")
	}
}

// 缺版本号的响应也算失败，而不是当成「没有更新」。
func TestCheckRejectsEmptyTag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	if _, err := checkFrom(context.Background(), "v1.0.0", server.Client(), server.URL); err == nil {
		t.Error("没有版本号时应当返回错误")
	}
}

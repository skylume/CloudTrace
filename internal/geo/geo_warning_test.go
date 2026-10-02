package geo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// 六种取值的期望行为：只有「非中国、非未知、非 Tor」才提示。
func TestShouldWarn(t *testing.T) {
	cases := map[string]bool{
		"CN":   false,
		"XX":   false,
		"T1":   false,
		"US":   true,
		"JP":   true,
		"SG":   true,
		"":     false,
		"  ":   false,
		" cn":  false,
		" us ": true,
		"T1 ":  false,
	}
	for loc, want := range cases {
		if got := ShouldWarn(loc); got != want {
			t.Errorf("ShouldWarn(%q) = %v，期望 %v", loc, got, want)
		}
	}
}

// traceBody 造一份 trace 响应。
func traceBody(loc string) string {
	return "fl=123abc\nh=www.cloudflare.com\nip=1.2.3.4\nts=1234.5\nvisit_scheme=https\nuag=x\ncolo=HKG\nsliver=none\nhttp=http/2\nloc=" + loc + "\n"
}

func TestDetectExitCountryParsesLoc(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(traceBody("us")))
	}))
	defer ts.Close()

	loc, err := detectExitCountry(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("探测失败：%v", err)
	}
	if loc != "US" {
		t.Fatalf("地区码 = %q，期望归一化成大写 US", loc)
	}
}

// 失败要重试，且重试之后成功就返回成功——网络抖一次不该给出假警告。
func TestDetectExitCountryRetriesUntilSuccess(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(traceBody("JP")))
	}))
	defer ts.Close()

	loc, err := detectExitCountry(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("探测失败：%v", err)
	}
	if loc != "JP" {
		t.Fatalf("地区码 = %q，期望 JP", loc)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("请求次数 = %d，期望 3", got)
	}
}

// 一直失败时给出错误，调用方据此不提示——拿不到信息时静默比乱猜好。
func TestDetectExitCountryFailsAfterRetries(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer ts.Close()

	if _, err := detectExitCountry(context.Background(), ts.URL); err == nil {
		t.Fatal("一直失败应当报错")
	}
	if got := atomic.LoadInt32(&calls); got != traceRetries {
		t.Fatalf("请求次数 = %d，期望 %d", got, traceRetries)
	}
}

func TestDetectExitCountryWithoutLocField(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("fl=123\ncolo=HKG\n"))
	}))
	defer ts.Close()

	if _, err := detectExitCountry(context.Background(), ts.URL); err == nil {
		t.Fatal("响应里没有 loc 时应当报错，而不是当成空地区")
	}
}

func TestDetectExitCountryRejectsGarbageBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>hello</html>"))
	}))
	defer ts.Close()

	if _, err := detectExitCountry(context.Background(), ts.URL); err == nil {
		t.Fatal("非 trace 响应应当报错")
	}
}

func TestDetectExitCountryHonoursCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := detectExitCountry(ctx, "http://127.0.0.1:1/"); err == nil {
		t.Fatal("已取消的上下文应当报错")
	}
}

// 探测失败时调用方不该提示：失败与「确认是代理」是两回事。
func TestDetectionFailureDoesNotWarn(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	loc, err := detectExitCountry(context.Background(), ts.URL)
	if err == nil {
		t.Fatal("应当报错")
	}
	if ShouldWarn(loc) {
		t.Error("探测失败时不该触发提示")
	}
}

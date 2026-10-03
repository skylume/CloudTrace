package server

import (
	"fmt"
	"testing"
	"time"

	"cloudtrace/internal/config"
	"cloudtrace/internal/speed"
	"cloudtrace/internal/task"
)

// 熔断要额外广播一条可操作的通知。
//
// 与错误事件分开的理由：错误只说明「出错了」，而熔断有明确的下一步。界面拿到
// 这条才能把建议做成按钮，而不是让用户自己去设置页里找「并发在哪」。
func TestWSBreakerNoticeCarriesSuggestion(t *testing.T) {
	st := newTestStack(t, func(c *config.Config) {
		c.Speed.Concurrency = 8
		c.Speed.URLMode = "auto"
	})
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	st.svc.Bus.Publish(task.TopicError,
		fmt.Errorf("%w：连续 3 次遇到限速（HTTP 429），已停止测速", speed.ErrRateLimited))

	var payload speedBreakerPayload
	decode(t, readUntil(t, conn, eventSpeedBreaker, 3*time.Second), &payload)

	if payload.Message == "" {
		t.Error("通知必须带上说明，否则用户不知道发生了什么")
	}
	// 带上当前配置值而不是让前端猜：建议「降到多少」得有个起点。
	if payload.Concurrency != 8 {
		t.Errorf("并发 = %d，期望 8（当前配置值）", payload.Concurrency)
	}
	if payload.URLMode != "auto" {
		t.Errorf("测速源模式 = %q，期望 auto", payload.URLMode)
	}
}

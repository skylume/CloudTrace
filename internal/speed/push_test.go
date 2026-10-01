package speed

import (
	"context"
	"testing"
	"time"

	"cloudtrace/internal/model"
)

// 第一条结果必须立刻推出去：测速结果逐个出来，让用户等满一个节流窗口
// 才看到第一行，会以为按钮没生效。
func TestPusherSendsFirstResultImmediately(t *testing.T) {
	clock := newFakeClock()
	rep := newFakeReporter(context.Background())
	p := newPusher(rep, clock.get, 400*time.Millisecond)

	p.add(model.IPRecord{IP: "1.1.1.1"})

	if got := len(rep.partials()); got != 1 {
		t.Fatalf("首次推送 = %d 条，期望立即推送 1 条", got)
	}
}

// 窗口内的结果先攒着，不逐条推送——测速结果多时逐条推会打爆连接。
func TestPusherThrottlesWithinWindow(t *testing.T) {
	clock := newFakeClock()
	rep := newFakeReporter(context.Background())
	p := newPusher(rep, clock.get, 400*time.Millisecond)

	p.add(model.IPRecord{IP: "1.1.1.1"}) // 领先沿：立即推
	p.add(model.IPRecord{IP: "1.1.1.2"})
	p.add(model.IPRecord{IP: "1.1.1.3"})

	if got := len(rep.partials()); got != 1 {
		t.Fatalf("窗口内推送 = %d 条，期望只推了领先沿那 1 条", got)
	}

	// 过了窗口，新结果把攒下的那些一起带出去：一次推 3 条。
	clock.advance(500 * time.Millisecond)
	p.add(model.IPRecord{IP: "1.1.1.4"})

	if got := len(rep.partials()); got != 4 {
		t.Errorf("窗口结束后累计推送 = %d 条，期望 4 条（1 + 攒下的 2 + 新的 1）", got)
	}
	if got := len(rep.events[TopicPartial]); got != 2 {
		t.Errorf("推送批次 = %d，期望 2 批", got)
	}
}

// 收尾必须补发，否则表格永远比实际少几条。
func TestPusherFlushSendsRemainder(t *testing.T) {
	clock := newFakeClock()
	rep := newFakeReporter(context.Background())
	p := newPusher(rep, clock.get, 400*time.Millisecond)

	p.add(model.IPRecord{IP: "1.1.1.1"})
	p.add(model.IPRecord{IP: "1.1.1.2"})
	p.flush()

	if got := len(rep.partials()); got != 2 {
		t.Fatalf("收尾后累计推送 = %d 条，期望 2", got)
	}
	if got := rep.partials()[1].IP; got != "1.1.1.2" {
		t.Errorf("补发的记录 = %q，期望 1.1.1.2", got)
	}
}

// 缓冲区为空时收尾不发空事件。
func TestPusherFlushWithoutBufferEmitsNothing(t *testing.T) {
	rep := newFakeReporter(context.Background())
	p := newPusher(rep, newFakeClock().get, time.Second)

	p.flush()
	if len(rep.events[TopicPartial]) != 0 {
		t.Errorf("空缓冲区不应推送，实际 %d 条", len(rep.events[TopicPartial]))
	}
}

func TestNewPusherDefaults(t *testing.T) {
	rep := newFakeReporter(context.Background())

	if got := newPusher(rep, nil, 0).interval; got != partialInterval {
		t.Errorf("间隔 = %v，期望回落到 %v", got, partialInterval)
	}
	if got := newPusher(rep, nil, 0).now; got == nil {
		t.Error("取时函数应当补上默认实现")
	}
}

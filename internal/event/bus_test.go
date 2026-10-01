package event

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waitFor 轮询等待条件成立，避免用固定 sleep 造成偶发失败。
func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal(msg)
}

func TestSubscribeRejectsBadInput(t *testing.T) {
	bus := New(1)
	if _, err := bus.Subscribe("", func(any) {}); err == nil {
		t.Fatal("空 topic 应报错")
	}
	if _, err := bus.Subscribe("t", nil); err == nil {
		t.Fatal("nil handler 应报错")
	}
}

func TestSubscribeThenUnsubscribeStopsDelivery(t *testing.T) {
	bus := New(8)

	var got atomic.Int64
	unsubscribe, err := bus.Subscribe("t", func(any) { got.Add(1) })
	if err != nil {
		t.Fatal(err)
	}

	bus.Publish("t", 1)
	waitFor(t, time.Second, func() bool { return got.Load() == 1 }, "订阅后未收到事件")

	unsubscribe()
	if n := bus.Subscribers("t"); n != 0 {
		t.Fatalf("取消后订阅数应为 0，实际 %d", n)
	}

	// 取消之后再发布，回调绝不能再被触发。
	bus.Publish("t", 2)
	time.Sleep(50 * time.Millisecond)
	if n := got.Load(); n != 1 {
		t.Fatalf("取消订阅后仍收到事件，累计 %d 次", n)
	}
}

func TestUnsubscribeIsIdempotent(t *testing.T) {
	bus := New(4)
	unsubscribe, err := bus.Subscribe("t", func(any) {})
	if err != nil {
		t.Fatal(err)
	}
	unsubscribe()
	unsubscribe() // 重复调用不得 panic
	unsubscribe()
}

func TestPublishNeverBlocksAndDropsOldest(t *testing.T) {
	bus := New(2)

	release := make(chan struct{})
	var received atomic.Int64

	if _, err := bus.Subscribe("t", func(any) {
		received.Add(1)
		<-release // 卡住消费者，人为制造队列积压
	}); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			bus.Publish("t", i)
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish 被慢订阅者阻塞了")
	}

	if bus.Dropped("t") == 0 {
		t.Fatal("队列满时应丢弃最旧事件并计数")
	}
	close(release)
}

func TestHandlerPanicDoesNotAffectOthers(t *testing.T) {
	bus := New(8)

	if _, err := bus.Subscribe("t", func(any) { panic("订阅者内部崩溃") }); err != nil {
		t.Fatal(err)
	}
	delivered := make(chan struct{}, 1)
	if _, err := bus.Subscribe("t", func(any) {
		select {
		case delivered <- struct{}{}:
		default:
		}
	}); err != nil {
		t.Fatal(err)
	}

	bus.Publish("t", 1)

	select {
	case <-delivered:
	case <-time.After(2 * time.Second):
		t.Fatal("一个订阅者 panic 影响了其他订阅者")
	}
}

func TestTopicsAndSubscriberCount(t *testing.T) {
	bus := New(4)
	if bus.Subscribers("nobody") != 0 {
		t.Fatal("未订阅的 topic 订阅数应为 0")
	}
	if _, err := bus.Subscribe("a", func(any) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.Subscribe("a", func(any) {}); err != nil {
		t.Fatal(err)
	}
	if n := bus.Subscribers("a"); n != 2 {
		t.Fatalf("订阅数应为 2，实际 %d", n)
	}
	if len(bus.Topics()) != 1 {
		t.Fatalf("应只有 1 个 topic，实际 %v", bus.Topics())
	}
}

func TestCloseStopsAllSubscriptions(t *testing.T) {
	bus := New(4)

	var got atomic.Int64
	if _, err := bus.Subscribe("t", func(any) { got.Add(1) }); err != nil {
		t.Fatal(err)
	}
	bus.Publish("t", 1)
	waitFor(t, time.Second, func() bool { return got.Load() == 1 }, "关闭前未收到事件")

	done := make(chan struct{})
	go func() {
		bus.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close 未能及时返回，说明订阅者 goroutine 没有退出")
	}

	if n := bus.Subscribers("t"); n != 0 {
		t.Fatalf("关闭后订阅数应为 0，实际 %d", n)
	}
	bus.Publish("t", 2)
	time.Sleep(20 * time.Millisecond)
	if n := got.Load(); n != 1 {
		t.Fatalf("关闭后仍收到事件，累计 %d 次", n)
	}
}

func TestConcurrentPublishSubscribeUnsubscribe(t *testing.T) {
	bus := New(16)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				bus.Publish("t", j)
			}
		}()
	}
	for i := 0; i < 4; i++ {
		unsubscribe, err := bus.Subscribe("t", func(any) {})
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(5 * time.Millisecond)
			unsubscribe()
		}()
	}
	wg.Wait()
}

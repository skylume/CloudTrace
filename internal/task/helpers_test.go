package task

import (
	"sync"
	"testing"
	"time"

	"cloudtrace/internal/event"
	"cloudtrace/internal/model"
)

// recorder 收集事件总线上的事件，供断言使用。
//
// 总线是异步投递的，所以断言前必须等事件到达，不能写完就读。
type recorder struct {
	mu     sync.Mutex
	events map[string][]any
}

func newRecorder(t *testing.T, bus *event.Bus, topics ...string) *recorder {
	t.Helper()
	r := &recorder{events: make(map[string][]any)}
	for _, topic := range topics {
		name := topic
		if _, err := bus.Subscribe(name, func(payload any) {
			r.mu.Lock()
			r.events[name] = append(r.events[name], payload)
			r.mu.Unlock()
		}); err != nil {
			t.Fatalf("订阅 %s 失败：%v", name, err)
		}
	}
	return r
}

func (r *recorder) count(topic string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events[topic])
}

func (r *recorder) last(topic string) any {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := r.events[topic]
	if len(list) == 0 {
		return nil
	}
	return list[len(list)-1]
}

// waitCount 等到 topic 上的事件数达到 want，或超时。
func (r *recorder) waitCount(t *testing.T, topic string, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if r.count(topic) >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("等待 %s 事件达到 %d 条超时，实际 %d 条", topic, want, r.count(topic))
}

// waitStatus 等到任务快照进入指定状态，或超时。
func waitStatus(t *testing.T, m *Manager, want string, timeout time.Duration) model.TaskState {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if st := m.Snapshot(); st.Status == want {
			return st
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("等待状态变为 %s 超时，当前 %+v", want, m.Snapshot())
	return model.TaskState{}
}

// waitDone 等到快照里的已完成数达到 want，或超时。
func waitDone(t *testing.T, m *Manager, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if st := m.Snapshot(); st.Done >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("等待已完成数达到 %d 超时，当前 %d", want, m.Snapshot().Done)
}

// closeTo 判断两个浮点数是否足够接近。
func closeTo(got, want float64) bool {
	diff := got - want
	if diff < 0 {
		diff = -diff
	}
	return diff < 1e-6
}

// newTestManager 构造一个节流间隔较短的编排器，便于测试。
func newTestManager(t *testing.T) (*Manager, *event.Bus) {
	t.Helper()
	bus := event.New(event.DefaultQueueSize)
	t.Cleanup(bus.Close)

	m, err := NewManager(bus, nil, Options{ProgressInterval: time.Millisecond})
	if err != nil {
		t.Fatalf("构造编排器失败：%v", err)
	}
	return m, bus
}

package event

import (
	"errors"
	"sync"
	"sync/atomic"
)

// DefaultQueueSize 是每个订阅者的默认队列长度。
//
// 队列满即丢弃最旧事件（并计数），保证发布方永不阻塞。
const DefaultQueueSize = 256

// Handler 是订阅者回调；payload 的具体类型由各 topic 自行约定。
type Handler func(payload any)

// Bus 是事件总线。
//
// 零值不可用，请用 New 构造。Bus 可安全并发使用。
type Bus struct {
	mu      sync.RWMutex
	subs    map[string]map[uint64]*subscription
	nextID  uint64
	queueSz int
}

// New 构造一个事件总线；queueSize <= 0 时使用 DefaultQueueSize。
func New(queueSize int) *Bus {
	if queueSize <= 0 {
		queueSize = DefaultQueueSize
	}
	return &Bus{
		subs:    make(map[string]map[uint64]*subscription),
		queueSz: queueSize,
	}
}

// Subscribe 订阅 topic，返回取消函数。
//
// 取消函数可安全重复调用（幂等）。取消返回后，**再发布的事件不会再回调 fn**。
// 注意：fn 内不要调用自身的取消函数（会自锁）。
func (b *Bus) Subscribe(topic string, fn Handler) (func(), error) {
	if topic == "" {
		return nil, errors.New("event: topic 不能为空")
	}
	if fn == nil {
		return nil, errors.New("event: handler 不能为 nil")
	}

	b.mu.Lock()
	b.nextID++
	s := &subscription{
		id:    b.nextID,
		topic: topic,
		fn:    fn,
		ch:    make(chan any, b.queueSz),
		quit:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	if b.subs[topic] == nil {
		b.subs[topic] = make(map[uint64]*subscription)
	}
	b.subs[topic][s.id] = s
	b.mu.Unlock()

	go s.run()

	return func() { b.unsubscribe(topic, s.id) }, nil
}

// Publish 向 topic 的所有订阅者投递 payload。
//
// 永不阻塞：队列满的订阅者会丢弃最旧事件并计数（可用 Dropped 观察）。
func (b *Bus) Publish(topic string, payload any) {
	b.mu.RLock()
	var list []*subscription
	if subs := b.subs[topic]; len(subs) > 0 {
		list = make([]*subscription, 0, len(subs))
		for _, s := range subs {
			list = append(list, s)
		}
	}
	b.mu.RUnlock()

	for _, s := range list {
		s.enqueue(payload)
	}
}

// Subscribers 返回 topic 当前的订阅者数量（用于自检与测试）。
func (b *Bus) Subscribers(topic string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs[topic])
}

// Topics 返回当前有订阅者的 topic 列表。
func (b *Bus) Topics() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]string, 0, len(b.subs))
	for t := range b.subs {
		out = append(out, t)
	}
	return out
}

// Close 取消所有订阅并等待各自 goroutine 退出。
//
// 供进程优雅退出时调用；调用后 Bus 仍可继续 Subscribe。
func (b *Bus) Close() {
	b.mu.Lock()
	all := make([]*subscription, 0, len(b.subs))
	for _, subs := range b.subs {
		for _, s := range subs {
			all = append(all, s)
		}
	}
	b.subs = make(map[string]map[uint64]*subscription)
	b.mu.Unlock()

	for _, s := range all {
		s.stop()
	}
	for _, s := range all {
		<-s.done
	}
}

func (b *Bus) unsubscribe(topic string, id uint64) {
	b.mu.Lock()
	var s *subscription
	if subs := b.subs[topic]; subs != nil {
		s = subs[id]
		delete(subs, id)
		if len(subs) == 0 {
			delete(b.subs, topic)
		}
	}
	b.mu.Unlock()

	if s == nil {
		return
	}
	s.stop()
}

// subscription 是单个订阅者：独立队列 + 独立消费 goroutine。
type subscription struct {
	id    uint64
	topic string
	fn    Handler
	ch    chan any
	quit  chan struct{}
	done  chan struct{}

	stopOnce sync.Once
	closed   atomic.Bool
	dropped  atomic.Uint64
}

func (s *subscription) run() {
	defer close(s.done)
	for {
		// 优先检查退出信号，避免退出后仍消费队列。
		select {
		case <-s.quit:
			return
		default:
		}

		select {
		case <-s.quit:
			return
		case p := <-s.ch:
			s.deliver(p)
		}
	}
}

func (s *subscription) deliver(p any) {
	if s.closed.Load() {
		return
	}
	defer func() {
		// 单个订阅者 panic 不影响其他订阅者，也不影响任务线程。
		_ = recover()
	}()
	s.fn(p)
}

// enqueue 非阻塞投递；队列满时丢弃最旧事件并计数。
func (s *subscription) enqueue(p any) {
	if s.closed.Load() {
		return
	}
	select {
	case s.ch <- p:
		return
	default:
	}

	// 队列已满：先丢弃最旧的一条，再尝试写入最新的一条。
	select {
	case <-s.ch:
		s.dropped.Add(1)
	default:
	}
	select {
	case s.ch <- p:
	default:
		s.dropped.Add(1)
	}
}

func (s *subscription) stop() {
	s.stopOnce.Do(func() {
		s.closed.Store(true)
		close(s.quit)
	})
}

// Dropped 返回该订阅者因队列满而丢弃的事件数。
func (s *subscription) Dropped() uint64 { return s.dropped.Load() }

// Dropped 返回 topic 下所有订阅者累计丢弃的事件数（用于诊断）。
func (b *Bus) Dropped(topic string) uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var total uint64
	for _, s := range b.subs[topic] {
		total += s.dropped.Load()
	}
	return total
}

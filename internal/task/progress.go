package task

import (
	"sync"
	"time"
)

// ProgressInterval 是进度事件的默认节流间隔。
//
// 逐项推进会产生成百上千次更新，不节流会打爆 WebSocket 连接、把前端渲染
// 拖垮。250ms 是「看起来是实时的」与「推送量可控」之间的折中：人眼察觉
// 不到这个延迟，而推送频率被压到每秒 4 次。
const ProgressInterval = 250 * time.Millisecond

// progressTracker 给进度回调做节流，并保证结束时补发最终值。
//
// 采用领先节流（首次立即发、之后按间隔发）：进度条的第一格必须马上出现，
// 否则用户会以为按钮没点动。
//
// 收尾单独补发是必需的：按间隔节流最常见的坑就是把最后一次更新吃掉，
// 于是进度条永远停在 99%，用户以为任务卡住了。
type progressTracker struct {
	mu       sync.Mutex
	interval time.Duration
	last     time.Time
	fired    int
	sent     bool
}

func newProgressTracker(interval time.Duration) *progressTracker {
	if interval <= 0 {
		interval = ProgressInterval
	}
	return &progressTracker{interval: interval}
}

// report 按节流间隔上报进度；间隔内的调用被丢弃。
//
// 回调在锁外执行：它可能要去序列化并推送事件，占着锁会把 worker 一起
// 堵住。
func (p *progressTracker) report(done, total int, fn func(int, int)) {
	if fn == nil {
		return
	}

	p.mu.Lock()
	now := time.Now()
	if p.sent && now.Sub(p.last) < p.interval {
		p.mu.Unlock()
		return
	}
	p.sent = true
	p.last = now
	p.fired = done
	p.mu.Unlock()

	fn(done, total)
}

// flush 补发最终值；已经上报过同一个值时不重复发。
func (p *progressTracker) flush(done, total int, fn func(int, int)) {
	if fn == nil {
		return
	}

	p.mu.Lock()
	if p.sent && p.fired == done {
		p.mu.Unlock()
		return
	}
	p.sent = true
	p.fired = done
	p.mu.Unlock()

	fn(done, total)
}

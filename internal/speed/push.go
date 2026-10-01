package speed

import (
	"sync"
	"time"

	"cloudtrace/internal/model"
	"cloudtrace/internal/task"
)

// partialInterval 是测速结果的推送节流间隔。
//
// 比进度的 250ms 宽：测速结果本身就是逐个出来的，推得太密只是让前端反复
// 重排表格，用户看不出区别。
const partialInterval = 400 * time.Millisecond

// pusher 把测速结果攒起来，按间隔分批推给前端。
//
// 收尾必须 flush：节流最常见的坑就是把最后一批吃掉，用户看到的表格永远
// 比实际少几条。
type pusher struct {
	rep      task.Reporter
	now      func() time.Time
	interval time.Duration

	mu     sync.Mutex
	buffer []model.IPRecord
	last   time.Time
}

func newPusher(rep task.Reporter, now func() time.Time, interval time.Duration) *pusher {
	if now == nil {
		now = time.Now
	}
	if interval <= 0 {
		interval = partialInterval
	}
	return &pusher{rep: rep, now: now, interval: interval}
}

// add 收下一条结果；距上次推送已满一个间隔时立即推出去。
func (p *pusher) add(rec model.IPRecord) {
	p.mu.Lock()
	p.buffer = append(p.buffer, rec)
	due := p.last.IsZero() || p.now().Sub(p.last) >= p.interval
	var chunk []model.IPRecord
	if due {
		chunk = p.take()
	}
	p.mu.Unlock()

	if chunk != nil {
		p.emit(chunk)
	}
}

// flush 把剩余结果推出去。
func (p *pusher) flush() {
	p.mu.Lock()
	chunk := p.take()
	p.mu.Unlock()

	if chunk != nil {
		p.emit(chunk)
	}
}

// take 取出缓冲区里的全部结果并重置计时；调用方需持有锁。
func (p *pusher) take() []model.IPRecord {
	if len(p.buffer) == 0 {
		return nil
	}
	chunk := p.buffer
	p.buffer = nil
	p.last = p.now()
	return chunk
}

func (p *pusher) emit(chunk []model.IPRecord) {
	p.rep.Emit(TopicPartial, chunk)
}

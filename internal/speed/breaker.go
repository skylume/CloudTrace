// Package speed 执行下载测速任务：选源、并发下载、限流熔断、提前收敛。
//
// 与扫描一样，网络原语全部由调用方注入，本包不持有包级可变状态，因此
// 离线测试能复现出同一批结果。
package speed

import (
	"fmt"
	"sync"
)

// Breaker 是下载源的限流熔断计数器。
//
// 计的是**连续**次数而不是累计：偶尔撞上一次限流是正常的，把它累计起来
// 会让一轮测速中途无故熔断；反过来，连续撞上说明源已经把我们限住了，
// 这时继续测出来的速度全是假的，停比测有价值。
//
// 任意一次成功即清零，因为成功本身证明限流已经解除。
type Breaker struct {
	threshold int

	mu      sync.Mutex
	streak  int
	tripped bool
}

// NewBreaker 构造熔断计数器。threshold <= 0 时按 1 处理。
func NewBreaker(threshold int) *Breaker {
	if threshold <= 0 {
		threshold = 1
	}
	return &Breaker{threshold: threshold}
}

// Success 记一次成功，把连续计数清零。
func (b *Breaker) Success() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.streak = 0
}

// RateLimited 记一次限流，并报告是否已触发熔断。
func (b *Breaker) RateLimited() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.streak++
	if b.streak >= b.threshold {
		b.tripped = true
	}
	return b.tripped
}

// Tripped 报告是否已熔断。
func (b *Breaker) Tripped() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.tripped
}

// Streak 返回当前连续限流次数。
func (b *Breaker) Streak() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.streak
}

// Message 返回给用户看的熔断说明。
//
// 必须说清「停下来了」和「为什么」，不能只报一个错误码：用户看到测速提前
// 结束，第一反应是程序坏了。
func (b *Breaker) Message() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return fmt.Sprintf("连续 %d 次遇到限速（HTTP 429），已停止测速以避免拿到不准确的速度",
		b.threshold)
}

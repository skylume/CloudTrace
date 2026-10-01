package speed

import (
	"strings"
	"testing"
)

// 计的是「连续」次数：偶尔撞上一次限流是正常的，累计计数会让一轮测速
// 中途无故熔断。
func TestBreakerCountsConsecutiveRateLimits(t *testing.T) {
	b := NewBreaker(3)

	if b.RateLimited() {
		t.Fatal("第 1 次限流就熔断了")
	}
	if b.RateLimited() {
		t.Fatal("第 2 次限流就熔断了")
	}
	if !b.RateLimited() {
		t.Fatal("连续第 3 次限流未熔断")
	}
	if !b.Tripped() {
		t.Error("熔断后 Tripped 应为 true")
	}
	if b.Streak() != 3 {
		t.Errorf("连续次数 = %d，期望 3", b.Streak())
	}
}

// 任意一次成功即清零：成功本身证明限流已经解除。
func TestBreakerSuccessResetsStreak(t *testing.T) {
	b := NewBreaker(3)

	b.RateLimited()
	b.RateLimited()
	b.Success()
	if b.Streak() != 0 {
		t.Fatalf("成功后的连续次数 = %d，期望 0", b.Streak())
	}

	// 清零后重新计数：再连撞两次不该熔断。
	if b.RateLimited() || b.RateLimited() {
		t.Error("清零后连撞两次不应熔断")
	}
	if b.Tripped() {
		t.Error("不应处于熔断状态")
	}
}

func TestBreakerThresholdBounds(t *testing.T) {
	// 阈值 <= 0 按 1 处理：等于「第一次限流就停」，是这里最保守的行为。
	if b := NewBreaker(0); !b.RateLimited() {
		t.Error("阈值为 0 时应立即熔断")
	}
	if b := NewBreaker(-5); !b.RateLimited() {
		t.Error("阈值为负时应立即熔断")
	}
}

// 熔断文案要说清「停了」和「为什么」，只说错误码用户会以为程序坏了。
func TestBreakerMessage(t *testing.T) {
	msg := NewBreaker(3).Message()
	for _, want := range []string{"连续 3 次", "429", "已停止测速"} {
		if !strings.Contains(msg, want) {
			t.Errorf("熔断文案 %q 缺少 %q", msg, want)
		}
	}
}

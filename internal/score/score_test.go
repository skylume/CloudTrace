package score

import (
	"math"
	"testing"
)

// 对照算法规格给出的期望值：公式里的每一处常数都有人工算过的答案。
func TestScoreExpectedValues(t *testing.T) {
	unit := Weights{Speed: 1, Latency: 1}

	tests := []struct {
		name    string
		speed   float64
		latency float64
		jitter  float64
		w       Weights
		want    float64
	}{
		{"10MB/s 配 100ms", 10, 100, 0, unit, 10 / 1.1},
		{"延迟为零时不被分母吃掉", 10, 0, 0, unit, 10},
		{"没测出速度", 0, 50, 0, unit, 0},
		{"抖动项按权重参与分母", 10, 100, 50, Weights{Speed: 1, Latency: 1, Jitter: 1}, 10 / 1.15},
		{"抖动权重为零时不影响评分", 10, 100, 50, unit, 10 / 1.1},
		{"速度权重加倍", 10, 100, 0, Weights{Speed: 2, Latency: 1}, 20 / 1.1},
		{"延迟权重加倍", 10, 100, 0, Weights{Speed: 1, Latency: 2}, 10 / 1.2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Score(tc.speed, tc.latency, tc.jitter, tc.w)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("Score(%v, %v, %v, %+v) = %v，期望 %v",
					tc.speed, tc.latency, tc.jitter, tc.w, got, tc.want)
			}
		})
	}
}

// 不可达用负哨兵表示，它比任何正阈值都小；只比大小的话「完全不可达」
// 会被当成「极快」排到最前面。
func TestScoreRejectsUnreachable(t *testing.T) {
	unit := Weights{Speed: 1, Latency: 1}
	if got := Score(100, -1, 0, unit); got != 0 {
		t.Errorf("不可达记录的评分 = %v，期望 0", got)
	}
}

func TestScoreRejectsInvalidWeights(t *testing.T) {
	tests := []struct {
		name string
		w    Weights
	}{
		{"速度权重为负", Weights{Speed: -1, Latency: 1}},
		{"延迟权重为负到分母归零", Weights{Speed: 1, Latency: -10}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Score(10, 100, 0, tc.w); got != 0 {
				t.Errorf("权重 %+v 下的评分 = %v，期望 0", tc.w, got)
			}
		})
	}
}

// 未测速（speed = 0）与不可达都必须落回 0，且 0 是「不参与排序」的标记。
func TestScoreNeverReturnsNegative(t *testing.T) {
	for _, w := range []Weights{
		{Speed: 1, Latency: 1},
		{Speed: 0.5, Latency: 3, Jitter: 2},
		{Speed: -1},
	} {
		for _, speed := range []float64{-5, 0, 0.001, 10, 1000} {
			for _, latency := range []float64{-1, 0, 1, 5000} {
				if got := Score(speed, latency, 3, w); got < 0 {
					t.Errorf("Score(%v, %v, 3, %+v) = %v，评分不应为负", speed, latency, w, got)
				}
			}
		}
	}
}

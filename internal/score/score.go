// Package score 计算测速结果的综合评分。
//
// 评分只做一件事：把速度与延迟折成一个可排序的数值。它不参与任何网络
// 交互，也不持有状态，因此可以逐条对照期望值验证。
package score

// Weights 是综合评分的权重。
//
// 抖动项默认权重为 0（即不参与），保留它是为了让「延迟抖动大的节点排在
// 后面」这种偏好可以只调配置就生效，不必改算法。
type Weights struct {
	Speed   float64
	Latency float64
	Jitter  float64
}

// Score 计算综合评分：速度越高越好，延迟与抖动越低越好。
//
// 公式为 speed × W_speed / (1 + W_latency × 延迟秒 + W_jitter × 抖动秒)。
// 分母加 1 而不是直接乘延迟，是为了让「延迟接近 0」的节点不会得到无穷大的
// 评分——那会让排序被一个测不准的小数点后几位左右。
//
// 返回 0 的两种情形都表示「不该参与排序」：
//   - 没测出速度（speed <= 0）：0 在结果里表示「没测过」，不是「很慢」；
//   - 延迟是哨兵值（latency < 0）：不可达的记录不能因为延迟数值小就排到前面。
//
// 权重取负或分母被压到非正时同样返回 0：那属于配置错误，给一个能被识别的
// 结果比给出一个方向相反的排名安全。
func Score(speedMBps, latencyAvgMs, jitterMs float64, w Weights) float64 {
	if speedMBps <= 0 || latencyAvgMs < 0 {
		return 0
	}

	denom := 1 + w.Latency*latencyAvgMs/1000 + w.Jitter*jitterMs/1000
	if denom <= 0 {
		return 0
	}

	value := w.Speed * speedMBps / denom
	if value <= 0 {
		return 0
	}
	return value
}

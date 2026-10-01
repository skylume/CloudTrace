package scan

import (
	"sync"

	"cloudtrace/internal/model"
)

// Stage 是漏斗的四个级别。
type Stage int

const (
	// StageGenerated 候选池生成完毕。
	StageGenerated Stage = iota
	// StageLatencyOK 延迟达标。
	StageLatencyOK
	// StageRegionOK 归属地解析成功。
	StageRegionOK
	// StageUsable 最终可用。
	StageUsable
)

// Funnel 是扫描漏斗：生成 → 延迟达标 → 地区解析 → 可用。
//
// 每次变更立刻上报，而不是等流程结束再一次性算：漏斗的意义就是让用户
// 看到「卡在哪一级」，事后算出来的数字帮不上忙。
//
// 各级的包含关系是：
//
//	生成 ⊇ 延迟达标 ⊇ 地区解析
//	生成 ⊇ 延迟达标 ⊇ 可用
//
// 「地区解析」与「可用」是并列的两条线，不是一条链：前者衡量地区信息的
// 覆盖度，后者是最终产出。地区没解析出来不等于结果不可用——把两者串成
// 链，关掉明细采集时（拿不到地区）就会把结果全数抹掉。
//
// 越界时向下钳制：万一某处统计口径出错，宁可显示成不增长，也不要出现
// 「可用 300 / 延迟达标 100」这种自相矛盾的界面。
type Funnel struct {
	mu       sync.Mutex
	counts   model.Funnel
	onChange func(model.Funnel)
}

// NewFunnel 构造漏斗；onChange 在每次变更后被调用，可为 nil。
//
// 回调在锁外执行：它要去推送事件，占着锁会把并发的统计一起堵住。
func NewFunnel(onChange func(model.Funnel)) *Funnel {
	return &Funnel{onChange: onChange}
}

// Set 写入某一级的计数，并把受它约束的级别钳到不超过它。
func (f *Funnel) Set(stage Stage, n int) model.Funnel {
	if n < 0 {
		n = 0
	}

	f.mu.Lock()
	switch stage {
	case StageGenerated:
		f.counts.Generated = n
	case StageLatencyOK:
		f.counts.LatencyOK = n
	case StageRegionOK:
		f.counts.RegionOK = n
	case StageUsable:
		f.counts.Usable = n
	}
	// 上一级可能刚被调小，受它约束的级别跟着收回来。
	f.counts.LatencyOK = min(f.counts.LatencyOK, f.counts.Generated)
	f.counts.RegionOK = min(f.counts.RegionOK, f.counts.LatencyOK)
	f.counts.Usable = min(f.counts.Usable, f.counts.LatencyOK)

	snapshot := f.counts
	onChange := f.onChange
	f.mu.Unlock()

	if onChange != nil {
		onChange(snapshot)
	}
	return snapshot
}

// Snapshot 返回当前计数。
func (f *Funnel) Snapshot() model.Funnel {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.counts
}

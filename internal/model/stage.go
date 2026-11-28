package model

// 流水线阶段标识。
//
// 前端按名字映射文案；新增阶段时两边一起改，未知名字前端回退显示原值。
const (
	StagePool      = "pool"      // 生成候选池
	StageFilter    = "filter"    // 前置过滤
	StageProbe     = "probe"     // 延迟探测
	StageCollect   = "collect"   // 节点校验与明细采集
	StageUsability = "usability" // 测速前的可用性校验
	StageMeasure   = "measure"   // 下载测速
)

// StageStat 是一个流水线阶段的统计。
//
// Done 定死为 OK + Failed + Skipped：进入阶段的目标最终必然落在其中一类，
// 前端据 Done 画阶段进度，不必再自己相加。Skipped 是「被跳过」而非失败，
// 例如已测过的节点、提前收敛或熔断后没有派发的目标。
type StageStat struct {
	Name    string  `json:"name"`
	Total   int     `json:"total"`
	OK      int     `json:"ok"`
	Failed  int     `json:"failed"`
	Skipped int     `json:"skipped,omitempty"`
	Done    int     `json:"done"`
	Elapsed float64 `json:"elapsed_s,omitempty"`
}

// NewStageStat 汇总一个阶段的统计，Done 由三类结果相加得出。
func NewStageStat(name string, total, ok, failed, skipped int, elapsed float64) StageStat {
	return StageStat{
		Name:    name,
		Total:   total,
		OK:      ok,
		Failed:  failed,
		Skipped: skipped,
		Done:    ok + failed + skipped,
		Elapsed: elapsed,
	}
}

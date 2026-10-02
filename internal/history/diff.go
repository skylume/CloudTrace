package history

import (
	"sort"
	"strconv"
	"strings"

	"cloudtrace/internal/model"
)

// 变化原因，出现在 ChangedEntry.Reasons 里。
const (
	ReasonLatency = "latency"
	ReasonScore   = "score"
	ReasonRegion  = "region"
)

// DiffOptions 是变化判定的阈值。
//
// 阈值存在是因为两次扫描之间总会有抖动：延迟差个一两毫秒、评分差 1%，
// 那是网络噪声而不是「变了」。不做阈值过滤的话，两次结果会显示成「几乎
// 全部有变化」，对比就失去了意义。
type DiffOptions struct {
	// LatencyAbsMS 是延迟变化的绝对下限（毫秒）。
	LatencyAbsMS float64
	// LatencyPct 是延迟变化的相对下限（相对基准侧）。
	LatencyPct float64
	// ScorePct 是评分变化的相对下限（相对基准侧）。
	ScorePct float64
}

// DefaultDiffOptions 返回默认阈值：延迟 20ms 或 20%，评分 20%。
func DefaultDiffOptions() DiffOptions {
	return DiffOptions{LatencyAbsMS: 20, LatencyPct: 0.2, ScorePct: 0.2}
}

// ChangedEntry 是同一目标在两次结果之间的差异。
type ChangedEntry struct {
	Key  string `json:"key"`
	IP   string `json:"ip"`
	Port int    `json:"port"`

	LatencyA     float64 `json:"latency_a"`
	LatencyB     float64 `json:"latency_b"`
	LatencyDelta float64 `json:"latency_delta"`

	ScoreA float64 `json:"score_a"`
	ScoreB float64 `json:"score_b"`

	ColoA string `json:"colo_a"`
	ColoB string `json:"colo_b"`

	// Reasons 说明这条为什么被判为有变化，取值见 Reason* 常量。
	Reasons []string `json:"reasons"`
}

// HistoryDiff 是两次历史记录的对比结果。
type HistoryDiff struct {
	AddedCount     int `json:"added_count"`
	RemovedCount   int `json:"removed_count"`
	ChangedCount   int `json:"changed_count"`
	UnchangedCount int `json:"unchanged_count"`

	AvgLatencyA  float64 `json:"avg_latency_a"`
	AvgLatencyB  float64 `json:"avg_latency_b"`
	LatencyDelta float64 `json:"latency_delta"`

	BestChanged bool   `json:"best_changed"`
	BestA       string `json:"best_a"`
	BestB       string `json:"best_b"`

	// RegionDelta 是各地区数量的变化（B 减 A），只列出有变化的部分。
	RegionDelta map[string]int `json:"region_delta"`

	Added   []model.IPRecord `json:"added"`
	Removed []model.IPRecord `json:"removed"`
	Changed []ChangedEntry   `json:"changed"`
}

// Diff 对比两次历史记录。
//
// 键取 `IP:端口` 而不是只用 IP：同一台机器的 443 与 8443 是两个不同的
// 目标，按 IP 合并会把「换了端口」误判成「没变」。端口相同的两次对比
// 结果与按 IP 合并完全一致。
//
// 全部计算在内存里完成，结果不落盘。
func Diff(a, b HistoryRecord, opts DiffOptions) HistoryDiff {
	opts = opts.withDefaults()

	indexA := indexByKey(a.Results)
	indexB := indexByKey(b.Results)

	diff := HistoryDiff{
		AvgLatencyA: avgLatency(a.Results),
		AvgLatencyB: avgLatency(b.Results),
		RegionDelta: map[string]int{},
		BestA:       bestKey(a.Results),
		BestB:       bestKey(b.Results),
	}
	diff.LatencyDelta = diff.AvgLatencyB - diff.AvgLatencyA
	diff.BestChanged = diff.BestA != diff.BestB

	for _, key := range sortedKeys(indexA) {
		recA := indexA[key]
		recB, ok := indexB[key]
		if !ok {
			diff.Removed = append(diff.Removed, recA)
			continue
		}
		if entry, changed := compare(key, recA, recB, opts); changed {
			diff.Changed = append(diff.Changed, entry)
			continue
		}
		diff.UnchangedCount++
	}
	for _, key := range sortedKeys(indexB) {
		if _, ok := indexA[key]; !ok {
			diff.Added = append(diff.Added, indexB[key])
		}
	}

	diff.AddedCount = len(diff.Added)
	diff.RemovedCount = len(diff.Removed)
	diff.ChangedCount = len(diff.Changed)
	diff.RegionDelta = regionDelta(a.Results, b.Results)
	return diff
}

// compare 判断同一目标是否算「有变化」，并给出原因。
func compare(key string, a, b model.IPRecord, opts DiffOptions) (ChangedEntry, bool) {
	e := ChangedEntry{
		Key:          key,
		IP:           a.IP,
		Port:         a.Port,
		LatencyA:     a.LatencyAvg,
		LatencyB:     b.LatencyAvg,
		LatencyDelta: b.LatencyAvg - a.LatencyAvg,
		ScoreA:       a.Score,
		ScoreB:       b.Score,
		ColoA:        a.Colo,
		ColoB:        b.Colo,
	}

	if latencyChanged(a, b, opts) {
		e.Reasons = append(e.Reasons, ReasonLatency)
	}
	if scoreChanged(a.Score, b.Score, opts.ScorePct) {
		e.Reasons = append(e.Reasons, ReasonScore)
	}
	if regionChanged(a, b) {
		e.Reasons = append(e.Reasons, ReasonRegion)
	}
	return e, len(e.Reasons) > 0
}

// regionChanged 判断归属地是否变了。
//
// 比较忽略大小写与首尾空白：数据中心代码的大小写取决于它是从响应头还是
// 从响应体里读出来的，同一次扫描里两种写法都可能出现，把它当成「换了地区」
// 会制造大量假变化。
func regionChanged(a, b model.IPRecord) bool {
	return !strings.EqualFold(strings.TrimSpace(a.Colo), strings.TrimSpace(b.Colo)) ||
		!strings.EqualFold(strings.TrimSpace(a.Loc), strings.TrimSpace(b.Loc))
}

// latencyChanged 判断延迟变化是否超过阈值。
//
// 一侧可达、另一侧不可达时直接算变化：那不是「差得多」，而是从能用变成
// 不能用，任何百分比阈值都不该把它过滤掉。
func latencyChanged(a, b model.IPRecord, opts DiffOptions) bool {
	reachA, reachB := a.Reachable(), b.Reachable()
	if reachA != reachB {
		return true
	}
	if !reachA {
		return false
	}
	limit := opts.LatencyAbsMS
	if pct := opts.LatencyPct * abs(a.LatencyAvg); pct > limit {
		limit = pct
	}
	return abs(b.LatencyAvg-a.LatencyAvg) > limit
}

// scoreChanged 判断评分变化是否超过阈值。
//
// 一侧没测速（评分为 0）时直接算变化：0 表示「没测过」，不是「得了 0 分」，
// 拿它做百分比没有意义。
func scoreChanged(a, b, pct float64) bool {
	if (a > 0) != (b > 0) {
		return true
	}
	if a <= 0 {
		return false
	}
	return abs(b-a) > pct*abs(a)
}

// indexByKey 把结果集按 `IP:端口` 建索引。
func indexByKey(records []model.IPRecord) map[string]model.IPRecord {
	out := make(map[string]model.IPRecord, len(records))
	for _, r := range records {
		out[recordKey(r)] = r
	}
	return out
}

func recordKey(r model.IPRecord) string {
	return r.IP + ":" + strconv.Itoa(r.Port)
}

func sortedKeys(m map[string]model.IPRecord) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// avgLatency 计算可达记录的平均延迟；一个都不可达时返回 0。
func avgLatency(records []model.IPRecord) float64 {
	var sum float64
	var n int
	for _, r := range records {
		if !r.Reachable() {
			continue
		}
		sum += r.LatencyAvg
		n++
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

// bestKey 返回「最优目标」的键。
//
// 测过速就按评分排，没测速就按延迟排：扫描结果里所有评分都是 0，只按评分
// 排会得出「没有最优」的结论，而用户显然想知道哪个最快。
func bestKey(records []model.IPRecord) string {
	var best model.IPRecord
	var found bool
	byScore := false
	for _, r := range records {
		if r.Score > 0 {
			byScore = true
			break
		}
	}

	for _, r := range records {
		if !found {
			if byScore && r.Score <= 0 {
				continue
			}
			if !byScore && !r.Reachable() {
				continue
			}
			best, found = r, true
			continue
		}
		if byScore {
			if r.Score > best.Score {
				best = r
			}
			continue
		}
		if r.Reachable() && r.LatencyAvg < best.LatencyAvg {
			best = r
		}
	}
	if !found {
		return ""
	}
	return recordKey(best)
}

// regionDelta 计算两侧各地区数量的差值。
func regionDelta(a, b []model.IPRecord) map[string]int {
	counts := func(records []model.IPRecord) map[string]int {
		out := map[string]int{}
		for _, r := range records {
			if r.Colo == "" {
				continue
			}
			out[r.Colo]++
		}
		return out
	}
	ca, cb := counts(a), counts(b)

	out := map[string]int{}
	for code, n := range cb {
		if d := n - ca[code]; d != 0 {
			out[code] = d
		}
	}
	for code, n := range ca {
		if _, ok := cb[code]; !ok && n != 0 {
			out[code] = -n
		}
	}
	return out
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func (o DiffOptions) withDefaults() DiffOptions {
	d := DefaultDiffOptions()
	if o.LatencyAbsMS <= 0 {
		o.LatencyAbsMS = d.LatencyAbsMS
	}
	if o.LatencyPct <= 0 {
		o.LatencyPct = d.LatencyPct
	}
	if o.ScorePct <= 0 {
		o.ScorePct = d.ScorePct
	}
	return o
}

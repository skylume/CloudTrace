package speed

import (
	"math"
	"sort"
	"strings"

	"cloudtrace/internal/model"
)

// selectTargets 按测速范围挑出本次要测的记录。
//
// 分地区 TopN 只在「完全测速」时生效：地区测速是用户点名要测某个地区的
// 全部节点，再按地区内部排名截断就等于把用户要的东西砍掉了。
func selectTargets(p model.SpeedParams) []model.IPRecord {
	if p.Scope != model.SpeedScopeAll || p.PerRegionTopN <= 0 {
		return p.Targets
	}
	return topNByRegion(p.Targets, p.PerRegionTopN)
}

// topNByRegion 按地区分桶，每个桶保留延迟最低的 N 个。
//
// 输出顺序按地区代码排序，桶内按延迟升序，保证同样的输入得到同样的顺序：
// 结果表每次刷新都换一个顺序，用户会以为数据变了。
func topNByRegion(records []model.IPRecord, n int) []model.IPRecord {
	buckets := make(map[string][]model.IPRecord, 8)
	var unknown []model.IPRecord

	for _, rec := range records {
		key := regionKey(rec)
		if key == "" {
			// 地区未知的节点不参与分桶截断，原样保留：判不出来就放过去，
			// 否则关掉明细采集时几乎全部节点都会被截掉。
			unknown = append(unknown, rec)
			continue
		}
		buckets[key] = append(buckets[key], rec)
	}

	keys := make([]string, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]model.IPRecord, 0, len(records))
	for _, key := range keys {
		bucket := buckets[key]
		sort.SliceStable(bucket, func(i, j int) bool {
			return latencyKey(bucket[i]) < latencyKey(bucket[j])
		})
		if len(bucket) > n {
			bucket = bucket[:n]
		}
		out = append(out, bucket...)
	}
	return append(out, unknown...)
}

// regionKey 取记录的地区标识，优先数据中心代码。
func regionKey(rec model.IPRecord) string {
	if colo := strings.TrimSpace(rec.Colo); colo != "" {
		return strings.ToUpper(colo)
	}
	return strings.ToUpper(strings.TrimSpace(rec.Loc))
}

// latencyKey 把不可达的哨兵值换成最大浮点数。
//
// 不可达用负数表示，直接参与升序排序会排到最前面——「最快的」其实是
// 「连不上的」。
func latencyKey(rec model.IPRecord) float64 {
	if rec.Latency < 0 {
		return math.MaxFloat64
	}
	return rec.Latency
}

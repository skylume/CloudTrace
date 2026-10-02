package model

import (
	"net/netip"
	"sort"
	"strings"
)

// SortKey 是结果集的排序维度，取值与记录里的字段名一致。
//
// 用字段名而不是另起一套编号：前端点列头时直接把列对应的字段名发过来，
// 中间少一层映射就少一处会改漏的地方。
type SortKey string

const (
	SortLatency    SortKey = "latency"
	SortLatencyAvg SortKey = "latency_avg"
	SortLoss       SortKey = "loss"
	SortJitter     SortKey = "jitter"
	SortScore      SortKey = "score"
	SortSpeed      SortKey = "speed_mbps"
	SortRegion     SortKey = "region"
)

// DefaultSortKey 是默认排序维度：丢包率升序 → 延迟升序。
//
// 先看丢包再看延迟：一个丢包的节点延迟再低也不可用，而延迟略高但完全不
// 丢包的节点实际体验更好。
const DefaultSortKey = SortLoss

// sortOrder 既是界面上的维度顺序，也是「可识别的维度」清单。
var sortOrder = []SortKey{
	SortLatency,
	SortLatencyAvg,
	SortLoss,
	SortJitter,
	SortScore,
	SortSpeed,
	SortRegion,
}

// SortKeys 返回全部可选维度。
func SortKeys() []SortKey { return append([]SortKey(nil), sortOrder...) }

// ParseSortKey 解析维度名。
//
// 空串按默认维度处理并返回 true；无法识别时返回 false，由调用方决定是报错
// 还是兜底——静默换成别的维度会让用户以为「点了列头没反应」。
func ParseSortKey(name string) (SortKey, bool) {
	v := SortKey(strings.TrimSpace(strings.ToLower(name)))
	if v == "" {
		return DefaultSortKey, true
	}
	for _, k := range sortOrder {
		if k == v {
			return k, true
		}
	}
	return DefaultSortKey, false
}

// NaturalDesc 返回该维度的自然方向是否为降序。
//
// 速度与评分越大越好，延迟、丢包、抖动、地区越小越靠前。
func NaturalDesc(key SortKey) bool {
	switch key {
	case SortScore, SortSpeed:
		return true
	default:
		return false
	}
}

// SortRecords 按维度就地排序结果集。
//
// 两条规则贯穿所有维度：
//
//   - **没有该项数据的记录恒排在最后，与方向无关。** 不可达节点的延迟是 -1
//     哨兵，升序时会冒充「最快」；没测过速的记录速度是 0，升序时同样会冒充
//     「最快」。让它们垫底是唯一不会误导用户的位置。
//   - **主维度相同的按延迟升序、再按地址数值序兜底。** 延迟是量化到毫秒的
//     整数，大批记录打平是常态，没有稳定的兜底次序就会出现「同样的数据导出
//     两次、内容顺序不一样」。
func SortRecords(records []IPRecord, key SortKey, desc bool) {
	k := normalizeKey(key)
	sort.SliceStable(records, func(i, j int) bool {
		a, b := records[i], records[j]
		am, bm := missingValue(a, k), missingValue(b, k)
		switch {
		case am != bm:
			return !am
		case am && bm:
			// 两边都没有数据时忽略方向：一组「无数据」的记录按地址排好即可。
			return tieBreak(a, b) < 0
		}

		c := compareValue(a, b, k)
		if c == 0 {
			c = tieBreak(a, b)
		}
		if desc {
			return c > 0
		}
		return c < 0
	})
}

// normalizeKey 把无法识别的维度收敛到默认维度。
func normalizeKey(key SortKey) SortKey {
	for _, k := range sortOrder {
		if k == key {
			return k
		}
	}
	return DefaultSortKey
}

// missingValue 报告该记录在指定维度上是否有可用的值。
func missingValue(r IPRecord, key SortKey) bool {
	switch key {
	case SortSpeed:
		// 0 表示「没测过」而不是「速度极慢」，两者必须区分。
		return r.SpeedMBps <= 0
	case SortScore:
		// 0 表示「没评过分」。
		return r.Score == 0
	case SortRegion:
		return regionOf(r) == ""
	default:
		// 延迟类维度共用可达性判断：探测全失败时延迟是哨兵值，抖动为 0，
		// 丢包为 1，都不代表真实的网络质量。
		return !r.Reachable()
	}
}

// compareValue 比较同一维度上的两个值，返回 -1 / 0 / 1。
func compareValue(a, b IPRecord, key SortKey) int {
	switch key {
	case SortLatency:
		return cmpFloat(a.Latency, b.Latency)
	case SortLatencyAvg:
		return cmpFloat(a.LatencyAvg, b.LatencyAvg)
	case SortLoss:
		return cmpFloat(a.Loss, b.Loss)
	case SortJitter:
		return cmpFloat(a.Jitter, b.Jitter)
	case SortScore:
		return cmpFloat(a.Score, b.Score)
	case SortSpeed:
		return cmpFloat(a.SpeedMBps, b.SpeedMBps)
	case SortRegion:
		return strings.Compare(regionOf(a), regionOf(b))
	}
	return 0
}

// regionOf 返回排序用的地区标识。
//
// 优先用数据中心代码：它是 ASCII，升序就是字母序，同一地区的记录自然聚在
// 一起；中文名按码位排序看起来是乱的（「德国」会排到「美国」前面）。
// 没有数据中心代码时才退回中文名，保证地区列非空的记录仍能按地区聚合。
func regionOf(r IPRecord) string {
	if colo := strings.ToUpper(strings.TrimSpace(r.Colo)); colo != "" {
		return colo
	}
	return strings.TrimSpace(r.RegionName)
}

// tieBreak 在主维度相同时给出稳定的次序：延迟升序 → 地址数值序 → 端口。
func tieBreak(a, b IPRecord) int {
	if a.Reachable() && b.Reachable() {
		if c := cmpFloat(a.Latency, b.Latency); c != 0 {
			return c
		}
	}
	if c := cmpIP(a.IP, b.IP); c != 0 {
		return c
	}
	return cmpInt(a.Port, b.Port)
}

// cmpIP 按地址数值大小比较。
//
// 不能直接比字符串：那样 1.1.1.10 会排在 1.1.1.2 前面，用户看到的是「乱序」。
// 解析失败（不该发生）时退回字符串比较，至少保持次序稳定。
func cmpIP(a, b string) int {
	pa, ea := netip.ParseAddr(a)
	pb, eb := netip.ParseAddr(b)
	if ea == nil && eb == nil {
		return pa.Compare(pb)
	}
	return strings.Compare(a, b)
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

package model

import (
	"math"
	"testing"
)

// sortable 造一条有完整延迟、速度、评分与地区的记录。
func sortable(ip string, latency, jitter, loss, speed, score float64, colo string) IPRecord {
	return IPRecord{
		IP:         ip,
		Port:       443,
		Latency:    latency,
		LatencyAvg: latency + 5,
		Jitter:     jitter,
		Loss:       loss,
		Sent:       3,
		Recv:       3,
		SpeedMBps:  speed,
		Score:      score,
		Colo:       colo,
	}
}

// unreachable 造一条探测全失败的记录。
func unreachable(ip string) IPRecord {
	return IPRecord{
		IP:         ip,
		Port:       443,
		Latency:    Unreachable,
		LatencyAvg: Unreachable,
		Loss:       1,
		Sent:       3,
		Recv:       0,
	}
}

// ipsOf 抽出排序后的地址序列，便于逐条断言。
func ipsOf(records []IPRecord) []string {
	out := make([]string, 0, len(records))
	for _, r := range records {
		out = append(out, r.IP)
	}
	return out
}

func assertOrder(t *testing.T, got []IPRecord, want []string) {
	t.Helper()
	have := ipsOf(got)
	if len(have) != len(want) {
		t.Fatalf("排序结果 %v，期望 %v", have, want)
	}
	for i := range want {
		if have[i] != want[i] {
			t.Fatalf("排序结果 %v，期望 %v", have, want)
		}
	}
}

func TestSortKeysCoverEveryDimension(t *testing.T) {
	keys := SortKeys()
	want := []SortKey{SortLatency, SortLatencyAvg, SortLoss, SortJitter, SortScore, SortSpeed, SortRegion}
	if len(keys) != len(want) {
		t.Fatalf("维度数 = %d，期望 %d", len(keys), len(want))
	}
	for i, k := range want {
		if keys[i] != k {
			t.Fatalf("第 %d 个维度 = %q，期望 %q", i, keys[i], k)
		}
	}

	// 每个维度都必须能真的排出次序，且各维度的赢家互不相同——只实现了一
	// 部分维度、或者几个维度悄悄共用同一个字段，都会在这里露出来。
	base := []IPRecord{
		sortable("1.0.0.1", 30, 9, 0.30, 3, 60, "NRT"),
		sortable("1.0.0.2", 10, 1, 0.10, 9, 90, "LAX"),
		sortable("1.0.0.3", 20, 5, 0.20, 6, 70, "HKG"),
	}
	// 每个维度按自然方向排序时的首位。地区按数据中心代码字母序，HKG 最小；
	// 速度与评分越大越好，所以它们的赢家是 1.0.0.2 而不是 1.0.0.1。
	winners := map[SortKey]string{
		SortLatency:    "1.0.0.2",
		SortLatencyAvg: "1.0.0.2",
		SortLoss:       "1.0.0.2",
		SortJitter:     "1.0.0.2",
		SortScore:      "1.0.0.2",
		SortSpeed:      "1.0.0.2",
		SortRegion:     "1.0.0.3",
	}
	for _, k := range keys {
		recs := append([]IPRecord(nil), base...)
		SortRecords(recs, k, NaturalDesc(k))
		if got := ipsOf(recs)[0]; got != winners[k] {
			t.Errorf("维度 %q 按自然方向排序后首位 = %s，期望 %s", k, got, winners[k])
		}
	}
}

func TestParseSortKey(t *testing.T) {
	cases := []struct {
		in    string
		want  SortKey
		valid bool
	}{
		{"", DefaultSortKey, true},
		{"latency", SortLatency, true},
		{"  LATENCY_AVG ", SortLatencyAvg, true},
		{"speed_mbps", SortSpeed, true},
		{"region", SortRegion, true},
		{"nosuchkey", DefaultSortKey, false},
	}
	for _, c := range cases {
		got, ok := ParseSortKey(c.in)
		if got != c.want || ok != c.valid {
			t.Errorf("ParseSortKey(%q) = (%q, %v)，期望 (%q, %v)", c.in, got, ok, c.want, c.valid)
		}
	}
}

func TestNaturalDesc(t *testing.T) {
	// 速度与评分越大越好，其余越小越靠前。
	desc := map[SortKey]bool{
		SortLatency:    false,
		SortLatencyAvg: false,
		SortLoss:       false,
		SortJitter:     false,
		SortRegion:     false,
		SortScore:      true,
		SortSpeed:      true,
	}
	for k, want := range desc {
		if got := NaturalDesc(k); got != want {
			t.Errorf("NaturalDesc(%q) = %v，期望 %v", k, got, want)
		}
	}
}

func TestSortByLatencyAscending(t *testing.T) {
	recs := []IPRecord{
		sortable("1.0.0.1", 90, 0, 0, 0, 0, "HKG"),
		sortable("1.0.0.2", 10, 0, 0, 0, 0, "HKG"),
		sortable("1.0.0.3", 50, 0, 0, 0, 0, "HKG"),
	}
	SortRecords(recs, SortLatency, false)
	assertOrder(t, recs, []string{"1.0.0.2", "1.0.0.3", "1.0.0.1"})
}

func TestSortByLatencyAvgDiffersFromLatency(t *testing.T) {
	// 最小延迟与平均延迟指向不同的赢家，用它证明这一维度真的在看
	// latency_avg 而不是顺手复用了 latency。
	a := sortable("1.0.0.1", 10, 0, 0, 0, 0, "HKG")
	a.LatencyAvg = 100
	b := sortable("1.0.0.2", 20, 0, 0, 0, 0, "HKG")
	b.LatencyAvg = 25

	recs := []IPRecord{a, b}
	SortRecords(recs, SortLatencyAvg, false)
	assertOrder(t, recs, []string{"1.0.0.2", "1.0.0.1"})

	recs = []IPRecord{a, b}
	SortRecords(recs, SortLatency, false)
	assertOrder(t, recs, []string{"1.0.0.1", "1.0.0.2"})
}

func TestSortByLossAndJitter(t *testing.T) {
	recs := []IPRecord{
		sortable("1.0.0.1", 10, 9, 0.5, 0, 0, "HKG"),
		sortable("1.0.0.2", 10, 1, 0.0, 0, 0, "HKG"),
		sortable("1.0.0.3", 10, 5, 0.2, 0, 0, "HKG"),
	}
	SortRecords(recs, SortLoss, false)
	assertOrder(t, recs, []string{"1.0.0.2", "1.0.0.3", "1.0.0.1"})

	recs = []IPRecord{
		sortable("1.0.0.1", 10, 9, 0.5, 0, 0, "HKG"),
		sortable("1.0.0.2", 10, 1, 0.0, 0, 0, "HKG"),
		sortable("1.0.0.3", 10, 5, 0.2, 0, 0, "HKG"),
	}
	SortRecords(recs, SortJitter, false)
	assertOrder(t, recs, []string{"1.0.0.2", "1.0.0.3", "1.0.0.1"})
}

func TestSortByScoreAndSpeedDescending(t *testing.T) {
	recs := []IPRecord{
		sortable("1.0.0.1", 10, 0, 0, 3, 60, "HKG"),
		sortable("1.0.0.2", 10, 0, 0, 9, 90, "HKG"),
		sortable("1.0.0.3", 10, 0, 0, 6, 70, "HKG"),
	}
	SortRecords(recs, SortScore, true)
	assertOrder(t, recs, []string{"1.0.0.2", "1.0.0.3", "1.0.0.1"})

	recs = []IPRecord{
		sortable("1.0.0.1", 10, 0, 0, 3, 60, "HKG"),
		sortable("1.0.0.2", 10, 0, 0, 9, 90, "HKG"),
		sortable("1.0.0.3", 10, 0, 0, 6, 70, "HKG"),
	}
	SortRecords(recs, SortSpeed, true)
	assertOrder(t, recs, []string{"1.0.0.2", "1.0.0.3", "1.0.0.1"})
}

func TestSortByRegionGroupsSameColo(t *testing.T) {
	recs := []IPRecord{
		sortable("1.0.0.1", 10, 0, 0, 0, 0, "NRT"),
		sortable("1.0.0.2", 10, 0, 0, 0, 0, "HKG"),
		sortable("1.0.0.3", 10, 0, 0, 0, 0, "LAX"),
		sortable("1.0.0.4", 10, 0, 0, 0, 0, "hkg"),
	}
	SortRecords(recs, SortRegion, false)
	// 大小写视为同一地区（HKG 与 hkg 相邻），地区内部再按地址排。
	assertOrder(t, recs, []string{"1.0.0.2", "1.0.0.4", "1.0.0.3", "1.0.0.1"})
}

func TestSortRegionFallsBackToRegionName(t *testing.T) {
	// 没有数据中心代码时按中文名排，保证「地区」这一列不是摆设。
	a := sortable("1.0.0.1", 10, 0, 0, 0, 0, "")
	a.RegionName = "洛杉矶"
	b := sortable("1.0.0.2", 10, 0, 0, 0, 0, "")
	b.RegionName = "东京"

	recs := []IPRecord{a, b}
	SortRecords(recs, SortRegion, false)
	if regionOf(recs[0]) != "东京" {
		t.Fatalf("首位地区 = %q，期望 东京", regionOf(recs[0]))
	}
}

// 不可达记录在任何方向、任何延迟类维度上都必须垫底。
func TestSortPutsUnreachableLastInBothDirections(t *testing.T) {
	for _, key := range []SortKey{SortLatency, SortLatencyAvg, SortLoss, SortJitter} {
		for _, desc := range []bool{false, true} {
			recs := []IPRecord{
				unreachable("1.0.0.1"),
				sortable("1.0.0.2", 50, 1, 0.1, 0, 0, "HKG"),
				sortable("1.0.0.3", 10, 2, 0.2, 0, 0, "HKG"),
			}
			SortRecords(recs, key, desc)
			if got := ipsOf(recs)[2]; got != "1.0.0.1" {
				t.Errorf("维度 %q desc=%v 末位 = %s，期望不可达的 1.0.0.1", key, desc, got)
			}
		}
	}
}

// 没测过速的记录不能因为速度是 0 就冒充「最慢」或「最快」。
func TestSortSpeedPutsUnmeasuredLast(t *testing.T) {
	recs := []IPRecord{
		sortable("1.0.0.1", 10, 0, 0, 0, 0, "HKG"), // 没测过
		sortable("1.0.0.2", 10, 0, 0, 3, 0, "HKG"),
		sortable("1.0.0.3", 10, 0, 0, 9, 0, "HKG"),
	}
	SortRecords(recs, SortSpeed, false)
	assertOrder(t, recs, []string{"1.0.0.2", "1.0.0.3", "1.0.0.1"})

	recs = []IPRecord{
		sortable("1.0.0.1", 10, 0, 0, 0, 0, "HKG"),
		sortable("1.0.0.2", 10, 0, 0, 3, 0, "HKG"),
		sortable("1.0.0.3", 10, 0, 0, 9, 0, "HKG"),
	}
	SortRecords(recs, SortSpeed, true)
	assertOrder(t, recs, []string{"1.0.0.3", "1.0.0.2", "1.0.0.1"})
}

func TestSortScorePutsUnscoredLast(t *testing.T) {
	recs := []IPRecord{
		sortable("1.0.0.1", 10, 0, 0, 0, 0, "HKG"), // 没评过分
		sortable("1.0.0.2", 10, 0, 0, 0, 60, "HKG"),
		sortable("1.0.0.3", 10, 0, 0, 0, 90, "HKG"),
	}
	SortRecords(recs, SortScore, false)
	assertOrder(t, recs, []string{"1.0.0.2", "1.0.0.3", "1.0.0.1"})
}

func TestSortRegionPutsBlankLast(t *testing.T) {
	recs := []IPRecord{
		sortable("1.0.0.1", 10, 0, 0, 0, 0, ""),
		sortable("1.0.0.2", 10, 0, 0, 0, 0, "HKG"),
	}
	SortRecords(recs, SortRegion, false)
	assertOrder(t, recs, []string{"1.0.0.2", "1.0.0.1"})
}

// 延迟大批打平时必须有稳定兜底：地址按数值排，而不是按字符串。
func TestSortTieBreakIsNumericAndStable(t *testing.T) {
	recs := []IPRecord{
		sortable("1.1.1.10", 20, 0, 0, 0, 0, "HKG"),
		sortable("1.1.1.2", 20, 0, 0, 0, 0, "HKG"),
		sortable("1.1.1.9", 20, 0, 0, 0, 0, "HKG"),
	}
	SortRecords(recs, SortLatency, false)
	assertOrder(t, recs, []string{"1.1.1.2", "1.1.1.9", "1.1.1.10"})

	// 同一份数据排两次结果必须一致。
	first := ipsOf(recs)
	SortRecords(recs, SortLatency, false)
	assertOrder(t, recs, first)
}

func TestSortTieBreakByPort(t *testing.T) {
	a := sortable("1.0.0.1", 20, 0, 0, 0, 0, "HKG")
	a.Port = 8443
	b := sortable("1.0.0.1", 20, 0, 0, 0, 0, "HKG")
	b.Port = 443

	recs := []IPRecord{a, b}
	SortRecords(recs, SortLatency, false)
	if recs[0].Port != 443 {
		t.Fatalf("首位端口 = %d，期望 443", recs[0].Port)
	}
}

func TestSortTieBreakFallsBackToStringCompare(t *testing.T) {
	// 非法地址不该让排序崩掉或变得不确定。
	a := sortable("not-an-ip", 20, 0, 0, 0, 0, "HKG")
	b := sortable("also-bad", 20, 0, 0, 0, 0, "HKG")
	recs := []IPRecord{a, b}
	SortRecords(recs, SortLatency, false)
	if recs[0].IP != "also-bad" {
		t.Fatalf("首位 = %s，期望按字符串序取 also-bad", recs[0].IP)
	}
}

func TestSortTieBreakPrefersReachableLatency(t *testing.T) {
	// 两边都没有主维度数值时，兜底仍然先看延迟，再不可达的排前面
	// ——这里只需保证不 panic 且次序稳定。
	recs := []IPRecord{unreachable("2.0.0.1"), unreachable("1.0.0.1")}
	SortRecords(recs, SortLoss, false)
	assertOrder(t, recs, []string{"1.0.0.1", "2.0.0.1"})
}

func TestSortUnknownKeyFallsBackToDefault(t *testing.T) {
	recs := []IPRecord{
		sortable("1.0.0.1", 10, 0, 0.9, 0, 0, "HKG"),
		sortable("1.0.0.2", 10, 0, 0.1, 0, 0, "HKG"),
	}
	SortRecords(recs, SortKey("nonsense"), false)
	// 默认维度是丢包升序。
	assertOrder(t, recs, []string{"1.0.0.2", "1.0.0.1"})
}

func TestSortEmptyIsNoop(t *testing.T) {
	var recs []IPRecord
	SortRecords(recs, SortLatency, false)
	if len(recs) != 0 {
		t.Fatalf("空结果集排序后 = %v", recs)
	}
}

func TestSortMixedFamiliesAreOrdered(t *testing.T) {
	// IPv4 与 IPv6 混排时兜底次序也要确定（不 panic、不随机）。
	recs := []IPRecord{
		sortable("2606:4700::1", 20, 0, 0, 0, 0, "HKG"),
		sortable("1.1.1.1", 20, 0, 0, 0, 0, "HKG"),
	}
	SortRecords(recs, SortLatency, false)
	if recs[0].IP != "1.1.1.1" {
		t.Fatalf("首位 = %s，期望 IPv4 在前", recs[0].IP)
	}
}

func TestMissingValueDistinguishesZeroFromUnmeasured(t *testing.T) {
	// 0 是合法的极快延迟，不能被当成「没有数据」。
	fast := IPRecord{IP: "1.0.0.1", Port: 443, Latency: 0, LatencyAvg: 0, Sent: 3, Recv: 3, Colo: "HKG"}
	if missingValue(fast, SortLatency) {
		t.Fatal("延迟为 0 的可达记录被判成了「没有数据」")
	}
	// 哨兵值必须是「没有数据」。
	if !missingValue(unreachable("1.0.0.1"), SortLatency) {
		t.Fatal("不可达记录未被判成「没有数据」")
	}
	// 速度与评分的 0 是「没测过 / 没评过」。
	if !missingValue(IPRecord{SpeedMBps: 0}, SortSpeed) {
		t.Fatal("速度为 0 的记录未被判成「没测过」")
	}
	if !missingValue(IPRecord{Score: 0}, SortScore) {
		t.Fatal("评分为 0 的记录未被判成「没评过」")
	}
	// 浮点极小值仍算有数据。
	if missingValue(IPRecord{SpeedMBps: math.SmallestNonzeroFloat64}, SortSpeed) {
		t.Fatal("极小但非零的速度被判成了「没测过」")
	}
}

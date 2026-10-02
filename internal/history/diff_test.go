package history

import (
	"testing"

	"cloudtrace/internal/model"
)

// mk 造一条可达记录。
func mk(ip string, port int, latency float64, colo string, score float64) model.IPRecord {
	return model.IPRecord{
		IP: ip, Port: port, UseTLS: true,
		Latency: latency, LatencyAvg: latency,
		Sent: 3, Recv: 3, Colo: colo, Score: score,
	}
}

// mkDead 造一条不可达记录。
func mkDead(ip string, port int) model.IPRecord {
	return model.IPRecord{
		IP: ip, Port: port,
		Latency: model.Unreachable, LatencyAvg: model.Unreachable,
		Sent: 3, Recv: 0, Loss: 1,
	}
}

func diffRecords(a, b []model.IPRecord) HistoryDiff {
	return Diff(
		HistoryRecord{Results: a},
		HistoryRecord{Results: b},
		DefaultDiffOptions(),
	)
}

// 构造已知数据集，断言四类归属都正确。
func TestDiffFourCategories(t *testing.T) {
	a := []model.IPRecord{
		mk("1.1.1.1", 443, 100, "HKG", 0),
		mk("1.1.1.2", 443, 200, "NRT", 0),
		mk("1.1.1.3", 443, 300, "HKG", 0),
		mkDead("1.1.1.4", 443),
	}
	b := []model.IPRecord{
		mk("1.1.1.1", 443, 105, "HKG", 0), // 差 5ms，在阈值内
		mk("1.1.1.2", 443, 300, "SJC", 0), // 差 100ms 且换了地区
		mk("1.1.1.5", 443, 50, "HKG", 0),  // 新增
	}

	d := diffRecords(a, b)

	if d.AddedCount != 1 || len(d.Added) != 1 || d.Added[0].IP != "1.1.1.5" {
		t.Fatalf("新增 = %+v，期望只有 1.1.1.5", d.Added)
	}
	if d.RemovedCount != 2 || len(d.Removed) != 2 {
		t.Fatalf("消失 = %+v，期望 1.1.1.3 与 1.1.1.4", d.Removed)
	}
	if d.ChangedCount != 1 || len(d.Changed) != 1 {
		t.Fatalf("变化 = %+v，期望只有 1.1.1.2", d.Changed)
	}
	if d.UnchangedCount != 1 {
		t.Fatalf("未变数 = %d，期望 1", d.UnchangedCount)
	}

	c := d.Changed[0]
	if c.Key != "1.1.1.2:443" || c.IP != "1.1.1.2" {
		t.Fatalf("变化条目的键 = %q", c.Key)
	}
	if len(c.Reasons) != 2 || c.Reasons[0] != ReasonLatency || c.Reasons[1] != ReasonRegion {
		t.Fatalf("变化原因 = %v，期望 [latency region]", c.Reasons)
	}
	if c.LatencyDelta != 100 {
		t.Fatalf("延迟变化 = %v，期望 100", c.LatencyDelta)
	}
	if c.ColoA != "NRT" || c.ColoB != "SJC" {
		t.Fatalf("地区 = %s → %s，期望 NRT → SJC", c.ColoA, c.ColoB)
	}
}

func TestDiffSummaryNumbers(t *testing.T) {
	a := []model.IPRecord{
		mk("1.1.1.1", 443, 100, "HKG", 0),
		mk("1.1.1.2", 443, 200, "NRT", 0),
		mk("1.1.1.3", 443, 300, "HKG", 0),
		mkDead("1.1.1.4", 443),
	}
	b := []model.IPRecord{
		mk("1.1.1.1", 443, 105, "HKG", 0),
		mk("1.1.1.2", 443, 300, "SJC", 0),
		mk("1.1.1.5", 443, 50, "HKG", 0),
	}

	d := diffRecords(a, b)

	// 不可达的记录不计入平均延迟。
	if d.AvgLatencyA != 200 {
		t.Fatalf("A 平均延迟 = %v，期望 200", d.AvgLatencyA)
	}
	if got := d.AvgLatencyB; got < 151 || got > 152 {
		t.Fatalf("B 平均延迟 = %v，期望约 151.67", got)
	}
	if d.BestA != "1.1.1.1:443" {
		t.Fatalf("A 最优 = %q，期望延迟最低的 1.1.1.1:443", d.BestA)
	}
	if d.BestB != "1.1.1.5:443" {
		t.Fatalf("B 最优 = %q，期望延迟最低的 1.1.1.5:443", d.BestB)
	}
	if !d.BestChanged {
		t.Fatal("最优目标换了，BestChanged 应为 true")
	}

	want := map[string]int{"NRT": -1, "SJC": 1}
	if len(d.RegionDelta) != len(want) {
		t.Fatalf("地区变化 = %v，期望 %v", d.RegionDelta, want)
	}
	for code, n := range want {
		if d.RegionDelta[code] != n {
			t.Fatalf("地区 %s 变化 = %d，期望 %d", code, d.RegionDelta[code], n)
		}
	}
}

func TestDiffLatencyThresholds(t *testing.T) {
	cases := []struct {
		name    string
		a, b    float64
		changed bool
	}{
		{"绝对阈值内", 100, 119, false},
		{"刚好越过绝对阈值", 100, 125, true},
		{"相对阈值生效", 1000, 1195, false},
		{"越过相对阈值", 1000, 1210, true},
		{"变快同样算变化", 200, 100, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := diffRecords(
				[]model.IPRecord{mk("1.1.1.1", 443, tc.a, "HKG", 0)},
				[]model.IPRecord{mk("1.1.1.1", 443, tc.b, "HKG", 0)},
			)
			changed := d.ChangedCount == 1
			if changed != tc.changed {
				t.Fatalf("%v → %v 判定为变化=%v，期望 %v（%+v）", tc.a, tc.b, changed, tc.changed, d.Changed)
			}
		})
	}
}

// 一侧可达、另一侧不可达是「从能用变成不能用」，任何百分比阈值都不该把它滤掉。
func TestDiffReachabilityFlipIsAlwaysChanged(t *testing.T) {
	d := diffRecords(
		[]model.IPRecord{mk("1.1.1.1", 443, 100, "HKG", 0)},
		[]model.IPRecord{mkDead("1.1.1.1", 443)},
	)
	if d.ChangedCount != 1 || d.Changed[0].Reasons[0] != ReasonLatency {
		t.Fatalf("可达性翻转 = %+v，期望判为延迟变化", d.Changed)
	}

	// 两次都不可达：没有「变化」可言，都躺平了。
	d = diffRecords(
		[]model.IPRecord{mkDead("1.1.1.1", 443)},
		[]model.IPRecord{mkDead("1.1.1.1", 443)},
	)
	if d.ChangedCount != 0 || d.UnchangedCount != 1 {
		t.Fatalf("双双不可达 = %+v，期望判为未变", d)
	}
}

// 0 分表示「没测过」，不是「得了 0 分」，拿它算百分比没有意义。
func TestDiffScorePresenceFlip(t *testing.T) {
	d := diffRecords(
		[]model.IPRecord{mk("1.1.1.1", 443, 100, "HKG", 0)},
		[]model.IPRecord{mk("1.1.1.1", 443, 100, "HKG", 5)},
	)
	if d.ChangedCount != 1 || len(d.Changed[0].Reasons) != 1 || d.Changed[0].Reasons[0] != ReasonScore {
		t.Fatalf("评分从无到有 = %+v，期望判为评分变化", d.Changed)
	}

	d = diffRecords(
		[]model.IPRecord{mk("1.1.1.1", 443, 100, "HKG", 10)},
		[]model.IPRecord{mk("1.1.1.1", 443, 100, "HKG", 12)},
	)
	if d.ChangedCount != 0 {
		t.Fatalf("评分差 20%% 恰好不超阈值，却判为变化：%+v", d.Changed)
	}

	d = diffRecords(
		[]model.IPRecord{mk("1.1.1.1", 443, 100, "HKG", 10)},
		[]model.IPRecord{mk("1.1.1.1", 443, 100, "HKG", 13)},
	)
	if d.ChangedCount != 1 {
		t.Fatalf("评分差 30%% 超过阈值，却判为未变：%+v", d)
	}
}

// 同一台机器的不同端口是两个不同的目标。
func TestDiffTreatsPortsAsDistinctTargets(t *testing.T) {
	d := diffRecords(
		[]model.IPRecord{mk("1.1.1.1", 443, 100, "HKG", 0)},
		[]model.IPRecord{mk("1.1.1.1", 8443, 100, "HKG", 0)},
	)
	if d.AddedCount != 1 || d.RemovedCount != 1 {
		t.Fatalf("换端口 = 新增 %d / 消失 %d，期望各 1", d.AddedCount, d.RemovedCount)
	}
	if d.ChangedCount != 0 {
		t.Fatalf("换端口不该判成「同一个目标变了」：%+v", d.Changed)
	}
}

func TestDiffEmptySides(t *testing.T) {
	d := diffRecords(nil, nil)
	if d.AddedCount != 0 || d.RemovedCount != 0 || d.ChangedCount != 0 || d.UnchangedCount != 0 {
		t.Fatalf("两边都空时不该有任何计数：%+v", d)
	}
	if d.BestChanged {
		t.Fatal("两边都没有最优目标时不该报「最优变了」")
	}
	if d.AvgLatencyA != 0 || d.AvgLatencyB != 0 {
		t.Fatalf("没有可达记录时平均延迟应为 0：%v / %v", d.AvgLatencyA, d.AvgLatencyB)
	}

	d = diffRecords(nil, []model.IPRecord{mk("1.1.1.1", 443, 100, "HKG", 0)})
	if d.AddedCount != 1 || d.BestA != "" || d.BestB != "1.1.1.1:443" || !d.BestChanged {
		t.Fatalf("一侧为空时结果不对：%+v", d)
	}
}

// 测过速就按评分排最优，没测速就按延迟排——扫描结果里评分全是 0。
func TestDiffBestUsesScoreWhenPresent(t *testing.T) {
	// 延迟更低的反而评分更差，最优应当按评分取。
	records := []model.IPRecord{
		mk("1.1.1.1", 443, 50, "HKG", 3),
		mk("1.1.1.2", 443, 200, "HKG", 9),
	}
	if got := bestKey(records); got != "1.1.1.2:443" {
		t.Fatalf("最优 = %q，期望按评分取 1.1.1.2:443", got)
	}
	if got := bestKey(records[:1]); got != "1.1.1.1:443" {
		t.Fatalf("最优 = %q，期望 1.1.1.1:443", got)
	}
	if got := bestKey(nil); got != "" {
		t.Fatalf("空集合的最优 = %q，期望空串", got)
	}
	if got := bestKey([]model.IPRecord{mkDead("1.1.1.1", 443)}); got != "" {
		t.Fatalf("全不可达的最优 = %q，期望空串", got)
	}
}

func TestDiffOptionsDefaults(t *testing.T) {
	d := DefaultDiffOptions()
	if d.LatencyAbsMS != 20 || d.LatencyPct != 0.2 || d.ScorePct != 0.2 {
		t.Fatalf("默认阈值 = %+v，期望 20ms / 20%% / 20%%", d)
	}
	// 零值必须补成默认值，否则「不填阈值」会变成「任何变化都不算变化」。
	filled := DiffOptions{}.withDefaults()
	if filled != d {
		t.Fatalf("零值补齐后 = %+v，期望 %+v", filled, d)
	}
}

func TestDiffIgnoresRegionCaseDifference(t *testing.T) {
	d := diffRecords(
		[]model.IPRecord{mk("1.1.1.1", 443, 100, "HKG", 0)},
		[]model.IPRecord{mk("1.1.1.1", 443, 100, "hkg", 0)},
	)
	if d.ChangedCount != 0 {
		t.Fatalf("只有大小写不同不该算地区变化：%+v", d.Changed)
	}
}

func TestDiffResultIsSorted(t *testing.T) {
	a := []model.IPRecord{
		mk("1.1.1.9", 443, 100, "HKG", 0),
		mk("1.1.1.3", 443, 100, "HKG", 0),
		mk("1.1.1.7", 443, 100, "HKG", 0),
	}
	d := diffRecords(a, nil)
	want := []string{"1.1.1.3:443", "1.1.1.7:443", "1.1.1.9:443"}
	for i, w := range want {
		if got := d.Removed[i].IP + ":443"; got != w {
			t.Fatalf("消失列表第 %d 项 = %q，期望 %q（顺序必须稳定）", i, got, w)
		}
	}
}

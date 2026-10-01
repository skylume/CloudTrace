package speed

import (
	"math"
	"testing"

	"cloudtrace/internal/model"
)

func rec(ip, colo string, latency float64) model.IPRecord {
	return model.IPRecord{IP: ip, Port: 443, Colo: colo, Latency: latency}
}

func ips(records []model.IPRecord) []string {
	out := make([]string, 0, len(records))
	for _, r := range records {
		out = append(out, r.IP)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// 单点与地区测速都不做 TopN：地区测速是用户点名要测某地区的全部节点，
// 再按地区内部排名截断等于把用户要的东西砍掉了。
func TestSelectTargetsSkipsTopNOutsideFullScope(t *testing.T) {
	targets := []model.IPRecord{
		rec("1.1.1.1", "HKG", 10),
		rec("1.1.1.2", "HKG", 20),
		rec("1.1.1.3", "HKG", 30),
	}
	for _, scope := range []string{model.SpeedScopeSingle, model.SpeedScopeRegion} {
		p := model.SpeedParams{Scope: scope, Targets: targets, PerRegionTopN: 1}
		if got := selectTargets(p); len(got) != 3 {
			t.Errorf("范围 %s 下目标数 = %d，期望 3（不应截断）", scope, len(got))
		}
	}
}

// 完全测速 + TopN > 0：按地区分桶，每桶取延迟最低的 N 个。
func TestSelectTargetsTopNByRegion(t *testing.T) {
	targets := []model.IPRecord{
		rec("1.1.1.1", "HKG", 30),
		rec("1.1.1.2", "HKG", 10),
		rec("1.1.1.3", "HKG", 20),
		rec("2.2.2.1", "SIN", 50),
		rec("2.2.2.2", "SIN", 40),
	}
	p := model.SpeedParams{Scope: model.SpeedScopeAll, Targets: targets, PerRegionTopN: 2}
	got := ips(selectTargets(p))

	// 地区按代码升序、桶内按延迟升序。
	want := []string{"1.1.1.2", "1.1.1.3", "2.2.2.2", "2.2.2.1"}
	if !equalStrings(got, want) {
		t.Errorf("TopN 结果 = %v，期望 %v", got, want)
	}
}

// 地区未知的节点不参与分桶截断：判不出来就放过去，否则关掉明细采集时
// 几乎全部节点都会被截掉。
func TestSelectTargetsKeepsUnknownRegion(t *testing.T) {
	targets := []model.IPRecord{
		rec("1.1.1.1", "", 10),
		rec("1.1.1.2", "", 20),
		rec("1.1.1.3", "", 30),
		rec("2.2.2.1", "HKG", 40),
	}
	p := model.SpeedParams{Scope: model.SpeedScopeAll, Targets: targets, PerRegionTopN: 1}
	got := ips(selectTargets(p))

	// HKG 桶截到 1 个，三个地区未知的原样保留。
	want := []string{"2.2.2.1", "1.1.1.1", "1.1.1.2", "1.1.1.3"}
	if !equalStrings(got, want) {
		t.Errorf("结果 = %v，期望 %v", got, want)
	}
}

// 没有数据中心代码时退回出口国家码，两者都没有才算未知。
func TestRegionKeyFallsBackToLoc(t *testing.T) {
	if got := regionKey(model.IPRecord{Loc: "hk"}); got != "HK" {
		t.Errorf("regionKey = %q，期望 HK", got)
	}
	if got := regionKey(model.IPRecord{Colo: "hkg", Loc: "hk"}); got != "HKG" {
		t.Errorf("regionKey = %q，期望优先取 HKG", got)
	}
	if got := regionKey(model.IPRecord{}); got != "" {
		t.Errorf("regionKey = %q，期望空", got)
	}
}

// 不可达用负数哨兵表示，直接参与升序排序会排到最前面——「最快的」其实是
// 「连不上的」。
func TestLatencyKeyPushesUnreachableToEnd(t *testing.T) {
	if got := latencyKey(model.IPRecord{Latency: -1}); got != math.MaxFloat64 {
		t.Errorf("不可达的排序键 = %v，期望最大浮点数", got)
	}
	if got := latencyKey(model.IPRecord{Latency: 12.5}); got != 12.5 {
		t.Errorf("可达的排序键 = %v，期望原值", got)
	}

	targets := []model.IPRecord{
		rec("1.1.1.1", "HKG", -1),
		rec("1.1.1.2", "HKG", 20),
		rec("1.1.1.3", "HKG", 10),
	}
	p := model.SpeedParams{Scope: model.SpeedScopeAll, Targets: targets, PerRegionTopN: 2}
	got := ips(selectTargets(p))
	if !equalStrings(got, []string{"1.1.1.3", "1.1.1.2"}) {
		t.Errorf("TopN 结果 = %v，期望可达的两个排前面", got)
	}
}

// 同样的输入必须得到同样的顺序：结果表每次刷新都换一个顺序，用户会以为
// 数据变了。
func TestSelectTargetsIsDeterministic(t *testing.T) {
	targets := []model.IPRecord{
		rec("2.2.2.1", "SIN", 20),
		rec("1.1.1.1", "HKG", 20),
		rec("3.3.3.1", "LAX", 20),
		rec("1.1.1.2", "HKG", 20),
	}
	p := model.SpeedParams{Scope: model.SpeedScopeAll, Targets: targets, PerRegionTopN: 2}

	first := ips(selectTargets(p))
	for i := 0; i < 20; i++ {
		if got := ips(selectTargets(p)); !equalStrings(got, first) {
			t.Fatalf("第 %d 次结果 = %v，与首次 %v 不一致", i+1, got, first)
		}
	}
	// 地区按代码升序：HKG → LAX → SIN。
	if !equalStrings(first, []string{"1.1.1.1", "1.1.1.2", "3.3.3.1", "2.2.2.1"}) {
		t.Errorf("结果 = %v，期望按地区代码排序", first)
	}
}

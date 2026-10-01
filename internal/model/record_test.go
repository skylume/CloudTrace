package model

import (
	"encoding/json"
	"testing"
)

func TestReachable(t *testing.T) {
	tests := []struct {
		name   string
		record IPRecord
		want   bool
	}{
		{name: "正常可达", record: IPRecord{Recv: 1, Latency: 12}, want: true},
		{name: "全部失败", record: IPRecord{Recv: 0, Latency: Unreachable}, want: false},
		{name: "零值", record: IPRecord{}, want: false},
		{name: "有接收但延迟是哨兵值", record: IPRecord{Recv: 1, Latency: Unreachable}, want: false},
		{name: "延迟为零是合法值", record: IPRecord{Recv: 1, Latency: 0}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.record.Reachable(); got != tt.want {
				t.Errorf("Reachable() = %v，期望 %v", got, tt.want)
			}
		})
	}
}

func TestSummarize(t *testing.T) {
	records := []IPRecord{
		{IP: "1.1.1.1", Latency: 10, LatencyAvg: 12, Recv: 4, Colo: "HKG", ASOrg: "CLOUDFLARENET", SpeedMBps: 20},
		{IP: "1.1.1.2", Latency: 30, LatencyAvg: 33, Recv: 4, Colo: "HKG", ASOrg: "CLOUDFLARENET", SpeedMBps: 10},
		{IP: "1.1.1.3", Latency: 20, LatencyAvg: 25, Recv: 2, Colo: "NRT", SpeedMBps: 0},
		{IP: "1.1.1.4", Latency: Unreachable, LatencyAvg: Unreachable, Recv: 0},
	}

	got := Summarize(records)

	if got.Total != 4 {
		t.Errorf("Total = %d，期望 4", got.Total)
	}
	if got.MinLatency != 10 {
		t.Errorf("MinLatency = %v，期望 10", got.MinLatency)
	}
	if want := (12.0 + 33 + 25) / 3; got.AvgLatency != want {
		t.Errorf("AvgLatency = %v，期望 %v（不可达记录不参与平均）", got.AvgLatency, want)
	}
	if got.BestSpeed != 20 {
		t.Errorf("BestSpeed = %v，期望 20", got.BestSpeed)
	}
	if want := 15.0; got.AvgSpeed != want {
		t.Errorf("AvgSpeed = %v，期望 %v（未测速的记录不参与平均）", got.AvgSpeed, want)
	}
	if got.RegionDist["HKG"] != 2 || got.RegionDist["NRT"] != 1 {
		t.Errorf("RegionDist = %v，期望 HKG=2、NRT=1", got.RegionDist)
	}
	if got.ASNDist["CLOUDFLARENET"] != 2 {
		t.Errorf("ASNDist = %v，期望 CLOUDFLARENET=2", got.ASNDist)
	}
}

func TestSummarizeAllUnreachable(t *testing.T) {
	got := Summarize([]IPRecord{
		{IP: "1.1.1.1", Latency: Unreachable, LatencyAvg: Unreachable},
		{IP: "1.1.1.2", Latency: Unreachable, LatencyAvg: Unreachable},
	})

	if got.MinLatency != Unreachable {
		t.Errorf("MinLatency = %v，期望哨兵值", got.MinLatency)
	}
	if got.AvgLatency != 0 {
		t.Errorf("AvgLatency = %v，期望 0", got.AvgLatency)
	}
	if got.BestSpeed != 0 || got.AvgSpeed != 0 {
		t.Errorf("速度统计 = %v/%v，期望 0/0", got.BestSpeed, got.AvgSpeed)
	}
}

func TestSummarizeEmpty(t *testing.T) {
	got := Summarize(nil)
	if got.Total != 0 {
		t.Errorf("Total = %d，期望 0", got.Total)
	}
	if got.MinLatency != Unreachable {
		t.Errorf("MinLatency = %v，期望哨兵值", got.MinLatency)
	}
	if got.RegionDist == nil || got.ASNDist == nil {
		t.Error("分布表必须已初始化，否则前端拿到 null 无法遍历")
	}
}

func TestNewSummaryHasInitializedMaps(t *testing.T) {
	got := NewSummary()
	if got.RegionDist == nil || got.ASNDist == nil {
		t.Error("分布表必须已初始化")
	}
}

func TestIPRecordJSONRoundTrip(t *testing.T) {
	// 字段名就是前后端契约字段名，改名会直接打断前端。
	const raw = `{"ip":"1.1.1.1","port":443,"use_tls":true,"latency":10.5,` +
		`"latency_avg":12,"latency_max":15,"jitter":2,"loss":0,"sent":4,"recv":4,` +
		`"colo":"HKG","loc":"HK","region_name":"中国香港","asn":13335,` +
		`"as_org":"CLOUDFLARENET","speed_mbps":20.5,"score":9.09}`

	var record IPRecord
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		t.Fatalf("反序列化失败：%v", err)
	}
	if record.IP != "1.1.1.1" || record.Port != 443 || !record.UseTLS {
		t.Errorf("基本字段 = %+v", record)
	}
	if record.Latency != 10.5 || record.LatencyAvg != 12 || record.LatencyMax != 15 {
		t.Errorf("延迟字段 = %+v", record)
	}
	if record.ASN != 13335 || record.ASOrg != "CLOUDFLARENET" {
		t.Errorf("ASN 字段 = %+v", record)
	}
	if record.Colo != "HKG" || record.Loc != "HK" {
		t.Errorf("地区字段 = %+v", record)
	}

	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	var again IPRecord
	if err := json.Unmarshal(encoded, &again); err != nil {
		t.Fatalf("二次反序列化失败：%v", err)
	}
	reEncoded, err := json.Marshal(again)
	if err != nil {
		t.Fatalf("二次序列化失败：%v", err)
	}
	// 结构体里有 map，不能直接比较，用序列化结果比对。
	if string(reEncoded) != string(encoded) {
		t.Errorf("往返后不一致：\n%s\n%s", encoded, reEncoded)
	}
}

func TestParamOriginsGetDefaultsToDefault(t *testing.T) {
	var nilTable ParamOrigins
	if got := nilTable.Get("scan.workers"); got != OriginDefault {
		t.Errorf("nil 表 Get = %q，期望 %q", got, OriginDefault)
	}

	table := ParamOrigins{"scan.workers": OriginUser}
	if got := table.Get("scan.workers"); got != OriginUser {
		t.Errorf("Get = %q，期望 %q", got, OriginUser)
	}
	if got := table.Get("scan.port"); got != OriginDefault {
		t.Errorf("缺失键 Get = %q，期望 %q", got, OriginDefault)
	}

	table["empty"] = ""
	if got := table.Get("empty"); got != OriginDefault {
		t.Errorf("空值 Get = %q，期望 %q", got, OriginDefault)
	}
}

func TestParamOriginsCanAutoAdjust(t *testing.T) {
	// 这是「自动逻辑绝不覆盖用户的显式选择」的唯一判定入口。
	table := ParamOrigins{
		"scan.workers": OriginUser,
		"scan.port":    OriginPreset,
		"scan.mode":    OriginDefault,
	}

	tests := []struct {
		key  string
		want bool
	}{
		{key: "scan.workers", want: false},
		{key: "scan.port", want: true},
		{key: "scan.mode", want: true},
		{key: "scan.timeout_ms", want: true},
	}

	for _, tt := range tests {
		if got := table.CanAutoAdjust(tt.key); got != tt.want {
			t.Errorf("CanAutoAdjust(%q) = %v，期望 %v", tt.key, got, tt.want)
		}
	}
}

func TestParamOriginsSet(t *testing.T) {
	table := ParamOrigins{}
	table.Set("scan.workers", OriginUser)
	if table["scan.workers"] != OriginUser {
		t.Errorf("Set 未生效：%v", table)
	}

	// 空值按默认来源处理，避免写入一个无意义的空字符串。
	table.Set("scan.port", "")
	if table["scan.port"] != OriginDefault {
		t.Errorf("空值应写成 %q，实际 %q", OriginDefault, table["scan.port"])
	}

	// 空键直接忽略。
	table.Set("", OriginUser)
	if _, ok := table[""]; ok {
		t.Error("空键不应写入")
	}
}

func TestParamOriginsClone(t *testing.T) {
	original := ParamOrigins{"scan.workers": OriginUser}
	clone := original.Clone()
	clone["scan.workers"] = OriginDefault
	clone["scan.port"] = OriginPreset

	if original["scan.workers"] != OriginUser {
		t.Error("修改副本不应影响原表")
	}
	if len(original) != 1 {
		t.Errorf("原表长度 = %d，期望 1", len(original))
	}

	var nilTable ParamOrigins
	if nilTable.Clone() != nil {
		t.Error("nil 表 Clone 应返回 nil")
	}
}

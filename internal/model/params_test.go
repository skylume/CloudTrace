package model

import (
	"encoding/json"
	"testing"
)

func TestScanParamsSourceMode(t *testing.T) {
	tests := []struct {
		mode         string
		wantOfficial bool
		wantCustom   bool
	}{
		{mode: "official", wantOfficial: true, wantCustom: false},
		{mode: "custom", wantOfficial: false, wantCustom: true},
		{mode: "both", wantOfficial: true, wantCustom: true},
		{mode: "", wantOfficial: false, wantCustom: false},
		{mode: "乱填", wantOfficial: false, wantCustom: false},
	}

	for _, tt := range tests {
		p := ScanParams{SourceMode: tt.mode}
		if got := p.UsesOfficial(); got != tt.wantOfficial {
			t.Errorf("SourceMode=%q UsesOfficial = %v，期望 %v", tt.mode, got, tt.wantOfficial)
		}
		if got := p.UsesCustom(); got != tt.wantCustom {
			t.Errorf("SourceMode=%q UsesCustom = %v，期望 %v", tt.mode, got, tt.wantCustom)
		}
	}
}

func TestScanParamsJSONFieldNames(t *testing.T) {
	// 字段名就是前后端契约，改名等于前端读不到值，因此这里逐一锁死。
	p := ScanParams{
		Mode:             "tcping",
		Workers:          200,
		SampleMax:        5000,
		LatencyThreshold: 230,
		PingTimes:        4,
		Port:             443,
		IPVersion:        4,
		SourceMode:       "official",
		CustomSource:     "1.1.1.1",
		PreFilterPorts:   []int{443},
		AllowedRegions:   []string{"HK"},
		BlockedRegions:   []string{"US"},
		TwoPhase:         true,
		VerifyNodes:      true,
		TimeoutMS:        1000,
		Retry:            0,
	}

	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}

	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("反序列化失败：%v", err)
	}

	want := []string{
		"mode", "workers", "sample_max", "latency_threshold", "ping_times",
		"port", "ip_version", "source_mode", "custom_source",
		"pre_filter_ports", "allowed_regions", "blocked_regions",
		"two_phase", "verify_nodes", "timeout_ms", "retry",
	}
	for _, key := range want {
		if _, ok := fields[key]; !ok {
			t.Errorf("缺少字段 %q", key)
		}
	}
	if len(fields) != len(want) {
		t.Errorf("字段数 = %d，期望 %d（%v）", len(fields), len(want), fields)
	}

	var back ScanParams
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("回读失败：%v", err)
	}
	if back.Mode != p.Mode || back.Workers != p.Workers || back.IPVersion != p.IPVersion {
		t.Errorf("回读结果与原文不一致：%+v", back)
	}
	if len(back.PreFilterPorts) != 1 || back.PreFilterPorts[0] != 443 {
		t.Errorf("pre_filter_ports 回读为 %v", back.PreFilterPorts)
	}
}

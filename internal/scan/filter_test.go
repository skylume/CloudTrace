package scan

import "testing"

func TestPreFilterOrderIsPortThenBlockThenAllow(t *testing.T) {
	// 三组候选各自同时命中多个过滤条件，看它被记在哪一组上，就能反推
	// 执行顺序。顺序错了（例如白名单跑到黑名单前面），计数会跟着错位。
	opts := PreFilterOptions{
		Ports:          []int{443},
		BlockedRegions: []string{"US"},
		AllowedRegions: []string{"HK"},
	}
	candidates := []Candidate{
		{IP: "1.1.1.1", Port: 8443, Loc: "US"}, // 端口错 + 被拉黑 + 不在白名单
		{IP: "1.1.1.2", Port: 443, Loc: "US"},  // 端口对 + 被拉黑 + 不在白名单
		{IP: "1.1.1.3", Port: 443, Loc: "JP"},  // 端口对 + 未拉黑 + 不在白名单
		{IP: "1.1.1.4", Port: 443, Loc: "HK"},  // 全通过
		{IP: "1.1.1.5", Port: 443},             // 地区未知
	}

	got := PreFilter(candidates, opts)

	if got.PortRejected != 1 {
		t.Errorf("端口淘汰 %d 个，期望 1 个（端口必须是第一轮）", got.PortRejected)
	}
	if got.BlockRejected != 1 {
		t.Errorf("黑名单淘汰 %d 个，期望 1 个（黑名单必须在端口之后、白名单之前）", got.BlockRejected)
	}
	if got.AllowRejected != 1 {
		t.Errorf("白名单淘汰 %d 个，期望 1 个（白名单必须最后）", got.AllowRejected)
	}

	if len(got.Kept) != 2 {
		t.Fatalf("保留 %d 个 %v，期望 2 个", len(got.Kept), got.Kept)
	}
	if got.Kept[0].IP != "1.1.1.4" {
		t.Errorf("第 1 个保留项 = %s，期望 1.1.1.4", got.Kept[0].IP)
	}
	// 地区未知的候选必须活下来：判不出来就放过去，误杀比多探一次更糟。
	if got.Kept[1].IP != "1.1.1.5" {
		t.Errorf("第 2 个保留项 = %s，期望 1.1.1.5（地区未知应保守保留）", got.Kept[1].IP)
	}
	if got.Rejected() != 3 {
		t.Errorf("淘汰总数 = %d，期望 3", got.Rejected())
	}
}

func TestPreFilterPorts(t *testing.T) {
	candidates := []Candidate{
		{IP: "1.1.1.1", Port: 443},
		{IP: "1.1.1.2", Port: 8443},
		{IP: "1.1.1.3", Port: 2053},
	}

	t.Run("列表为空表示不限制", func(t *testing.T) {
		got := PreFilter(candidates, PreFilterOptions{})
		if len(got.Kept) != 3 {
			t.Errorf("保留 %d 个，期望 3 个（空列表不得过滤）", len(got.Kept))
		}
	})

	t.Run("只保留列表里的端口", func(t *testing.T) {
		got := PreFilter(candidates, PreFilterOptions{Ports: []int{443, 2053}})
		if len(got.Kept) != 2 {
			t.Fatalf("保留 %d 个，期望 2 个", len(got.Kept))
		}
		if got.Kept[0].Port != 443 || got.Kept[1].Port != 2053 {
			t.Errorf("保留项端口 = %d/%d，期望 443/2053", got.Kept[0].Port, got.Kept[1].Port)
		}
		if got.PortRejected != 1 {
			t.Errorf("端口淘汰 %d 个，期望 1 个", got.PortRejected)
		}
	})
}

func TestPreFilterRegionMatching(t *testing.T) {
	tests := []struct {
		name   string
		cand   Candidate
		opts   PreFilterOptions
		reject bool
	}{
		{
			name:   "黑名单命中出口国家码",
			cand:   Candidate{IP: "1.1.1.1", Port: 443, Loc: "US"},
			opts:   PreFilterOptions{BlockedRegions: []string{"US"}},
			reject: true,
		},
		{
			name:   "黑名单命中数据中心代码",
			cand:   Candidate{IP: "1.1.1.1", Port: 443, Colo: "HKG"},
			opts:   PreFilterOptions{BlockedRegions: []string{"hkg"}},
			reject: true,
		},
		{
			name:   "黑名单大小写不敏感",
			cand:   Candidate{IP: "1.1.1.1", Port: 443, Loc: "jp"},
			opts:   PreFilterOptions{BlockedRegions: []string{"JP"}},
			reject: true,
		},
		{
			name:   "黑名单未命中",
			cand:   Candidate{IP: "1.1.1.1", Port: 443, Loc: "HK"},
			opts:   PreFilterOptions{BlockedRegions: []string{"US"}},
			reject: false,
		},
		{
			name:   "白名单命中",
			cand:   Candidate{IP: "1.1.1.1", Port: 443, Loc: "HK"},
			opts:   PreFilterOptions{AllowedRegions: []string{"HK"}},
			reject: false,
		},
		{
			name:   "白名单未命中",
			cand:   Candidate{IP: "1.1.1.1", Port: 443, Loc: "JP"},
			opts:   PreFilterOptions{AllowedRegions: []string{"HK"}},
			reject: true,
		},
		{
			name:   "白名单非空但地区未知",
			cand:   Candidate{IP: "1.1.1.1", Port: 443},
			opts:   PreFilterOptions{AllowedRegions: []string{"HK"}},
			reject: false,
		},
		{
			name:   "空白地区码不参与匹配",
			cand:   Candidate{IP: "1.1.1.1", Port: 443, Loc: "   "},
			opts:   PreFilterOptions{BlockedRegions: []string{"  ", "US"}},
			reject: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PreFilter([]Candidate{tt.cand}, tt.opts)
			rejected := len(got.Kept) == 0
			if rejected != tt.reject {
				t.Errorf("淘汰 = %v，期望 %v（结果 %+v）", rejected, tt.reject, got)
			}
		})
	}
}

func TestPreFilterEmptyInput(t *testing.T) {
	got := PreFilter(nil, PreFilterOptions{Ports: []int{443}})
	if len(got.Kept) != 0 || got.Rejected() != 0 {
		t.Errorf("空输入应原样返回，实际 %+v", got)
	}
}

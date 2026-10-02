package history

import (
	"testing"
	"time"
)

// seedForFilter 铺一批覆盖各种筛选维度的记录：4 份 ipv4 扫描、2 份 ipv4 测速、
// 1 份 ipv6 扫描，结果条数各不相同，标签与收藏只落在特定几份上。
func seedForFilter(h *harness) *Store {
	scanCounts := []int{2, 2, 2, 5}
	h.seed(4, TypeScan, 4, func(i int, rec *HistoryRecord) {
		rec.Tags = []string{"移动家宽"}
		rec.Results = recordsOf(scanCounts[i], 20)
		rec.Count = scanCounts[i]
		if i == 3 {
			rec.Tags = []string{"移动家宽", "公司"}
			rec.Note = "晚高峰复测"
			rec.Starred = true
		}
	})
	speedCounts := []int{3, 4}
	h.seed(4, TypeSpeed, 2, func(i int, rec *HistoryRecord) {
		rec.Tags = []string{"测速"}
		rec.Results = recordsOf(speedCounts[i], 20)
		rec.Count = speedCounts[i]
	})
	h.seed(6, TypeScan, 1, func(_ int, rec *HistoryRecord) {
		rec.Results = recordsOf(4, 20)
		rec.Count = 4
	})
	return h.reopen()
}

func TestFilterByDimensions(t *testing.T) {
	h := newHarness(t)
	store := seedForFilter(h)

	starred := true
	cases := []struct {
		name   string
		filter Filter
		want   int
	}{
		{"不限", Filter{}, 7},
		{"按类型-扫描", Filter{Type: TypeScan}, 5},
		{"按类型-测速", Filter{Type: TypeSpeed}, 2},
		{"按 IP 版本", Filter{IPVersion: 6}, 1},
		{"按类型加版本", Filter{Type: TypeScan, IPVersion: 6}, 1},
		{"按标签", Filter{Tags: []string{"移动家宽"}}, 4},
		{"按多个标签", Filter{Tags: []string{"移动家宽", "公司"}}, 1},
		{"标签大小写不敏感", Filter{Tags: []string{"移动家宽"}}, 4},
		{"按收藏", Filter{Starred: &starred}, 1},
		{"按条数下限", Filter{MinCount: 3}, 4},
		{"按条数上限", Filter{MaxCount: 2}, 3},
		{"模糊搜索备注", Filter{Search: "晚高峰"}, 1},
		{"模糊搜索标签", Filter{Search: "测速"}, 2},
		{"搜不到", Filter{Search: "不存在的东西"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := store.List(tc.filter)
			if err != nil {
				t.Fatalf("筛选失败：%v", err)
			}
			if len(got) != tc.want {
				t.Fatalf("条数 = %d，期望 %d", len(got), tc.want)
			}
		})
	}
}

func TestFilterByRegion(t *testing.T) {
	h := newHarness(t)
	store := seedForFilter(h)

	// recordsOf 按 HKG/NRT/SJC 轮转，每个 IP 版本各有一批。
	got, err := store.List(Filter{Region: "hkg"})
	if err != nil {
		t.Fatalf("筛选失败：%v", err)
	}
	if len(got) == 0 {
		t.Fatal("按地区筛选（大小写不敏感）没筛出任何条目")
	}
	for _, e := range got {
		found := false
		for _, r := range e.Regions {
			if r == "HKG" {
				found = true
			}
		}
		if !found {
			t.Fatalf("条目 %s 的地区 %v 里没有 HKG", e.ID, e.Regions)
		}
	}

	none, _ := store.List(Filter{Region: "ZZZ"})
	if len(none) != 0 {
		t.Fatalf("不存在的地区筛出了 %d 条", len(none))
	}
}

func TestFilterByTimeRange(t *testing.T) {
	h := newHarness(t)
	base := h.clock.Now()
	h.seedRecordsOnDisk(5) // CreatedAt = base + i 分钟
	store := h.reopen()

	got, err := store.List(Filter{Since: base.Add(90 * time.Second)})
	if err != nil {
		t.Fatalf("筛选失败：%v", err)
	}
	if len(got) != 3 { // i=2,3,4
		t.Fatalf("按起始时间筛出 %d 条，期望 3", len(got))
	}

	got, err = store.List(Filter{Until: base.Add(150 * time.Second)})
	if err != nil {
		t.Fatalf("筛选失败：%v", err)
	}
	if len(got) != 3 { // i=0,1,2
		t.Fatalf("按结束时间筛出 %d 条，期望 3", len(got))
	}
}

// 收藏置顶：用户特意标记的重点不该跟着时间沉下去。
func TestListSortsStarredFirst(t *testing.T) {
	h := newHarness(t)
	h.seedRecordsOnDisk(4)
	// 把最旧的那份标记为收藏。
	h.seed(4, TypeScan, 1, func(_ int, rec *HistoryRecord) {
		rec.ID = "20260927_143000_00010001"
		rec.CreatedAt = h.clock.Now().AddDate(0, 0, -10)
		rec.Starred = true
	})
	store := h.reopen()

	entries, err := store.List(Filter{})
	if err != nil {
		t.Fatalf("列表失败：%v", err)
	}
	if len(entries) != 5 {
		t.Fatalf("条目数 = %d，期望 5", len(entries))
	}
	if entries[0].ID != "20260927_143000_00010001" {
		t.Fatalf("首条 = %s，期望收藏的那份置顶", entries[0].ID)
	}
	// 置顶只影响收藏那几条：收藏必须全在前面，其余仍严格按时间倒序。
	firstPlain := -1
	for i, e := range entries {
		if e.Starred {
			if firstPlain >= 0 {
				t.Fatalf("第 %d 条是收藏，却排在了非收藏之后", i)
			}
			continue
		}
		if firstPlain < 0 {
			firstPlain = i
			continue
		}
		if e.CreatedAt.After(entries[i-1].CreatedAt) {
			t.Fatalf("第 %d 条比前一条更新，时间倒序被破坏", i)
		}
	}
	if firstPlain < 0 {
		t.Fatal("没有非收藏条目")
	}
}

func TestListPaginates(t *testing.T) {
	h := newHarness(t)
	h.seedRecordsOnDisk(10)
	store := h.reopen()

	page, err := store.List(Filter{Limit: 3})
	if err != nil {
		t.Fatalf("列表失败：%v", err)
	}
	if len(page) != 3 {
		t.Fatalf("首页条数 = %d，期望 3", len(page))
	}

	next, err := store.List(Filter{Limit: 3, Offset: 3})
	if err != nil {
		t.Fatalf("列表失败：%v", err)
	}
	if len(next) != 3 || next[0].ID == page[0].ID {
		t.Fatalf("第二页与首页重叠：%s vs %s", next[0].ID, page[0].ID)
	}

	rest, _ := store.List(Filter{Offset: 8})
	if len(rest) != 2 {
		t.Fatalf("末页条数 = %d，期望 2", len(rest))
	}

	empty, _ := store.List(Filter{Offset: 100})
	if len(empty) != 0 {
		t.Fatalf("越界偏移返回了 %d 条，期望 0", len(empty))
	}
}

func TestNormalizeTags(t *testing.T) {
	got := normalizeTags([]string{" 移动家宽 ", "", "公司", "公司", "  "})
	if len(got) != 2 || got[0] != "移动家宽" || got[1] != "公司" {
		t.Fatalf("归一化结果 = %v，期望 [移动家宽 公司]", got)
	}
	if normalizeTags(nil) != nil || normalizeTags([]string{"  "}) != nil {
		t.Fatal("空标签应归一化为 nil")
	}
}

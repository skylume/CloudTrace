package history

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"cloudtrace/internal/config"
)

// seedInAllCategories 在四个分类里各铺 n 份记录。
func seedInAllCategories(h *harness, n int) {
	for _, c := range Categories() {
		h.seed(c.IPVersion, c.Type, n, nil)
	}
}

// countRecords 统计磁盘上真实存在的记录文件数（按分类）。
func countRecords(t *testing.T, h *harness) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, c := range Categories() {
		entries, err := os.ReadDir(filepath.Join(h.dir, c.DirName()))
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || name == latestName || filepath.Ext(name) != ".json" {
				continue
			}
			out[c.DirName()]++
		}
	}
	return out
}

func TestCleanupKeepsCountPerCategory(t *testing.T) {
	h := newHarness(t, func(c *config.HistoryConfig) { c.KeepCount = 3 })
	seedInAllCategories(h, 5)

	store := h.reopen()
	n, err := store.Cleanup()
	if err != nil {
		t.Fatalf("清理失败：%v", err)
	}
	if n != 8 { // 四类各 5 份，各留 3 份
		t.Fatalf("删除份数 = %d，期望 8", n)
	}

	for dir, count := range countRecords(t, h) {
		if count != 3 {
			t.Fatalf("%s 剩 %d 份，期望 3", dir, count)
		}
	}
}

// 保留份数是设置项，改完必须立刻生效，不能等重启。
func TestCleanupCountChangeTakesEffectImmediately(t *testing.T) {
	h := newHarness(t, func(c *config.HistoryConfig) { c.KeepCount = 20 })
	h.seedRecordsOnDisk(5)

	store := h.reopen()
	if entries, _ := store.List(Filter{}); len(entries) != 5 {
		t.Fatalf("初始条目数 = %d，期望 5", len(entries))
	}

	h.SetConfig(func(c *config.HistoryConfig) { c.KeepCount = 3 })
	if _, err := store.Cleanup(); err != nil {
		t.Fatalf("清理失败：%v", err)
	}

	entries, _ := store.List(Filter{})
	if len(entries) != 3 {
		t.Fatalf("改小保留份数后条目数 = %d，期望 3", len(entries))
	}
	if got := countRecords(t, h)[Category{4, TypeScan}.DirName()]; got != 3 {
		t.Fatalf("磁盘上剩 %d 份，期望 3", got)
	}
}

// 收藏是用户的显式意图，任何自动清理都不能覆盖它。
func TestCleanupNeverDeletesStarred(t *testing.T) {
	h := newHarness(t, func(c *config.HistoryConfig) { c.KeepCount = 2 })
	// i 越小越旧；把最旧的两份标记为收藏。
	h.seed(4, TypeScan, 5, func(i int, rec *HistoryRecord) { rec.Starred = i < 2 })

	store := h.reopen()
	if _, err := store.Cleanup(); err != nil {
		t.Fatalf("清理失败：%v", err)
	}

	entries, _ := store.List(Filter{})
	if len(entries) != 4 {
		t.Fatalf("条目数 = %d，期望「2 份收藏 + 2 份最新的非收藏」= 4", len(entries))
	}
	starred := 0
	for _, e := range entries {
		if e.Starred {
			starred++
		}
	}
	if starred != 2 {
		t.Fatalf("收藏数 = %d，期望 2", starred)
	}
	for _, e := range entries {
		if e.Starred {
			if _, err := store.Load(e.ID); err != nil {
				t.Fatalf("收藏的记录被删掉了：%v", err)
			}
		}
	}
}

func TestCleanupDaysMode(t *testing.T) {
	h := newHarness(t, func(c *config.HistoryConfig) {
		c.KeepMode = "days"
		c.KeepDays = 2
	})
	// i 天前创建；保留 2 天意味着「两天前」还在，「三天前」被删。
	h.seed(4, TypeScan, 5, func(i int, rec *HistoryRecord) {
		rec.CreatedAt = h.clock.Now().AddDate(0, 0, -i)
	})

	store := h.reopen()
	if _, err := store.Cleanup(); err != nil {
		t.Fatalf("清理失败：%v", err)
	}

	entries, _ := store.List(Filter{})
	if len(entries) != 3 {
		t.Fatalf("条目数 = %d，期望保留最近 3 天的 3 份", len(entries))
	}
}

func TestCleanupDaysModeStillExemptsStarred(t *testing.T) {
	h := newHarness(t, func(c *config.HistoryConfig) {
		c.KeepMode = "days"
		c.KeepDays = 1
	})
	h.seed(4, TypeScan, 3, func(i int, rec *HistoryRecord) {
		rec.CreatedAt = h.clock.Now().AddDate(0, 0, -(i + 5))
		rec.Starred = i == 0
	})

	store := h.reopen()
	if _, err := store.Cleanup(); err != nil {
		t.Fatalf("清理失败：%v", err)
	}

	entries, _ := store.List(Filter{})
	if len(entries) != 1 || !entries[0].Starred {
		t.Fatalf("条目 = %+v，期望只剩那份收藏的", entries)
	}
}

// 清理只允许删历史目录里的记录文件。
func TestCleanupDoesNotTouchOutsideHistory(t *testing.T) {
	h := newHarness(t, func(c *config.HistoryConfig) { c.KeepCount = 1 })

	outside := filepath.Join(h.dir, "..", "outside.json")
	if err := os.WriteFile(outside, []byte("外部文件"), 0o600); err != nil {
		t.Fatalf("准备外部文件失败：%v", err)
	}
	t.Cleanup(func() { _ = os.Remove(outside) })

	h.seedRecordsOnDisk(5)
	store := h.reopen()
	if _, err := store.Cleanup(); err != nil {
		t.Fatalf("清理失败：%v", err)
	}

	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("历史目录外的文件被删了：%v", err)
	}
	// 索引文件本身也不该被当成记录删掉。
	if _, err := os.Stat(filepath.Join(h.dir, indexName)); err != nil {
		t.Fatalf("索引文件被删了：%v", err)
	}
}

func TestAssertInsideRejectsEscape(t *testing.T) {
	root := filepath.Join("data", "history")
	if err := assertInside(root, filepath.Join(root, "ipv4", "scan", "a.json")); err != nil {
		t.Fatalf("目录内的路径不该被拒：%v", err)
	}
	for _, p := range []string{
		filepath.Join(root, "..", "config.json"),
		filepath.Join(root, "..", "..", "etc", "passwd"),
		"data",
	} {
		if err := assertInside(root, p); err == nil {
			t.Fatalf("期望拒绝 %s", p)
		}
	}
}

func TestOverCountSkipsStarredAndOtherCategories(t *testing.T) {
	base := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	idx := NewIndex()
	// 最新的在前：s1 收藏、s2 s3 s4 非收藏、p1 属于另一类。
	idx.Entries = []HistoryIndexEntry{
		{ID: "s1", Type: TypeScan, IPVersion: 4, CreatedAt: base.Add(4 * time.Minute), Starred: true},
		{ID: "s2", Type: TypeScan, IPVersion: 4, CreatedAt: base.Add(3 * time.Minute)},
		{ID: "s3", Type: TypeScan, IPVersion: 4, CreatedAt: base.Add(2 * time.Minute)},
		{ID: "s4", Type: TypeScan, IPVersion: 4, CreatedAt: base.Add(time.Minute)},
		{ID: "p1", Type: TypeSpeed, IPVersion: 4, CreatedAt: base},
	}

	got := overCount(idx, Category{4, TypeScan}, 2)
	if len(got) != 1 || got[0].ID != "s4" {
		t.Fatalf("超出份数的条目 = %+v，期望只有 s4", got)
	}

	// 保留份数被写成 0 或负数时按 1 处理，绝不能理解成「一份都不留」。
	got = overCount(idx, Category{4, TypeScan}, 0)
	if len(got) != 2 {
		t.Fatalf("保留份数 0 时删除 %d 份，期望按 1 处理删 2 份", len(got))
	}
}

func TestCleanupOnEmptyIndexIsNoop(t *testing.T) {
	h := newHarness(t, func(c *config.HistoryConfig) { c.KeepCount = 3 })
	store := h.reopen()

	n, err := store.Cleanup()
	if err != nil || n != 0 {
		t.Fatalf("空历史清理 = (%d, %v)，期望 (0, nil)", n, err)
	}
	if got := h.actions(); len(got) != 0 {
		t.Fatalf("没有删除却发了事件：%v", got)
	}
}

func TestCleanupPublishesChange(t *testing.T) {
	h := newHarness(t, func(c *config.HistoryConfig) { c.KeepCount = 1 })
	h.seedRecordsOnDisk(3)
	store := h.reopen()

	if _, err := store.Cleanup(); err != nil {
		t.Fatalf("清理失败：%v", err)
	}
	got := h.actions()
	if len(got) != 1 || got[0] != ActionCleanup {
		t.Fatalf("事件 = %v，期望 [cleanup]", got)
	}
}

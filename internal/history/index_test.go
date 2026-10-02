package history

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"cloudtrace/internal/model"
)

func TestIndexRebuildsWhenCorrupt(t *testing.T) {
	h := newHarness(t)
	h.seedRecordsOnDisk(5)
	if err := os.MkdirAll(h.dir, 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	if err := os.WriteFile(filepath.Join(h.dir, indexName), []byte("{ 这不是 JSON"), 0o600); err != nil {
		t.Fatalf("写坏索引失败：%v", err)
	}

	// 索引坏了也必须能起来：它是纯派生数据，记录文件才是真源。
	reopened := h.reopen()
	entries, err := reopened.List(Filter{})
	if err != nil {
		t.Fatalf("列列表失败：%v", err)
	}
	if len(entries) != 5 {
		t.Fatalf("条目数 = %d，期望重建出 5 条", len(entries))
	}

	// 重建后的索引文件本身要能解析，否则下次启动还得再修一遍。
	data, err := os.ReadFile(filepath.Join(h.dir, indexName))
	if err != nil {
		t.Fatalf("读取索引失败：%v", err)
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		t.Fatalf("重建后的索引仍无法解析：%v", err)
	}
	if idx.Version != IndexVersion || len(idx.Entries) != 5 {
		t.Fatalf("索引 = %+v，期望版本 %d 且 5 条", idx, IndexVersion)
	}
}

func TestIndexDropsEntriesWhoseFileIsGone(t *testing.T) {
	h := newHarness(t)
	rec := h.saveScan(4, scanParams(150), recordsOf(2, 20))

	// 模拟用户手工删了记录文件，或磁盘故障丢了它。
	if err := os.Remove(h.recordPath(4, TypeScan, rec.ID)); err != nil {
		t.Fatalf("删除记录文件失败：%v", err)
	}

	reopened := h.reopen()
	entries, _ := reopened.List(Filter{})
	if len(entries) != 0 {
		t.Fatalf("条目数 = %d，期望指向不存在文件的条目被清掉", len(entries))
	}
}

func TestIndexPicksUpUntrackedRecord(t *testing.T) {
	h := newHarness(t)
	h.saveScan(4, scanParams(150), recordsOf(2, 20))

	// 手工丢一份记录文件进目录（比如从别的机器拷过来），索引里没有它。
	h.seed(4, TypeScan, 1, func(_ int, rec *HistoryRecord) {
		rec.ID = "20260927_150000_beef0001"
	})

	reopened := h.reopen()
	entries, _ := reopened.List(Filter{})
	if len(entries) != 2 {
		t.Fatalf("条目数 = %d，期望把没有索引的记录也收进来", len(entries))
	}
}

func TestCorruptRecordIsSkippedNotFatal(t *testing.T) {
	h := newHarness(t)
	h.seedRecordsOnDisk(3)
	bad := h.recordPath(4, TypeScan, "20260927_143099_ffff0000")
	if err := os.WriteFile(bad, []byte("半截 JSON"), 0o600); err != nil {
		t.Fatalf("写坏记录失败：%v", err)
	}

	reopened := h.reopen()
	entries, _ := reopened.List(Filter{})
	if len(entries) != 3 {
		t.Fatalf("条目数 = %d，期望跳过坏记录后仍有 3 条", len(entries))
	}
}

func TestConsistentIndexIsNotRebuilt(t *testing.T) {
	h := newHarness(t)
	h.seedRecordsOnDisk(4)
	reopened := h.reopen()

	// 已经一致时不该再重建：重建要读全部记录文件，每次启动都做一次是浪费。
	rebuilt, err := reopened.ensureConsistent()
	if err != nil {
		t.Fatalf("一致性校验失败：%v", err)
	}
	if rebuilt {
		t.Fatal("索引已经一致，不该重建")
	}
}

func TestIndexUpsertRemoveAndSort(t *testing.T) {
	base := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	idx := NewIndex()
	idx.Upsert(HistoryIndexEntry{ID: "a", CreatedAt: base})
	idx.Upsert(HistoryIndexEntry{ID: "c", CreatedAt: base.Add(2 * time.Minute)})
	idx.Upsert(HistoryIndexEntry{ID: "b", CreatedAt: base.Add(time.Minute)})

	got := []string{idx.Entries[0].ID, idx.Entries[1].ID, idx.Entries[2].ID}
	if got[0] != "c" || got[1] != "b" || got[2] != "a" {
		t.Fatalf("排序 = %v，期望 [c b a]", got)
	}

	// 同一个 ID 再写一次是替换而不是追加。
	idx.Upsert(HistoryIndexEntry{ID: "b", CreatedAt: base.Add(3 * time.Minute), Count: 7})
	if len(idx.Entries) != 3 {
		t.Fatalf("条目数 = %d，期望替换后仍是 3", len(idx.Entries))
	}
	if e, ok := idx.Find("b"); !ok || e.Count != 7 || idx.Entries[0].ID != "b" {
		t.Fatalf("替换后 = %+v，期望 b 排到最前且计数为 7", idx.Entries)
	}

	if !idx.Remove("b") {
		t.Fatal("删除已存在的条目应返回 true")
	}
	if idx.Remove("b") {
		t.Fatal("删除不存在的条目应返回 false")
	}
	if _, ok := idx.Find("b"); ok {
		t.Fatal("条目未被删除")
	}
}

func TestCategoryDirNames(t *testing.T) {
	cases := []struct {
		c    Category
		want string
	}{
		{Category{4, TypeScan}, filepath.Join("ipv4", "scan")},
		{Category{4, TypeSpeed}, filepath.Join("ipv4", "speed")},
		{Category{6, TypeScan}, filepath.Join("ipv6", "scan")},
		{Category{6, TypeSpeed}, filepath.Join("ipv6", "speed")},
	}
	for _, tc := range cases {
		if got := tc.c.DirName(); got != tc.want {
			t.Fatalf("%+v 的目录 = %q，期望 %q", tc.c, got, tc.want)
		}
	}
	if len(Categories()) != 4 {
		t.Fatalf("分类数 = %d，期望 4", len(Categories()))
	}
}

func TestSafeJoinRejectsEscape(t *testing.T) {
	root := filepath.Join("data", "history")
	if _, err := safeJoin(root, "..", "..", "config.json"); err == nil {
		t.Fatal("期望拒绝越界路径，实际通过")
	}
	got, err := safeJoin(root, "ipv4", "scan", "a.json")
	if err != nil {
		t.Fatalf("正常路径不该报错：%v", err)
	}
	if got != filepath.Join(root, "ipv4", "scan", "a.json") {
		t.Fatalf("路径 = %q", got)
	}
}

func TestValidID(t *testing.T) {
	good := []string{
		"20260927_143012_ab12cd34",
		"20260101_000000_00000000",
	}
	bad := []string{
		"", "20260927_143012_AB12CD34", "20260927-143012-ab12cd34",
		"20260927_14301_ab12cd34", "20260927_143012_ab12cd3",
		"20260927_143012_ab12cd345", "../20260927_143012_ab12cd34",
		"20260927_143012_zzzzzzzz", "20260927_143012_ab12",
	}
	for _, id := range good {
		if !validID(id) {
			t.Fatalf("%q 应当是合法 ID", id)
		}
	}
	for _, id := range bad {
		if validID(id) {
			t.Fatalf("%q 应当被拒绝", id)
		}
	}
}

func TestNewIDIsWellFormed(t *testing.T) {
	now := time.Date(2026, 9, 27, 14, 30, 12, 0, time.UTC)
	seen := make(map[string]bool, 200)
	for i := 0; i < 200; i++ {
		id, err := NewID(now)
		if err != nil {
			t.Fatalf("生成 ID 失败：%v", err)
		}
		if !validID(id) {
			t.Fatalf("生成的 ID 形状不对：%q", id)
		}
		if seen[id] {
			t.Fatalf("同一秒内出现重复 ID：%q", id)
		}
		seen[id] = true
	}
}

// ID 同时是文件名，撞名的后果是**静默覆盖**一份已有记录，用户不会收到
// 任何提示。随机后缀把概率压得很低，但存档仍然要对着索引确认一次。
func TestSaveRetriesWhenGeneratedIDCollides(t *testing.T) {
	dir := newTestDir(t)
	ids := []string{
		"20260927_143012_aaaaaaaa",
		"20260927_143012_aaaaaaaa", // 撞上第一份
		"20260927_143012_bbbbbbbb",
	}
	var mu sync.Mutex
	next := 0
	store := newStoreWithID(t, dir, func(time.Time) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if next >= len(ids) {
			return "20260927_143012_cccccccc", nil
		}
		id := ids[next]
		next++
		return id, nil
	})

	first, err := store.Save(HistoryRecord{Type: TypeScan, IPVersion: 4, Results: recordsOf(2, 20)})
	if err != nil {
		t.Fatalf("首次存档失败：%v", err)
	}
	if first.ID != "20260927_143012_aaaaaaaa" {
		t.Fatalf("首次 ID = %q", first.ID)
	}

	second, err := store.Save(HistoryRecord{Type: TypeScan, IPVersion: 4, Results: recordsOf(3, 20)})
	if err != nil {
		t.Fatalf("二次存档失败：%v", err)
	}
	if second.ID == first.ID {
		t.Fatal("ID 撞名后没有重试，会覆盖掉前一份记录")
	}

	entries, _ := store.List(Filter{})
	if len(entries) != 2 {
		t.Fatalf("条目数 = %d，期望两份都在", len(entries))
	}
	if rec, err := store.Load(first.ID); err != nil || rec.Count != 2 {
		t.Fatalf("前一份记录被覆盖了：%+v %v", rec, err)
	}
}

func TestSaveGivesUpWhenIDsKeepColliding(t *testing.T) {
	dir := newTestDir(t)
	const fixed = "20260927_143012_deadbeef"
	store := newStoreWithID(t, dir, func(time.Time) (string, error) { return fixed, nil })

	if _, err := store.Save(HistoryRecord{Type: TypeScan, IPVersion: 4, Results: recordsOf(2, 20)}); err != nil {
		t.Fatalf("首次存档失败：%v", err)
	}
	if _, err := store.Save(HistoryRecord{Type: TypeScan, IPVersion: 4, Results: recordsOf(2, 20)}); err == nil {
		t.Fatal("ID 一直撞名时期望报错，而不是覆盖已有记录")
	}
}

func TestSaveRejectsMalformedGeneratedID(t *testing.T) {
	dir := newTestDir(t)
	store := newStoreWithID(t, dir, func(time.Time) (string, error) { return "not-an-id", nil })
	if _, err := store.Save(HistoryRecord{Type: TypeScan, IPVersion: 4, Results: recordsOf(2, 20)}); err == nil {
		t.Fatal("期望拒绝形状不合法的 ID")
	}
}

// newStoreWithID 用指定的 ID 生成器构造一个 Store。
func newStoreWithID(t *testing.T, dir string, gen func(time.Time) (string, error)) *Store {
	t.Helper()
	store, err := New(Options{
		Dir:        dir,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		UndoWindow: time.Hour,
		NewID:      gen,
	})
	if err != nil {
		t.Fatalf("构造 Store 失败：%v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestParamsHash(t *testing.T) {
	if ParamsHash(nil) != "" {
		t.Fatal("空参数快照的摘要应为空串")
	}
	if ParamsHash(json.RawMessage("")) != "" {
		t.Fatal("空参数快照的摘要应为空串")
	}
	a := ParamsHash(json.RawMessage(`{"workers":150}`))
	b := ParamsHash(json.RawMessage(`{"workers":150}`))
	c := ParamsHash(json.RawMessage(`{"workers":200}`))
	if a == "" || a != b {
		t.Fatalf("相同内容应得到相同摘要：%q vs %q", a, b)
	}
	if a == c {
		t.Fatal("不同内容不该得到相同摘要")
	}
}

func TestEntryExtractsRegionsAndLatency(t *testing.T) {
	results := []model.IPRecord{
		{IP: "1.1.1.1", Port: 443, Colo: "hkg", Latency: 20, LatencyAvg: 20, Recv: 3},
		{IP: "1.1.1.2", Port: 443, Colo: "NRT", Latency: 30, LatencyAvg: 30, Recv: 3},
		{IP: "1.1.1.3", Port: 443, Colo: "HKG", Latency: 40, LatencyAvg: 40, Recv: 3},
		{IP: "1.1.1.4", Port: 443, Latency: model.Unreachable, LatencyAvg: model.Unreachable},
	}
	rec := HistoryRecord{
		ID: "20260927_143012_ab12cd34", Type: TypeScan, IPVersion: 4,
		CreatedAt: time.Now(), Results: results, Summary: model.Summarize(results),
		Params: json.RawMessage(`{"a":1}`),
	}

	e := rec.Entry()
	if len(e.Regions) != 2 || e.Regions[0] != "HKG" || e.Regions[1] != "NRT" {
		t.Fatalf("地区 = %v，期望去重、大写、排序后的 [HKG NRT]", e.Regions)
	}
	if e.Count != 0 {
		t.Fatalf("Count = %d，期望沿用记录里的 0（由 Save 补）", e.Count)
	}
	if e.ParamsHash != ParamsHash(rec.Params) {
		t.Fatalf("参数摘要 = %q", e.ParamsHash)
	}
}

func TestValidateRejectsIncompleteRecords(t *testing.T) {
	base := HistoryRecord{
		ID: "20260927_143012_ab12cd34", Type: TypeScan, IPVersion: 4, CreatedAt: time.Now(),
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("合法记录被判为非法：%v", err)
	}

	cases := []HistoryRecord{
		{Type: TypeScan, IPVersion: 4, CreatedAt: time.Now()},
		{ID: "x", Type: "unknown", IPVersion: 4, CreatedAt: time.Now()},
		{ID: "x", Type: TypeScan, IPVersion: 5, CreatedAt: time.Now()},
		{ID: "x", Type: TypeScan, IPVersion: 4},
	}
	for i, c := range cases {
		if err := c.Validate(); err == nil {
			t.Fatalf("第 %d 个用例应当被判为非法", i)
		}
	}
}

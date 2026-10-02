package history

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cloudtrace/internal/config"
	"cloudtrace/internal/model"
)

// 这是本包最要紧的一条约束：列个表只允许读索引。
//
// 一份记录带着几千条结果，逐份解析就是几十次全量 JSON 反序列化——同类实现
// 里最典型的性能塌方点，正是索引要解决的问题。
func TestListNeverOpensRecordFiles(t *testing.T) {
	h := newHarness(t)
	h.seedRecordsOnDisk(100)

	// 换一个 Store 打开同一个目录：走的就是「首次运行、索引不存在 → 从
	// 记录文件重建」这条真实路径，顺带把一致性校验也覆盖了。
	reopened := h.reopen()
	if entries, err := reopened.List(Filter{}); err != nil {
		t.Fatalf("列列表失败：%v", err)
	} else if len(entries) != 100 {
		t.Fatalf("条目数 = %d，期望 100", len(entries))
	}

	h.opens.reset()
	entries, err := reopened.List(Filter{})
	if err != nil {
		t.Fatalf("列列表失败：%v", err)
	}
	if len(entries) != 100 {
		t.Fatalf("条目数 = %d，期望 100", len(entries))
	}
	if opened := h.opens.snapshot(); len(opened) != 0 {
		t.Fatalf("列列表打开了 %d 个文件：%v", len(opened), opened)
	}
}

// 一键复用是最高频的路径，必须一次文件读取就结束。
func TestLoadLatestReadsOnlyLatestFile(t *testing.T) {
	h := newHarness(t)
	h.saveScan(4, scanParams(150), recordsOf(3, 20))
	h.saveScan(6, scanParams(150), recordsOf(2, 30))

	h.opens.reset()
	rec, err := h.store.LoadLatest(TypeScan, 4)
	if err != nil {
		t.Fatalf("加载最新一份失败：%v", err)
	}
	if rec.IPVersion != 4 || rec.Type != TypeScan {
		t.Fatalf("加载到的记录 = %s/%d，期望 scan/4", rec.Type, rec.IPVersion)
	}

	for _, path := range h.opens.snapshot() {
		if filepath.Base(path) != latestName {
			t.Fatalf("加载最新一份读了 %s，期望只读 %s", path, latestName)
		}
	}
}

func TestLoadLatestMissingReturnsNotFound(t *testing.T) {
	h := newHarness(t)
	if _, err := h.store.LoadLatest(TypeSpeed, 4); !errors.Is(err, ErrNotFound) {
		t.Fatalf("错误 = %v，期望 ErrNotFound", err)
	}
}

// 参数快照必须能原样取回，否则加载历史时的「参数差异提示」就是错的。
func TestParamsSnapshotRoundTrip(t *testing.T) {
	h := newHarness(t)
	want := scanParams(200)
	rec := h.saveScan(4, want, recordsOf(2, 20))

	loaded, err := h.store.Load(rec.ID)
	if err != nil {
		t.Fatalf("读取记录失败：%v", err)
	}
	got, err := loaded.ScanParams()
	if err != nil {
		t.Fatalf("解析参数快照失败：%v", err)
	}
	// 结构体含切片，不能直接比较，逐字段比对规范化后的 JSON。
	wantJSON, _ := json.Marshal(want)
	gotJSON, _ := json.Marshal(got)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("参数快照 = %s，期望 %s", gotJSON, wantJSON)
	}
	if loaded.Origins.Get("scan.workers") != model.OriginUser {
		t.Fatalf("参数来源 = %q，期望 %q", loaded.Origins.Get("scan.workers"), model.OriginUser)
	}
	if loaded.Preset != "标准" {
		t.Fatalf("档位 = %q，期望 标准", loaded.Preset)
	}
}

// 测速参数与扫描参数是两种类型，装在同一份记录的同一个字段里。
func TestSpeedParamsSnapshotRoundTrip(t *testing.T) {
	h := newHarness(t)
	want := model.SpeedParams{
		Scope:             model.SpeedScopeAll,
		URLMode:           "auto",
		Concurrency:       8,
		TargetQualified:   10,
		IntervalMS:        1200,
		WeightSpeed:       1,
		WeightLatency:     1,
		PerRegionTopN:     3,
		DownloadDurationS: 10,
		Breaker429:        3,
		UsabilityCheck:    true,
	}
	rec := h.saveSpeed(4, want, recordsOf(2, 20))

	loaded, err := h.store.Load(rec.ID)
	if err != nil {
		t.Fatalf("读取记录失败：%v", err)
	}
	got, err := loaded.SpeedParams()
	if err != nil {
		t.Fatalf("解析测速参数失败：%v", err)
	}
	if got.Scope != want.Scope || got.Concurrency != want.Concurrency ||
		got.TargetQualified != want.TargetQualified || got.PerRegionTopN != want.PerRegionTopN {
		t.Fatalf("测速参数快照 = %+v，期望 %+v", got, want)
	}

	// 类型对不上时必须报错，而不是返回一个全零的结构体让调用方以为「参数就是这样」。
	if _, err := loaded.ScanParams(); err == nil {
		t.Fatal("对测速记录调用 ScanParams 期望报错，实际成功")
	}
}

func TestSaveFillsIDAndCount(t *testing.T) {
	h := newHarness(t)
	rec := h.saveScan(4, scanParams(150), recordsOf(4, 20))

	if !validID(rec.ID) {
		t.Fatalf("生成的 ID 形状不对：%q", rec.ID)
	}
	if rec.Count != 4 {
		t.Fatalf("条数 = %d，期望 4", rec.Count)
	}
	if rec.CreatedAt.IsZero() {
		t.Fatal("创建时间未填充")
	}
}

func TestSavePublishesChange(t *testing.T) {
	h := newHarness(t)
	h.saveScan(4, scanParams(150), recordsOf(2, 20))

	if got := h.actions(); len(got) != 1 || got[0] != ActionSave {
		t.Fatalf("事件 = %v，期望 [save]", got)
	}
}

func TestSaveRejectsInvalidRecord(t *testing.T) {
	h := newHarness(t)
	if _, err := h.store.Save(HistoryRecord{Type: "bogus", IPVersion: 4}); err == nil {
		t.Fatal("期望拒绝非法类型，实际成功")
	}
	if _, err := h.store.Save(HistoryRecord{Type: TypeScan, IPVersion: 5}); err == nil {
		t.Fatal("期望拒绝非法 IP 版本，实际成功")
	}
}

func TestDeleteIsUndoable(t *testing.T) {
	h := newHarness(t)
	rec := h.saveScan(4, scanParams(150), recordsOf(2, 20))
	path := h.recordPath(4, TypeScan, rec.ID)

	if err := h.store.Delete(rec.ID); err != nil {
		t.Fatalf("删除失败：%v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("删除后原文件仍在：%v", err)
	}
	entries, _ := h.store.List(Filter{})
	if len(entries) != 0 {
		t.Fatalf("删除后列表仍有 %d 条", len(entries))
	}

	if err := h.store.Restore(rec.ID); err != nil {
		t.Fatalf("撤销失败：%v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("撤销后原文件不在：%v", err)
	}
	if _, err := h.store.Load(rec.ID); err != nil {
		t.Fatalf("撤销后读取失败：%v", err)
	}
	if got := h.actions(); len(got) != 3 || got[1] != ActionDelete || got[2] != ActionRestore {
		t.Fatalf("事件 = %v，期望 [save delete restore]", got)
	}
}

func TestDeletePurgesAfterWindow(t *testing.T) {
	h := newHarness(t)
	// 用一个很短的窗口，让定时器在测试里真的会触发。
	h.store.undoWindow = 20 * time.Millisecond

	rec := h.saveScan(4, scanParams(150), recordsOf(2, 20))
	if err := h.store.Delete(rec.ID); err != nil {
		t.Fatalf("删除失败：%v", err)
	}

	trash := filepath.Join(h.dir, trashDir, rec.ID+".json")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(trash); os.IsNotExist(err) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("撤销窗口过后中转文件仍未被真正删除")
}

func TestRestoreUnknownDeleteFails(t *testing.T) {
	h := newHarness(t)
	rec := h.saveScan(4, scanParams(150), recordsOf(2, 20))
	if err := h.store.Restore(rec.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("错误 = %v，期望 ErrNotFound", err)
	}
}

// 标注同时写进记录文件与索引：只写索引的话，下次重建索引就全丢了。
func TestUpdateMetaPersistsEverywhere(t *testing.T) {
	h := newHarness(t)
	rec := h.saveScan(4, scanParams(150), recordsOf(2, 20))

	if err := h.store.UpdateMeta(rec.ID, []string{" 移动家宽 ", "公司", "公司"}, "晚高峰", true); err != nil {
		t.Fatalf("更新标注失败：%v", err)
	}

	loaded, err := h.store.Load(rec.ID)
	if err != nil {
		t.Fatalf("读取失败：%v", err)
	}
	if len(loaded.Tags) != 2 || loaded.Tags[0] != "移动家宽" || loaded.Tags[1] != "公司" {
		t.Fatalf("标签 = %v，期望去空白去重后的 [移动家宽 公司]", loaded.Tags)
	}
	if loaded.Note != "晚高峰" || !loaded.Starred {
		t.Fatalf("备注/收藏 = %q/%v，期望 晚高峰/true", loaded.Note, loaded.Starred)
	}

	entries, _ := h.store.List(Filter{})
	if len(entries) != 1 || !entries[0].Starred || entries[0].Note != "晚高峰" {
		t.Fatalf("索引未同步：%+v", entries)
	}

	// 最新副本也要跟着更新，否则「一键复用」拿到的还是旧标注。
	latest, err := h.store.LoadLatest(TypeScan, 4)
	if err != nil {
		t.Fatalf("加载最新一份失败：%v", err)
	}
	if !latest.Starred || latest.Note != "晚高峰" {
		t.Fatalf("最新副本未同步：%+v", latest)
	}

	// 重建索引后标注必须还在——这是「记录文件才是真源」的验证。
	rebuilt, err := h.store.rebuildIndex()
	if err != nil {
		t.Fatalf("重建索引失败：%v", err)
	}
	if len(rebuilt.Entries) != 1 || !rebuilt.Entries[0].Starred {
		t.Fatalf("重建后标注丢失：%+v", rebuilt.Entries)
	}
}

// 记录 ID 参与拼路径，构造成 `../` 就能删到历史目录之外。
func TestIDIsValidatedBeforeTouchingDisk(t *testing.T) {
	h := newHarness(t)
	victim := filepath.Join(h.dir, "..", "victim.txt")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatalf("准备文件失败：%v", err)
	}
	t.Cleanup(func() { _ = os.Remove(victim) })

	for _, id := range []string{"../../victim", "..", "a/b", "20260927_143012_zzzz", ""} {
		if _, err := h.store.Load(id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Load(%q) 错误 = %v，期望 ErrNotFound", id, err)
		}
		if err := h.store.Delete(id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Delete(%q) 错误 = %v，期望 ErrNotFound", id, err)
		}
		if err := h.store.UpdateMeta(id, nil, "", false); !errors.Is(err, ErrNotFound) {
			t.Fatalf("UpdateMeta(%q) 错误 = %v，期望 ErrNotFound", id, err)
		}
	}

	if data, err := os.ReadFile(victim); err != nil || string(data) != "keep" {
		t.Fatalf("历史目录外的文件被动了：%v %q", err, data)
	}
}

// 同参数的重复存档只保留最新的一份，且保留的是**新**的那份。
func TestDedupKeepsNewestOfSameParams(t *testing.T) {
	h := newHarness(t)
	params := scanParams(150)

	first := h.saveScan(4, params, recordsOf(2, 20))
	h.clock.Advance(5 * time.Second)
	second := h.saveScan(4, params, recordsOf(3, 20))

	entries, _ := h.store.List(Filter{})
	if len(entries) != 1 {
		t.Fatalf("条目数 = %d，期望合并成 1 条", len(entries))
	}
	if entries[0].ID != second.ID {
		t.Fatalf("保留的是 %s，期望最新的 %s", entries[0].ID, second.ID)
	}
	if _, err := h.store.Load(first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("旧的重复记录仍可读取：%v", err)
	}
}

func TestDedupKeepsRecordsOutsideWindow(t *testing.T) {
	h := newHarness(t)
	params := scanParams(150)

	h.saveScan(4, params, recordsOf(2, 20))
	h.clock.Advance(10 * time.Minute)
	h.saveScan(4, params, recordsOf(2, 20))

	entries, _ := h.store.List(Filter{})
	if len(entries) != 2 {
		t.Fatalf("条目数 = %d，期望窗口外保留 2 条", len(entries))
	}
}

func TestDedupDisabledKeepsEverything(t *testing.T) {
	h := newHarness(t, func(c *config.HistoryConfig) { c.AutoDedup = false })
	params := scanParams(150)

	h.saveScan(4, params, recordsOf(2, 20))
	h.clock.Advance(time.Second)
	h.saveScan(4, params, recordsOf(2, 20))

	entries, _ := h.store.List(Filter{})
	if len(entries) != 2 {
		t.Fatalf("条目数 = %d，期望关掉去重后保留 2 条", len(entries))
	}
}

// 收藏的记录不参与去重：用户特意标记过的不能被自动合并掉。
func TestDedupSkipsStarred(t *testing.T) {
	h := newHarness(t)
	params := scanParams(150)

	first := h.saveScan(4, params, recordsOf(2, 20))
	if err := h.store.UpdateMeta(first.ID, nil, "", true); err != nil {
		t.Fatalf("收藏失败：%v", err)
	}
	h.clock.Advance(time.Second)
	h.saveScan(4, params, recordsOf(2, 20))

	entries, _ := h.store.List(Filter{})
	if len(entries) != 2 {
		t.Fatalf("条目数 = %d，期望收藏的那份被保留", len(entries))
	}
	if _, err := h.store.Load(first.ID); err != nil {
		t.Fatalf("收藏的记录被删掉了：%v", err)
	}
}

func TestLoadReturnsNotFoundForUnknownID(t *testing.T) {
	h := newHarness(t)
	if _, err := h.store.Load("20260927_143012_abcd0001"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("错误 = %v，期望 ErrNotFound", err)
	}
}

func TestNewRejectsEmptyDir(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("期望拒绝空目录，实际成功")
	}
}

// 记录文件里的结果集必须完整落盘，不只是索引里的摘要。
func TestRecordFileContainsResults(t *testing.T) {
	h := newHarness(t)
	rec := h.saveScan(4, scanParams(150), recordsOf(5, 20))

	data, err := os.ReadFile(h.recordPath(4, TypeScan, rec.ID))
	if err != nil {
		t.Fatalf("读取记录文件失败：%v", err)
	}
	var got HistoryRecord
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("记录文件不是合法 JSON：%v", err)
	}
	if len(got.Results) != 5 {
		t.Fatalf("结果条数 = %d，期望 5", len(got.Results))
	}
	if got.Summary.RegionDist["HKG"] == 0 {
		t.Fatalf("摘要地区分布缺失：%+v", got.Summary.RegionDist)
	}
}

// 目录里残留的临时文件不能被当成记录。
func TestLeftoverTempFilesAreIgnored(t *testing.T) {
	h := newHarness(t)
	rec := h.saveScan(4, scanParams(150), recordsOf(2, 20))

	dir := filepath.Join(h.dir, "ipv4", TypeScan)
	for _, name := range []string{".20260927_143012_abcd.json.tmp123", ".hidden.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{"), 0o600); err != nil {
			t.Fatalf("准备临时文件失败：%v", err)
		}
	}

	ids, err := h.store.listRecordIDs()
	if err != nil {
		t.Fatalf("扫描记录 ID 失败：%v", err)
	}
	if len(ids) != 1 || !ids[rec.ID] {
		t.Fatalf("识别到的记录 = %v，期望只有 %s", ids, rec.ID)
	}
}

func TestListReturnsCopies(t *testing.T) {
	h := newHarness(t)
	h.saveScan(4, scanParams(150), recordsOf(2, 20))

	entries, _ := h.store.List(Filter{})
	if len(entries) == 0 {
		t.Fatal("没有条目")
	}
	entries[0].Regions = append(entries[0].Regions, "污染")
	entries[0].ID = "污染"

	again, _ := h.store.List(Filter{})
	for _, e := range again {
		if strings.Contains(strings.Join(e.Regions, ","), "污染") || e.ID == "污染" {
			t.Fatal("列表返回了内部数据，外部修改污染了索引")
		}
	}
}

// 「这是 N 分钟前扫的」用的就是 AgeMinutes。
func TestAgeMinutes(t *testing.T) {
	h := newHarness(t)
	base := h.clock.Now()

	if got := h.store.AgeMinutes(base); got != 0 {
		t.Fatalf("同一时刻 = %v 分钟，期望 0", got)
	}

	h.clock.Advance(90 * time.Minute)
	if got := h.store.AgeMinutes(base); got != 90 {
		t.Fatalf("90 分钟后 = %v，期望 90", got)
	}

	// 时钟回拨时按 0 处理：显示「-30 分钟前」比不显示更让人困惑。
	h.clock.Advance(-2 * time.Hour)
	if got := h.store.AgeMinutes(base); got != 0 {
		t.Fatalf("时钟回拨后 = %v，期望 0", got)
	}

	// 零值时间表示没有时间信息，同样按 0 处理。
	if got := h.store.AgeMinutes(time.Time{}); got != 0 {
		t.Fatalf("零值时间 = %v，期望 0", got)
	}
}

// 历史根目录要能读出来，前端要靠它展示数据目录位置。
func TestStoreExposesDir(t *testing.T) {
	h := newHarness(t)
	if got := h.store.Dir(); got != h.dir {
		t.Fatalf("历史目录 = %q，期望 %q", got, h.dir)
	}
}

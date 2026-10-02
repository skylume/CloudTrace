package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// 并发读写：存档、列列表、读取、打标签、删除、清理同时发生。
//
// 这条用例的价值在 `-race` 下：索引是共享可变状态，任何一处漏加锁都会在这里
// 被逮到。
func TestConcurrentAccessIsRaceFree(t *testing.T) {
	h := newHarness(t)
	store := h.store

	// 每条存档要付三次 fsync，本机一次 fsync 要几百毫秒，用例规模刻意压小：
	// 这里要验的是并发正确性，不是吞吐。
	const writers = 3
	const perWriter = 2

	var wg sync.WaitGroup
	var mu sync.Mutex
	saved := make([]string, 0, writers*perWriter)

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				rec, err := store.Save(HistoryRecord{
					Type:      TypeScan,
					IPVersion: 4,
					Params:    json.RawMessage(fmt.Sprintf(`{"workers":%d}`, 100+w*10+i)),
					Results:   recordsOf(2, 20),
				})
				if err != nil {
					t.Errorf("并发存档失败：%v", err)
					return
				}
				mu.Lock()
				saved = append(saved, rec.ID)
				mu.Unlock()
			}
		}(w)
	}

	// 读侧：不断列列表、读最新一份、按 ID 读取。
	for r := 0; r < 3; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				if _, err := store.List(Filter{}); err != nil {
					t.Errorf("并发列表失败：%v", err)
					return
				}
				// 读侧可能跑在第一次存档之前，「还没有记录」是合法结果。
				if _, err := store.LoadLatest(TypeScan, 4); err != nil && !errors.Is(err, ErrNotFound) {
					t.Errorf("并发加载最新一份失败：%v", err)
					return
				}
				mu.Lock()
				ids := append([]string(nil), saved...)
				mu.Unlock()
				for _, id := range ids {
					_, _ = store.Load(id)
				}
			}
		}()
	}

	// 写侧：打标签、软删除、撤销、清理。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 2; i++ {
			mu.Lock()
			ids := append([]string(nil), saved...)
			mu.Unlock()
			if len(ids) > 2 {
				ids = ids[:2]
			}
			for _, id := range ids {
				_ = store.UpdateMeta(id, []string{"并发"}, "备注", i%2 == 0)
				if err := store.Delete(id); err == nil {
					_ = store.Restore(id)
				}
			}
			_, _ = store.Cleanup()
		}
	}()

	wg.Wait()

	// 并发跑完之后，磁盘上必须仍然是一份自洽的历史：索引能解析，列表里的
	// 每一条都能真正读出来。任何一次非原子写入都会在这里露出来。
	reopened := h.reopen()
	entries, err := reopened.List(Filter{})
	if err != nil {
		t.Fatalf("列表失败：%v", err)
	}
	for _, e := range entries {
		if _, err := reopened.Load(e.ID); err != nil {
			t.Fatalf("索引里的 %s 读不出来：%v", e.ID, err)
		}
	}
}

// 写入过程中断电（进程被杀）不能留下半截 JSON。
//
// 无法真的拔电，改用等效的做法：直接检查磁盘上不存在「只有一半」的文件——
// 每次写入要么落在临时文件里（名字以点开头，不会被当成记录），要么已经完整
// 落到目标文件上。
func TestNoHalfWrittenJSONOnDisk(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 3; i++ {
		h.saveScan(4, scanParams(150+i), recordsOf(2, 20))
		h.clock.Advance(time.Second)
	}

	// 每个 .json 都必须能完整解析。
	var checked int
	for _, c := range Categories() {
		entries, err := os.ReadDir(filepath.Join(h.dir, c.DirName()))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
				continue
			}
			data, err := os.ReadFile(filepath.Join(h.dir, c.DirName(), e.Name()))
			if err != nil {
				t.Fatalf("读取 %s 失败：%v", e.Name(), err)
			}
			var v any
			if err := json.Unmarshal(data, &v); err != nil {
				t.Fatalf("%s 不是完整 JSON：%v", e.Name(), err)
			}
			checked++
		}
	}
	// 3 份记录 + 1 份 latest（每类只有一份，会被反复覆盖）。
	if checked != 4 {
		t.Fatalf("检查了 %d 个 JSON，期望 4", checked)
	}

	// 索引文件也必须是完整的。
	data, err := os.ReadFile(filepath.Join(h.dir, indexName))
	if err != nil {
		t.Fatalf("读取索引失败：%v", err)
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		t.Fatalf("索引不是完整 JSON：%v", err)
	}
	if len(idx.Entries) != 3 {
		t.Fatalf("索引条目数 = %d，期望 3", len(idx.Entries))
	}
}

// 目录里残留的临时文件（上次写到一半就崩了）不能被当成记录，也不该让
// 一致性校验每次都判定「不一致」而反复重建。
func TestLeftoverTempFilesDoNotTriggerRebuildLoop(t *testing.T) {
	h := newHarness(t)
	h.seedRecordsOnDisk(3)
	store := h.reopen()

	dir := filepath.Join(h.dir, Category{4, TypeScan}.DirName())
	if err := os.WriteFile(filepath.Join(dir, ".20260927_143012_abcd.json.tmp9"), []byte("{"), 0o600); err != nil {
		t.Fatalf("准备临时文件失败：%v", err)
	}

	// 再开一次：索引与记录文件仍然一致，不该触发重建。
	rebuilt, err := store.ensureConsistent()
	if err != nil {
		t.Fatalf("一致性校验失败：%v", err)
	}
	if rebuilt {
		t.Fatal("残留的临时文件让校验误判为不一致")
	}
}

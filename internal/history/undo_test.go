package history

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 记录文件已经被别的手段删掉时，删除操作仍要把索引修好。
//
// 索引与磁盘不同步是常态（用户手动清过目录、外挂盘掉线过），这时候报错退出
// 会让用户既删不掉这一条，也看不到它——列表里永远挂着一个点不开的条目。
func TestDeleteWhenFileAlreadyGoneStillFixesIndex(t *testing.T) {
	h := newHarness(t)
	rec := h.saveScan(4, scanParams(150), recordsOf(2, 20))

	if err := os.Remove(h.recordPath(4, TypeScan, rec.ID)); err != nil {
		t.Fatalf("准备场景失败：%v", err)
	}

	if err := h.store.Delete(rec.ID); err != nil {
		t.Fatalf("删除失败：%v", err)
	}
	entries, _ := h.store.List(Filter{})
	if len(entries) != 0 {
		t.Fatalf("列表仍有 %d 条，期望索引已修好", len(entries))
	}
	if _, err := h.store.Load(rec.ID); err == nil {
		t.Fatal("已删除的记录仍能读到")
	}

	// 没有中转文件时，窗口到点应当是空操作而不是报错。
	h.store.purgeOne(rec.ID)
	h.store.purgeOne("20260927_143012_abcd0001")
}

// 中转目录里的文件要清掉，子目录保留：只处理本层文件，不递归删除。
func TestPurgeTrashRemovesFilesButKeepsDirectories(t *testing.T) {
	h := newHarness(t)
	trash := filepath.Join(h.dir, trashDir)
	if err := os.MkdirAll(filepath.Join(trash, "sub"), 0o755); err != nil {
		t.Fatalf("准备子目录失败：%v", err)
	}
	stale := filepath.Join(trash, "20260927_143012_abcd0001.json")
	if err := os.WriteFile(stale, []byte("{}"), 0o600); err != nil {
		t.Fatalf("准备中转文件失败：%v", err)
	}

	if err := h.store.purgeTrash(); err != nil {
		t.Fatalf("清理中转目录失败：%v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("中转文件未被删除：%v", err)
	}
	if _, err := os.Stat(filepath.Join(trash, "sub")); err != nil {
		t.Fatalf("子目录被误删：%v", err)
	}
}

// 还没删过任何东西时中转目录并不存在，清理应当是空操作。
func TestPurgeTrashMissingDirIsNoop(t *testing.T) {
	h := newHarness(t)
	if _, err := os.Stat(filepath.Join(h.dir, trashDir)); !os.IsNotExist(err) {
		t.Skip("中转目录已存在，本用例的场景不成立")
	}
	if err := h.store.purgeTrash(); err != nil {
		t.Fatalf("中转目录不存在时清理报错：%v", err)
	}
}

// 上次退出时遗留的中转文件在启动时清掉：撤销窗口早就过去了。
func TestStartupClearsLeftoverTrash(t *testing.T) {
	h := newHarness(t)
	trash := filepath.Join(h.dir, trashDir)
	if err := os.MkdirAll(trash, 0o755); err != nil {
		t.Fatalf("准备中转目录失败：%v", err)
	}
	stale := filepath.Join(trash, "20260927_143012_abcd0001.json")
	if err := os.WriteFile(stale, []byte("{}"), 0o600); err != nil {
		t.Fatalf("准备中转文件失败：%v", err)
	}

	h.reopen()

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("重启后中转文件仍在：%v", err)
	}
}

// 关闭时把还没到点的删除直接落定：进程都要走了，撤销窗口没有意义。
func TestClosePurgesPendingDeletes(t *testing.T) {
	h := newHarness(t)
	rec := h.saveScan(4, scanParams(150), recordsOf(2, 20))
	if err := h.store.Delete(rec.ID); err != nil {
		t.Fatalf("删除失败：%v", err)
	}

	trash := filepath.Join(h.dir, trashDir, rec.ID+".json")
	if _, err := os.Stat(trash); err != nil {
		t.Fatalf("删除后中转文件不在：%v", err)
	}

	if err := h.store.Close(); err != nil {
		t.Fatalf("关闭失败：%v", err)
	}
	if _, err := os.Stat(trash); !os.IsNotExist(err) {
		t.Fatalf("关闭后中转文件仍在：%v", err)
	}

	// 再关一次不能出错（真实调用里可能会走到两次）。
	if err := h.store.Close(); err != nil {
		t.Fatalf("重复关闭报错：%v", err)
	}
}

// 撤销窗口内撤销，事件序列是 save → delete → restore，顺序不能乱。
func TestRestoreKeepsEventOrder(t *testing.T) {
	h := newHarness(t)
	rec := h.saveScan(4, scanParams(150), recordsOf(1, 20))

	if err := h.store.Delete(rec.ID); err != nil {
		t.Fatalf("删除失败：%v", err)
	}
	if err := h.store.Restore(rec.ID); err != nil {
		t.Fatalf("撤销失败：%v", err)
	}

	got := h.actions()
	want := []string{ActionSave, ActionDelete, ActionRestore}
	if len(got) != len(want) {
		t.Fatalf("事件 = %v，期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("事件 = %v，期望 %v", got, want)
		}
	}
}

// 撤销窗口可配，窗口值要能原样读出来给前端。
func TestUndoWindowIsConfigurable(t *testing.T) {
	dir := newTestDir(t)
	store, err := New(Options{
		Dir:        dir,
		UndoWindow: 42 * time.Second,
	})
	if err != nil {
		t.Fatalf("构造 Store 失败：%v", err)
	}
	defer store.Close()

	if got := store.UndoWindow(); got != 42*time.Second {
		t.Fatalf("撤销窗口 = %v，期望 42s", got)
	}

	// 不指定时用默认值。
	def, err := New(Options{Dir: newTestDir(t)})
	if err != nil {
		t.Fatalf("构造 Store 失败：%v", err)
	}
	defer def.Close()
	if got := def.UndoWindow(); got != defaultUndoWindow {
		t.Fatalf("默认撤销窗口 = %v，期望 %v", got, defaultUndoWindow)
	}
}

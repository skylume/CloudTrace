package history

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"cloudtrace/internal/atomicfile"
)

// pendingDelete 是一次「已删但还能撤销」的删除。
type pendingDelete struct {
	entry HistoryIndexEntry
	trash string
	orig  string
	timer *time.Timer
}

// Delete 软删除一份记录，在撤销窗口内可以恢复。
//
// 删除是不可逆操作里最容易误触的一个，所以它默认是「先挪走、过一会儿才真删」：
// 用户在提示条消失前点撤销，文件原样搬回来。
func (s *Store) Delete(id string) error {
	if !validID(id) {
		return fmt.Errorf("%w：%s", ErrNotFound, id)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.index.Find(id)
	if !ok {
		return fmt.Errorf("%w：%s", ErrNotFound, id)
	}
	c := Category{IPVersion: e.IPVersion, Type: e.Type}

	orig, err := s.recordPath(c, id)
	if err != nil {
		return err
	}
	trash, err := safeJoin(s.dir, trashDir, id+".json")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(trash), 0o755); err != nil {
		return err
	}

	if err := atomicfile.Rename(orig, trash); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		// 文件本来就不在了：索引与磁盘已经不同步，那就只修索引。
		trash = ""
	}

	s.index.Remove(id)
	if err := s.saveIndex(s.index); err != nil {
		return err
	}

	s.schedulePurge(id, e, trash)
	s.notify(id, ActionDelete)
	return nil
}

// Restore 撤销一次删除。
func (s *Store) Restore(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.pending[id]
	if !ok {
		return fmt.Errorf("%w：%s 不在可撤销的删除里", ErrNotFound, id)
	}
	delete(s.pending, id)
	if p.timer != nil {
		p.timer.Stop()
	}

	if p.trash != "" {
		c := Category{IPVersion: p.entry.IPVersion, Type: p.entry.Type}
		orig, err := s.recordPath(c, id)
		if err != nil {
			return err
		}
		if err := atomicfile.Rename(p.trash, orig); err != nil {
			return err
		}
	}

	s.index.Upsert(p.entry)
	if err := s.saveIndex(s.index); err != nil {
		return err
	}
	s.notify(id, ActionRestore)
	return nil
}

// Close 结束所有待撤销的删除，把它们真正落定。
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, p := range s.pending {
		if p.timer != nil {
			p.timer.Stop()
		}
		delete(s.pending, id)
	}
	return s.purgeTrashLocked()
}

// schedulePurge 安排撤销窗口结束后的真正删除。
func (s *Store) schedulePurge(id string, e HistoryIndexEntry, trash string) {
	p := &pendingDelete{entry: e, trash: trash}
	p.timer = time.AfterFunc(s.undoWindow, func() { s.purgeOne(id) })
	s.pending[id] = p
}

// purgeOne 真正删除一份待撤销的记录。
func (s *Store) purgeOne(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.pending[id]
	if !ok {
		return
	}
	delete(s.pending, id)
	if p.trash == "" {
		return
	}
	if err := os.Remove(p.trash); err != nil && !errors.Is(err, fs.ErrNotExist) {
		s.logf("删除历史记录失败", "id", id, "err", err)
		return
	}
	s.logf("历史记录已删除", "id", id, "type", p.entry.Type)
}

// purgeTrash 清空中转目录（启动时调用）。
func (s *Store) purgeTrash() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.purgeTrashLocked()
}

// purgeTrashLocked 清空中转目录。
//
// 进程已经退出过一轮，说明撤销窗口早就过去了：这些文件按「删除已完成」
// 处理。留着它们只会占磁盘又不出现在列表里。
func (s *Store) purgeTrashLocked() error {
	dir := filepath.Join(s.dir, trashDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path, err := safeJoin(dir, e.Name())
		if err != nil {
			s.logf("中转文件路径异常，已跳过", "name", e.Name())
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			s.logf("删除中转文件失败", "path", path, "err", err)
		}
	}
	return nil
}

package history

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"cloudtrace/internal/atomicfile"
)

// Clear 清空全部历史，返回清掉的条数。
//
// 与单条删除不同，这里**不走撤销窗口**：撤销是给「点错了一个」准备的，而清空
// 是用户明确要求把整个列表抹掉——再留一个窗口只会让「到底清没清干净」变得不
// 确定。调用方负责二次确认。
//
// 先把文件挪进中转目录、再重置索引：中途失败时不会留下「索引说没有、文件还在」
// 这种半截状态。中转目录随后立刻清掉——它在下次启动时本来也会被清，留着只是
// 占磁盘。
func (s *Store) Clear() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	trash := filepath.Join(s.dir, trashDir)
	if err := os.MkdirAll(trash, 0o755); err != nil {
		return 0, err
	}

	removed := 0
	for _, entry := range s.index.Entries {
		c := Category{IPVersion: entry.IPVersion, Type: entry.Type}
		orig, err := s.recordPath(c, entry.ID)
		if err != nil {
			s.logf("清空历史时解析记录路径失败", "id", entry.ID, "err", err)
			continue
		}
		dst, err := safeJoin(s.dir, trashDir, entry.ID+".json")
		if err != nil {
			s.logf("清空历史时解析中转路径失败", "id", entry.ID, "err", err)
			continue
		}
		if err := atomicfile.Rename(orig, dst); err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				s.logf("清空历史时移动记录失败", "id", entry.ID, "err", err)
				continue
			}
			// 文件本来就不在了：索引与磁盘已经不同步，照样把它从索引里去掉。
		}
		removed++
	}

	s.index = NewIndex()
	if err := s.saveIndex(s.index); err != nil {
		return removed, err
	}

	// `latest.json` 是「最新一份」的副本，内容与刚清掉的那些记录完全一样。
	// 留着它等于没清干净：`/latest` 还会把旧数据交出去，结果页刷新一次就又把
	// 它拉了回来。它只在写入时产生，因此这里得显式删。
	for _, c := range Categories() {
		if err := os.Remove(s.latestPath(c)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			s.logf("清空历史时删除最新副本失败", "category", c.DirName(), "err", err)
		}
	}

	if err := s.purgeTrashLocked(); err != nil {
		s.logf("清空历史后清理中转目录失败", "err", err)
	}
	s.notify("", ActionClear)
	return removed, nil
}

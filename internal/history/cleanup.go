package history

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Cleanup 按配置的保留策略清理历史，返回删除的份数。
//
// 策略每次现取配置：用户在设置里把保留份数从 20 改成 3，下一次存档就该
// 只剩 3 份，不需要重启进程。
func (s *Store) Cleanup() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	n, err := s.cleanupLocked()
	if n > 0 {
		s.notify("", ActionCleanup)
	}
	return n, err
}

// cleanupLocked 是清理的实现，调用方需已持有写锁。
func (s *Store) cleanupLocked() (int, error) {
	cfg := s.cfg()
	var doomed []HistoryIndexEntry

	switch cfg.KeepMode {
	case "days":
		cutoff := s.now().AddDate(0, 0, -cfg.KeepDays)
		for _, e := range s.index.Entries {
			// 收藏是用户的显式意图，自动清理永远不能覆盖它。
			if e.Starred || !e.CreatedAt.Before(cutoff) {
				continue
			}
			doomed = append(doomed, e)
		}
	default:
		for _, c := range Categories() {
			doomed = append(doomed, overCount(s.index, c, cfg.KeepCount)...)
		}
	}

	if len(doomed) == 0 {
		return 0, nil
	}

	removed := 0
	for _, e := range doomed {
		if err := s.removeFile(e); err != nil {
			// 单份删不掉（被占用、权限不足）不该中断整轮清理，更不该让
			// 索引与磁盘出现新的不一致。
			s.logf("历史记录删除失败，已跳过", "id", e.ID, "err", err)
			continue
		}
		s.index.Remove(e.ID)
		removed++
	}
	if removed == 0 {
		return 0, nil
	}

	if err := s.saveIndex(s.index); err != nil {
		return removed, err
	}
	s.logf("历史清理完成",
		"removed", removed, "mode", cfg.KeepMode, "keep_count", cfg.KeepCount, "keep_days", cfg.KeepDays)
	return removed, nil
}

// overCount 返回某分类里超出保留份数的条目。
//
// 收藏不占保留名额：用户收藏 25 份而保留份数是 20 时，不该有任何一份被删。
func overCount(idx Index, c Category, keep int) []HistoryIndexEntry {
	if keep < 1 {
		keep = 1
	}
	var kept int
	var doomed []HistoryIndexEntry
	for _, e := range idx.Entries {
		if e.Type != c.Type || e.IPVersion != c.IPVersion {
			continue
		}
		if e.Starred {
			continue
		}
		kept++
		if kept > keep {
			doomed = append(doomed, e)
		}
	}
	return doomed
}

// removeFile 删除一条记录的文件，并确认路径确实落在历史目录内。
func (s *Store) removeFile(e HistoryIndexEntry) error {
	c := Category{IPVersion: e.IPVersion, Type: e.Type}
	path, err := s.recordPath(c, e.ID)
	if err != nil {
		return err
	}
	if err := assertInside(s.dir, path); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// assertInside 确认 path 落在 root 之内。
//
// 这是清理的最后一道闸：记录 ID 参与拼路径，一旦它被构造成 `../../x`，
// 没有这道检查就会删到历史目录之外的文件。
func assertInside(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return fmt.Errorf("路径 %s 无法相对化：%w", path, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("拒绝操作历史目录之外的路径：%s", path)
	}
	return nil
}

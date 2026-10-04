package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"cloudtrace/internal/atomicfile"
)

// Save 写入一份历史记录，返回补全了 ID 与创建时间的记录。
func (s *Store) Save(rec HistoryRecord) (HistoryRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// ID 在锁内生成：生成前要对着索引查重，生成后要立刻落盘，中间不能有
	// 别的存档插进来。
	if rec.ID == "" {
		id, err := s.newUniqueID(s.now())
		if err != nil {
			return HistoryRecord{}, err
		}
		rec.ID = id
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = s.now()
	}
	if rec.Count == 0 {
		rec.Count = len(rec.Results)
	}
	if err := rec.Validate(); err != nil {
		return HistoryRecord{}, err
	}

	if err := s.dedupLocked(rec); err != nil {
		s.logf("去重检查失败，按新记录写入", "err", err)
	}

	if err := s.writeRecord(rec); err != nil {
		return HistoryRecord{}, err
	}
	if err := s.writeLatest(rec); err != nil {
		return HistoryRecord{}, err
	}

	s.index.Upsert(rec.Entry())
	if err := s.saveIndex(s.index); err != nil {
		return HistoryRecord{}, err
	}

	if _, err := s.cleanupLocked(); err != nil {
		// 清理失败不影响这份记录已经存好这件事。
		s.logf("清理历史失败", "err", err)
	}

	s.notify(rec.ID, ActionSave)
	return rec, nil
}

// newUniqueID 生成一个不与现有记录冲突的 ID。
//
// 随机后缀本身已经把撞名概率压得很低，这里再对着索引查一次是为了把「概率
// 很小」变成「不会发生」：ID 同时是文件名，撞名的后果是静默覆盖一份已有
// 记录，而用户不会收到任何提示。
func (s *Store) newUniqueID(now time.Time) (string, error) {
	for i := 0; i < idRetries; i++ {
		id, err := s.newID(now)
		if err != nil {
			return "", err
		}
		if !validID(id) {
			return "", fmt.Errorf("生成的记录 ID %q 形状不合法", id)
		}
		if _, exists := s.index.Find(id); !exists {
			return id, nil
		}
	}
	return "", errors.New("记录 ID 反复冲突，已放弃存档")
}

// idRetries 是 ID 撞名后的重试次数。
const idRetries = 8

// dedupLocked 处理「同参数短时间内的重复存档」。
//
// 做法是**删掉旧的那份、留下新的**，而不是跳过新的。反过来会让用户刚跑出来
// 的结果反而没进历史，列表里躺着一份更旧的。
func (s *Store) dedupLocked(rec HistoryRecord) error {
	if !s.cfg().AutoDedup {
		return nil
	}
	hash := ParamsHash(rec.Params)
	if hash == "" {
		return nil
	}

	// 只清理**比新记录更早**的那份：去重的意思是「同一组参数连着跑了两遍，
	// 留新的那份」。差值为负表示已有记录反而更新，那说明这份是补录进来的
	// （导入旧数据、或补一份历史），删掉它等于把用户更想要的那份弄丢。
	var stale []HistoryIndexEntry
	for _, e := range s.index.Entries {
		if e.ParamsHash != hash || e.Starred || e.Type != rec.Type || e.IPVersion != rec.IPVersion {
			continue
		}
		gap := rec.CreatedAt.Sub(e.CreatedAt)
		if gap <= 0 || gap > dedupWindow {
			continue
		}
		stale = append(stale, e)
	}
	for _, e := range stale {
		if err := s.removeEntryLocked(e); err != nil {
			return err
		}
		s.logf("同参数的重复存档已合并", "removed", e.ID, "kept", rec.ID)
	}
	return nil
}

// writeRecord 原子写一份完整记录。
func (s *Store) writeRecord(rec HistoryRecord) error {
	path, err := s.recordPath(Category{IPVersion: rec.IPVersion, Type: rec.Type}, rec.ID)
	if err != nil {
		return err
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return atomicWrite(path, data)
}

// writeLatest 原子更新该分类的最新副本。
//
// 副本是「一键复用最近一次结果」的全部依据：有它，那条路径就不需要读索引、
// 不需要扫目录，一次文件读取结束。
func (s *Store) writeLatest(rec HistoryRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return atomicWrite(s.latestPath(Category{IPVersion: rec.IPVersion, Type: rec.Type}), data)
}

// removeEntryLocked 删掉一条记录的文件与索引项。
func (s *Store) removeEntryLocked(e HistoryIndexEntry) error {
	c := Category{IPVersion: e.IPVersion, Type: e.Type}
	path, err := s.recordPath(c, e.ID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	s.index.Remove(e.ID)
	return s.saveIndex(s.index)
}

// newestOf 返回某分类里最新的一份索引条目。
func (s *Store) newestOf(c Category) (HistoryIndexEntry, bool) {
	for _, e := range s.index.Entries {
		if e.Type == c.Type && e.IPVersion == c.IPVersion {
			return e, true
		}
	}
	return HistoryIndexEntry{}, false
}

// atomicWrite 是历史目录下所有写入的唯一出口。
func atomicWrite(path string, data []byte) error {
	return atomicfile.Write(path, data, 0o600)
}

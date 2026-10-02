package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// indexName 是索引文件名，固定放在历史目录根部。
const indexName = "index.json"

// trashDir 是软删除的中转目录，位于历史目录内部。
//
// 放在历史目录**内部**而不是系统临时目录，是为了让「删除只允许发生在
// 历史目录下」这条约束对撤销也成立——撤销同样只搬动自己目录里的文件。
const trashDir = ".trash"

// Category 是一类归档目录：IP 版本 × 记录类型。
type Category struct {
	IPVersion int
	Type      string
}

// Categories 是全部四类归档目录。
func Categories() []Category {
	return []Category{
		{IPVersion: 4, Type: TypeScan},
		{IPVersion: 4, Type: TypeSpeed},
		{IPVersion: 6, Type: TypeScan},
		{IPVersion: 6, Type: TypeSpeed},
	}
}

// DirName 返回该分类相对历史目录的路径，例如 ipv4/scan。
func (c Category) DirName() string {
	return filepath.Join(ipDirName(c.IPVersion), c.Type)
}

func ipDirName(version int) string {
	if version == 6 {
		return "ipv6"
	}
	return "ipv4"
}

// indexPath 返回索引文件路径。
func (s *Store) indexPath() string { return filepath.Join(s.dir, indexName) }

// categoryDir 返回某分类的绝对路径。
func (s *Store) categoryDir(c Category) string { return filepath.Join(s.dir, c.DirName()) }

// recordPath 返回一条记录的文件路径。
func (s *Store) recordPath(c Category, id string) (string, error) {
	return safeJoin(s.categoryDir(c), id+".json")
}

// latestPath 返回某分类的「最新一份」副本路径。
func (s *Store) latestPath(c Category) string { return filepath.Join(s.categoryDir(c), latestName) }

// safeJoin 把片段拼到 root 之下，并确认结果没有越出 root。
//
// 所有由外部输入（记录 ID）参与拼接的路径都必须过这里：ID 里带上 `..`
// 就能让「删除某份历史」变成「删除历史目录之外的任意文件」。
func safeJoin(root string, parts ...string) (string, error) {
	p := filepath.Join(append([]string{root}, parts...)...)
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return "", fmt.Errorf("路径 %s 无法相对化：%w", p, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("路径 %s 越出 %s", p, root)
	}
	return p, nil
}

// idTimeLen 是 ID 里时间戳部分的长度，形如 20060102_150405。
const idTimeLen = len("20060102_150405")

// validID 校验记录 ID 的形状，形如 20260927_143012_ab12cd34。
//
// ID 由本包生成，形状固定；凡是进到这里却不符合的，要么是调用方传错了，
// 要么是有人在构造路径。两种都不该继续往下走。
func validID(id string) bool {
	if len(id) != idTimeLen+1+idRandBytes*2 {
		return false
	}
	for i := 0; i < idTimeLen; i++ {
		ch := id[i]
		if i == 8 { // 日期与时间之间的分隔符
			if ch != '_' {
				return false
			}
			continue
		}
		if ch < '0' || ch > '9' {
			return false
		}
	}
	if id[idTimeLen] != '_' {
		return false
	}
	for i := idTimeLen + 1; i < len(id); i++ {
		if !isHexLower(id[i]) {
			return false
		}
	}
	return true
}

func isHexLower(ch byte) bool {
	return (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')
}

// loadIndex 读取索引。
//
// 文件不存在 → 空索引（首次运行）；内容损坏 → 重建（索引可以完全由磁盘上
// 的记录文件推导出来，没有理由因为一个坏文件就让用户看不到历史）。
func (s *Store) loadIndex() (Index, error) {
	data, err := s.open(s.indexPath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return NewIndex(), nil
		}
		return NewIndex(), err
	}

	idx := NewIndex()
	if err := json.Unmarshal(data, &idx); err != nil {
		s.logf("索引无法解析，将从记录文件重建", "err", err)
		rebuilt, rerr := s.rebuildIndex()
		if rerr != nil {
			return NewIndex(), rerr
		}
		// 立刻把重建结果写回去。只返回给调用方是不够的：调用方多半只是
		// 拿它填充内存，磁盘上那份坏索引会一直躺到下次有人动它为止。
		if serr := s.saveIndex(rebuilt); serr != nil {
			s.logf("重建后的索引写回失败", "err", serr)
		}
		return rebuilt, nil
	}
	if idx.Version == 0 {
		idx.Version = IndexVersion
	}
	idx.Sort()
	return idx, nil
}

// saveIndex 原子写索引。
func (s *Store) saveIndex(idx Index) error {
	idx.Version = IndexVersion
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(s.indexPath(), append(data, '\n'))
}

// ensureConsistent 校验索引与磁盘是否一致，不一致就重建。
//
// 校验本身只比对文件名，不读记录内容——它要在每次启动时跑，不能顺手把
// 所有记录文件都解析一遍。只有确实不一致时才走重建。
func (s *Store) ensureConsistent() (bool, error) {
	idx, err := s.loadIndex()
	if err != nil {
		return false, err
	}

	onDisk, err := s.listRecordIDs()
	if err != nil {
		return false, err
	}

	indexed := make(map[string]bool, len(idx.Entries))
	for _, e := range idx.Entries {
		indexed[e.ID] = true
	}
	if len(indexed) == len(onDisk) {
		consistent := true
		for id := range onDisk {
			if !indexed[id] {
				consistent = false
				break
			}
		}
		if consistent {
			return false, nil
		}
	}

	rebuilt, err := s.rebuildIndex()
	if err != nil {
		return false, err
	}
	if err := s.saveIndex(rebuilt); err != nil {
		return false, err
	}
	// 重建结果必须装回内存：否则磁盘上的索引是好的，进程里用的还是那份
	// 空的，表现为「重启后历史列表是空的，再重启一次才有」。
	s.index = rebuilt
	s.logf("索引与记录文件不一致，已重建", "entries", len(rebuilt.Entries))
	return true, nil
}

// listRecordIDs 扫描四类归档目录，返回磁盘上实际存在的记录 ID 集合。
func (s *Store) listRecordIDs() (map[string]bool, error) {
	out := make(map[string]bool)
	for _, c := range Categories() {
		entries, err := os.ReadDir(s.categoryDir(c))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".json") {
				continue
			}
			// 最新副本与写入中途的临时文件都不是记录：前者是副本，后者
			// 可能只写了一半。名字以点开头的都是临时文件。
			if name == latestName || strings.HasPrefix(name, ".") {
				continue
			}
			out[strings.TrimSuffix(name, ".json")] = true
		}
	}
	return out, nil
}

// rebuildIndex 从磁盘上的记录文件重建索引。
//
// 这里会真正读一遍记录内容，因此是慢路径；换来的是「索引丢了也能恢复」——
// 索引是纯派生数据，任何情况下都不该成为用户数据的单点。
func (s *Store) rebuildIndex() (Index, error) {
	idx := NewIndex()
	for _, c := range Categories() {
		entries, err := os.ReadDir(s.categoryDir(c))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return NewIndex(), err
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".json") ||
				name == latestName || strings.HasPrefix(name, ".") {
				continue
			}
			path := filepath.Join(s.categoryDir(c), name)
			rec, err := s.readRecord(path)
			if err != nil {
				// 单份记录坏了不该拖垮整个索引：跳过它并点名，其余照常恢复。
				s.logf("记录无法解析，已跳过", "path", path, "err", err)
				continue
			}
			idx.Entries = append(idx.Entries, rec.Entry())
		}
	}
	idx.Sort()
	return idx, nil
}

// readRecord 读取并解析一份完整记录。
func (s *Store) readRecord(path string) (HistoryRecord, error) {
	data, err := s.open(path)
	if err != nil {
		return HistoryRecord{}, err
	}
	var rec HistoryRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return HistoryRecord{}, err
	}
	if err := rec.Validate(); err != nil {
		return HistoryRecord{}, err
	}
	return rec, nil
}

// findCategory 在索引里定位一条记录所属的分类。
func (s *Store) findCategory(idx Index, id string) (Category, error) {
	e, ok := idx.Find(id)
	if !ok {
		return Category{}, fmt.Errorf("%w：%s", ErrNotFound, id)
	}
	return Category{IPVersion: e.IPVersion, Type: e.Type}, nil
}

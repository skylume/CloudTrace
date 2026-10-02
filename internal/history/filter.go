package history

import (
	"sort"
	"strings"
	"time"
)

// Filter 是历史列表的筛选条件。
//
// 所有字段都作用在索引条目上：列表页的任何一次筛选都不允许打开记录文件。
type Filter struct {
	// Type 限定记录类型（scan / speed），空表示不限。
	Type string
	// IPVersion 限定 4 或 6，0 表示不限。
	IPVersion int
	// Region 限定结果里出现过某个数据中心代码（大小写不敏感）。
	Region string
	// Tags 要求同时带有这些标签。
	Tags []string
	// Starred 限定收藏状态，nil 表示不限。
	Starred *bool
	// MinCount / MaxCount 限定结果条数区间，0 表示该侧不限。
	MinCount int
	MaxCount int
	// Since / Until 限定创建时间区间，零值表示该侧不限。
	Since time.Time
	Until time.Time
	// Search 是备注、标签与 ID 的模糊匹配（大小写不敏感）。
	Search string
	// Limit 是返回条数上限，<= 0 表示不限。
	Limit int
	// Offset 是跳过的条数。
	Offset int
}

// Match 判断一条索引条目是否满足全部条件。
func (f Filter) Match(e HistoryIndexEntry) bool {
	if f.Type != "" && e.Type != f.Type {
		return false
	}
	if f.IPVersion != 0 && e.IPVersion != f.IPVersion {
		return false
	}
	if f.Region != "" && !containsFold(e.Regions, f.Region) {
		return false
	}
	if len(f.Tags) > 0 && !hasAllTags(e.Tags, f.Tags) {
		return false
	}
	if f.Starred != nil && e.Starred != *f.Starred {
		return false
	}
	if f.MinCount > 0 && e.Count < f.MinCount {
		return false
	}
	if f.MaxCount > 0 && e.Count > f.MaxCount {
		return false
	}
	if !f.Since.IsZero() && e.CreatedAt.Before(f.Since) {
		return false
	}
	if !f.Until.IsZero() && e.CreatedAt.After(f.Until) {
		return false
	}
	if f.Search != "" && !matchesSearch(e, f.Search) {
		return false
	}
	return true
}

// sortForList 按「收藏置顶，其次时间倒序」排列。
//
// 收藏置顶是用户自己标记出来的重点，让它跟着时间沉下去等于这个标记白做。
func sortForList(entries []HistoryIndexEntry) {
	sort.SliceStable(entries, func(a, b int) bool {
		if entries[a].Starred != entries[b].Starred {
			return entries[a].Starred
		}
		if entries[a].CreatedAt.Equal(entries[b].CreatedAt) {
			return entries[a].ID > entries[b].ID
		}
		return entries[a].CreatedAt.After(entries[b].CreatedAt)
	})
}

// paginate 按 limit / offset 切片。
func paginate(entries []HistoryIndexEntry, limit, offset int) []HistoryIndexEntry {
	if offset > 0 {
		if offset >= len(entries) {
			return []HistoryIndexEntry{}
		}
		entries = entries[offset:]
	}
	if limit > 0 && limit < len(entries) {
		entries = entries[:limit]
	}
	return entries
}

func containsFold(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}

func hasAllTags(have, want []string) bool {
	if len(want) == 0 {
		return true
	}
	for _, w := range want {
		if !containsFold(have, w) {
			return false
		}
	}
	return true
}

func matchesSearch(e HistoryIndexEntry, q string) bool {
	q = strings.ToLower(q)
	if strings.Contains(strings.ToLower(e.ID), q) || strings.Contains(strings.ToLower(e.Note), q) {
		return true
	}
	for _, t := range e.Tags {
		if strings.Contains(strings.ToLower(t), q) {
			return true
		}
	}
	return false
}

// normalizeTags 去空白、去空项、去重，并保持用户给定的顺序。
func normalizeTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	out := make([]string, 0, len(tags))
	seen := make(map[string]bool, len(tags))
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		key := strings.ToLower(t)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

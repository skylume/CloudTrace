// Package history 负责扫描与测速结果的存档、索引、复用与对比。
//
// 这是本项目的核心能力：扫描完的结果不会随着关闭页面消失，下次可以直接
// 拿它去测速，不必重扫。
//
// 两条硬性约束贯穿本包：
//
//  1. **列表页只读索引**。索引里放够了列表要显示的字段，任何「列个表」的
//     调用都不允许打开记录文件——一份记录带着几千条结果，几十份就是几十次
//     全量 JSON 解析，这是同类实现里最典型的性能塌方点。
//  2. **一切写入都是原子的**。断电、被杀进程、磁盘满都会发生在写文件中途，
//     半截 JSON 会让下次启动直接读不出这份记录。索引尤其如此：它坏了整
//     个历史列表就都没了。
package history

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"cloudtrace/internal/model"
)

// 记录类型。
const (
	TypeScan  = "scan"
	TypeSpeed = "speed"
)

// IndexVersion 是索引格式版本，供未来迁移判断。
const IndexVersion = 1

// latestName 是每类目录下「最新一份」的副本名。
//
// 有了它，「加载最近一次结果」不需要读索引、不需要扫目录，一次文件读取就够。
const latestName = "latest.json"

// HistoryRecord 是一份完整的历史记录，落盘为单个 JSON 文件。
type HistoryRecord struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	IPVersion int       `json:"ip_version"`
	CreatedAt time.Time `json:"created_at"`

	// Duration 是这次任务实际耗时（秒）。
	Duration float64 `json:"duration_s"`
	// Preset 是当时使用的档位名，回看时能知道这份结果是什么条件下跑出来的。
	Preset string `json:"preset,omitempty"`

	// Params 是参数快照，内容按 Type 决定：scan 对应 model.ScanParams，
	// speed 对应 model.SpeedParams。
	//
	// 这里刻意用 json.RawMessage 而不是某个具体类型：一份历史要么是扫描、
	// 要么是测速，用单一结构体装不下两种参数，用联合结构体又会让前端拿到
	// 一半字段是空的。取用请走 ScanParams / SpeedParams。
	Params json.RawMessage `json:"params"`
	// Origins 记录每个参数是默认值、档位带入还是用户显式设置。
	Origins model.ParamOrigins `json:"origins,omitempty"`

	Summary model.Summary    `json:"summary"`
	Count   int              `json:"count"`
	Tags    []string         `json:"tags,omitempty"`
	Note    string           `json:"note,omitempty"`
	Starred bool             `json:"starred"`
	Results []model.IPRecord `json:"results"`
}

// ScanParams 解码扫描参数快照。
func (r HistoryRecord) ScanParams() (model.ScanParams, error) {
	if r.Type != TypeScan {
		return model.ScanParams{}, fmt.Errorf("历史记录 %s 的类型是 %s，不是扫描", r.ID, r.Type)
	}
	return decodeParams[model.ScanParams](r)
}

// SpeedParams 解码测速参数快照。
func (r HistoryRecord) SpeedParams() (model.SpeedParams, error) {
	if r.Type != TypeSpeed {
		return model.SpeedParams{}, fmt.Errorf("历史记录 %s 的类型是 %s，不是测速", r.ID, r.Type)
	}
	return decodeParams[model.SpeedParams](r)
}

func decodeParams[T any](r HistoryRecord) (T, error) {
	var out T
	if len(r.Params) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(r.Params, &out); err != nil {
		return out, fmt.Errorf("历史记录 %s 的参数快照无法解析：%w", r.ID, err)
	}
	return out, nil
}

// Validate 检查一份记录是否具备落盘的最低要求。
func (r HistoryRecord) Validate() error {
	if r.ID == "" {
		return errors.New("历史记录缺少 ID")
	}
	if r.Type != TypeScan && r.Type != TypeSpeed {
		return fmt.Errorf("历史记录 %s 的类型 %q 无效", r.ID, r.Type)
	}
	if r.IPVersion != 4 && r.IPVersion != 6 {
		return fmt.Errorf("历史记录 %s 的 IP 版本 %d 无效", r.ID, r.IPVersion)
	}
	if r.CreatedAt.IsZero() {
		return fmt.Errorf("历史记录 %s 缺少创建时间", r.ID)
	}
	return nil
}

// Entry 从完整记录里抽出索引条目。
//
// 索引条目的字段集合是「列表页需要显示的全部内容」，多一个字段就意味着
// 列表页多一次打开记录文件的理由。
func (r HistoryRecord) Entry() HistoryIndexEntry {
	return HistoryIndexEntry{
		ID:         r.ID,
		Type:       r.Type,
		IPVersion:  r.IPVersion,
		CreatedAt:  r.CreatedAt,
		Duration:   r.Duration,
		Preset:     r.Preset,
		Count:      r.Count,
		Tags:       cloneStrings(r.Tags),
		Note:       r.Note,
		Starred:    r.Starred,
		BestSpeed:  r.Summary.BestSpeed,
		MinLatency: r.Summary.MinLatency,
		Regions:    distinctRegions(r.Results),
		ParamsHash: ParamsHash(r.Params),
	}
}

// HistoryIndexEntry 是索引里的一条，只放列表页要用的字段。
type HistoryIndexEntry struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	IPVersion  int       `json:"ip_version"`
	CreatedAt  time.Time `json:"created_at"`
	Duration   float64   `json:"duration_s,omitempty"`
	Preset     string    `json:"preset,omitempty"`
	Count      int       `json:"count"`
	Tags       []string  `json:"tags,omitempty"`
	Note       string    `json:"note,omitempty"`
	Starred    bool      `json:"starred"`
	BestSpeed  float64   `json:"best_speed,omitempty"`
	MinLatency float64   `json:"min_latency,omitempty"`

	// Regions 是本次结果里出现过的数据中心代码（去重、已排序）。
	//
	// 地区筛选必须能在索引上完成，否则「按地区找历史」就退化成了逐份打开
	// 记录文件——正是索引要避免的事。
	Regions []string `json:"regions,omitempty"`

	// ParamsHash 是参数快照的摘要，用于判断「同参数的重复存档」。
	// 存摘要而不是参数本身，是为了让去重判断也不必打开记录文件。
	ParamsHash string `json:"params_hash,omitempty"`
}

// Index 是历史索引，落盘为 index.json。
type Index struct {
	Version int                 `json:"version"`
	Entries []HistoryIndexEntry `json:"entries"`
}

// NewIndex 返回一个空索引。
func NewIndex() Index { return Index{Version: IndexVersion} }

// Sort 按创建时间倒序排列，同一时间时按 ID 兜底，保证顺序稳定。
func (i *Index) Sort() {
	sort.SliceStable(i.Entries, func(a, b int) bool {
		if i.Entries[a].CreatedAt.Equal(i.Entries[b].CreatedAt) {
			return i.Entries[a].ID > i.Entries[b].ID
		}
		return i.Entries[a].CreatedAt.After(i.Entries[b].CreatedAt)
	})
}

// Find 按 ID 查索引条目。
func (i Index) Find(id string) (HistoryIndexEntry, bool) {
	for _, e := range i.Entries {
		if e.ID == id {
			return e, true
		}
	}
	return HistoryIndexEntry{}, false
}

// Upsert 插入或替换一条索引条目，并保持倒序。
func (i *Index) Upsert(e HistoryIndexEntry) {
	for idx := range i.Entries {
		if i.Entries[idx].ID == e.ID {
			i.Entries[idx] = e
			i.Sort()
			return
		}
	}
	i.Entries = append(i.Entries, e)
	i.Sort()
}

// Remove 删除一条索引条目，返回是否真的删掉了。
func (i *Index) Remove(id string) bool {
	for idx := range i.Entries {
		if i.Entries[idx].ID == id {
			i.Entries = append(i.Entries[:idx], i.Entries[idx+1:]...)
			return true
		}
	}
	return false
}

// ParamsHash 计算参数快照的摘要。
//
// 空快照返回空串：把「没有参数」和「参数恰好哈希成某值」区分开，调用方
// 见到空串就知道这份记录不参与去重判断。
func ParamsHash(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

// NewID 生成记录 ID：时间戳 + 随机后缀。
//
// 时间戳前缀让同目录下的文件按名字排序即按时间排序，人工翻目录时一眼能看懂；
// 随机后缀避免同一秒内的两次任务撞名。后缀取 4 字节（8 位十六进制）而不是
// 2 字节：ID 同时是文件名，撞名的后果是**静默覆盖**一份已有记录，这种事
// 不能用「概率很小」来搪塞。
func NewID(now time.Time) (string, error) {
	var buf [idRandBytes]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("生成历史记录 ID 失败：%w", err)
	}
	return now.Format("20060102_150405") + "_" + hex.EncodeToString(buf[:]), nil
}

// idRandBytes 是 ID 随机后缀的字节数。
const idRandBytes = 4

// distinctRegions 取出结果里出现过的数据中心代码。
func distinctRegions(records []model.IPRecord) []string {
	seen := make(map[string]bool, len(records))
	out := make([]string, 0, 4)
	for _, r := range records {
		code := strings.ToUpper(strings.TrimSpace(r.Colo))
		if code == "" || seen[code] {
			continue
		}
		seen[code] = true
		out = append(out, code)
	}
	sort.Strings(out)
	return out
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return append([]string(nil), in...)
}

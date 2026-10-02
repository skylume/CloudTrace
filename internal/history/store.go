package history

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"sync"
	"time"

	"cloudtrace/internal/config"
)

// ErrNotFound 表示指定的历史记录不存在。
var ErrNotFound = errors.New("历史记录不存在")

// 事件动作，随 history/changed 一起下发，前端据此决定要不要刷新整表。
const (
	ActionSave    = "save"
	ActionDelete  = "delete"
	ActionRestore = "restore"
	ActionMeta    = "meta"
	ActionCleanup = "cleanup"
)

// TopicChanged 是历史发生变更时发布的事件 topic。
const TopicChanged = "history/changed"

// Change 是 history/changed 事件的载荷。
type Change struct {
	// ID 是发生变更的记录；整轮清理没有单一记录，此时为空。
	ID string `json:"id,omitempty"`
	// Action 取值见 Action* 常量。
	Action string `json:"action"`
}

// defaultUndoWindow 是删除后的可撤销窗口。
//
// 窗口内文件只是被移进历史目录下的中转目录，撤销就是把文件搬回来；
// 窗口一过才真正删除。
const defaultUndoWindow = 15 * time.Second

// dedupWindow 是「同参数重复存档」的判定窗口。
//
// 用户在几秒内连点几次扫描是很常见的，每次都存一份会让列表被同样的结果
// 刷满。窗口外的重复存档保留：那多半是用户真的想对比不同时间的表现。
const dedupWindow = 30 * time.Second

// Options 是 Store 的构造参数。
type Options struct {
	// Dir 是历史根目录，即 data/history。
	Dir string
	// Config 每次调用时现取历史配置。
	//
	// 取的是函数而不是值：用户在设置里把保留份数从 20 改成 3，下一次存档
	// 就该按 3 来清理，不需要重启。
	Config func() config.HistoryConfig
	// Now 取当前时间，为 nil 时用 time.Now。注入是为了让撤销窗口与清理
	// 策略可测。
	Now func() time.Time
	// Logger 为 nil 时用 slog.Default()。
	Logger *slog.Logger
	// UndoWindow 是删除后的可撤销窗口，<= 0 时用默认值。
	UndoWindow time.Duration
	// OnChanged 在历史发生变更时回调，用于对外广播。允许为 nil。
	OnChanged func(id, action string)
	// NewID 生成记录 ID，为 nil 时用 NewID。
	//
	// 唯一的用途是让测试能构造出「ID 撞名」这种低概率情形，验证存档不会
	// 静默覆盖一份已有记录。
	NewID func(now time.Time) (string, error)
	// Open 读取文件内容，为 nil 时用 os.ReadFile。
	//
	// 唯一的用途是让测试能断言「列个表没有打开过任何记录文件」——这是本包
	// 最要紧的一条性能约束，只有能被观测才守得住。
	Open func(path string) ([]byte, error)
}

// Store 是历史记录的存储核心，可安全并发使用。
type Store struct {
	dir        string
	cfg        func() config.HistoryConfig
	now        func() time.Time
	logger     *slog.Logger
	undoWindow time.Duration
	onChanged  func(id, action string)
	newID      func(now time.Time) (string, error)
	open       func(string) ([]byte, error)

	mu      sync.RWMutex
	index   Index
	pending map[string]*pendingDelete
}

// New 构造 Store，并做一次启动期的一致性校验。
//
// 校验结果不影响构造成功：索引是派生数据，坏了就重建，不能因为一个索引
// 文件让整个程序起不来。
func New(opts Options) (*Store, error) {
	if opts.Dir == "" {
		return nil, errors.New("历史目录不能为空")
	}
	s := &Store{
		dir:        opts.Dir,
		cfg:        opts.Config,
		now:        opts.Now,
		logger:     opts.Logger,
		undoWindow: opts.UndoWindow,
		onChanged:  opts.OnChanged,
		newID:      opts.NewID,
		open:       opts.Open,
		pending:    make(map[string]*pendingDelete),
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.logger == nil {
		s.logger = slog.Default()
	}
	if s.cfg == nil {
		s.cfg = func() config.HistoryConfig { return config.Default().History }
	}
	if s.undoWindow <= 0 {
		s.undoWindow = defaultUndoWindow
	}
	if s.newID == nil {
		s.newID = NewID
	}
	if s.open == nil {
		s.open = os.ReadFile
	}

	if err := s.init(); err != nil {
		return nil, err
	}
	return s, nil
}

// init 建立目录、读取索引、校验一致性并清掉上次遗留的中转文件。
func (s *Store) init() error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	for _, c := range Categories() {
		if err := os.MkdirAll(s.categoryDir(c), 0o755); err != nil {
			return err
		}
	}

	if err := s.purgeTrash(); err != nil {
		s.logf("清理中转目录失败", "err", err)
	}

	idx, err := s.loadIndex()
	if err != nil {
		return err
	}
	s.index = idx

	if _, err := s.ensureConsistent(); err != nil {
		// 校验失败不是致命问题：内存里已经有一份可用索引了。
		s.logf("索引一致性校验失败", "err", err)
	}
	return nil
}

// Load 读取一份完整记录。
func (s *Store) Load(id string) (HistoryRecord, error) {
	if !validID(id) {
		return HistoryRecord{}, fmt.Errorf("%w：%s", ErrNotFound, id)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	c, err := s.findCategory(s.index, id)
	if err != nil {
		return HistoryRecord{}, err
	}
	path, err := s.recordPath(c, id)
	if err != nil {
		return HistoryRecord{}, err
	}
	rec, err := s.readRecord(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return HistoryRecord{}, fmt.Errorf("%w：%s", ErrNotFound, id)
		}
		return HistoryRecord{}, err
	}
	return rec, nil
}

// LoadLatest 读取某类的最新一份记录。
//
// 这条路径刻意不碰索引、不扫目录：一键复用是最高频的操作，一次文件读取
// 就该拿到结果。
func (s *Store) LoadLatest(typ string, ipVersion int) (HistoryRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	c := Category{IPVersion: ipVersion, Type: typ}
	rec, err := s.readRecord(s.latestPath(c))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return HistoryRecord{}, fmt.Errorf("%w：%s 还没有任何记录", ErrNotFound, c.DirName())
		}
		return HistoryRecord{}, err
	}
	return rec, nil
}

// List 按筛选条件返回索引条目，绝不打开记录文件。
func (s *Store) List(f Filter) ([]HistoryIndexEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]HistoryIndexEntry, 0, len(s.index.Entries))
	for _, e := range s.index.Entries {
		if f.Match(e) {
			out = append(out, cloneEntry(e))
		}
	}
	sortForList(out)
	return paginate(out, f.Limit, f.Offset), nil
}

// UpdateMeta 修改标签、备注与收藏。
func (s *Store) UpdateMeta(id string, tags []string, note string, starred bool) error {
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
	path, err := s.recordPath(c, id)
	if err != nil {
		return err
	}

	// 记录文件与索引都要改。只改索引的话，下次重建索引这些标注就全丢了
	// ——索引是从记录文件推导出来的，它不能成为标注的唯一存放处。
	rec, err := s.readRecord(path)
	if err != nil {
		return err
	}
	rec.Tags = normalizeTags(tags)
	rec.Note = note
	rec.Starred = starred
	if err := s.writeRecord(rec); err != nil {
		return err
	}

	e.Tags = cloneStrings(rec.Tags)
	e.Note = rec.Note
	e.Starred = rec.Starred
	s.index.Upsert(e)
	if err := s.saveIndex(s.index); err != nil {
		return err
	}

	// 这份恰好是该类最新一份时，副本也要跟着更新，否则「一键复用」
	// 拿到的会是改动前的标注。
	if newest, ok := s.newestOf(c); ok && newest.ID == id {
		if err := s.writeLatest(rec); err != nil {
			return err
		}
	}

	s.notify(id, ActionMeta)
	return nil
}

// AgeMinutes 返回某份记录距今多少分钟。
//
// 前端拿它拼「这是 N 分钟前扫的」。负数（时钟回拨）按 0 处理：显示「-3 分钟前」
// 比不显示更让人困惑。
func (s *Store) AgeMinutes(createdAt time.Time) float64 {
	if createdAt.IsZero() {
		return 0
	}
	m := s.now().Sub(createdAt).Minutes()
	if m < 0 {
		return 0
	}
	return m
}

// UndoWindow 返回删除的可撤销窗口。
func (s *Store) UndoWindow() time.Duration { return s.undoWindow }

// Dir 返回历史根目录。
func (s *Store) Dir() string { return s.dir }

// notify 发布一次历史变更。
func (s *Store) notify(id, action string) {
	if s.onChanged != nil {
		s.onChanged(id, action)
	}
}

func (s *Store) logf(msg string, args ...any) {
	s.logger.Info(msg, args...)
}

func cloneEntry(e HistoryIndexEntry) HistoryIndexEntry {
	e.Tags = cloneStrings(e.Tags)
	e.Regions = cloneStrings(e.Regions)
	return e
}

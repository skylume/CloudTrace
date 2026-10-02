package history

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cloudtrace/internal/config"
	"cloudtrace/internal/model"
)

// 本机删一个极小的目录也要将近一秒（实测 RemoveAll 单个空目录 790ms），
// 每个用例各用一个 t.TempDir() 会让整个包多花一分多钟，全耗在清理上。
// 改成整个包共用一个基目录，每个用例在其中占一个子目录，最后统一删一次。
var (
	testBaseOnce sync.Once
	testBaseDir  string
	testBaseSeq  atomic.Int64
)

// TestMain 不做清理。
//
// 本机删目录极慢：删一个只有两个空子目录的目录要 790ms，把整棵测试目录删掉
// 要将近一分钟——比全部用例本身还长二十倍（4.2s vs 68s）。与其把时间花在
// 删除上，不如把它留给系统临时目录的定期回收。
//
// 每次运行用独立的目录名，因此上一次的残留不会干扰本次。
func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

// newTestDir 在共享基目录下分配一个用例专属目录。
//
// 基目录整个包只建一次，各用例在其中占一个子目录：每个用例各用一次
// t.TempDir() 会把清理开销乘以用例数。
func newTestDir(t *testing.T) string {
	t.Helper()
	testBaseOnce.Do(func() {
		dir, err := os.MkdirTemp("", "cloudtrace-history-test-*")
		if err != nil {
			panic(err)
		}
		testBaseDir = dir
	})
	dir := filepath.Join(testBaseDir, fmt.Sprintf("case%04d", testBaseSeq.Add(1)))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建用例目录失败：%v", err)
	}
	return dir
}

// harness 把 Store 的可注入依赖都换成可控实现，便于断言。
type harness struct {
	t     *testing.T
	store *Store
	dir   string
	clock *fakeClock
	opens *openLog

	mu      sync.Mutex
	cfg     config.HistoryConfig
	changes []changeRecord
}

// changeRecord 记录一次 history/changed 回调。
type changeRecord struct {
	ID     string
	Action string
}

// newHarness 构造一个用临时目录的 Store。
func newHarness(t *testing.T, tweak ...func(*config.HistoryConfig)) *harness {
	t.Helper()

	h := &harness{
		t:     t,
		dir:   newTestDir(t),
		clock: newFakeClock(time.Date(2026, 9, 27, 14, 30, 12, 0, time.UTC)),
		opens: &openLog{},
		cfg:   config.Default().History,
	}
	// 默认放开保留份数：绝大多数用例关心的是存档与读取，不该被清理策略
	// 顺手裁掉。清理相关的用例自己把份数改小。
	h.cfg.KeepCount = 1000
	for _, fn := range tweak {
		fn(&h.cfg)
	}

	store, err := New(Options{
		Dir:        h.dir,
		Config:     h.Config,
		Now:        h.clock.Now,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		UndoWindow: time.Hour, // 测试里默认不自动落定，需要时再手动触发
		OnChanged:  h.onChanged,
		Open:       h.opens.open,
	})
	if err != nil {
		t.Fatalf("构造 Store 失败：%v", err)
	}
	h.store = store
	t.Cleanup(func() { _ = store.Close() })
	return h
}

// Config 返回当前历史配置，供 Store 回调。
func (h *harness) Config() config.HistoryConfig {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cfg
}

// SetConfig 在测试中改配置，模拟用户改设置。
func (h *harness) SetConfig(fn func(*config.HistoryConfig)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	fn(&h.cfg)
}

func (h *harness) onChanged(id, action string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.changes = append(h.changes, changeRecord{ID: id, Action: action})
}

// actions 返回到目前为止收到的事件动作序列。
func (h *harness) actions() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.changes))
	for _, c := range h.changes {
		out = append(out, c.Action)
	}
	return out
}

// recordPath 返回某条记录在磁盘上的路径（按分类推导）。
func (h *harness) recordPath(ipVersion int, typ, id string) string {
	return filepath.Join(h.dir, ipDirName(ipVersion), typ, id+".json")
}

// seedRecordsOnDisk 绕过 Store 直接往磁盘上铺 n 份扫描记录。
//
// 这样构造出的就是「攒了很久的历史目录」的真实状态，而且不必为每份记录付
// 三次 fsync 的代价（本机一次 fsync 要几百毫秒，走 Save 铺 100 份要几十秒）。
func (h *harness) seedRecordsOnDisk(n int) {
	h.t.Helper()
	h.seed(4, TypeScan, n, nil)
}

// seed 往磁盘铺 n 份记录，opt 可逐份定制。
func (h *harness) seed(ipVersion int, typ string, n int, opt func(i int, rec *HistoryRecord)) {
	h.t.Helper()
	for i := 0; i < n; i++ {
		results := recordsOf(3, 20)
		rec := HistoryRecord{
			ID:        fmt.Sprintf("20260927_%06d_%08x", 143012+i, i),
			Type:      typ,
			IPVersion: ipVersion,
			CreatedAt: h.clock.Now().Add(time.Duration(i) * time.Minute),
			Duration:  10,
			Count:     len(results),
			Summary:   model.Summarize(results),
			Results:   results,
		}
		if opt != nil {
			opt(i, &rec)
		}
		if rec.Count == 0 {
			rec.Count = len(rec.Results)
		}
		h.writeRecordFile(rec)
	}
}

// writeRecordFile 把一份记录直接写到磁盘，不经过 Store。
func (h *harness) writeRecordFile(rec HistoryRecord) {
	h.t.Helper()
	raw, err := json.Marshal(rec)
	if err != nil {
		h.t.Fatalf("序列化记录失败：%v", err)
	}
	path := h.recordPath(rec.IPVersion, rec.Type, rec.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		h.t.Fatalf("建目录失败：%v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		h.t.Fatalf("写记录失败：%v", err)
	}
}

// reopen 用同一个目录再开一个 Store，模拟「重启后打开历史目录」。
func (h *harness) reopen() *Store {
	h.t.Helper()
	store, err := New(Options{
		Dir:        h.dir,
		Config:     h.Config,
		Now:        h.clock.Now,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		UndoWindow: time.Hour,
		OnChanged:  h.onChanged,
		Open:       h.opens.open,
	})
	if err != nil {
		h.t.Fatalf("重新打开 Store 失败：%v", err)
	}
	h.t.Cleanup(func() { _ = store.Close() })
	return store
}

// saveScan 存一份扫描历史。
func (h *harness) saveScan(ipVersion int, params model.ScanParams, results []model.IPRecord) HistoryRecord {
	h.t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		h.t.Fatalf("序列化扫描参数失败：%v", err)
	}
	rec, err := h.store.Save(HistoryRecord{
		Type:      TypeScan,
		IPVersion: ipVersion,
		Params:    raw,
		Origins:   model.ParamOrigins{"scan.workers": model.OriginUser},
		Preset:    "标准",
		Duration:  12.5,
		Summary:   model.Summarize(results),
		Results:   results,
	})
	if err != nil {
		h.t.Fatalf("存档失败：%v", err)
	}
	return rec
}

// saveSpeed 存一份测速历史。
func (h *harness) saveSpeed(ipVersion int, params model.SpeedParams, results []model.IPRecord) HistoryRecord {
	h.t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		h.t.Fatalf("序列化测速参数失败：%v", err)
	}
	rec, err := h.store.Save(HistoryRecord{
		Type:      TypeSpeed,
		IPVersion: ipVersion,
		Params:    raw,
		Duration:  8,
		Summary:   model.Summarize(results),
		Results:   results,
	})
	if err != nil {
		h.t.Fatalf("存档失败：%v", err)
	}
	return rec
}

// fakeClock 是可推进的时钟。
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock(t time.Time) *fakeClock { return &fakeClock{t: t} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// openLog 记录所有经过 Store 的文件读取，用来断言「列表页没有打开记录文件」。
type openLog struct {
	mu    sync.Mutex
	paths []string
}

func (l *openLog) open(path string) ([]byte, error) {
	l.mu.Lock()
	l.paths = append(l.paths, path)
	l.mu.Unlock()
	return os.ReadFile(path)
}

func (l *openLog) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.paths = nil
}

func (l *openLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.paths...)
}

// recordsOf 造 n 条带地区信息的结果，第 i 条的延迟为 base+i。
func recordsOf(n int, base float64) []model.IPRecord {
	out := make([]model.IPRecord, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, model.IPRecord{
			IP:         fmt.Sprintf("1.1.%d.%d", i/256, i%256),
			Port:       443,
			UseTLS:     true,
			Latency:    base + float64(i),
			LatencyAvg: base + float64(i),
			Recv:       3,
			Sent:       3,
			Colo:       []string{"HKG", "NRT", "SJC"}[i%3],
		})
	}
	return out
}

// scanParams 造一份可复现的扫描参数。
func scanParams(workers int) model.ScanParams {
	return model.ScanParams{
		Mode:             "tcping",
		Workers:          workers,
		SampleMax:        500,
		LatencyThreshold: 230,
		PingTimes:        3,
		Port:             443,
		IPVersion:        4,
		SourceMode:       "official",
		TimeoutMS:        1000,
	}
}

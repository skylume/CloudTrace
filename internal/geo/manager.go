package geo

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"cloudtrace/internal/config"
	"cloudtrace/internal/netx"
)

// Status 是 ASN 库的当前状态，供设置页展示。
type Status struct {
	// Source 是当前生效的数据源标识。
	Source string `json:"source"`
	// Path 是库文件所在位置：TSV 源是目录，mmdb 源是文件本身。
	Path string `json:"path"`
	// FileTime 是库文件的最后修改时间。
	FileTime time.Time `json:"file_time"`
	// Records 是索引里的区间条数；0 表示未知（mmdb 不提供这个数字）。
	Records int `json:"records"`
	// UpdatedAt 是本次成功加载或更新的时间。
	UpdatedAt time.Time `json:"updated_at"`
	// Loaded 表示库当前可用。
	Loaded bool `json:"loaded"`
	// Error 是最近一次失败的原因，为空表示没有问题。
	//
	// 失败只记在这里，不向外抛：ASN 查询是锦上添花，任何情况下都不该让
	// 扫描或测速停下来。
	Error string `json:"error,omitempty"`

	// Downloading 表示正在下载库文件。
	Downloading bool `json:"downloading"`
	// DownloadRead 是本次下载已读取的字节数。
	DownloadRead int64 `json:"download_read"`
	// DownloadTotal 是本次下载的总字节数；0 表示服务端没给长度，
	// 此时界面只显示「进行中」，不显示百分比。
	DownloadTotal int64 `json:"download_total"`
}

// Options 是 Manager 的构造参数。
type Options struct {
	// Config 每次调用时现取地理配置。
	//
	// 取函数而不是值：用户在设置里切了数据源或改了更新周期，下一次加载就
	// 该按新配置走，不需要重启。
	Config func() config.GeoConfig
	// DataDir 是数据目录：geo.asn_db_path 里的相对路径按它解释。
	DataDir string
	// Now 取当前时间，为 nil 时用 time.Now。
	Now func() time.Time
	// Fetch 拉取远程文件，为 nil 时用默认的 HTTP 实现。
	Fetch Fetcher
	// Dialer 接管域名解析；为 nil 时走系统解析。
	//
	// 只在 Fetch 为空时生效——调用方自己给了拉取实现，就由它自己决定怎么解析。
	Dialer netx.Dialer
	// Cache 是归属地缓存，为 nil 时不缓存。
	//
	// 由调用方持有：它记录的是扫描过程中顺手学到的地区，与 ASN 库的加载
	// 生命周期无关，关掉 ASN 查询也照样有用。
	Cache *InfoCache
	// OnProgress 在下载过程中被调用，用来把进度推给界面。
	//
	// 取回调而不是让调用方轮询：下载是后台发生的，轮询要么太密（浪费）要么
	// 太疏（进度一跳一跳）。
	OnProgress func(Status)
	// Logger 为 nil 时用 slog.Default()。
	Logger *slog.Logger
}

// 进度上报的步长。
//
// 库文件是几兆到十几兆，按 256KB 上报大约是几十次——足够让进度条动起来，又
// 不会把事件总线刷满。
const progressStep = 256 << 10

// TopicProgress 是下载进度变化时发布的事件 topic。
//
// 只带「状态变了」这个信号，载荷是当时的 Status；订阅方拿它当触发条件，而不是
// 权威数据源——状态以 Status() 为准，那里才是加了锁的一致快照。
const TopicProgress = "geo/progress"

// Manager 管理 ASN 库的加载、下载与更新。
type Manager struct {
	cfg     func() config.GeoConfig
	dataDir string
	now     func() time.Time
	fetch   Fetcher
	// dialer 接管域名解析；为 nil 时走系统解析。
	dialer netx.Dialer
	cache  *InfoCache
	logger *slog.Logger

	onProgress func(Status)

	mu     sync.RWMutex
	lookup ASNLookup
	status Status
	// reported 是上一次上报进度时的已读字节数，用来按步长节流。
	reported int64

	// 本机出口地区的探测结论，只探一次。
	exitLoc     string
	exitChecked bool
}

// NewManager 构造管理器，并按当前配置尝试加载一次库。
//
// 加载失败不返回错误：拿不到库只是结果里少两列，让程序因为一份可选数据
// 起不来是本末倒置。失败原因记在 Status 里。
func NewManager(opts Options) *Manager {
	m := &Manager{
		cfg:        opts.Config,
		dataDir:    opts.DataDir,
		now:        opts.Now,
		fetch:      opts.Fetch,
		dialer:     opts.Dialer,
		cache:      opts.Cache,
		onProgress: opts.OnProgress,
		logger:     opts.Logger,
	}
	if m.cfg == nil {
		m.cfg = func() config.GeoConfig { return config.Default().Geo }
	}
	if m.now == nil {
		m.now = time.Now
	}
	if m.fetch == nil {
		m.fetch = httpFetcher(downloadTimeout, m.dialer)
	}
	if m.logger == nil {
		m.logger = slog.Default()
	}
	if strings.TrimSpace(m.dataDir) == "" {
		m.dataDir = "."
	}
	m.Reload()
	return m
}

// Reload 按当前配置重新加载库。
//
// 切数据源、手动替换了库文件之后都走这里；加载不出来就卸载旧库，避免继续
// 拿一个已经和配置对不上的库去查询。
func (m *Manager) Reload() {
	cfg := m.cfg()
	if cfg.ASNSource == SourceOff {
		m.unload(Status{Source: SourceOff, Path: m.libraryPath(cfg)})
		return
	}

	path := m.libraryPath(cfg)
	lookup, err := NewASNLookup(cfg.ASNSource, path)
	if err != nil {
		if errors.Is(err, ErrSourceOff) {
			m.unload(Status{Source: SourceOff, Path: path})
			return
		}
		m.unload(Status{Source: cfg.ASNSource, Path: path, Error: err.Error()})
		m.logger.Warn("ASN 库不可用，结果中不会显示运营商归属", "source", cfg.ASNSource, "err", err)
		return
	}

	st := Status{
		Source:    cfg.ASNSource,
		Path:      path,
		Records:   lookup.Count(),
		UpdatedAt: m.now(),
		Loaded:    true,
	}
	if info, err := os.Stat(m.libraryFile(cfg, path)); err == nil {
		st.FileTime = info.ModTime()
	}

	m.mu.Lock()
	old := m.lookup
	m.inheritError(&st)
	m.lookup = lookup
	m.status = st
	m.mu.Unlock()

	if old != nil {
		_ = old.Close()
	}
	m.logger.Info("ASN 库已加载", "source", cfg.ASNSource, "records", st.Records)
}

// Ensure 保证库处于可用状态：缺失就下载，超期就更新。
//
// 刻意不返回错误：这个方法在启动时的后台 goroutine 里调用，任何失败都只能
// 降级——少两列归属信息不该影响用户能不能扫出结果。结果记在 Status 里。
func (m *Manager) Ensure(ctx context.Context) {
	cfg := m.cfg()
	if cfg.ASNSource == SourceOff {
		m.Reload()
		return
	}
	if !cfg.ASNAutoUpdate {
		// 自动更新关掉时不做任何网络请求，本地有什么就用什么。
		m.Reload()
		return
	}

	missing := !m.libraryExists(cfg)
	if missing {
		if err := m.Update(ctx); err != nil {
			m.logger.Warn("下载 ASN 库失败，结果中不会显示运营商归属", "err", err)
		}
		return
	}

	if !m.stale(cfg) {
		m.Reload()
		return
	}
	if err := m.Update(ctx); err != nil {
		m.logger.Warn("更新 ASN 库失败，继续使用本地已有的库", "err", err)
	}
}

// Update 强制下载一次库并重新加载。
//
// 与 Ensure 的区别是它会报错：这是用户在设置页主动点的，失败了要给他一个
// 明确的说法。
func (m *Manager) Update(ctx context.Context) error {
	cfg := m.cfg()
	if cfg.ASNSource == SourceOff {
		return ErrSourceOff
	}

	err := m.download(ctx, cfg)
	m.setError(err)
	// 成功与部分成功都要重新加载：两个库文件里有一个下好了，已经拿到的
	// 那一半就该立刻生效。下载全失败时 Reload 会退回使用本地已有的库。
	m.Reload()
	return err
}

// download 按数据源拉取并落盘。
func (m *Manager) download(ctx context.Context, cfg config.GeoConfig) error {
	switch {
	case isMMDB(cfg.ASNSource):
		return m.fetchTo(ctx, mmdbURL, m.libraryPath(cfg), validateMMDB)

	case cfg.ASNSource == "" || strings.EqualFold(cfg.ASNSource, SourceIPToASN):
		// 两个文件分别落盘：只有一个成功时另一个保持原样，比整批失败强。
		dir := m.libraryPath(cfg)
		var firstErr error
		for _, spec := range []struct {
			url  string
			name string
		}{
			{iptoASNv4URL, iptoASNv4Name},
			{iptoASNv6URL, iptoASNv6Name},
		} {
			path := libraryTarget(dir, spec.name)
			if err := m.fetchTo(ctx, spec.url, path, validateTSV); err != nil {
				m.logger.Warn("下载 ASN 库文件失败", "file", spec.name, "err", err)
				if firstErr == nil {
					firstErr = err
				}
			}
		}
		return firstErr

	default:
		return errors.New("未知的 ASN 数据源：" + cfg.ASNSource)
	}
}

// fetchTo 下载一个文件，校验通过后原子落盘。
//
// 下载期间状态里会带上下载进度，并在开始与结束时各通知一次——「正在下载」和
// 「下载完了」都必须让界面知道，否则进度条会一直挂在那里。
func (m *Manager) fetchTo(ctx context.Context, url, path string, validate func([]byte) error) error {
	m.beginDownload()
	defer m.endDownload()

	data, err := m.fetch(ctx, url, m.reportProgress)
	if err != nil {
		return err
	}
	if err := validate(data); err != nil {
		return errors.New("下载到的内容不可用（" + err.Error() + "）")
	}
	return writeLibrary(path, data)
}

// beginDownload 把状态切到「正在下载」并通知一次。
func (m *Manager) beginDownload() {
	m.mu.Lock()
	m.status.Downloading = true
	m.status.DownloadRead = 0
	m.status.DownloadTotal = 0
	m.reported = 0
	status := m.status
	m.mu.Unlock()
	m.notify(status)
}

// endDownload 把状态切回「不在下载」并通知一次。
func (m *Manager) endDownload() {
	m.mu.Lock()
	m.status.Downloading = false
	m.status.DownloadRead = 0
	m.status.DownloadTotal = 0
	status := m.status
	m.mu.Unlock()
	m.notify(status)
}

// reportProgress 按步长节流地上报下载进度。
//
// 节流是必须的：读取是分块的，每块都上报会把事件总线刷满，而进度条并不需要
// 那么细的粒度。首个字节与最后一块一律上报，否则进度条会缺头少尾。
func (m *Manager) reportProgress(read, total int64) {
	m.mu.Lock()
	step := read - m.reported
	if step < progressStep && read != total {
		m.mu.Unlock()
		return
	}
	m.reported = read
	m.status.DownloadRead = read
	m.status.DownloadTotal = total
	status := m.status
	m.mu.Unlock()
	m.notify(status)
}

// notify 把状态交给回调；没接回调时什么也不做。
func (m *Manager) notify(status Status) {
	if m.onProgress != nil {
		m.onProgress(status)
	}
}

// Lookup 查询一个地址；库不可用时返回 false。
func (m *Manager) Lookup(ip netip.Addr) (ASNInfo, bool) {
	m.mu.RLock()
	lookup := m.lookup
	m.mu.RUnlock()
	if lookup == nil {
		return ASNInfo{}, false
	}
	info, err := lookup.Lookup(ip)
	if err != nil || info.ASN == 0 {
		return ASNInfo{}, false
	}
	return info, true
}

// LookupFunc 返回可直接注入的函数形式。
func (m *Manager) LookupFunc() LookupFunc { return m.Lookup }

// Status 返回库的当前状态。
func (m *Manager) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

// Close 释放库占用的资源。
func (m *Manager) Close() error {
	m.mu.Lock()
	lookup := m.lookup
	m.lookup = nil
	m.mu.Unlock()
	if lookup != nil {
		return lookup.Close()
	}
	return nil
}

// unload 卸载库并记录状态。
func (m *Manager) unload(st Status) {
	m.mu.Lock()
	old := m.lookup
	m.inheritError(&st)
	m.lookup = nil
	m.status = st
	m.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
}

// inheritError 决定新状态要不要沿用上一次的失败原因。
//
// 同一份配置下已经有原因时以它为准：那通常是根因（例如下载失败），紧随其后
// 的加载失败只是它的后果，报出来反而看不出真正发生了什么。换了数据源或库
// 路径就是另一回事，旧原因不再适用。
func (m *Manager) inheritError(st *Status) {
	if m.status.Source != st.Source || m.status.Path != st.Path {
		return
	}
	if m.status.Error != "" {
		st.Error = m.status.Error
	}
}

// setError 把失败原因记进状态；传 nil 表示清除。
func (m *Manager) setError(err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	m.mu.Lock()
	m.status.Error = msg
	m.mu.Unlock()
}

package geo

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cloudtrace/internal/config"
)

// gzBytes 把文本压成 gzip，模拟官方提供的压缩库文件。
func gzBytes(t *testing.T, text string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(text)); err != nil {
		t.Fatalf("压缩失败：%v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("压缩失败：%v", err)
	}
	return buf.Bytes()
}

// 一份最小可用的 v4 库内容。
const v4Fixture = "1.1.1.0\t1.1.1.255\t13335\tAU\tCLOUDFLARENET\n"

// fakeFetcher 记录每次拉取，并返回预设内容。
type fakeFetcher struct {
	mu    sync.Mutex
	calls []string
	reply func(url string) ([]byte, error)
}

func (f *fakeFetcher) fetch(_ context.Context, url string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, url)
	f.mu.Unlock()
	if f.reply == nil {
		return nil, errors.New("没有配置返回内容")
	}
	return f.reply(url)
}

func (f *fakeFetcher) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeFetcher) urls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// managerFixture 造一个测试用的管理器，并返回可随时修改的配置。
func managerFixture(t *testing.T, fetch Fetcher, now func() time.Time, mutate func(*config.GeoConfig)) (*Manager, *config.GeoConfig) {
	t.Helper()
	cfg := config.Default().Geo
	if mutate != nil {
		mutate(&cfg)
	}
	m := NewManager(Options{
		Config:  func() config.GeoConfig { return cfg },
		DataDir: t.TempDir(),
		Now:     now,
		Fetch:   fetch,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	t.Cleanup(func() { _ = m.Close() })
	return m, &cfg
}

// asnDir 返回数据目录下默认的库位置。
func asnDir(dataDir string) string { return filepath.Join(config.CacheDir(dataDir), "asn") }

// 本地已经有库时直接加载，不发任何请求。
func TestManagerLoadsExistingLibrary(t *testing.T) {
	fetch := &fakeFetcher{}
	m, _ := managerFixture(t, fetch.fetch, nil, nil)

	dir := asnDir(m.dataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	writeFile(t, dir, iptoASNv4Name, tsvLines(v4Fixture))
	m.Reload()

	st := m.Status()
	if !st.Loaded || st.Records != 1 {
		t.Fatalf("状态 = %+v，期望已加载且 1 条记录", st)
	}
	if st.FileTime.IsZero() {
		t.Error("文件时间没有填上")
	}
	if st.Source != SourceIPToASN {
		t.Errorf("数据源 = %q", st.Source)
	}
	if fetch.count() != 0 {
		t.Errorf("本地已有库却发起了 %d 次请求", fetch.count())
	}

	info, ok := m.Lookup(netip.MustParseAddr("1.1.1.1"))
	if !ok || info.ASN != 13335 {
		t.Fatalf("查询结果 = %+v ok=%v", info, ok)
	}
	if _, ok := m.LookupFunc()(netip.MustParseAddr("9.9.9.9")); ok {
		t.Error("查不到的地址应当返回 false")
	}
}

// 库缺失时自动下载并落盘。
func TestManagerDownloadsWhenMissing(t *testing.T) {
	fetch := &fakeFetcher{reply: func(url string) ([]byte, error) {
		if strings.Contains(url, "v4") {
			return gzBytes(t, v4Fixture), nil
		}
		return gzBytes(t, "2606:4700::\t2606:4700:ffff:ffff:ffff:ffff:ffff:ffff\t13335\tAU\tCLOUDFLARENET\n"), nil
	}}
	m, _ := managerFixture(t, fetch.fetch, nil, nil)

	m.Ensure(context.Background())

	st := m.Status()
	if !st.Loaded {
		t.Fatalf("下载后仍未加载：%+v", st)
	}
	if st.Records != 2 {
		t.Errorf("区间条数 = %d，期望 2（v4 + v6 各一条）", st.Records)
	}
	if fetch.count() != 2 {
		t.Errorf("请求次数 = %d，期望 2", fetch.count())
	}
	if st.Error != "" {
		t.Errorf("不该有错误：%s", st.Error)
	}

	// 两个文件都要落盘，且是压缩原件。
	for _, name := range []string{iptoASNv4Name + ".gz", iptoASNv6Name + ".gz"} {
		if _, err := os.Stat(filepath.Join(asnDir(m.dataDir), name)); err != nil {
			t.Errorf("文件 %s 没有落盘：%v", name, err)
		}
	}
	if _, ok := m.Lookup(netip.MustParseAddr("2606:4700::1")); !ok {
		t.Error("下载的 v6 库没有生效")
	}
}

// 关掉自动更新时一个请求都不发：用户关掉它就是想自己控制。
func TestManagerSkipsDownloadWhenAutoUpdateOff(t *testing.T) {
	fetch := &fakeFetcher{reply: func(string) ([]byte, error) {
		return gzBytes(t, v4Fixture), nil
	}}
	m, _ := managerFixture(t, fetch.fetch, nil, func(c *config.GeoConfig) {
		c.ASNAutoUpdate = false
	})

	m.Ensure(context.Background())

	if fetch.count() != 0 {
		t.Fatalf("关掉自动更新后仍发起了 %d 次请求", fetch.count())
	}
	if st := m.Status(); st.Loaded {
		t.Errorf("没有库时不该报告已加载：%+v", st)
	}
}

// 超过更新周期时后台更新。
func TestManagerUpdatesWhenStale(t *testing.T) {
	fetch := &fakeFetcher{reply: func(url string) ([]byte, error) {
		return gzBytes(t, v4Fixture), nil
	}}
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	m, _ := managerFixture(t, fetch.fetch, func() time.Time { return now }, func(c *config.GeoConfig) {
		c.ASNUpdateIntervalDays = 7
	})

	// 先放一份 8 天前的库。
	dir := asnDir(m.dataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	path := writeFile(t, dir, iptoASNv4Name, tsvLines(v4Fixture))
	old := now.Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("改时间失败：%v", err)
	}

	m.Ensure(context.Background())

	if fetch.count() == 0 {
		t.Fatal("库已超期却没有更新")
	}
	if st := m.Status(); !st.Loaded {
		t.Fatalf("更新后仍未加载：%+v", st)
	}
	// 更新要写回正在用的那个文件，否则加载时还会读到旧内容。
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取更新后的库失败：%v", err)
	}
	if len(raw) < 2 || raw[0] != 0x1f || raw[1] != 0x8b {
		t.Fatalf("更新后的文件不是下载回来的内容：%q", raw)
	}
}

// 没超期时不更新：每次启动都重下一遍几十兆没有道理。
func TestManagerKeepsFreshLibrary(t *testing.T) {
	fetch := &fakeFetcher{reply: func(string) ([]byte, error) {
		return gzBytes(t, v4Fixture), nil
	}}
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	m, _ := managerFixture(t, fetch.fetch, func() time.Time { return now }, func(c *config.GeoConfig) {
		c.ASNUpdateIntervalDays = 7
	})

	dir := asnDir(m.dataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	path := writeFile(t, dir, iptoASNv4Name, tsvLines(v4Fixture))
	fresh := now.Add(-24 * time.Hour)
	if err := os.Chtimes(path, fresh, fresh); err != nil {
		t.Fatalf("改时间失败：%v", err)
	}

	m.Ensure(context.Background())

	if fetch.count() != 0 {
		t.Fatalf("库还新鲜却更新了 %d 次", fetch.count())
	}
	if st := m.Status(); !st.Loaded {
		t.Fatalf("本地库应当被加载：%+v", st)
	}
}

// 周期设为 0 表示「仅手动更新」，多旧都不自动拉。
func TestManagerIntervalZeroMeansManualOnly(t *testing.T) {
	fetch := &fakeFetcher{reply: func(string) ([]byte, error) {
		return gzBytes(t, v4Fixture), nil
	}}
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	m, _ := managerFixture(t, fetch.fetch, func() time.Time { return now }, func(c *config.GeoConfig) {
		c.ASNUpdateIntervalDays = config.ASNUpdateIntervalOff
	})

	dir := asnDir(m.dataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	path := writeFile(t, dir, iptoASNv4Name, tsvLines(v4Fixture))
	ancient := now.Add(-365 * 24 * time.Hour)
	if err := os.Chtimes(path, ancient, ancient); err != nil {
		t.Fatalf("改时间失败：%v", err)
	}

	m.Ensure(context.Background())
	if fetch.count() != 0 {
		t.Fatalf("周期为 0 时不该自动更新，实际发了 %d 次请求", fetch.count())
	}
}

// 手动更新不看周期，也不看自动更新开关。
func TestManagerUpdateIsAlwaysAllowed(t *testing.T) {
	fetch := &fakeFetcher{reply: func(string) ([]byte, error) {
		return gzBytes(t, v4Fixture), nil
	}}
	m, _ := managerFixture(t, fetch.fetch, nil, func(c *config.GeoConfig) {
		c.ASNAutoUpdate = false
		c.ASNUpdateIntervalDays = 0
	})

	if err := m.Update(context.Background()); err != nil {
		t.Fatalf("手动更新失败：%v", err)
	}
	if fetch.count() == 0 {
		t.Fatal("手动更新没有发起请求")
	}
	if st := m.Status(); !st.Loaded {
		t.Fatalf("手动更新后仍未加载：%+v", st)
	}
}

// 下载失败时保留本地已有的库，并记下原因。
func TestManagerKeepsLibraryWhenUpdateFails(t *testing.T) {
	fetch := &fakeFetcher{reply: func(string) ([]byte, error) {
		return nil, errors.New("网络不通")
	}}
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	m, _ := managerFixture(t, fetch.fetch, func() time.Time { return now }, func(c *config.GeoConfig) {
		c.ASNUpdateIntervalDays = 7
	})

	dir := asnDir(m.dataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	path := writeFile(t, dir, iptoASNv4Name, tsvLines(v4Fixture))
	old := now.Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("改时间失败：%v", err)
	}

	m.Ensure(context.Background())

	st := m.Status()
	if !st.Loaded {
		t.Fatalf("更新失败后应当继续用本地已有的库：%+v", st)
	}
	if st.Error == "" {
		t.Error("失败原因没有记进状态")
	}
	if _, ok := m.Lookup(netip.MustParseAddr("1.1.1.1")); !ok {
		t.Error("本地库查询应当仍然可用")
	}
}

// 下载回来的内容不可用时绝不能覆盖已有的好库：一次坏下载会让库彻底读不出来。
func TestManagerRejectsBadDownloadWithoutClobbering(t *testing.T) {
	fetch := &fakeFetcher{reply: func(string) ([]byte, error) {
		return []byte("<html>404 Not Found</html>"), nil
	}}
	m, _ := managerFixture(t, fetch.fetch, nil, nil)

	dir := asnDir(m.dataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	path := writeFile(t, dir, iptoASNv4Name, tsvLines(v4Fixture))
	m.Reload()

	if err := m.Update(context.Background()); err == nil {
		t.Fatal("坏内容应当让更新失败")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取原有库失败：%v", err)
	}
	if string(raw) != string(tsvLines(v4Fixture)) {
		t.Fatalf("原有的好库被坏内容覆盖了：%q", raw)
	}
	if st := m.Status(); !st.Loaded {
		t.Fatalf("原有库应当仍然可用：%+v", st)
	}
}

// v4 成功、v6 失败时保留成功的那一份，而不是整批回滚。
func TestManagerKeepsPartialDownload(t *testing.T) {
	fetch := &fakeFetcher{reply: func(url string) ([]byte, error) {
		if strings.Contains(url, "v6") {
			return nil, errors.New("v6 拉不下来")
		}
		return gzBytes(t, v4Fixture), nil
	}}
	m, _ := managerFixture(t, fetch.fetch, nil, nil)

	err := m.Update(context.Background())
	if err == nil {
		t.Fatal("有文件失败时应当报错")
	}
	if st := m.Status(); !st.Loaded {
		t.Fatalf("成功的 v4 应当生效：%+v", st)
	}
	if _, ok := m.Lookup(netip.MustParseAddr("1.1.1.1")); !ok {
		t.Error("v4 库没有生效")
	}
}

// 关掉 ASN 查询时不做任何事，也不报错。
func TestManagerSourceOff(t *testing.T) {
	fetch := &fakeFetcher{reply: func(string) ([]byte, error) {
		return gzBytes(t, v4Fixture), nil
	}}
	m, cfg := managerFixture(t, fetch.fetch, nil, func(c *config.GeoConfig) {
		c.ASNSource = SourceOff
	})
	// 先放一份本地库，用来验证「切回来之后要重新加载」。
	dir := asnDir(m.dataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	writeFile(t, dir, iptoASNv4Name, tsvLines(v4Fixture))

	m.Ensure(context.Background())

	st := m.Status()
	if st.Source != SourceOff || st.Loaded || st.Error != "" {
		t.Fatalf("关掉查询时的状态 = %+v", st)
	}
	if fetch.count() != 0 {
		t.Fatalf("关掉查询后仍发起了 %d 次请求", fetch.count())
	}
	if _, ok := m.Lookup(netip.MustParseAddr("1.1.1.1")); ok {
		t.Error("关掉查询后不该查到东西")
	}
	if err := m.Update(context.Background()); !errors.Is(err, ErrSourceOff) {
		t.Errorf("关掉查询时手动更新应当返回 ErrSourceOff，实际 %v", err)
	}

	// 切回来应当重新加载。
	cfg.ASNSource = SourceIPToASN
	m.Reload()
	if !m.Status().Loaded {
		t.Error("切回默认源后没有重新加载")
	}
}

// 切到关闭状态时要把已经加载的库卸掉，避免拿一个和配置对不上的库继续查。
func TestManagerUnloadsOnSourceOff(t *testing.T) {
	m, cfg := managerFixture(t, nil, nil, nil)
	dir := asnDir(m.dataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	writeFile(t, dir, iptoASNv4Name, tsvLines(v4Fixture))
	m.Reload()
	if !m.Status().Loaded {
		t.Fatal("前置条件不成立：库没有加载")
	}

	cfg.ASNSource = SourceOff
	m.Reload()

	if m.Status().Loaded {
		t.Error("切到关闭状态后库仍处于已加载")
	}
	if _, ok := m.Lookup(netip.MustParseAddr("1.1.1.1")); ok {
		t.Error("卸载后仍能查到结果")
	}
}

// 库文件损坏时降级为「不显示 ASN」，并把原因记进状态。
func TestManagerReportsCorruptedLibrary(t *testing.T) {
	m, _ := managerFixture(t, nil, nil, nil)
	dir := asnDir(m.dataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	writeFile(t, dir, iptoASNv4Name, []byte("这不是 TSV\n"))
	m.Reload()

	st := m.Status()
	if st.Loaded {
		t.Fatalf("坏库不该报告已加载：%+v", st)
	}
	if st.Error == "" {
		t.Error("坏库的原因没有记进状态")
	}
	if _, ok := m.Lookup(netip.MustParseAddr("1.1.1.1")); ok {
		t.Error("坏库不该查出结果")
	}
}

// 库路径可以是用户手动放置的绝对路径。
func TestManagerUsesAbsoluteConfiguredPath(t *testing.T) {
	manual := t.TempDir()
	writeFile(t, manual, iptoASNv4Name, tsvLines(v4Fixture))

	m, _ := managerFixture(t, nil, nil, func(c *config.GeoConfig) {
		c.ASNDBPath = manual
	})
	m.Reload()

	if !m.Status().Loaded {
		t.Fatalf("手动放置的库没有被识别：%+v", m.Status())
	}
	if got := m.Status().Path; got != manual {
		t.Errorf("状态里的路径 = %q，期望 %q", got, manual)
	}
}

// 默认路径按数据目录解析：配置里的默认值自带 data/ 前缀，不能拼成 data/data/。
func TestManagerResolvesDefaultPathUnderDataDir(t *testing.T) {
	m, _ := managerFixture(t, nil, nil, nil)
	want := filepath.Join(m.dataDir, "cache", "asn")
	if got := m.libraryPath(config.Default().Geo); got != want {
		t.Fatalf("默认库路径 = %q，期望 %q", got, want)
	}

	// 相对路径也按数据目录解析。
	got := m.libraryPath(config.GeoConfig{ASNDBPath: "cache/asn"})
	if got != want {
		t.Errorf("相对路径解析为 %q，期望 %q", got, want)
	}
	got = m.libraryPath(config.GeoConfig{ASNDBPath: "data/cache/asn"})
	if got != want {
		t.Errorf("带 data/ 前缀的相对路径解析为 %q，期望 %q", got, want)
	}
}

// mmdb 源的库位置是文件本身，状态里要给出文件路径。
func TestManagerMMDBPathPointsAtFile(t *testing.T) {
	dataDir := t.TempDir()
	m := NewManager(Options{
		Config:  func() config.GeoConfig { return config.Default().Geo },
		DataDir: dataDir,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer func() { _ = m.Close() }()

	want := filepath.Join(dataDir, "cache", "asn", mmdbName)
	if got := m.libraryPath(config.GeoConfig{ASNSource: SourceMMDB, ASNDBPath: "data/cache/asn"}); got != want {
		t.Fatalf("mmdb 库路径 = %q，期望 %q", got, want)
	}
	// 默认路径（配置为空）也要落到 mmdb 文件名上。
	if got := m.libraryPath(config.GeoConfig{ASNSource: SourceMMDB}); got != want {
		t.Fatalf("默认 mmdb 库路径 = %q，期望 %q", got, want)
	}
}

// 未知数据源要报错而不是静默当成默认源。
func TestManagerUnknownSource(t *testing.T) {
	fetch := &fakeFetcher{reply: func(string) ([]byte, error) {
		return gzBytes(t, v4Fixture), nil
	}}
	m, _ := managerFixture(t, fetch.fetch, nil, func(c *config.GeoConfig) {
		c.ASNSource = "magic"
	})

	if err := m.Update(context.Background()); err == nil {
		t.Fatal("未知数据源应当报错")
	}
	if st := m.Status(); st.Loaded {
		t.Errorf("未知数据源不该报告已加载：%+v", st)
	}
}

// ---------------------------------------------------------------------------
// 下载层
// ---------------------------------------------------------------------------

func TestValidateTSV(t *testing.T) {
	if err := validateTSV(gzBytes(t, v4Fixture)); err != nil {
		t.Fatalf("合法内容被判为不可用：%v", err)
	}
	if err := validateTSV(tsvLines(v4Fixture)); err != nil {
		t.Fatalf("未压缩的合法内容被判为不可用：%v", err)
	}
	if err := validateTSV([]byte("<html>404</html>")); err == nil {
		t.Error("HTML 错误页应当被判为不可用")
	}
	if err := validateTSV(gzBytes(t, "只有表头\n")); err == nil {
		t.Error("没有任何区间记录应当被判为不可用")
	}
	if err := validateTSV([]byte{0x1f, 0x8b, 0x08, 0x00}); err == nil {
		t.Error("截断的 gzip 应当被判为不可用")
	}
}

func TestValidateMMDB(t *testing.T) {
	if err := validateMMDB([]byte("这不是 mmdb")); err == nil {
		t.Error("非 mmdb 内容应当被判为不可用")
	}
}

func TestWriteLibraryCreatesDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "lib.tsv")
	if err := writeLibrary(path, []byte("x")); err != nil {
		t.Fatalf("写入失败：%v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "x" {
		t.Fatalf("读回内容 = %q err=%v", raw, err)
	}
}

func TestHTTPFetcher(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write([]byte("hello"))
		case "/big":
			// 比上限多一个字节，用来验证超限会被拒绝。
			_, _ = w.Write(bytes.Repeat([]byte("x"), int(maxDownloadBytes)+1))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	fetch := httpFetcher(10 * time.Second)

	data, err := fetch(context.Background(), ts.URL+"/ok")
	if err != nil {
		t.Fatalf("正常拉取失败：%v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("内容 = %q", data)
	}

	if _, err := fetch(context.Background(), ts.URL+"/missing"); err == nil {
		t.Error("非 200 应当报错")
	}

	// 超限的内容必须被拒绝，而不是截断成一个坏文件。
	if _, err := fetch(context.Background(), ts.URL+"/big"); err == nil {
		t.Error("超过上限的响应应当报错")
	}

	// 上下文取消时立刻返回，不再等下载完。
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fetch(ctx, ts.URL+"/ok"); err == nil {
		t.Error("已取消的上下文应当报错")
	}
}

func TestManagerCloseIsIdempotent(t *testing.T) {
	m, _ := managerFixture(t, nil, nil, nil)
	if err := m.Close(); err != nil {
		t.Fatalf("关闭失败：%v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("重复关闭失败：%v", err)
	}
	if _, ok := m.Lookup(netip.MustParseAddr("1.1.1.1")); ok {
		t.Error("关闭后不该查到结果")
	}
}

func TestManagerStatusSnapshot(t *testing.T) {
	m, _ := managerFixture(t, nil, nil, nil)
	dir := asnDir(m.dataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	writeFile(t, dir, iptoASNv4Name, tsvLines(v4Fixture))
	m.Reload()

	// 状态是值拷贝：外部改动不能影响管理器内部。
	st := m.Status()
	st.Records = 999
	if got := m.Status().Records; got != 1 {
		t.Fatalf("状态被外部改动了：%d", got)
	}
}

func TestManagerLooksUpAcrossSources(t *testing.T) {
	// 同一份数据、两种数据源描述方式：确保按源分派的那段逻辑没有把默认源漏掉。
	for _, source := range []string{"", SourceIPToASN, "IPTOASN"} {
		m, _ := managerFixture(t, nil, nil, func(c *config.GeoConfig) {
			c.ASNSource = source
		})
		dir := asnDir(m.dataDir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("建目录失败：%v", err)
		}
		writeFile(t, dir, iptoASNv4Name, tsvLines(v4Fixture))
		m.Reload()

		if !m.Status().Loaded {
			t.Errorf("数据源 %q 没有加载本地库：%+v", source, m.Status())
		}
	}
}

func TestFetchErrorIsReported(t *testing.T) {
	fetch := &fakeFetcher{reply: func(string) ([]byte, error) {
		return nil, errors.New("连接被拒绝")
	}}
	m, _ := managerFixture(t, fetch.fetch, nil, nil)

	m.Ensure(context.Background())

	st := m.Status()
	if st.Error == "" {
		t.Fatal("下载失败的原因没有记进状态")
	}
	if !strings.Contains(st.Error, "连接被拒绝") {
		t.Errorf("状态里的原因 = %q", st.Error)
	}
	// 首次下载失败也不能影响主流程：Lookup 只是查不到。
	if _, ok := m.Lookup(netip.MustParseAddr("1.1.1.1")); ok {
		t.Error("没有库时不该查到结果")
	}
}

func TestManagerDownloadURLs(t *testing.T) {
	fetch := &fakeFetcher{reply: func(string) ([]byte, error) {
		return gzBytes(t, v4Fixture), nil
	}}
	m, _ := managerFixture(t, fetch.fetch, nil, nil)
	_ = m.Update(context.Background())

	urls := fetch.urls()
	if len(urls) != 2 {
		t.Fatalf("请求了 %d 个地址，期望 2 个（v4 与 v6）", len(urls))
	}
	for i, want := range []string{iptoASNv4URL, iptoASNv6URL} {
		if urls[i] != want {
			t.Errorf("第 %d 个地址 = %q，期望 %q", i+1, urls[i], want)
		}
	}
}

func TestManagerMMDBDownloadUsesMirror(t *testing.T) {
	fetch := &fakeFetcher{reply: func(string) ([]byte, error) {
		return nil, errors.New("内容不合法")
	}}
	m, _ := managerFixture(t, fetch.fetch, nil, func(c *config.GeoConfig) {
		c.ASNSource = SourceMMDB
	})
	_ = m.Update(context.Background())

	urls := fetch.urls()
	if len(urls) != 1 || urls[0] != mmdbURL {
		t.Fatalf("请求的地址 = %v，期望只请求镜像 %s", urls, mmdbURL)
	}
	if !strings.Contains(mmdbURL, "GeoLiet2") {
		t.Error("镜像路径里的 GeoLiet2 是上游仓库的真实名字，不是笔误")
	}
}

func TestManagerNowDefaultsToTimeNow(t *testing.T) {
	cfg := config.Default().Geo
	m := NewManager(Options{
		Config:  func() config.GeoConfig { return cfg },
		DataDir: t.TempDir(),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer func() { _ = m.Close() }()

	if m.now() == (time.Time{}) {
		t.Error("没有注入时钟时应当退回 time.Now")
	}
	if m.fetch == nil || m.logger == nil {
		t.Error("没有注入拉取器或日志器时应当有默认实现")
	}
	// 周期为 0 恒不超期。
	if m.stale(config.GeoConfig{ASNUpdateIntervalDays: 0}) {
		t.Error("周期为 0 时不该判定为超期")
	}
}

func TestManagerStaleWithoutFile(t *testing.T) {
	cfg := config.Default().Geo
	cfg.ASNUpdateIntervalDays = 7
	m := NewManager(Options{
		Config:  func() config.GeoConfig { return cfg },
		DataDir: t.TempDir(),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer func() { _ = m.Close() }()

	// 文件不存在时算超期，也就是「需要下载」。
	if !m.stale(cfg) {
		t.Error("库文件不存在时应当判定为需要下载")
	}
	if m.libraryExists(cfg) {
		t.Error("空目录里不该报告库已存在")
	}
}

func TestManagerEmptyDataDirFallsBack(t *testing.T) {
	m := NewManager(Options{
		Config: func() config.GeoConfig { return config.Default().Geo },
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer func() { _ = m.Close() }()
	if m.dataDir != "." {
		t.Fatalf("数据目录为空时应当退回当前目录，实际 %q", m.dataDir)
	}
}

func TestManagerErrorIsPreserved(t *testing.T) {
	// 状态里的原因要能直接显示给用户，不能被加工掉。
	fetch := &fakeFetcher{reply: func(string) ([]byte, error) {
		return nil, fmt.Errorf("拉取 %s 失败", iptoASNv4URL)
	}}
	m, _ := managerFixture(t, fetch.fetch, nil, nil)
	m.Ensure(context.Background())

	got := m.Status().Error
	if !strings.Contains(got, iptoASNv4URL) {
		t.Fatalf("状态里的原因被加工过：%q", got)
	}
}

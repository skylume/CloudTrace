package geo

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFile 在临时目录里写一个文件。
func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("写入 %s 失败：%v", name, err)
	}
	return path
}

// tsvLines 拼出几行 iptoasn 格式的记录。
func tsvLines(rows ...string) []byte {
	return []byte(strings.Join(rows, "\n") + "\n")
}

// 命中的区间要能查出 AS 号、组织名与国家码。
func TestIPToASNLookupHitsKnownRange(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, iptoASNv4Name, tsvLines(
		"1.1.1.0\t1.1.1.255\t13335\tAU\tCLOUDFLARENET",
		"8.8.8.0\t8.8.8.255\t15169\tUS\tGOOGLE",
	))

	lookup, err := NewASNLookup(SourceIPToASN, dir)
	if err != nil {
		t.Fatalf("打开库失败：%v", err)
	}
	defer func() { _ = lookup.Close() }()

	info, err := lookup.Lookup(netip.MustParseAddr("1.1.1.1"))
	if err != nil {
		t.Fatalf("查询失败：%v", err)
	}
	if info.ASN != 13335 {
		t.Errorf("ASN = %d，期望 13335", info.ASN)
	}
	if !strings.Contains(info.Org, "CLOUDFLARENET") {
		t.Errorf("组织名 = %q，期望包含 CLOUDFLARENET", info.Org)
	}
	if info.Country != "AU" {
		t.Errorf("国家码 = %q，期望 AU", info.Country)
	}

	if lookup.Count() != 2 {
		t.Errorf("区间条数 = %d，期望 2", lookup.Count())
	}
}

// 落在区间之间的空洞里必须查不到，而不是误报成相邻区间。
func TestIPToASNLookupMissesGap(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, iptoASNv4Name, tsvLines(
		"1.1.1.0\t1.1.1.255\t13335\tAU\tCLOUDFLARENET",
		"3.0.0.0\t3.0.0.255\t15169\tUS\tGOOGLE",
	))

	lookup, err := NewASNLookup(SourceIPToASN, dir)
	if err != nil {
		t.Fatalf("打开库失败：%v", err)
	}

	// 2.0.0.1 在两个区间之间的空洞里。
	info, err := lookup.Lookup(netip.MustParseAddr("2.0.0.1"))
	if err != nil {
		t.Fatalf("查询失败：%v", err)
	}
	if info.ASN != 0 {
		t.Fatalf("空洞里的地址查到了 ASN %d", info.ASN)
	}

	// 区间之外也要查不到。
	if info, _ := lookup.Lookup(netip.MustParseAddr("9.9.9.9")); info.ASN != 0 {
		t.Fatalf("区间外的地址查到了 ASN %d", info.ASN)
	}
}

// 文件里的行是乱序的：不能假设上游按起始地址排好了。
func TestIPToASNIndexSortsUnsortedInput(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, iptoASNv4Name, tsvLines(
		"100.0.0.0\t100.0.0.255\t3\tCN\tTHIRD",
		"10.0.0.0\t10.0.0.255\t1\tCN\tFIRST",
		"50.0.0.0\t50.0.0.255\t2\tCN\tSECOND",
	))

	lookup, err := NewASNLookup(SourceIPToASN, dir)
	if err != nil {
		t.Fatalf("打开库失败：%v", err)
	}

	for ip, want := range map[string]uint32{
		"10.0.0.5":  1,
		"50.0.0.5":  2,
		"100.0.0.5": 3,
	} {
		info, err := lookup.Lookup(netip.MustParseAddr(ip))
		if err != nil {
			t.Fatalf("查询 %s 失败：%v", ip, err)
		}
		if info.ASN != want {
			t.Errorf("%s 的 ASN = %d，期望 %d", ip, info.ASN, want)
		}
	}
}

// 二分查找要在大量区间上给出正确答案：这是「必须走索引」的直接验证。
func TestIPToASNBinarySearchAcrossManyRanges(t *testing.T) {
	dir := t.TempDir()
	var rows []string
	const count = 5000
	for i := 0; i < count; i++ {
		// 每段占 256 个地址，段之间留 256 个地址的空洞。
		base := i * 512
		start := fmt.Sprintf("10.%d.%d.0", base>>8&0xff, base&0xff)
		_ = start
		rows = append(rows, fmt.Sprintf("%d.%d.%d.0\t%d.%d.%d.255\t%d\tCN\tORG%d",
			base>>24&0xff, base>>16&0xff, base>>8&0xff,
			base>>24&0xff, base>>16&0xff, base>>8&0xff,
			i+1, i+1))
	}
	writeFile(t, dir, iptoASNv4Name, tsvLines(rows...))

	lookup, err := NewASNLookup(SourceIPToASN, dir)
	if err != nil {
		t.Fatalf("打开库失败：%v", err)
	}
	if lookup.Count() != count {
		t.Fatalf("区间条数 = %d，期望 %d", lookup.Count(), count)
	}

	for _, i := range []int{0, 1, 2, count / 2, count - 2, count - 1} {
		base := i * 512
		ip := fmt.Sprintf("%d.%d.%d.7", base>>24&0xff, base>>16&0xff, base>>8&0xff)
		info, err := lookup.Lookup(netip.MustParseAddr(ip))
		if err != nil {
			t.Fatalf("查询 %s 失败：%v", ip, err)
		}
		if info.ASN != uint32(i+1) {
			t.Errorf("%s 的 ASN = %d，期望 %d", ip, info.ASN, i+1)
		}
	}
}

// 官方提供的是 .gz，用户手动放置时也多半是原样下载的那个文件。
func TestIPToASNLoadsGzipFile(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(tsvLines("1.1.1.0\t1.1.1.255\t13335\tAU\tCLOUDFLARENET")); err != nil {
		t.Fatalf("压缩失败：%v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("压缩失败：%v", err)
	}
	writeFile(t, dir, iptoASNv4Name+".gz", buf.Bytes())

	lookup, err := NewASNLookup(SourceIPToASN, dir)
	if err != nil {
		t.Fatalf("打开库失败：%v", err)
	}
	info, _ := lookup.Lookup(netip.MustParseAddr("1.1.1.1"))
	if info.ASN != 13335 {
		t.Fatalf("压缩库里查到 ASN %d，期望 13335", info.ASN)
	}
}

// 几十万行里混进几行异常不该让整库加载失败：那等于把 ASN 功能整个废掉。
func TestIPToASNSkipsMalformedLines(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, iptoASNv4Name, tsvLines(
		"# 这是注释",
		"",
		"not-an-ip\t1.0.0.255\t1\tCN\tX",
		"1.0.0.0\talso-not-an-ip\t1\tCN\tX",
		"1.0.0.0\t1.0.0.255\tnot-a-number\tCN\tX",
		"只有一列",
		"1.1.1.0\t1.1.1.255\t13335\tAU\tCLOUDFLARENET",
	))

	lookup, err := NewASNLookup(SourceIPToASN, dir)
	if err != nil {
		t.Fatalf("打开库失败：%v", err)
	}
	if lookup.Count() != 1 {
		t.Fatalf("区间条数 = %d，期望只留下 1 条有效记录", lookup.Count())
	}
	info, _ := lookup.Lookup(netip.MustParseAddr("1.1.1.1"))
	if info.ASN != 13335 {
		t.Fatalf("有效记录没被保留：%+v", info)
	}
}

// 未分配的地址段在库里写作 AS 号 0 / 国家码 None，这类区间占了整个库的
// 一成半，留着既占内存又会让查询命中一个没有意义的 AS。
func TestIPToASNSkipsUnassignedRanges(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, iptoASNv4Name, tsvLines(
		"1.0.0.0\t1.0.0.255\t13335\tAU\tCLOUDFLARENET",
		"1.0.1.0\t1.0.3.255\t0\tNone\tNot routed",
		"2.0.0.0\t2.0.0.255\t15169\tUS\tGOOGLE",
	))

	lookup, err := NewASNLookup(SourceIPToASN, dir)
	if err != nil {
		t.Fatalf("打开库失败：%v", err)
	}
	if lookup.Count() != 2 {
		t.Fatalf("区间条数 = %d，期望丢掉未分配段后剩 2 条", lookup.Count())
	}

	info, err := lookup.Lookup(netip.MustParseAddr("1.0.1.5"))
	if err != nil {
		t.Fatalf("查询失败：%v", err)
	}
	if info.ASN != 0 || info.Org != "" {
		t.Fatalf("未分配段不该查出内容：%+v", info)
	}
	// 丢掉之后这些地址落进区间空洞，相邻区间不能被误命中。
	info, _ = lookup.Lookup(netip.MustParseAddr("1.0.0.5"))
	if info.ASN != 13335 {
		t.Fatalf("相邻区间受到了影响：%+v", info)
	}
}

// 国家码只认两位字母：库里把「没有国家」写成字面量 None，当国家码用会在
// 界面上显示成「None」这个国家。
func TestNormalizeCountry(t *testing.T) {
	cases := map[string]string{
		"US":    "US",
		"cn":    "CN",
		" None": "",
		"None":  "",
		"":      "",
		"-":     "",
		"N/A":   "",
		"USA":   "",
		"U":     "",
		"12":    "",
	}
	for in, want := range cases {
		if got := normalizeCountry(in); got != want {
			t.Errorf("normalizeCountry(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// 组织名里的空格要原样保留。
func TestIPToASNPreservesOrgWithSpaces(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, iptoASNv4Name, tsvLines(
		"1.1.1.0\t1.1.1.255\t9808\tCN\tCHINA MOBILE COMMUNICATIONS",
	))

	lookup, err := NewASNLookup(SourceIPToASN, dir)
	if err != nil {
		t.Fatalf("打开库失败：%v", err)
	}
	info, _ := lookup.Lookup(netip.MustParseAddr("1.1.1.1"))
	if info.Org != "CHINA MOBILE COMMUNICATIONS" {
		t.Fatalf("组织名 = %q", info.Org)
	}
}

// 一个文件都没有时必须报错，由调用方降级为「不显示 ASN」。
func TestIPToASNWithoutLibraryFails(t *testing.T) {
	if _, err := NewASNLookup(SourceIPToASN, t.TempDir()); err == nil {
		t.Fatal("空目录应当报错")
	}
}

// 只有 v4 库时 v6 查询返回「查不到」而不是报错。
func TestIPToASNMissingFamilyIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, iptoASNv4Name, tsvLines("1.1.1.0\t1.1.1.255\t13335\tAU\tCLOUDFLARENET"))

	lookup, err := NewASNLookup(SourceIPToASN, dir)
	if err != nil {
		t.Fatalf("打开库失败：%v", err)
	}

	info, err := lookup.Lookup(netip.MustParseAddr("2606:4700::1"))
	if err != nil {
		t.Fatalf("缺少 v6 库时不该报错：%v", err)
	}
	if info.ASN != 0 {
		t.Fatalf("v6 查询查到了 ASN %d", info.ASN)
	}
}

// v4 与 v6 分开索引：同一份库里的两族地址各查各的。
func TestIPToASNHandlesBothFamilies(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, iptoASNv4Name, tsvLines("1.1.1.0\t1.1.1.255\t13335\tAU\tCF-V4"))
	writeFile(t, dir, iptoASNv6Name, tsvLines("2606:4700::\t2606:4700:ffff:ffff:ffff:ffff:ffff:ffff\t13335\tAU\tCF-V6"))

	lookup, err := NewASNLookup(SourceIPToASN, dir)
	if err != nil {
		t.Fatalf("打开库失败：%v", err)
	}

	v4, _ := lookup.Lookup(netip.MustParseAddr("1.1.1.1"))
	if v4.Org != "CF-V4" {
		t.Errorf("v4 组织名 = %q", v4.Org)
	}
	v6, _ := lookup.Lookup(netip.MustParseAddr("2606:4700::1111"))
	if v6.Org != "CF-V6" {
		t.Errorf("v6 组织名 = %q", v6.Org)
	}
	// v4-mapped 写法要按 v4 处理，不能因为写成 ::ffff: 就去查 v6 库。
	mapped, _ := lookup.Lookup(netip.MustParseAddr("::ffff:1.1.1.1"))
	if mapped.Org != "CF-V4" {
		t.Errorf("v4-mapped 地址的组织名 = %q，期望按 v4 查", mapped.Org)
	}
}

// 地址族由内容推断：指向一个文件名随意的文件也能用。
func TestParseAddrInfersFamily(t *testing.T) {
	if _, v6, ok := parseAddr("1.1.1.1"); !ok || v6 {
		t.Errorf("v4 地址判定为 v6=%v ok=%v", v6, ok)
	}
	if _, v6, ok := parseAddr("2606:4700::1"); !ok || !v6 {
		t.Errorf("v6 地址判定为 v6=%v ok=%v", v6, ok)
	}
	// v4-mapped 写法按 v4 处理。
	if _, v6, ok := parseAddr("::ffff:1.1.1.1"); !ok || v6 {
		t.Errorf("v4-mapped 地址判定为 v6=%v ok=%v", v6, ok)
	}
	if _, _, ok := parseAddr("不是地址"); ok {
		t.Error("非法地址不该被接受")
	}
	if _, _, ok := parseAddr(" 1.1.1.1 "); !ok {
		t.Error("带空白的地址应当被接受")
	}
}

// 库路径可以直接指向文件：用户手动放置时更可能这么用。
func TestIPToASNPathCanBeAFile(t *testing.T) {
	dir := t.TempDir()
	file := writeFile(t, dir, iptoASNv4Name, tsvLines("1.1.1.0\t1.1.1.255\t13335\tAU\tCLOUDFLARENET"))

	lookup, err := NewASNLookup(SourceIPToASN, file)
	if err != nil {
		t.Fatalf("直接用文件路径打开失败：%v", err)
	}
	info, _ := lookup.Lookup(netip.MustParseAddr("1.1.1.1"))
	if info.ASN != 13335 {
		t.Fatalf("ASN = %d，期望 13335", info.ASN)
	}
}

// 目录里只有自定义文件名的库文件时按约定找不到，报错而不是静默用不上；
// 但直接把路径指向那个文件是可以的（地址族由内容推断）。
func TestIPToASNCustomFileNameNeedsExplicitPath(t *testing.T) {
	dir := t.TempDir()
	file := writeFile(t, dir, "my-asn.tsv", tsvLines("1.1.1.0\t1.1.1.255\t13335\tAU\tCLOUDFLARENET"))

	if _, err := NewASNLookup(SourceIPToASN, dir); err == nil {
		t.Fatal("自定义文件名不在约定之内，只给目录时应当报错")
	}
	lookup, err := NewASNLookup(SourceIPToASN, file)
	if err != nil {
		t.Fatalf("直接指向自定义文件应当可用：%v", err)
	}
	info, _ := lookup.Lookup(netip.MustParseAddr("1.1.1.1"))
	if info.ASN != 13335 {
		t.Fatalf("ASN = %d，期望 13335", info.ASN)
	}
}

func TestNewASNLookupSourceOff(t *testing.T) {
	_, err := NewASNLookup(SourceOff, t.TempDir())
	if err == nil {
		t.Fatal("关掉查询时应当返回错误让调用方跳过")
	}
	if err != ErrSourceOff {
		t.Fatalf("错误 = %v，期望 ErrSourceOff（关掉是用户的选择，不该被当成故障）", err)
	}
}

func TestNewASNLookupUnknownSource(t *testing.T) {
	if _, err := NewASNLookup("magic", t.TempDir()); err == nil {
		t.Fatal("未知数据源应当报错")
	}
}

func TestAsFunc(t *testing.T) {
	if fn := AsFunc(nil); fn != nil {
		t.Fatal("空库应当返回 nil 函数，调用方据此跳过查询")
	}

	dir := t.TempDir()
	writeFile(t, dir, iptoASNv4Name, tsvLines("1.1.1.0\t1.1.1.255\t13335\tAU\tCLOUDFLARENET"))
	lookup, err := NewASNLookup(SourceIPToASN, dir)
	if err != nil {
		t.Fatalf("打开库失败：%v", err)
	}

	fn := AsFunc(lookup)
	if _, ok := fn(netip.MustParseAddr("1.1.1.1")); !ok {
		t.Error("命中的地址应当返回 true")
	}
	if _, ok := fn(netip.MustParseAddr("9.9.9.9")); ok {
		t.Error("查不到的地址应当返回 false")
	}
}

// 十万次查询必须在一秒内完成：这是「走索引而不是线性扫描」的性能底线。
func TestLookupPerformance(t *testing.T) {
	dir := t.TempDir()
	var rows []string
	const count = 20000
	for i := 0; i < count; i++ {
		base := i * 512
		rows = append(rows, fmt.Sprintf("%d.%d.%d.0\t%d.%d.%d.255\t%d\tCN\tORG",
			base>>24&0xff, base>>16&0xff, base>>8&0xff,
			base>>24&0xff, base>>16&0xff, base>>8&0xff, i+1))
	}
	writeFile(t, dir, iptoASNv4Name, tsvLines(rows...))

	lookup, err := NewASNLookup(SourceIPToASN, dir)
	if err != nil {
		t.Fatalf("打开库失败：%v", err)
	}

	const queries = 100000
	started := time.Now()
	for i := 0; i < queries; i++ {
		base := (i * 512) % (count * 512)
		ip := netip.AddrFrom4([4]byte{
			byte(base >> 24), byte(base >> 16), byte(base >> 8), byte(base + 7),
		})
		if _, err := lookup.Lookup(ip); err != nil {
			t.Fatalf("查询失败：%v", err)
		}
	}
	elapsed := time.Since(started)
	if elapsed > time.Second {
		t.Fatalf("%d 次查询耗时 %v，超过 1 秒", queries, elapsed)
	}
	t.Logf("%d 次查询耗时 %v", queries, elapsed)
}

// ---------------------------------------------------------------------------
// mmdb 数据源
// ---------------------------------------------------------------------------

// 库文件不存在时打开失败，由调用方降级。
func TestOpenMMDBMissingFile(t *testing.T) {
	if _, err := NewASNLookup(SourceMMDB, t.TempDir()); err == nil {
		t.Fatal("库文件不存在时应当报错")
	}
}

// 文件不是 mmdb 时打开失败，而不是留一个半坏的 reader。
func TestOpenMMDBRejectsNonDatabase(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, mmdbName, []byte("这不是一个 mmdb 文件"))

	if _, err := NewASNLookup(SourceMMDB, dir); err == nil {
		t.Fatal("非法文件应当报错")
	}
}

// 真实的 mmdb 库需要一份几十兆的文件，不适合放进仓库。
// 需要验证时可以指向一个真实文件：CLOUDTRACE_ASN_MMDB=/path/to/GeoLite2-ASN.mmdb
func TestMMDBWithRealLibrary(t *testing.T) {
	path := os.Getenv("CLOUDTRACE_ASN_MMDB")
	if path == "" {
		t.Skip("未设置 CLOUDTRACE_ASN_MMDB，跳过真实库验证")
	}

	lookup, err := NewASNLookup(SourceMMDB, path)
	if err != nil {
		t.Fatalf("打开库失败：%v", err)
	}
	defer func() { _ = lookup.Close() }()

	info, err := lookup.Lookup(netip.MustParseAddr("1.1.1.1"))
	if err != nil {
		t.Fatalf("查询失败：%v", err)
	}
	if info.ASN != 13335 {
		t.Errorf("1.1.1.1 的 ASN = %d，期望 13335", info.ASN)
	}
	if !strings.Contains(strings.ToUpper(info.Org), "CLOUDFLARE") {
		t.Errorf("1.1.1.1 的组织名 = %q，期望包含 CLOUDFLARE", info.Org)
	}

	// mmdb 不提供记录条数，返回 0 表示未知。
	if lookup.Count() != 0 {
		t.Errorf("mmdb 的条数 = %d，期望 0（表示未知）", lookup.Count())
	}
	// 查不到也要是「零值 + 无错误」。
	miss, err := lookup.Lookup(netip.MustParseAddr("240.0.0.1"))
	if err != nil {
		t.Fatalf("查不到不该报错：%v", err)
	}
	if miss.ASN != 0 {
		t.Errorf("保留地址查到了 ASN %d", miss.ASN)
	}
	// 关闭之后不能再查。
	if err := lookup.Close(); err != nil {
		t.Fatalf("关闭失败：%v", err)
	}
}

func TestResolveDBPath(t *testing.T) {
	dir := t.TempDir()
	file := writeFile(t, dir, mmdbName, []byte("x"))

	if got := resolveDBPath(dir, mmdbName); got != filepath.Join(dir, mmdbName) {
		t.Errorf("目录输入 = %q", got)
	}
	if got := resolveDBPath(file, mmdbName); got != file {
		t.Errorf("文件输入 = %q，期望原样使用", got)
	}
	if got := resolveDBPath("", mmdbName); got != mmdbName {
		t.Errorf("空输入 = %q", got)
	}
	// 还不存在的目录按目录处理。
	if got := resolveDBPath(filepath.Join(dir, "nope"), mmdbName); got != filepath.Join(dir, "nope", mmdbName) {
		t.Errorf("不存在的目录 = %q", got)
	}
}

func TestDirOf(t *testing.T) {
	dir := t.TempDir()
	file := writeFile(t, dir, "a.tsv", []byte("x"))

	if got := dirOf(dir); got != dir {
		t.Errorf("目录输入 = %q", got)
	}
	if got := dirOf(file); got != dir {
		t.Errorf("文件输入 = %q，期望 %q", got, dir)
	}
	if got := dirOf(""); got != "." {
		t.Errorf("空输入 = %q，期望 .", got)
	}
	if got := dirOf(filepath.Join(dir, "missing")); got != filepath.Join(dir, "missing") {
		t.Errorf("不存在的无扩展名路径 = %q", got)
	}
}

package geo

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ipToASN 是 iptoasn.com 的 TSV 库。
//
// 库文件按地址族分成两份，各自建索引：v4 与 v6 的地址空间没有可比性，
// 混在一个有序数组里排序结果没有意义。
type ipToASN struct {
	v4 *asnRanges
	v6 *asnRanges
}

// openIPToASN 打开 iptoasn 的 TSV 库。
//
// path 可以直接是一个文件，也可以是一个目录。指向文件时地址族由内容推断，
// 因此文件名随便叫什么都行；指向目录时按约定的两个文件名找，同时接受
// gzip 压缩的原件——官方提供的就是 .gz。
func openIPToASN(path string) (*ipToASN, error) {
	files := libraryFiles(path)
	if len(files) == 0 {
		return nil, fmt.Errorf("在 %s 里找不到 %s 或 %s", dirOf(path), iptoASNv4Name, iptoASNv6Name)
	}

	var all []rawRange
	for _, file := range files {
		raw, err := readTSV(file)
		if err != nil {
			return nil, fmt.Errorf("加载 %s 失败：%w", filepath.Base(file), err)
		}
		all = append(all, raw...)
	}
	if len(all) == 0 {
		return nil, errors.New("库文件里没有可用的区间记录")
	}

	lookup := &ipToASN{}
	lookup.v4, lookup.v6 = buildIndex(all)
	return lookup, nil
}

// libraryFiles 决定要读哪几个文件。
func libraryFiles(path string) []string {
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return []string{path}
	}

	dir := dirOf(path)
	var out []string
	for _, name := range []string{iptoASNv4Name, iptoASNv6Name} {
		if file, ok := findLibrary(dir, name); ok {
			out = append(out, file)
		}
	}
	return out
}

// findLibrary 在目录里找库文件，同时接受原名与 .gz 后缀。
func findLibrary(dir, name string) (string, bool) {
	for _, candidate := range []string{name, name + ".gz"} {
		file := filepath.Join(dir, candidate)
		if info, err := os.Stat(file); err == nil && !info.IsDir() {
			return file, true
		}
	}
	return "", false
}

// Lookup 查询一个地址。
func (l *ipToASN) Lookup(ip netip.Addr) (ASNInfo, error) {
	addr := ip
	if addr.Is4In6() {
		addr = addr.Unmap()
	}

	table := l.v4
	if !addr.Is4() {
		table = l.v6
	}
	if table == nil {
		// 只有另一族的库：这一族就是查不到，不是故障。
		return ASNInfo{}, nil
	}
	return table.find(addr), nil
}

// Count 返回两族索引里的区间总条数。
func (l *ipToASN) Count() int { return l.v4.count() + l.v6.count() }

// Close 对纯内存索引而言无事可做。
func (l *ipToASN) Close() error { return nil }

// asnRanges 是一个地址族的区间索引。
//
// 地址压成 16 字节定长数组而不是 netip.Addr：库里几十万条区间，netip.Addr
// 每条 24 字节还要带一个 zone 指针，光地址数组就要多占十几兆；定长数组的
// 比较还能直接用 bytes.Compare，二分查找不必绕一层方法调用。
type asnRanges struct {
	start   [][16]byte
	end     [][16]byte
	info    []uint32
	entries []ASNInfo
}

func (r *asnRanges) count() int {
	if r == nil {
		return 0
	}
	return len(r.start)
}

// find 二分查找覆盖该地址的区间。
//
// 先找最后一个起始地址不大于目标的区间，再看目标是否落在它的结束地址之内
// ——区间之间有空洞（未分配地址段），落在空洞里就是查不到。
func (r *asnRanges) find(addr netip.Addr) ASNInfo {
	if r == nil || len(r.start) == 0 {
		return ASNInfo{}
	}
	key := addr.As16()
	i := sort.Search(len(r.start), func(i int) bool {
		return bytes.Compare(r.start[i][:], key[:]) > 0
	}) - 1
	if i < 0 || bytes.Compare(key[:], r.end[i][:]) > 0 {
		return ASNInfo{}
	}
	return r.entries[r.info[i]]
}

// readTSV 读取并解析一个 TSV 库文件。
func readTSV(path string) ([]rawRange, error) {
	file, err := openMaybeGzip(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return parseTSV(file)
}

// parseBytes 从内存里的内容解析区间，同样按魔数识别 gzip。
//
// 下载回来的内容要先解析一遍再落盘，避免把一个坏文件写到库文件位置上。
func parseBytes(data []byte) ([]rawRange, error) {
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer func() { _ = zr.Close() }()
		return parseTSV(zr)
	}
	return parseTSV(bytes.NewReader(data))
}

// rawRange 是解析出来的原始区间。
type rawRange struct {
	start   [16]byte
	end     [16]byte
	asn     uint32
	country string
	org     string
	v6      bool
}

// parseTSV 解析 iptoasn 的 TSV。
//
// 每行五列：起始地址、结束地址、AS 号、国家码、AS 描述。只按制表符切列，
// 描述里的空格原样保留。认不出的行直接跳过而不是报错：几十万行里混进几行
// 异常不该让整库加载失败，那等于把 ASN 功能整个废掉。
func parseTSV(r io.Reader) ([]rawRange, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<20)

	out := make([]rawRange, 0, 1<<18)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), "\t")
		if len(fields) < 3 {
			continue
		}

		start, v6, ok := parseAddr(fields[0])
		if !ok {
			continue
		}
		end, endV6, ok := parseAddr(fields[1])
		if !ok || endV6 != v6 {
			continue
		}
		asn, err := strconv.ParseUint(strings.TrimSpace(fields[2]), 10, 32)
		if err != nil {
			continue
		}
		// AS 号 0 是保留值，库里用它表示「未分配地址段」（组织名写作
		// Not routed）。这种区间占了整个库的一成半，留着既占内存又会让
		// 查询命中一个没有意义的 AS，直接丢掉：丢掉之后这些地址落进
		// 区间空洞，查询结果一样是「查不到」。
		if asn == 0 {
			continue
		}

		item := rawRange{start: start, end: end, asn: uint32(asn), v6: v6}
		if len(fields) > 3 {
			item.country = normalizeCountry(fields[3])
		}
		if len(fields) > 4 {
			item.org = strings.TrimSpace(strings.Join(fields[4:], " "))
		}
		out = append(out, item)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// parseAddr 解析一个地址并报告它属于哪个地址族。
func parseAddr(text string) ([16]byte, bool, bool) {
	addr, err := netip.ParseAddr(strings.TrimSpace(text))
	if err != nil {
		return [16]byte{}, false, false
	}
	if addr.Is4In6() {
		addr = addr.Unmap()
	}
	return addr.As16(), !addr.Is4(), true
}

// normalizeCountry 归一化国家码。
//
// 未分配的地址段在库里把国家码写成字面量 "None"，直接当国家码用就会在
// 界面上显示成「None」这个国家。只认两位字母，其余一律当作没有。
func normalizeCountry(raw string) string {
	code := strings.ToUpper(strings.TrimSpace(raw))
	if len(code) != 2 || !isASCIILetter(code[0]) || !isASCIILetter(code[1]) {
		return ""
	}
	return code
}

func isASCIILetter(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

// buildIndex 把原始区间按地址族分桶并各自建索引。
func buildIndex(raw []rawRange) (v4, v6 *asnRanges) {
	var four, six []rawRange
	for _, item := range raw {
		if item.v6 {
			six = append(six, item)
			continue
		}
		four = append(four, item)
	}
	return buildRanges(four), buildRanges(six)
}

// buildRanges 把原始区间整理成可二分的索引。
//
// 自己排序而不是依赖文件已经有序：库文件是外部生成的，格式说明里并没有
// 承诺顺序，一旦上游改了生成方式，二分查找就会静默给出错误答案。
func buildRanges(raw []rawRange) *asnRanges {
	if len(raw) == 0 {
		return nil
	}
	sort.Slice(raw, func(i, j int) bool {
		return bytes.Compare(raw[i].start[:], raw[j].start[:]) < 0
	})

	out := &asnRanges{
		start: make([][16]byte, len(raw)),
		end:   make([][16]byte, len(raw)),
		info:  make([]uint32, len(raw)),
	}
	// 组织名与国家码在库里重复度极高（一个 AS 往往有几十段地址），去重之后
	// 索引能小一大截。
	seen := make(map[ASNInfo]uint32, 1<<16)
	out.entries = make([]ASNInfo, 0, 1<<16)

	for i, item := range raw {
		out.start[i] = item.start
		out.end[i] = item.end

		info := ASNInfo{ASN: item.asn, Org: item.org, Country: item.country}
		idx, ok := seen[info]
		if !ok {
			idx = uint32(len(out.entries))
			out.entries = append(out.entries, info)
			seen[info] = idx
		}
		out.info[i] = idx
	}
	return out
}

// maybeGzip 读取「可能是 gzip」的文本文件。
//
// 魔数判断要在读之前做，因此底层套一层带缓冲的读取器；解压与不解压两条
// 路径共用同一个缓冲器，不会把已经读掉的两字节弄丢。
type maybeGzip struct {
	file *os.File
	buf  *bufio.Reader
	gz   *gzip.Reader
}

func openMaybeGzip(path string) (*maybeGzip, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	m := &maybeGzip{file: f, buf: bufio.NewReaderSize(f, 1<<16)}

	if magic, err := m.buf.Peek(2); err == nil && magic[0] == 0x1f && magic[1] == 0x8b {
		zr, err := gzip.NewReader(m.buf)
		if err != nil {
			_ = f.Close()
			return nil, err
		}
		m.gz = zr
	}
	return m, nil
}

func (m *maybeGzip) Read(p []byte) (int, error) {
	if m.gz != nil {
		return m.gz.Read(p)
	}
	return m.buf.Read(p)
}

func (m *maybeGzip) Close() error {
	if m.gz != nil {
		_ = m.gz.Close()
	}
	return m.file.Close()
}

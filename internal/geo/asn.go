package geo

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
)

// 数据源标识，与配置里的 geo.asn_source 取值一致。
const (
	SourceIPToASN = "iptoasn"
	SourceMMDB    = "geolite2_mmdb"
	SourceOff     = "off"
)

// 库文件名。用户可以把文件直接放进 geo.asn_db_path 指向的目录。
//
// iptoasn 的 v4 与 v6 是两个文件，两个都认：只有一个时另一个族查不到，
// 不影响已经有的那一半。
const (
	iptoASNv4Name = "ip2asn-v4.tsv"
	iptoASNv6Name = "ip2asn-v6.tsv"
	mmdbName      = "GeoLite2-ASN.mmdb"
)

// ASNInfo 是一次 ASN 查询的结果。
type ASNInfo struct {
	// ASN 是自治系统号；0 表示没查到。
	ASN uint32 `json:"asn"`
	// Org 是自治系统所属组织，通常就是运营商名字。
	Org string `json:"org"`
	// Country 是库文件里标注的国家码，可能为空。
	Country string `json:"country,omitempty"`
}

// LookupFunc 是 ASN 查询的最小形态。
//
// 上层按函数注入而不是直接持有接口：扫描与测速的测试只需要一个假函数，
// 不必构造一个真的库。
type LookupFunc func(ip netip.Addr) (ASNInfo, bool)

// ASNLookup 是 ASN 库的统一查询接口。
type ASNLookup interface {
	// Lookup 查询一个地址。
	//
	// 查不到返回零值 ASNInfo 与 nil 错误——「这个 IP 不在库里」是正常结果，
	// 不是故障。错误只表示库本身不可用。
	Lookup(ip netip.Addr) (ASNInfo, error)
	// Count 返回索引里的区间条数，供设置页展示「记录条数」。
	Count() int
	// Close 释放库占用的资源。
	Close() error
}

// ErrSourceOff 表示用户把 ASN 查询关掉了。
//
// 单独一个哨兵是为了让调用方把它与「库坏了」区分开：关掉是用户的选择，
// 不该记日志、不该提示。
var ErrSourceOff = errors.New("ASN 查询已关闭")

// NewASNLookup 按数据源打开本地库。
//
// path 既可以是目录也可以是文件：默认值指向数据目录下的 cache/asn，而用户
// 手动放置时更可能直接指向那个文件，两种都认。目录里按数据源的约定文件名
// 找，一个都没有就报错，由调用方降级为「不显示 ASN」。
func NewASNLookup(source, path string) (ASNLookup, error) {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case SourceOff:
		return nil, ErrSourceOff
	case SourceMMDB:
		return openMMDB(resolveDBPath(path, mmdbName))
	case "", SourceIPToASN:
		return openIPToASN(path)
	default:
		return nil, fmt.Errorf("未知的 ASN 数据源 %q", source)
	}
}

// AsFunc 把查询接口包成函数形式，便于按依赖注入。
//
// 查不到与出错都返回 false：对调用方来说两者的处置完全一样——不显示 ASN，
// 继续跑。
func AsFunc(l ASNLookup) LookupFunc {
	if l == nil {
		return nil
	}
	return func(ip netip.Addr) (ASNInfo, bool) {
		info, err := l.Lookup(ip)
		if err != nil || info.ASN == 0 {
			return ASNInfo{}, false
		}
		return info, true
	}
}

// resolveDBPath 在目录里按约定文件名定位库文件。
//
// path 直接是一个文件时原样使用：用户可能把库放在别处，或者在同一个目录里
// 放了好几个版本的库。
func resolveDBPath(path, name string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return name
	}
	if info, err := os.Stat(p); err == nil && !info.IsDir() {
		return p
	}
	return filepath.Join(p, name)
}

// dirOf 返回库路径所在的目录。
func dirOf(path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return "."
	}
	if info, err := os.Stat(p); err == nil && info.IsDir() {
		return p
	}
	if filepath.Ext(p) == "" {
		// 还不存在的目录：按目录处理。
		return p
	}
	return filepath.Dir(p)
}

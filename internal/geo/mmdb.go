package geo

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"

	"github.com/oschwald/maxminddb-golang"
)

// mmdbRecord 是 GeoLite2-ASN 库里的一条记录。
type mmdbRecord struct {
	ASN uint32 `maxminddb:"autonomous_system_number"`
	Org string `maxminddb:"autonomous_system_organization"`
}

// mmdbLookup 是 GeoLite2-ASN.mmdb 数据源。
//
// 与 TSV 源的取舍：mmdb 查询更快、格式标准，但体积大得多；TSV 免账号、
// 体积小，代价是加载时要自己建索引。默认给 TSV，需要更快查询时可以切过来。
type mmdbLookup struct {
	reader *maxminddb.Reader
}

// openMMDB 打开 mmdb 库。
func openMMDB(path string) (*mmdbLookup, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	reader, err := maxminddb.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开 mmdb 库失败：%w", err)
	}
	return &mmdbLookup{reader: reader}, nil
}

// Lookup 查询一个地址。
//
// 库里没有这个地址时 maxminddb 返回零值记录而不报错，因此这里按「查不到」
// 处理；真正的解码错误同样归为查不到——一个 IP 查不出来不该让整批结果出错。
func (l *mmdbLookup) Lookup(ip netip.Addr) (ASNInfo, error) {
	addr := ip
	if addr.Is4In6() {
		addr = addr.Unmap()
	}
	if !addr.IsValid() {
		return ASNInfo{}, nil
	}

	var rec mmdbRecord
	if err := l.reader.Lookup(net.IP(addr.AsSlice()), &rec); err != nil {
		return ASNInfo{}, nil
	}
	if rec.ASN == 0 {
		return ASNInfo{}, nil
	}
	return ASNInfo{ASN: rec.ASN, Org: strings.TrimSpace(rec.Org)}, nil
}

// Count 返回 0：mmdb 格式只暴露搜索树的节点数，没有「记录条数」这个概念，
// 拿节点数冒充记录数会让设置页显示一个解释不通的数字。
func (l *mmdbLookup) Count() int { return 0 }

// Close 释放库文件映射。
func (l *mmdbLookup) Close() error {
	if l.reader == nil {
		return nil
	}
	return l.reader.Close()
}

package geo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"cloudtrace/internal/atomicfile"
)

// 库的下载地址。
//
// GeoLite2 走镜像：官方源需要 MaxMind 账号，镜像免账号且与参考实现一致。
// 镜像路径里的 GeoLiet2 是上游仓库的真实名字，不是笔误。
const (
	iptoASNv4URL = "https://iptoasn.com/data/ip2asn-v4.tsv.gz"
	iptoASNv6URL = "https://iptoasn.com/data/ip2asn-v6.tsv.gz"
	mmdbURL      = "https://jsd.onmicrosoft.cn/gh/seketiti/GeoLiet2@release/GeoLite2-ASN.mmdb"
)

// 下载参数。
const (
	// downloadTimeout 是单个文件的下载超时。
	downloadTimeout = 5 * time.Minute
	// maxDownloadBytes 是单个库文件的大小上限。
	//
	// 真实文件是几兆到十几兆，设上限是为了挡住「地址配错、拿回来一个几百兆
	// 的东西」这种情况——那会把内存吃光。
	maxDownloadBytes = 128 << 20
)

// Fetcher 拉取一个地址的完整内容。
//
// 单独抽成函数类型是为了让下载流程可测：测试里换成返回固定字节的假实现，
// 不必碰网络。
type Fetcher func(ctx context.Context, url string) ([]byte, error)

// httpFetcher 是默认的 HTTP 拉取实现。
func httpFetcher(timeout time.Duration) Fetcher {
	client := &http.Client{Timeout: timeout}
	return func(ctx context.Context, url string) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s 返回状态码 %d", url, resp.StatusCode)
		}
		// 多读一个字节：读满了上限就说明还有更多内容，直接判为超限，
		// 而不是静默截断成一个坏文件。
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadBytes+1))
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > maxDownloadBytes {
			return nil, fmt.Errorf("%s 的响应超过 %d 字节上限", url, maxDownloadBytes)
		}
		return data, nil
	}
}

// validateTSV 校验一份下载回来的 TSV。
//
// 必须真的解压并解析一遍：地址配错时拿回来的多半是一个 HTML 错误页，
// 只检查长度或前缀都拦不住，而一旦写进库文件位置，下一次启动就再也读不出
// 库了。解析过程本身也能发现截断——gzip 流被截断时读取会报错。
func validateTSV(data []byte) error {
	raw, err := parseBytes(data)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return errors.New("没有解析出任何区间记录")
	}
	return nil
}

// validateMMDB 校验一份下载回来的 mmdb。
func validateMMDB(data []byte) error {
	lookup, err := openMMDBBytes(data)
	if err != nil {
		return err
	}
	return lookup.Close()
}

// writeLibrary 把内容原子写入库文件位置。
//
// 先落到同目录的临时文件再改名：写到一半断电、被强杀、磁盘满，都不会在库
// 文件位置留下半截内容。
func writeLibrary(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return atomicfile.Write(path, data, 0o644)
}

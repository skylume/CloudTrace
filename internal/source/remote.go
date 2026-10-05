package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"cloudtrace/internal/model"
	"cloudtrace/internal/netx"
	"cloudtrace/internal/probe"
)

// maxRemoteBody 是单个远程源响应体的读取上限，防止异常源把内存吃满。
const maxRemoteBody = 8 << 20

// defaultRemoteTimeout 是未指定超时时的取值。
const defaultRemoteTimeout = 15 * time.Second

// MergeStrategy 决定多个源的结果怎么合并。
type MergeStrategy string

const (
	// MergeUnion 取并集：任一个源里出现过的节点都留下。默认。
	MergeUnion MergeStrategy = "union"
	// MergeIntersect 取交集：只在所有成功的源里都出现的节点才留下。
	//
	// 用在「宁可少而准」的场合：多个源各自维护一份列表时，同时出现在全部
	// 源里的那些才是它们都认可的。
	MergeIntersect MergeStrategy = "intersect"
)

// RemoteOptions 控制远程源的拉取行为。
type RemoteOptions struct {
	Timeout  time.Duration // 单个地址的超时（含重试）
	Retries  int           // 失败后的重试次数，不含首次
	Interval time.Duration // 重试间隔
	Merge    MergeStrategy // 多源合并方式；空值按并集
	// Dialer 接管域名解析；为 nil 时走系统解析。
	//
	// 远程源是这套程序里少数几个要解析域名的地方（其余全是 IP 直连），
	// 因此 net.custom_dns 主要作用在这里。
	Dialer netx.Dialer
	Client *http.Client // 可注入；为空时使用禁代理的默认客户端
}

// outcome 是单个源的拉取结果。
type outcome struct {
	records []model.IPRecord
	err     error
}

// RemoteFailure 记录一个拉取失败的地址。
type RemoteFailure struct {
	URL   string
	Error string
}

// RemoteResult 是多地址拉取的汇总。
//
// 部分成功是常态：某个源挂了不该让整次扫描失败，所以成功的结果与
// 失败的清单一起返回，由调用方在界面上标注「远程源失败 N 个」。
type RemoteResult struct {
	Records []model.IPRecord
	Failed  []RemoteFailure
}

// FetchRemote 并发拉取多个远程源并自适应解析。
//
// 各地址互相独立：一个失败不影响其他。返回结果里 Records 按传入顺序拼接，
// 保证同一组输入得到同一组输出。
func FetchRemote(ctx context.Context, urls []string, opts RemoteOptions) (RemoteResult, error) {
	if len(urls) == 0 {
		return RemoteResult{}, errors.New("没有配置远程源地址")
	}
	client := opts.Client
	if client == nil {
		client = newRemoteClient(opts.Timeout, opts.Dialer)
	}

	results := make([]outcome, len(urls))

	var wg sync.WaitGroup
	for i, rawURL := range urls {
		wg.Add(1)
		go func(index int, target string) {
			defer wg.Done()
			// 单个源出问题不能带崩整次拉取。
			defer func() {
				if r := recover(); r != nil {
					results[index] = outcome{err: fmt.Errorf("远程源处理异常：%v", r)}
				}
			}()
			records, err := fetchOne(ctx, client, target, opts)
			results[index] = outcome{records: records, err: err}
		}(i, rawURL)
	}
	wg.Wait()

	var out RemoteResult
	for i, rawURL := range urls {
		if results[i].err != nil {
			out.Failed = append(out.Failed, RemoteFailure{URL: rawURL, Error: results[i].err.Error()})
			continue
		}
		out.Records = append(out.Records, results[i].records...)
	}

	if opts.Merge == MergeIntersect {
		out.Records = intersect(results)
	}
	return out, nil
}

// intersect 取各源结果的交集。
//
// 只统计**成功**的源：一路拉挂了不该让交集变成空集——那不是「各源都认可」，
// 那是「有一路没通」。一个源都没成功时返回空，由调用方按「全部失败」处理。
//
// 顺序沿用第一个成功源里的顺序，保证同一组输入得到同一组输出。
func intersect(results []outcome) []model.IPRecord {
	// 逐个源求交：每处理一个源就把上一轮的候选里不属于它的删掉。
	var kept map[string]bool
	var first []model.IPRecord
	sources := 0

	for i := range results {
		if results[i].err != nil {
			continue
		}
		sources++

		present := make(map[string]bool, len(results[i].records))
		for _, rec := range results[i].records {
			present[recordKey(rec)] = true
		}

		if kept == nil {
			kept = present
			first = results[i].records
			continue
		}
		for key := range kept {
			if !present[key] {
				delete(kept, key)
			}
		}
	}

	if sources == 0 {
		return nil
	}

	// 按第一个成功源的顺序输出。kept 只会从这个集合里删，因此它的每个成员
	// 都在 first 里——不需要为「交集里有 first 之外的节点」做兜底，那种情况
	// 构造上就不存在。
	out := make([]model.IPRecord, 0, len(kept))
	seen := make(map[string]bool, len(kept))
	for _, rec := range first {
		key := recordKey(rec)
		if !kept[key] || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, rec)
	}
	return out
}

// recordKey 是跨源比对节点身份用的键。
//
// 用地址加端口而不是整条记录：不同源给的地区、延迟字段天然不同，按整条记录
// 比对会让交集永远是空的。
func recordKey(rec model.IPRecord) string {
	return rec.IP + ":" + strconv.Itoa(rec.Port)
}

// fetchOne 拉取单个地址，失败按配置重试。
func fetchOne(ctx context.Context, client *http.Client, rawURL string, opts RemoteOptions) ([]model.IPRecord, error) {
	attempts := opts.Retries + 1
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	for i := 0; i < attempts; i++ {
		if i > 0 && opts.Interval > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(opts.Interval):
			}
		}
		body, err := fetchBody(ctx, client, rawURL)
		if err == nil {
			return ParseRemotePayload(body), nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// fetchBody 发起一次 GET 并读出响应体。
func fetchBody(ctx context.Context, client *http.Client, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("地址无法解析：%w", err)
	}
	req.Header.Set("User-Agent", probe.ChromeUA)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败：%w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRemoteBody))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败：%w", err)
	}
	return body, nil
}

// newRemoteClient 返回远程源用的 HTTP 客户端。
//
// 禁用代理：远程源是公网地址，走用户环境里的代理会拿到与本机网络无关的
// 内容，还可能把代理自身的限流算到源头上。
func newRemoteClient(timeout time.Duration, dial netx.Dialer) *http.Client {
	if timeout <= 0 {
		timeout = defaultRemoteTimeout
	}
	transport := &http.Transport{
		Proxy:               nil,
		MaxIdleConns:        8,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: timeout,
	}
	if dial != nil {
		transport.DialContext = dial
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}

// ParseRemotePayload 自适应解析任意文本或 JSON 载荷。
//
// 先按 JSON 试，解析不出节点再按纯文本逐行提取。远程源的格式五花八门，
// 与其要求用户填对格式，不如把常见的几种都认下来。
func ParseRemotePayload(body []byte) []model.IPRecord {
	if records := parseJSONNodes(body); len(records) > 0 {
		return records
	}
	return parseTextNodes(string(body))
}

// nodeContainerKeys 是 JSON 里可能承载节点数组的字段名。
//
// 只在命中这几个字段时才继续下钻：否则会把配置项、统计数字之类的
// 对象也当成节点收进来。
func nodeContainerKeys() []string {
	return []string{"nodes", "data", "result", "list"}
}

// parseJSONNodes 递归提取 JSON 里的节点。
func parseJSONNodes(body []byte) []model.IPRecord {
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil
	}
	var out []model.IPRecord
	collectNodes(root, &out)
	return out
}

// collectNodes 递归下钻 JSON 结构。
func collectNodes(value any, out *[]model.IPRecord) {
	switch node := value.(type) {
	case []any:
		for _, item := range node {
			collectNodes(item, out)
		}
	case map[string]any:
		for _, key := range nodeContainerKeys() {
			if child, ok := node[key]; ok {
				collectNodes(child, out)
			}
		}
		if record, ok := nodeFromMap(node); ok {
			*out = append(*out, record)
		}
	case string:
		for _, record := range parseTextNodes(node) {
			*out = append(*out, record)
		}
	}
}

// nodeFromMap 从 JSON 对象里取一个节点。
func nodeFromMap(node map[string]any) (model.IPRecord, bool) {
	host := firstString(node, "ip", "host")
	if host == "" {
		return model.IPRecord{}, false
	}
	record := model.IPRecord{IP: host}
	if port := firstInt(node, "port"); port > 0 && port <= 65535 {
		record.Port = port
	}
	record.Loc = NormalizeCountry(firstString(node, "country", "cc"))
	return record, true
}

// firstString 返回第一个存在且非空的字符串字段。
func firstString(node map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := node[key]
		if !ok {
			continue
		}
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

// firstInt 返回第一个存在且能转成整数的字段。
//
// JSON 里的数字统一解成 float64，但远程源也常把端口写成字符串，
// 两种都要认。
func firstInt(node map[string]any, keys ...string) int {
	for _, key := range keys {
		value, ok := node[key]
		if !ok {
			continue
		}
		switch v := value.(type) {
		case float64:
			return int(v)
		case json.Number:
			if n, err := v.Int64(); err == nil {
				return int(n)
			}
		case string:
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				return n
			}
		}
	}
	return 0
}

// parseTextNodes 按行提取节点。
func parseTextNodes(text string) []model.IPRecord {
	var out []model.IPRecord
	for _, line := range strings.Split(text, "\n") {
		if record, ok := parseNodeLine(line); ok {
			out = append(out, record)
		}
	}
	return out
}

// parseNodeLine 解析一行节点文本。
//
// 支持两种写法：地址与地区标签用 # 分隔（形如 "1.2.3.4:443#US"），
// 或者用空白分隔（形如 "1.2.3.4:443 香港"）。
func parseNodeLine(line string) (model.IPRecord, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
		return model.IPRecord{}, false
	}

	endpoint := line
	label := ""
	if i := strings.IndexByte(line, '#'); i >= 0 {
		endpoint, label = line[:i], line[i+1:]
	} else if fields := strings.Fields(line); len(fields) > 1 {
		endpoint, label = fields[0], strings.Join(fields[1:], " ")
	}

	entry, ok := parseToken(strings.TrimSpace(endpoint))
	if !ok || (entry.Kind != KindIP && entry.Kind != KindHost) {
		return model.IPRecord{}, false
	}
	return model.IPRecord{
		IP:   entry.Value,
		Port: entry.Port,
		Loc:  NormalizeCountry(label),
	}, true
}

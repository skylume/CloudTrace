package diag

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"cloudtrace/internal/config"
)

// 诊断包里最多带多少行日志。
//
// 带全量日志没有意义：出问题时看的是最后那几十行，而几天的日志会让诊断包从
// 「能贴进聊天窗口」变成「得传文件」。
const maxLogLines = 300

// 脱敏后的占位。
const redacted = "***"

// Bundle 组装诊断包。
//
// 输出**纯文本**而不是 zip：用户拿到它的下一步几乎一定是「贴进 issue 或聊天
// 窗口」，纯文本可以直接粘，zip 还得先解压再挑文件。也就不需要压缩依赖。
//
// 版本与数据目录由调用方给：这个包不读全局状态，凡是能问出来的都问出来。
func Bundle(cfg config.Config, dataDir, version string, report *Report, now time.Time) string {
	var b strings.Builder

	writeHeader(&b, version, dataDir, now)
	writeReport(&b, report)
	writeConfig(&b, cfg)
	writeLogs(&b, dataDir, now)

	return b.String()
}

// BundleName 返回诊断包的文件名。
func BundleName(now time.Time) string {
	return "cloudtrace-diag-" + now.Format("20060102-150405") + ".txt"
}

func writeHeader(b *strings.Builder, version, dataDir string, now time.Time) {
	b.WriteString("CloudTrace 诊断包\n")
	b.WriteString(strings.Repeat("=", 48) + "\n")
	fmt.Fprintf(b, "生成时间  %s\n", now.Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(b, "版本      %s\n", orDash(version))
	fmt.Fprintf(b, "系统      %s/%s  Go %s\n", runtime.GOOS, runtime.GOARCH, runtime.Version())
	fmt.Fprintf(b, "数据目录  %s\n", orDash(dataDir))
	b.WriteString("\n")
}

func writeReport(b *strings.Builder, report *Report) {
	b.WriteString("网络诊断\n")
	b.WriteString(strings.Repeat("-", 48) + "\n")
	if report == nil {
		b.WriteString("（本次没有跑诊断。在设置页点一次「开始诊断」再导出会更有用）\n\n")
		return
	}

	fmt.Fprintf(b, "总体结论  %s\n", report.Status)
	fmt.Fprintf(b, "耗时      %dms\n\n", report.ElapsedMS)

	for _, item := range report.Items {
		fmt.Fprintf(b, "[%s] %s\n", item.Status, item.Key)
		fmt.Fprintf(b, "    %s\n", item.Summary)
		if item.Detail != "" {
			fmt.Fprintf(b, "    原始  %s\n", item.Detail)
		}
		if item.Advice != "" {
			fmt.Fprintf(b, "    建议  %s\n", item.Advice)
		}
	}
	b.WriteString("\n")
}

func writeConfig(b *strings.Builder, cfg config.Config) {
	b.WriteString("配置（已脱敏）\n")
	b.WriteString(strings.Repeat("-", 48) + "\n")

	raw, err := json.Marshal(cfg)
	if err != nil {
		// 配置序列化失败本身就是值得反馈的信息，但不能因此让整个包空掉。
		fmt.Fprintf(b, "（配置读取失败：%v）\n\n", err)
		return
	}

	var values map[string]any
	if err := json.Unmarshal(raw, &values); err != nil {
		fmt.Fprintf(b, "（配置解析失败：%v）\n\n", err)
		return
	}

	redact(values)

	pretty, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		fmt.Fprintf(b, "（配置格式化失败：%v）\n\n", err)
		return
	}
	b.Write(pretty)
	b.WriteString("\n\n")
}

// redact 就地打码敏感项。
//
// 只处理**已知会带凭据**的几处，不做「看到像密钥就打码」的猜测：猜错一次就把
// 用户真正需要反馈的配置项遮掉了，那比漏打码更麻烦。
func redact(values map[string]any) {
	setPath(values, []string{"server", "token"}, redacted)

	// 代理地址与远端源地址都可能带用户名密码或查询串里的 token。
	if proxy, ok := getPath(values, []string{"net", "proxy"}).(string); ok && proxy != "" {
		setPath(values, []string{"net", "proxy"}, maskURL(proxy))
	}
	if list, ok := getPath(values, []string{"source", "remote_urls"}).([]any); ok {
		for _, entry := range list {
			item, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			if raw, ok := item["url"].(string); ok && raw != "" {
				item["url"] = maskURL(raw)
			}
		}
	}
}

// maskURL 去掉 URL 里的凭据与查询串，只留下「是什么地址」。
//
// 认不出是 URL 就整串打码：一个认不出的字符串里可能藏着任何东西。
func maskURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return redacted
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func getPath(values map[string]any, path []string) any {
	var current any = values
	for _, key := range path {
		node, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = node[key]
	}
	return current
}

func setPath(values map[string]any, path []string, value any) {
	node := values
	for _, key := range path[:len(path)-1] {
		next, ok := node[key].(map[string]any)
		if !ok {
			return
		}
		node = next
	}
	last := path[len(path)-1]
	if _, exists := node[last]; exists {
		node[last] = value
	}
}

func writeLogs(b *strings.Builder, dataDir string, now time.Time) {
	b.WriteString("最近日志\n")
	b.WriteString(strings.Repeat("-", 48) + "\n")

	lines, err := tailLog(dataDir, now, maxLogLines)
	if err != nil {
		fmt.Fprintf(b, "（读不到日志：%v）\n", err)
		return
	}
	if len(lines) == 0 {
		b.WriteString("（今天还没有日志）\n")
		return
	}
	for _, line := range lines {
		b.WriteString(line)
		b.WriteString("\n")
	}
}

// tailLog 取当天日志的最后 n 行。
func tailLog(dataDir string, now time.Time, n int) ([]string, error) {
	if dataDir == "" {
		return nil, nil
	}
	// 日志按天切分，文件名与 launch 包一致。这里按同样规则拼而不是复用那个
	// 未导出的函数：一个只读日志的包不该为此把 launch 的内部约定暴露出去。
	name := "cloudtrace-" + now.Format("20060102") + ".log"
	path := filepath.Join(dataDir, "logs", name)

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	all := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all, nil
}

// SectionKeys 返回诊断项的顺序。
//
// 顺序有意义：DNS → TCP → trace → 出口，正是「一层套一层」的排查顺序。
// 前端按这个顺序渲染，用户顺着往下看就知道最早断在哪一层。
func SectionKeys() []string {
	return []string{KeyDNS, KeyTCP, KeyTrace, KeyEgress}
}

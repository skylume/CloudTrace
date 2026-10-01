// Package assets 承载随二进制一起发布的内置数据。
//
// 官方网段清单随版本固化进二进制，首次运行即可开始扫描，不依赖任何网络
// 请求；需要更新时由上层另行拉取并落到数据目录，不影响这里的兜底副本。
//
// 注意：go:embed 只能嵌入本包目录及其子目录，所以内嵌声明必须放在这里。
package assets

import (
	"embed"
	"strings"
)

//go:embed ips-v4.txt ips-v6.txt
var files embed.FS

// OfficialRanges 返回内置的官方网段清单（CIDR 文本，每行一条）。
//
// ipVersion 只认 4 与 6，其他取值返回 nil —— 调用方据此判定「该协议族
// 没有内置副本」，而不是拿到一个空列表后误以为官方段为空。
func OfficialRanges(ipVersion int) []string {
	name := ""
	switch ipVersion {
	case 4:
		name = "ips-v4.txt"
	case 6:
		name = "ips-v6.txt"
	default:
		return nil
	}

	raw, err := files.ReadFile(name)
	if err != nil {
		return nil
	}
	return SplitRanges(string(raw))
}

// SplitRanges 把多行网段文本切成切片，忽略空行与 `#` 注释行。
//
// 只做切分不做校验：网段是否合法交给解析层判断，这里保持纯文本处理，
// 便于内置清单里临时注释掉某一段。
func SplitRanges(text string) []string {
	out := make([]string, 0, 16)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

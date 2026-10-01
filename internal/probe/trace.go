package probe

import (
	"fmt"
	"strings"
)

// TracePath 是 Cloudflare 的 trace 端点，返回 key=value 逐行文本。
const TracePath = "/cdn-cgi/trace"

// ParseTrace 解析 trace 响应体。
//
// 格式为每行一个 key=value，键统一转小写，值去掉首尾空白；空行与不含
// 等号的行直接跳过。一个 key=value 都没解析到时返回错误，让调用方能把
// 「返回了 HTML 错误页」与「返回了 trace」区分开。
func ParseTrace(body string) (map[string]string, error) {
	out := make(map[string]string)
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		out[key] = strings.TrimSpace(value)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("响应体不是 trace 格式：未解析到任何 key=value 行")
	}
	return out, nil
}

// ExtractColo 取数据中心代码（如 HKG），统一转大写。
func ExtractColo(trace map[string]string) string {
	return strings.ToUpper(strings.TrimSpace(trace["colo"]))
}

// ExtractLoc 取出口国家码（如 CN），统一转大写。
func ExtractLoc(trace map[string]string) string {
	return strings.ToUpper(strings.TrimSpace(trace["loc"]))
}

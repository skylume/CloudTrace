// Package update 检查有没有新版本。
//
// 只做一件事：问一次 GitHub 上最新的发行版是什么，和当前版本比一比。
// 不下载、不安装——「有新版本」这句话本身就是结果，剩下的交给用户。
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ReleaseURL 是查询最新发行版的接口地址。
//
// 写死仓库地址而不是做成配置项：这个程序只会从它自己的仓库更新，
// 让用户填一个地址只会多一个填错的机会。
const ReleaseURL = "https://api.github.com/repos/skylume/CloudTrace/releases/latest"

// maxBody 是响应体的读取上限。发行版信息只有几 KB，给足余量即可。
const maxBody = 256 << 10

// Result 是一次检查的结果。
type Result struct {
	// Current 是当前版本，原样带出来方便界面显示。
	Current string `json:"current"`
	// Latest 是查到的最新版本；查询失败时为空。
	Latest string `json:"latest,omitempty"`
	// HasUpdate 表示确实有更新的版本。
	HasUpdate bool `json:"has_update"`
	// URL 是新版本的下载页。
	URL string `json:"url,omitempty"`
	// Notes 是发行说明（可能是空）。
	Notes string `json:"notes,omitempty"`
	// Skipped 说明这次为什么没查；非空时其余字段无意义。
	Skipped string `json:"skipped,omitempty"`
}

// Client 是查询用的 HTTP 客户端；为 nil 时用默认值。
//
// 默认超时压得比较短：检查更新是锦上添花，不该让启动多等几秒。
const defaultTimeout = 8 * time.Second

// Check 查询最新版本并与 current 比较。
//
// 返回的错误只用于「查询本身失败」；版本比不出来（比如当前版本是 dev）
// 走 Result.Skipped，不算错误——那不是故障，是没什么可比的。
func Check(ctx context.Context, current string, client *http.Client) (Result, error) {
	if !comparable(current) {
		return Result{Current: current, Skipped: "当前版本不是发行版本，无法比较"}, nil
	}

	return checkFrom(ctx, current, client, ReleaseURL)
}

// checkFrom 是 Check 的实现体，地址与客户端可注入以便测试。
func checkFrom(ctx context.Context, current string, client *http.Client, url string) (Result, error) {
	res := Result{Current: current}

	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return res, fmt.Errorf("构造更新检查请求失败：%w", err)
	}
	// GitHub 对没有 UA 的请求直接返回 403。
	req.Header.Set("User-Agent", "CloudTrace")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return res, fmt.Errorf("检查更新失败：%w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return res, fmt.Errorf("检查更新失败：HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return res, fmt.Errorf("读取更新信息失败：%w", err)
	}

	var payload struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Body    string `json:"body"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return res, fmt.Errorf("更新信息不是合法 JSON：%w", err)
	}
	if payload.TagName == "" {
		return res, errors.New("更新信息里没有版本号")
	}

	res.Latest = payload.TagName
	res.URL = payload.HTMLURL
	res.Notes = payload.Body
	res.HasUpdate = Newer(current, payload.TagName)
	return res, nil
}

// Newer 报告 latest 是否比 current 新。
//
// 比不出来时一律返回 false（当作「没有更新」）：宁可漏报一次，也不要在用户
// 已经是最新版时天天提示他升级。版本号按点分段的数字比较，缺位补 0，
// 因此 1.2 与 1.2.0 视为相同。
func Newer(current, latest string) bool {
	cur, ok1 := parse(current)
	lat, ok2 := parse(latest)
	if !ok1 || !ok2 {
		return false
	}
	for i := 0; i < len(cur) || i < len(lat); i++ {
		var a, b int
		if i < len(cur) {
			a = cur[i]
		}
		if i < len(lat) {
			b = lat[i]
		}
		if a != b {
			return b > a
		}
	}
	return false
}

// comparable 报告一个版本号是否值得去比。
//
// 开发版（dev）与空值都跳过：拿它去比永远比不过，而每次启动都提示「有新版本」
// 比不提示更糟。
func comparable(version string) bool {
	_, ok := parse(version)
	return ok
}

// parse 把 v1.2.3 / 1.2.3 这样的版本号拆成数字段。
//
// 认不出就返回 false——上层据此跳过而不是硬比。
func parse(version string) ([]int, bool) {
	raw := strings.TrimSpace(version)
	raw = strings.TrimPrefix(raw, "v")
	raw = strings.TrimPrefix(raw, "V")
	if raw == "" {
		return nil, false
	}
	// 去掉 -beta.1 / +build 之类的后缀，只比主版本段。
	if idx := strings.IndexAny(raw, "-+"); idx >= 0 {
		raw = raw[:idx]
	}

	parts := strings.Split(raw, ".")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			return nil, false
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

package migrate

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"cloudtrace/internal/config"
	"cloudtrace/internal/model"
)

// legacySettings 是旧配置文件的形状。
//
// 用 map 而不是结构体：旧文件里可能有关键字拼写不同、或者某个键整个缺失的版本，
// 结构体会让「缺一个键」变成「整份配置解析失败」。
type legacySettings map[string]any

func buildSettings(path string, plan *Plan) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取旧配置失败：%w", err)
	}
	var old legacySettings
	if err := json.Unmarshal(raw, &old); err != nil {
		return fmt.Errorf("旧配置不是合法 JSON：%w", err)
	}

	m := &settingsMapper{plan: plan, old: old}
	m.apply()
	return nil
}

// settingsMapper 把旧键一个个搬过去。
//
// 单独一个结构体是为了让「读过哪些键」这件事有地方记——迁移最容易出的错是
// 悄悄漏掉一项，而用户不会发现：新界面里那一项显示的是默认值，看起来很正常。
type settingsMapper struct {
	plan *Plan
	old  legacySettings
	// seen 记录处理过的旧键，用来算出「没处理的」。
	seen map[string]bool
}

func (m *settingsMapper) apply() {
	m.seen = map[string]bool{}

	// ---- server.* ----
	m.int_("http_port", "server.port", m.portNote)
	m.str("http_token", "server.token", nil)
	m.bool_("allow_lan", "server.bind", func(v bool) {
		if v {
			m.set("server.bind", "0.0.0.0")
			m.note("旧配置允许局域网访问，已设为监听 0.0.0.0；请确认访问 Token 已设置")
		} else {
			m.set("server.bind", "127.0.0.1")
		}
	})

	// ---- scan.* ----
	m.str("scan_mode", "scan.mode", nil)
	m.int_("sample_max", "scan.sample_max", nil)
	m.int_("workers", "scan.workers", nil)
	m.int_("latency_threshold", "scan.latency_threshold", nil)
	m.int_("ping_times", "scan.ping_times", nil)
	m.bool_("verify_nodes", "scan.verify_nodes", nil)
	m.intList("pre_filter_ports", "scan.pre_filter_ports")
	m.list("allowed_regions", "scan.allowed_regions", nil)
	m.list("blocked_regions", "scan.blocked_regions", nil)
	m.str("cidr_mode", "scan.source_mode", func(v string) string {
		switch v {
		case "仅官方":
			return "official"
		case "仅自定义":
			return "custom"
		case "两者", "两者合并":
			return "both"
		default:
			m.skip("cidr_mode", "认不出的来源模式："+v)
			return ""
		}
	})

	// ---- speed.* ----
	m.int_("speed_workers", "speed.concurrency", nil)
	m.float("min_speed", "speed.min_speed", nil)
	m.int_("per_region_topn", "speed.per_region_topn", nil)
	m.float("score_speed_weight", "speed.weight_speed", nil)
	m.float("score_latency_weight", "speed.weight_latency", nil)
	// 旧版间隔是秒，新版是毫秒。
	m.seconds("download_interval", "speed.interval_ms", nil)
	m.str("speed_url", "speed.url_mode", func(v string) string {
		if v == "" || v == "auto" {
			return "auto"
		}
		// 旧版直接存地址；新版把它当作自定义源。
		m.set("speed.custom_url", v)
		return "custom"
	})

	// ---- source.* ----
	m.int_("source_retries", "source.retry", nil)
	m.seconds("source_retry_delay", "source.retry_interval_ms", nil)
	m.seconds("source_timeout", "source.timeout_ms", nil)
	m.remoteSources()

	// ---- 明确不搬的 ----
	m.skip("tray_on_close", "托盘通知尚未实现，新版暂时没有对应项")
	m.skip("use_ip_cache", "新版始终启用归属地缓存，没有开关")
	m.skip("speed_result_limit", "新版改用「收够多少个合格结果就停」，语义不同，没有直接对应")
	m.skip("http_enabled", "新版面板始终可用，没有开关")
	m.skip("client_ip", "旧版记录的出口地址，新版每次探测时重新判断")
	m.skip("score_jitter_weight", "新版抖动权重默认不参与评分")

	// 剩下的是既没搬也没登记的——列出来，避免「悄悄漏掉一项」。
	for key := range m.old {
		if !m.seen[key] {
			m.plan.Skipped = append(m.plan.Skipped, key+"（新版没有对应项）")
		}
	}
	sort.Strings(m.plan.Skipped)
}

// portNote 处理旧端口的统一。
//
// 无论旧值是多少都要写进去：只处理「旧值等于早期端口」那一种情况的话，端口
// 恰好等于新默认值时就整项都不写了——而用户明明设置过它。
func (m *settingsMapper) portNote(v int) {
	if v == legacyPort {
		m.note(fmt.Sprintf("旧版端口 %d 已统一为 %d，界面地址随之变化", legacyPort, config.DefaultPort))
	}
	m.set("server.port", config.DefaultPort)
}

// ---- 取值与写入 ----

func (m *settingsMapper) set(path string, value any) {
	setPath(m.plan.Patch, path, value)
	// 迁移过来的值一律标成 user：旧配置里分不清哪些是用户改的、哪些是默认值，
	// 宁可保守——自适应逻辑不会去动它们。
	m.plan.Origins.Set(path, model.OriginUser)
}

func (m *settingsMapper) note(text string) {
	m.plan.Notes = append(m.plan.Notes, text)
}

func (m *settingsMapper) skip(key, reason string) {
	m.seen[key] = true
	m.plan.Skipped = append(m.plan.Skipped, key+"（"+reason+"）")
}

func (m *settingsMapper) int_(oldKey, path string, after func(int)) {
	m.seen[oldKey] = true
	value, ok := toInt(m.old[oldKey])
	if !ok {
		return
	}
	if after != nil {
		after(value)
		return
	}
	m.set(path, value)
}

func (m *settingsMapper) float(oldKey, path string, after func(float64)) {
	m.seen[oldKey] = true
	value, ok := toFloat(m.old[oldKey])
	if !ok {
		return
	}
	if after != nil {
		after(value)
		return
	}
	m.set(path, value)
}

func (m *settingsMapper) bool_(oldKey, path string, after func(bool)) {
	m.seen[oldKey] = true
	value, ok := m.old[oldKey].(bool)
	if !ok {
		return
	}
	if after != nil {
		after(value)
		return
	}
	m.set(path, value)
}

func (m *settingsMapper) str(oldKey, path string, convert func(string) string) {
	m.seen[oldKey] = true
	value, ok := m.old[oldKey].(string)
	if !ok {
		return
	}
	if convert != nil {
		if mapped := convert(value); mapped != "" {
			m.set(path, mapped)
		}
		return
	}
	if value != "" {
		m.set(path, value)
	}
}

// seconds 把「秒」换算成毫秒。旧版用秒，新版统一毫秒。
func (m *settingsMapper) seconds(oldKey, path string, after func(int)) {
	m.seen[oldKey] = true
	value, ok := toFloat(m.old[oldKey])
	if !ok {
		return
	}
	ms := int(value * 1000)
	if ms < 0 {
		return
	}
	if after != nil {
		after(ms)
		return
	}
	m.set(path, ms)
}

// list 把逗号/空白分隔的字符串转成数组。旧版是文本输入，新版是列表。
func (m *settingsMapper) list(oldKey, path string, after func([]string)) {
	m.seen[oldKey] = true
	raw, ok := m.old[oldKey].(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return
	}
	items := splitList(raw)
	if len(items) == 0 {
		return
	}
	if after != nil {
		after(items)
		return
	}
	m.set(path, items)
}

// intList 把逗号分隔的文本转成整数数组。
//
// 端口是数字，存成字符串数组的话配置校验会拒绝——而报出来的错是「类型不对」，
// 用户看着一列端口号完全不知道哪里不对。
func (m *settingsMapper) intList(oldKey, path string) {
	m.seen[oldKey] = true
	raw, ok := m.old[oldKey].(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return
	}
	items := splitList(raw)
	out := make([]int, 0, len(items))
	for _, item := range items {
		n, ok := toInt(item)
		if !ok {
			// 认不出的那一项单独报出来，其余照常带过去。
			m.plan.Skipped = append(m.plan.Skipped, oldKey+" 里的 "+item+"（不是端口号）")
			continue
		}
		out = append(out, n)
	}
	if len(out) > 0 {
		m.set(path, out)
	}
}

// remoteSources 处理远程数据源：旧版是一个数组，每项有 name/url/enabled。
func (m *settingsMapper) remoteSources() {
	m.seen["remote_sources"] = true
	raw, ok := m.old["remote_sources"].([]any)
	if !ok || len(raw) == 0 {
		return
	}

	out := make([]config.RemoteSource, 0, len(raw))
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		url, _ := entry["url"].(string)
		if strings.TrimSpace(url) == "" {
			continue
		}
		enabled, _ := entry["enabled"].(bool)
		name, _ := entry["name"].(string)
		out = append(out, config.RemoteSource{URL: url, Enabled: enabled, Note: name})
	}
	if len(out) == 0 {
		return
	}
	m.set("source.remote_urls", out)
	m.note(fmt.Sprintf("已带入 %d 个远程数据源，默认是停用的，需要时在扫描页打开", len(out)))
}

// ---- 小工具 ----

func splitList(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '，' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(n))
		return parsed, err == nil
	default:
		return 0, false
	}
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

// setPath 按点号路径写进嵌套补丁，顺带补出缺失的中间层。
func setPath(patch map[string]any, path string, value any) {
	parts := strings.Split(path, ".")
	cur := patch
	for i, part := range parts {
		if i == len(parts)-1 {
			cur[part] = value
			return
		}
		next, ok := cur[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[part] = next
		}
		cur = next
	}
}

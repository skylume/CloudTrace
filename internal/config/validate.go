package config

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// FieldError 描述单个配置项的问题。
type FieldError struct {
	Key    string `json:"key"`
	Value  any    `json:"value"`
	Reason string `json:"reason"`
}

// ValidationError 汇总所有非法配置项。
//
// 前端收到 E_INVALID_PARAM 时应定位到具体字段并高亮，
// 因此这里必须给出**全部**问题而不是遇到第一个就返回。
type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Fields))
	for _, f := range e.Fields {
		parts = append(parts, fmt.Sprintf("%s=%v（%s）", f.Key, f.Value, f.Reason))
	}
	return "配置校验失败：" + strings.Join(parts, "；")
}

// Keys 返回所有出错的配置键，便于前端高亮。
func (e *ValidationError) Keys() []string {
	out := make([]string, 0, len(e.Fields))
	for _, f := range e.Fields {
		out = append(out, f.Key)
	}
	return out
}

// Validate 校验配置的范围与枚举。
//
// 约定：
//   - 越界（超出硬上限）→ 报错，拒绝保存；
//   - 危险但允许（如并发 > WarnWorkers、阈值 < WarnLatency）→ **不报错**，
//     由 UI 警示与配置体检（internal/health）负责提示。
func (c Config) Validate() error {
	var fields []FieldError
	add := func(key string, value any, reason string) {
		fields = append(fields, FieldError{Key: key, Value: value, Reason: reason})
	}

	// ---- scan.* ----
	if !oneOf(c.Scan.Mode, "tcping", "httping") {
		add("scan.mode", c.Scan.Mode, "只能是 tcping 或 httping")
	}
	// 上限跟着 net.max_workers 走，而不是写死一个数：那是用户自己能调的天花板，
	// 写死的话调它就没有意义了。
	if c.Scan.Workers < 1 {
		add("scan.workers", c.Scan.Workers, "必须至少为 1")
	} else if c.Scan.Workers > c.Net.MaxWorkers {
		add("scan.workers", c.Scan.Workers,
			fmt.Sprintf("超过了全局并发上限 %d，先把它调大或者把并发降下来", c.Net.MaxWorkers))
	}
	if c.Scan.SampleMax < 0 || c.Scan.SampleMax > MaxSampleMax {
		add("scan.sample_max", c.Scan.SampleMax, fmt.Sprintf("必须在 0–%d 之间（0 = 不限制）", MaxSampleMax))
	}
	if c.Scan.LatencyThreshold < 1 || c.Scan.LatencyThreshold > 10000 {
		add("scan.latency_threshold", c.Scan.LatencyThreshold, "必须在 1–10000 之间")
	}
	if c.Scan.PingTimes < 1 || c.Scan.PingTimes > 100 {
		add("scan.ping_times", c.Scan.PingTimes, "必须在 1–100 之间")
	}
	if !validPort(c.Scan.Port) {
		add("scan.port", c.Scan.Port, "必须是 1–65535 的端口")
	}
	if !oneOf(c.Scan.SourceMode, "official", "custom", "both") {
		add("scan.source_mode", c.Scan.SourceMode, "只能是 official / custom / both")
	}
	for _, p := range c.Scan.PreFilterPorts {
		if !validPort(p) {
			add("scan.pre_filter_ports", p, "必须是 1–65535 的端口")
			break
		}
	}
	if c.Scan.TimeoutMS < 1 || c.Scan.TimeoutMS > 60000 {
		add("scan.timeout_ms", c.Scan.TimeoutMS, "必须在 1–60000 之间")
	}
	if c.Scan.Retry < 0 || c.Scan.Retry > 10 {
		add("scan.retry", c.Scan.Retry, "必须在 0–10 之间")
	}
	if c.Scan.SourceMode == "custom" && strings.TrimSpace(c.Scan.CustomSource) == "" {
		add("scan.custom_source", "", "source_mode = custom 时必须填写来源")
	}

	c.validateSpeed(add)
	c.validateSource(add)
	c.validateNet(add)
	c.validateGeo(add)
	c.validateExport(add)

	// ---- history.* ----
	if c.History.KeepCount < 1 || c.History.KeepCount > 1000 {
		add("history.keep_count", c.History.KeepCount, "必须在 1–1000 之间")
	}
	if !oneOf(c.History.KeepMode, "count", "days") {
		add("history.keep_mode", c.History.KeepMode, "只能是 count 或 days")
	}
	if c.History.KeepDays < 1 || c.History.KeepDays > 3650 {
		add("history.keep_days", c.History.KeepDays, "必须在 1–3650 之间")
	}

	// ---- ui.* ----
	if !oneOf(c.UI.Theme, "dark", "light", "system") {
		add("ui.theme", c.UI.Theme, "只能是 dark / light / system")
	}
	if !oneOf(c.UI.Lang, "zh", "en") {
		add("ui.lang", c.UI.Lang, "只能是 zh 或 en")
	}
	if !oneOf(c.UI.FontScale, "small", "medium", "large") {
		add("ui.font_scale", c.UI.FontScale, "只能是 small / medium / large")
	}
	if !oneOf(c.UI.Density, "auto", "simple", "advanced") {
		add("ui.density", c.UI.Density, "只能是 auto / simple / advanced")
	}
	if !oneOf(c.UI.TableDensity, "compact", "normal", "comfortable") {
		add("ui.table_density", c.UI.TableDensity, "只能是 compact / normal / comfortable")
	}
	if !oneOf(c.UI.TimeFormat, "local", "utc") {
		add("ui.time_format", c.UI.TimeFormat, "只能是 local 或 utc")
	}
	// 测速不在其中：它是结果页的一个视图，不是独立页面。列进合法取值会让用户
	// 以为它是一个页面，而界面上一旦选不到，就会变成一个只能手改配置项的值。
	if !oneOf(c.UI.StartPage, "scan", "result", "history", "settings") {
		add("ui.start_page", c.UI.StartPage, "不是有效的页面名")
	}
	if c.UI.PageSize < 1 || c.UI.PageSize > 1000 {
		add("ui.page_size", c.UI.PageSize, "必须在 1–1000 之间")
	}

	// ---- server.* ----
	if !validPort(c.Server.Port) {
		add("server.port", c.Server.Port, "必须是 1–65535 的端口")
	}
	if !oneOf(c.Server.Bind, "127.0.0.1", "0.0.0.0") {
		add("server.bind", c.Server.Bind, "只能是 127.0.0.1 或 0.0.0.0")
	}
	if c.Server.SessionTTLMin < 1 || c.Server.SessionTTLMin > 10080 {
		add("server.session_ttl_min", c.Server.SessionTTLMin, "必须在 1–10080 分钟之间")
	}
	// 局域网暴露必须要有访问密码，否则等于把面板完全敞开。
	//
	// 这条挡在**保存**这一关，而不是等启动时再拒绝：用户是在设置页把绑定改成
	// 0.0.0.0 的，那就该在那里告诉他「先设个密码」，而不是让他改完、重启、
	// 然后发现进不去了。
	if c.Server.Bind == "0.0.0.0" && strings.TrimSpace(c.Server.Token) == "" {
		add("server.token", "", "开放局域网访问前必须先设置访问密码")
	}

	// ---- advanced.* ----
	if !oneOf(c.Advanced.LogLevel, "debug", "info", "warn", "error") {
		add("advanced.log_level", c.Advanced.LogLevel, "只能是 debug / info / warn / error")
	}
	if c.Advanced.LogKeepDays < 1 || c.Advanced.LogKeepDays > 365 {
		add("advanced.log_keep_days", c.Advanced.LogKeepDays, "必须在 1–365 之间")
	}

	if len(fields) == 0 {
		return nil
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].Key < fields[j].Key })
	return &ValidationError{Fields: fields}
}

// Warnings 返回「危险但允许」的配置项，供界面警示与配置体检使用。
//
// 这些**不是错误**：任何高级选项都要有合理默认值，同时也要求
// 危险值给出后果说明，因此这里只提示、不拒绝。
func (c Config) Warnings() []FieldError {
	var out []FieldError
	if c.Scan.Workers > WarnWorkers {
		out = append(out, FieldError{
			Key:    "scan.workers",
			Value:  c.Scan.Workers,
			Reason: fmt.Sprintf("并发超过 %d 可能导致断网或路由器过载，建议降到 %d 以内", WarnWorkers, MaxWorkersPreset),
		})
	}
	if c.Scan.LatencyThreshold < WarnLatency {
		out = append(out, FieldError{
			Key:    "scan.latency_threshold",
			Value:  c.Scan.LatencyThreshold,
			Reason: fmt.Sprintf("阈值低于 %dms 会大幅减少可用结果，建议提到 200 左右", WarnLatency),
		})
	}
	return out
}

// ---- 新增分组的校验 ----
//
// 与既有分组同一口径：越界才报错，危险但允许的值留给 Warnings 与配置体检。

func (c Config) validateSpeed(add func(string, any, string)) {
	if !oneOf(c.Speed.URLMode, "auto", "official", "mobile_friendly", "mobile_only", "custom") {
		add("speed.url_mode", c.Speed.URLMode, "只能是 auto / official / mobile_friendly / mobile_only / custom")
	}
	if c.Speed.URLMode == "custom" && !validSpeedURL(c.Speed.CustomURL) {
		add("speed.custom_url", c.Speed.CustomURL, "custom 模式下必须是有效的 http(s) 地址")
	}
	if c.Speed.Concurrency < 1 || c.Speed.Concurrency > 16 {
		add("speed.concurrency", c.Speed.Concurrency, "必须在 1–16 之间")
	}
	if c.Speed.TargetQualified < 1 || c.Speed.TargetQualified > 10000 {
		add("speed.target_qualified", c.Speed.TargetQualified, "必须在 1–10000 之间")
	}
	if c.Speed.IntervalMS < 0 || c.Speed.IntervalMS > 60000 {
		add("speed.interval_ms", c.Speed.IntervalMS, "必须在 0–60000 之间")
	}
	if c.Speed.MinSpeed < 0 {
		add("speed.min_speed", c.Speed.MinSpeed, "不能为负")
	}
	for k, v := range map[string]float64{
		"speed.weight_speed":   c.Speed.WeightSpeed,
		"speed.weight_latency": c.Speed.WeightLatency,
		"speed.weight_jitter":  c.Speed.WeightJitter,
	} {
		if v < 0 {
			add(k, v, "权重不能为负")
		}
	}
	if c.Speed.PerRegionTopN < 0 || c.Speed.PerRegionTopN > 1000 {
		add("speed.per_region_topn", c.Speed.PerRegionTopN, "必须在 0–1000 之间（0 = 不分地区）")
	}
	if c.Speed.DownloadDurationS < 1 || c.Speed.DownloadDurationS > 600 {
		add("speed.download_duration_s", c.Speed.DownloadDurationS, "必须在 1–600 秒之间")
	}
	if c.Speed.MaxDownloadMB < 0 {
		add("speed.max_download_mb", c.Speed.MaxDownloadMB, "不能为负（0 = 不限）")
	}
	if c.Speed.Breaker429 < 1 || c.Speed.Breaker429 > 100 {
		add("speed.breaker_429", c.Speed.Breaker429, "必须在 1–100 之间")
	}
}

func (c Config) validateSource(add func(string, any, string)) {
	for i, s := range c.Source.RemoteURLs {
		if s.Enabled && strings.TrimSpace(s.URL) == "" {
			add("source.remote_urls", i, fmt.Sprintf("第 %d 个来源已启用但地址为空", i+1))
		}
	}
	if c.Source.Retry < 0 || c.Source.Retry > 10 {
		add("source.retry", c.Source.Retry, "必须在 0–10 之间")
	}
	if c.Source.RetryIntervalMS < 0 || c.Source.RetryIntervalMS > 60000 {
		add("source.retry_interval_ms", c.Source.RetryIntervalMS, "必须在 0–60000 之间")
	}
	if c.Source.TimeoutMS < 1 || c.Source.TimeoutMS > 120000 {
		add("source.timeout_ms", c.Source.TimeoutMS, "必须在 1–120000 之间")
	}
	if !oneOf(c.Source.MergeStrategy, "union", "intersect") {
		add("source.merge_strategy", c.Source.MergeStrategy, "只能是 union 或 intersect")
	}
}

func (c Config) validateNet(add func(string, any, string)) {
	if !oneOf(c.Net.UseTLS, "auto", "true", "false") {
		add("net.use_tls", c.Net.UseTLS, "只能是 auto / true / false")
	}
	if !oneOf(c.Net.IPVersion, "auto", "v4", "v6") {
		add("net.ip_version", c.Net.IPVersion, "只能是 auto / v4 / v6")
	}
	if c.Net.MaxWorkers < 1 || c.Net.MaxWorkers > MaxWorkersCeiling {
		add("net.max_workers", c.Net.MaxWorkers, fmt.Sprintf("必须在 1–%d 之间", MaxWorkersCeiling))
	}
	// 代理地址写错了不会报错，只会让所有请求静默失败——这是最难自查的一类
	// 配置错误，因此在保存时就拦下。
	if p := strings.TrimSpace(c.Net.Proxy); p != "" {
		if !validProxy(p) {
			add("net.proxy", p, "必须是 http://、https:// 或 socks5:// 开头的地址")
		}
	}
}

func (c Config) validateGeo(add func(string, any, string)) {
	if !oneOf(c.Geo.ASNSource, "iptoasn", "geolite2_mmdb", "off") {
		add("geo.asn_source", c.Geo.ASNSource, "只能是 iptoasn / geolite2_mmdb / off")
	}
	if c.Geo.ASNUpdateIntervalDays < 0 || c.Geo.ASNUpdateIntervalDays > 365 {
		add("geo.asn_update_interval_days", c.Geo.ASNUpdateIntervalDays, "必须在 0–365 之间（0 = 仅手动）")
	}
}

func (c Config) validateExport(add func(string, any, string)) {
	if !oneOf(c.Export.DefaultFormat, FormatCSV, FormatJSON, FormatTXT) {
		add("export.default_format", c.Export.DefaultFormat, "只能是 csv / json / txt")
	}
	if !oneOf(c.Export.DefaultFields, FieldsAll, FieldsSlim, FieldsIPPort) {
		add("export.default_fields", c.Export.DefaultFields, "只能是 all / slim / ip_port")
	}
	// 模板为空会被补齐成默认值，但只有占位符没有文件名的模板会导出到一堆
	// 同名文件里，因此要求至少保留一个非占位符字符。
	if strings.TrimSpace(strings.NewReplacer("{type}", "", "{ts}", "", "{date}", "", "{time}", "").
		Replace(c.Export.FilenameTemplate)) == "" {
		add("export.filename_template", c.Export.FilenameTemplate, "必须包含文件名，不能只有占位符")
	}
}

func oneOf(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

func validPort(p int) bool { return p >= 1 && p <= 65535 }

// validSpeedURL 判断测速地址是否可用。
//
// 只接受 http(s)：测速走的是 HTTP 下载，其他协议在这里通过只会在真正发起
// 请求时才失败，而那时用户已经等了十几秒。
func validSpeedURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

// validProxy 判断代理地址是否可用。
func validProxy(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	switch u.Scheme {
	case "http", "https", "socks5":
		return true
	}
	return false
}

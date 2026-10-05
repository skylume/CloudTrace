package config

// 本文件是 spec 配置项总表里「M0 尚未实现的 6 组」：speed / source / net /
// geo / export / notify。
//
// 每组自带构造默认值、补齐零值与深拷贝三个方法，让 config.go 里只需要各
// 占一行——否则 Default 与 Clone 会各自膨胀到两百行以上。

// ---- speed.* ----

// SpeedConfig 是测速相关配置（前缀 speed.）。
type SpeedConfig struct {
	URLMode           string  `json:"url_mode"`            // auto | official | mobile_friendly | mobile_only | custom
	CustomURL         string  `json:"custom_url"`          // custom 模式下的测速地址
	Concurrency       int     `json:"concurrency"`         // 测速并发 1–16
	TargetQualified   int     `json:"target_qualified"`    // 收够多少个合格结果就提前收敛
	IntervalMS        int     `json:"interval_ms"`         // 串行测速间隔（防 429）
	MinSpeed          float64 `json:"min_speed"`           // 合格线 MB/s
	WeightSpeed       float64 `json:"weight_speed"`        // 评分权重：速度
	WeightLatency     float64 `json:"weight_latency"`      // 评分权重：延迟
	WeightJitter      float64 `json:"weight_jitter"`       // 评分权重：抖动
	PerRegionTopN     int     `json:"per_region_topn"`     // 分地区 TopN（0 = 不分地区）
	DownloadDurationS int     `json:"download_duration_s"` // 单次下载时长
	MaxDownloadMB     int     `json:"max_download_mb"`     // 最大下载量（0 = 不限）
	Breaker429        int     `json:"breaker_429"`         // 连续多少次限流即熔断
}

// 「测速前可用性校验」的开关属于 scan 分组（`scan.usability_check`）。
// 这里曾经另有一个 speed.usability_check，与本项同名同义却谁也没读，
// 两个开关并存只会让人以为它们各管一段——已删掉，只留 scan 那一个。

func defaultSpeed() SpeedConfig {
	return SpeedConfig{
		URLMode:           "auto",
		Concurrency:       1,
		TargetQualified:   10,
		IntervalMS:        1200,
		WeightSpeed:       1.0,
		WeightLatency:     1.0,
		WeightJitter:      0.0,
		DownloadDurationS: 10,
		Breaker429:        3,
	}
}

// ---- source.* ----

// RemoteSource 是一个远程网段来源，可独立启停。
type RemoteSource struct {
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
	Note    string `json:"note,omitempty"`
}

// SourceConfig 是数据源相关配置（前缀 source.）。
type SourceConfig struct {
	RemoteURLs      []RemoteSource `json:"remote_urls"`       // 远程源列表
	Retry           int            `json:"retry"`             // 拉取重试次数
	RetryIntervalMS int            `json:"retry_interval_ms"` // 重试间隔
	TimeoutMS       int            `json:"timeout_ms"`        // 拉取超时
	MergeStrategy   string         `json:"merge_strategy"`    // union | intersect
}

func defaultSource() SourceConfig {
	return SourceConfig{
		RemoteURLs:      []RemoteSource{},
		Retry:           2,
		RetryIntervalMS: 1000,
		TimeoutMS:       10000,
		MergeStrategy:   "union",
	}
}

// EnabledURLs 返回启用的远程源地址，顺序与配置一致。
//
// 停用是「保留配置但这次不拉」，因此过滤发生在读取时而不是保存时——
// 用户关掉一个源不该把它从列表里删掉。
func (c SourceConfig) EnabledURLs() []string {
	out := make([]string, 0, len(c.RemoteURLs))
	for _, s := range c.RemoteURLs {
		if s.Enabled && s.URL != "" {
			out = append(out, s.URL)
		}
	}
	return out
}

// ---- net.* ----

// DefaultUserAgent 是请求使用的 UA。
//
// 写死一个主流 Chrome 的 UA：部分网段来源会拒绝非常见 UA 的请求，而
// 「伪装成浏览器」这件事本身不需要用户理解，因此不给它做配置项。
const DefaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

// NetConfig 是网络相关配置（前缀 net.）。
type NetConfig struct {
	CustomDNS   []string `json:"custom_dns"`   // 自定义 DNS
	DNSFallback bool     `json:"dns_fallback"` // 系统 DNS 失败时回退内置
	UserAgent   string   `json:"user_agent"`   // 请求 UA
	Proxy       string   `json:"proxy"`        // HTTP 代理地址
	ForceDirect bool     `json:"force_direct"` // 强制直连
	UseTLS      string   `json:"use_tls"`      // auto（按端口推断）/ true / false
	IPVersion   string   `json:"ip_version"`   // auto | v4 | v6
	MaxWorkers  int      `json:"max_workers"`  // 全局并发硬上限
}

func defaultNet() NetConfig {
	return NetConfig{
		CustomDNS:   []string{},
		DNSFallback: true,
		UserAgent:   DefaultUserAgent,
		ForceDirect: true,
		UseTLS:      "auto",
		IPVersion:   "auto",
		MaxWorkers:  MaxWorkersDefault,
	}
}

// ---- geo.* ----

// ASN 库的存放位置与更新周期默认值。
const (
	DefaultASNDBPath      = "data/cache/asn"
	DefaultASNUpdateDays  = 7
	ASNUpdateIntervalOff  = 0
	DefaultGeoWarnEnabled = true
)

// GeoConfig 是 ASN 与地理相关配置（前缀 geo.）。
type GeoConfig struct {
	ASNSource             string   `json:"asn_source"`               // iptoasn | geolite2_mmdb | off
	ASNDBPath             string   `json:"asn_db_path"`              // 库文件位置
	ASNAutoUpdate         bool     `json:"asn_auto_update"`          // 是否允许后台自动更新
	ASNUpdateIntervalDays int      `json:"asn_update_interval_days"` // 自动更新周期（天）；0 = 仅手动
	GeoWarnEnabled        bool     `json:"geo_warn_enabled"`         // 代理国家地理警告开关
	FilterASN             []string `json:"filter_asn"`               // 按 ASN / ISP 过滤
}

func defaultGeo() GeoConfig {
	return GeoConfig{
		ASNSource:             "iptoasn",
		ASNDBPath:             DefaultASNDBPath,
		ASNAutoUpdate:         true,
		ASNUpdateIntervalDays: DefaultASNUpdateDays,
		GeoWarnEnabled:        DefaultGeoWarnEnabled,
		FilterASN:             []string{},
	}
}

// ---- export.* ----

// 导出格式与字段预设。
const (
	FormatCSV  = "csv"
	FormatJSON = "json"
	FormatTXT  = "txt"

	FieldsAll    = "all"
	FieldsSlim   = "slim"
	FieldsIPPort = "ip_port"

	DefaultFilenameTemplate = "cloudtrace_{type}_{ts}"
)

// ExportConfig 是导出相关配置（前缀 export.）。
type ExportConfig struct {
	DefaultFormat    string            `json:"default_format"`    // csv | json | txt
	DefaultFields    string            `json:"default_fields"`    // all | slim | ip_port
	FieldAliases     map[string]string `json:"field_aliases"`     // 字段别名（表头自定义）
	CSVBOM           bool              `json:"csv_bom"`           // CSV 是否带 BOM（Excel 兼容）
	FilenameTemplate string            `json:"filename_template"` // 文件名模板
}

func defaultExport() ExportConfig {
	return ExportConfig{
		DefaultFormat:    FormatCSV,
		DefaultFields:    FieldsAll,
		FieldAliases:     map[string]string{},
		CSVBOM:           true,
		FilenameTemplate: DefaultFilenameTemplate,
	}
}

// ---- notify.* ----

// NotifyConfig 是通知相关配置（前缀 notify.）。
type NotifyConfig struct {
	Tray   bool `json:"tray"`    // 托盘通知（桌面版）
	Web    bool `json:"web"`     // Web 通知
	OnDone bool `json:"on_done"` // 完成提醒
	OnFail bool `json:"on_fail"` // 失败提醒
	Sound  bool `json:"sound"`   // 声音开关
}

func defaultNotify() NotifyConfig {
	return NotifyConfig{
		Tray:   true,
		Web:    false,
		OnDone: true,
		OnFail: true,
		Sound:  false,
	}
}

// ---- 补齐零值 ----
//
// 只补「零值必然是无效值」的字段。权重、合格线、TopN、更新周期这些字段的
// 0 是合法取值（不惩罚抖动 / 不设合格线 / 不分地区 / 仅手动更新），
// 补默认值会把用户的显式设置悄悄改掉。

func (c *SpeedConfig) normalize() {
	d := defaultSpeed()
	if c.URLMode == "" {
		c.URLMode = d.URLMode
	}
	if c.Concurrency == 0 {
		c.Concurrency = d.Concurrency
	}
	if c.TargetQualified == 0 {
		c.TargetQualified = d.TargetQualified
	}
	if c.IntervalMS == 0 {
		c.IntervalMS = d.IntervalMS
	}
	if c.DownloadDurationS == 0 {
		c.DownloadDurationS = d.DownloadDurationS
	}
	if c.Breaker429 == 0 {
		c.Breaker429 = d.Breaker429
	}
}

func (c *SourceConfig) normalize() {
	d := defaultSource()
	if c.RemoteURLs == nil {
		c.RemoteURLs = []RemoteSource{}
	}
	if c.Retry == 0 {
		c.Retry = d.Retry
	}
	if c.RetryIntervalMS == 0 {
		c.RetryIntervalMS = d.RetryIntervalMS
	}
	if c.TimeoutMS == 0 {
		c.TimeoutMS = d.TimeoutMS
	}
	if c.MergeStrategy == "" {
		c.MergeStrategy = d.MergeStrategy
	}
}

func (c *NetConfig) normalize() {
	d := defaultNet()
	if c.CustomDNS == nil {
		c.CustomDNS = []string{}
	}
	if c.UserAgent == "" {
		c.UserAgent = d.UserAgent
	}
	if c.UseTLS == "" {
		c.UseTLS = d.UseTLS
	}
	if c.IPVersion == "" {
		c.IPVersion = d.IPVersion
	}
	if c.MaxWorkers == 0 {
		c.MaxWorkers = d.MaxWorkers
	}
}

func (c *GeoConfig) normalize() {
	d := defaultGeo()
	if c.FilterASN == nil {
		c.FilterASN = []string{}
	}
	if c.ASNSource == "" {
		c.ASNSource = d.ASNSource
	}
	if c.ASNDBPath == "" {
		c.ASNDBPath = d.ASNDBPath
	}
	// ASNUpdateIntervalDays 刻意不补：0 表示「仅手动更新」，是合法取值。
}

func (c *ExportConfig) normalize() {
	d := defaultExport()
	if c.FieldAliases == nil {
		c.FieldAliases = map[string]string{}
	}
	if c.DefaultFormat == "" {
		c.DefaultFormat = d.DefaultFormat
	}
	if c.DefaultFields == "" {
		c.DefaultFields = d.DefaultFields
	}
	if c.FilenameTemplate == "" {
		c.FilenameTemplate = d.FilenameTemplate
	}
}

// ---- 深拷贝 ----

func (c SpeedConfig) clone() SpeedConfig { return c }

func (c SourceConfig) clone() SourceConfig {
	out := c
	out.RemoteURLs = append([]RemoteSource(nil), c.RemoteURLs...)
	return out
}

func (c NetConfig) clone() NetConfig {
	out := c
	out.CustomDNS = append([]string(nil), c.CustomDNS...)
	return out
}

func (c GeoConfig) clone() GeoConfig {
	out := c
	out.FilterASN = append([]string(nil), c.FilterASN...)
	return out
}

func (c ExportConfig) clone() ExportConfig {
	out := c
	if c.FieldAliases != nil {
		out.FieldAliases = make(map[string]string, len(c.FieldAliases))
		for k, v := range c.FieldAliases {
			out.FieldAliases[k] = v
		}
	}
	return out
}

func (c NotifyConfig) clone() NotifyConfig { return c }

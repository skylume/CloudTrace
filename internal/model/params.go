package model

// ScanParams 是一次扫描的完整参数快照。
//
// 它有两个身份：`scan/start` 命令的载荷，以及历史记录里「当时是用什么
// 参数跑的」凭据。因此字段一旦发布就不能改名——改名等于旧历史读不出来。
//
// 这里刻意与配置结构分开：配置是「用户当前的设置」，参数快照是「这一次
// 运行的既定事实」，档位与自适应会让两者不同。
type ScanParams struct {
	Mode             string   `json:"mode"`              // tcping | httping
	Workers          int      `json:"workers"`           // 并发 worker 数
	SampleMax        int      `json:"sample_max"`        // 采样上限，0 = 不限制
	LatencyThreshold int      `json:"latency_threshold"` // 延迟阈值 ms
	PingTimes        int      `json:"ping_times"`        // 精扫阶段每 IP 探测次数
	Port             int      `json:"port"`              // 默认端口
	IPVersion        int      `json:"ip_version"`        // 4 | 6
	SourceMode       string   `json:"source_mode"`       // official | custom | both
	CustomSource     string   `json:"custom_source"`     // 多形态来源文本
	PreFilterPorts   []int    `json:"pre_filter_ports"`  // 前置端口过滤
	AllowedRegions   []string `json:"allowed_regions"`   // 地区白名单（空 = 不限制）
	BlockedRegions   []string `json:"blocked_regions"`   // 地区黑名单
	TwoPhase         bool     `json:"two_phase"`         // 两阶段扫描
	VerifyNodes      bool     `json:"verify_nodes"`      // 节点明细采集
	TimeoutMS        int      `json:"timeout_ms"`        // 单次探测超时
	Retry            int      `json:"retry"`             // 探测失败重试次数
}

// UsesOfficial 报告本次扫描是否要取官方网段。
func (p ScanParams) UsesOfficial() bool {
	return p.SourceMode == "official" || p.SourceMode == "both"
}

// UsesCustom 报告本次扫描是否要解析自定义来源文本。
func (p ScanParams) UsesCustom() bool {
	return p.SourceMode == "custom" || p.SourceMode == "both"
}

// 测速范围。
const (
	// SpeedScopeSingle 只测调用方勾选的记录。
	SpeedScopeSingle = "single"
	// SpeedScopeRegion 测某个地区的全部记录。
	SpeedScopeRegion = "region"
	// SpeedScopeAll 测全部记录，并按配置决定是否分地区取 TopN。
	SpeedScopeAll = "all"
)

// SpeedParams 是一次测速的完整参数快照。
//
// 待测记录由调用方挑好后随参数一起传进来，服务端不保存「上一次扫描的结果
// 集」：那会让测速依赖隐式的会话状态，重连、切页、重启之后行为都不一样。
type SpeedParams struct {
	// Scope 是测速范围，取值见 SpeedScope* 常量。它只影响 TopN 是否生效。
	Scope string `json:"scope"`
	// Targets 是本次要测的记录，已由调用方按范围挑好。
	Targets []IPRecord `json:"targets"`
	// IPVersion 是这批目标属于 4 还是 6，决定历史归档到哪一类。
	//
	// 不从 Targets 里现推：目标可能混着两个版本，而「这次任务是在哪个模式下
	// 跑的」是调用方本来就掌握的信息。
	IPVersion int `json:"ip_version"`

	URLMode           string  `json:"url_mode"`            // auto | official | mobile_friendly | mobile_only | custom
	CustomURL         string  `json:"custom_url"`          // URLMode = custom 时的测速地址
	UseTLS            string  `json:"use_tls"`             // auto | true | false
	Concurrency       int     `json:"concurrency"`         // 测速并发
	TargetQualified   int     `json:"target_qualified"`    // 收够多少个合格结果就提前收敛
	IntervalMS        int     `json:"interval_ms"`         // 相邻两次测速之间的间隔
	MinSpeed          float64 `json:"min_speed"`           // 合格线（MB/s）
	WeightSpeed       float64 `json:"weight_speed"`        // 评分权重：速度
	WeightLatency     float64 `json:"weight_latency"`      // 评分权重：延迟
	WeightJitter      float64 `json:"weight_jitter"`       // 评分权重：抖动
	PerRegionTopN     int     `json:"per_region_topn"`     // 完全测速时每个地区取前 N 个（0 = 不限）
	DownloadDurationS int     `json:"download_duration_s"` // 单个目标的下载时长
	MaxDownloadMB     int     `json:"max_download_mb"`     // 单个目标的下载量上限（0 = 不限）
	Breaker429        int     `json:"breaker_429"`         // 连续多少次限流即熔断
	UsabilityCheck    bool    `json:"usability_check"`     // 测速前是否先做可用性校验
	TimeoutMS         int     `json:"timeout_ms"`          // 可用性校验的单次超时
}

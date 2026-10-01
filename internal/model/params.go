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

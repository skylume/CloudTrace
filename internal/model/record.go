package model

// Unreachable 是延迟字段的哨兵值：探测全部失败时延迟置为该值。
//
// 用 -1 而不是 0 是必要的：0 是一个合法的（极快）延迟，若用 0 表示不可达，
// 排序时无法与「真的一毫秒都不到」区分。任何排序 / 统计逻辑都必须先排除
// 等于哨兵值的记录。
const Unreachable = -1.0

// IPRecord 是单个 IP 的扫描与测速结果，扫描与测速两个阶段共用同一结构。
//
// 字段名即前后端契约字段名，不做转换。
type IPRecord struct {
	IP     string `json:"ip"`
	Port   int    `json:"port"`
	UseTLS bool   `json:"use_tls"`

	// 延迟统计，单位毫秒。全部失败时 Latency / LatencyAvg / LatencyMax
	// 均为 Unreachable，Jitter 为 0，Loss 为 1。
	Latency    float64 `json:"latency"`
	LatencyAvg float64 `json:"latency_avg"`
	LatencyMax float64 `json:"latency_max"`
	Jitter     float64 `json:"jitter"` // 样本标准差
	Loss       float64 `json:"loss"`   // 0..1
	Sent       int     `json:"sent"`
	Recv       int     `json:"recv"`

	Colo       string `json:"colo"`        // 数据中心 IATA 代码
	Loc        string `json:"loc"`         // 出口国家码
	RegionName string `json:"region_name"` // 地区中文名
	ASN        uint32 `json:"asn,omitempty"`
	ASOrg      string `json:"as_org,omitempty"`
	GeoWarn    string `json:"geo_warn,omitempty"`

	SpeedMBps float64 `json:"speed_mbps"`
	Score     float64 `json:"score"`

	// Trace 保存 trace 全字段，仅在开启节点明细采集时填充。
	Trace map[string]string `json:"trace,omitempty"`
}

// Reachable 报告该记录是否探测成功过。
func (r IPRecord) Reachable() bool {
	return r.Recv > 0 && r.Latency != Unreachable
}

// Summary 是一次任务的统计摘要。
type Summary struct {
	Funnel Funnel `json:"funnel"`

	RegionDist map[string]int `json:"region_dist"` // colo → 数量
	ASNDist    map[string]int `json:"asn_dist"`    // AS 组织 → 数量
	MinLatency float64        `json:"min_latency"`
	AvgLatency float64        `json:"avg_latency"`
	BestSpeed  float64        `json:"best_speed"`
	AvgSpeed   float64        `json:"avg_speed"`
	Qualified  int            `json:"qualified"` // 测速合格数
	Total      int            `json:"total"`
}

// NewSummary 返回可直接累加的摘要（两个分布 map 已初始化）。
func NewSummary() Summary {
	return Summary{
		RegionDist: map[string]int{},
		ASNDist:    map[string]int{},
	}
}

// Summarize 从结果集汇总统计。
//
// 不可达记录计入 Total，但不参与延迟与速度的平均；因此全部不可达时
// MinLatency / AvgLatency / BestSpeed / AvgSpeed 保持哨兵值或 0。
func Summarize(records []IPRecord) Summary {
	s := NewSummary()
	s.Total = len(records)

	var latencySum float64
	var latencyCount int
	var speedSum float64
	var speedCount int

	for _, r := range records {
		if r.Colo != "" {
			s.RegionDist[r.Colo]++
		}
		if r.ASOrg != "" {
			s.ASNDist[r.ASOrg]++
		}
		if r.Reachable() {
			if latencyCount == 0 || r.Latency < s.MinLatency {
				s.MinLatency = r.Latency
			}
			latencySum += r.LatencyAvg
			latencyCount++
		}
		if r.SpeedMBps > 0 {
			if r.SpeedMBps > s.BestSpeed {
				s.BestSpeed = r.SpeedMBps
			}
			speedSum += r.SpeedMBps
			speedCount++
		}
	}

	if latencyCount > 0 {
		s.AvgLatency = latencySum / float64(latencyCount)
	} else {
		s.MinLatency = Unreachable
	}
	if speedCount > 0 {
		s.AvgSpeed = speedSum / float64(speedCount)
	}
	return s
}

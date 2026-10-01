package model

// 任务阶段（TaskState.Phase）。
const (
	PhaseIdle  = "idle"
	PhaseScan  = "scan"
	PhaseSpeed = "speed"
)

// 任务状态（TaskState.Status），对应任务状态机：
//
//	Idle ──start──► Running ──┬──complete──► Done    （写历史）
//	                          ├──abort─────► Aborted （保留部分结果，不写历史）
//	                          └──error─────► Failed
const (
	StatusIdle    = "idle"
	StatusRunning = "running"
	StatusDone    = "done"
	StatusAborted = "aborted"
	StatusFailed  = "failed"
)

// Funnel 是扫描漏斗计数：生成 → 延迟达标 → 地区解析 → 可用。
//
// 这是独立类型，**不要复用 Summary**：Summary 自身包含 Funnel，
// 若把 TaskState.Funnel 声明为 Summary 会造成语义错位、前端字段对不上。
type Funnel struct {
	Generated int `json:"generated"`  // 生成候选
	LatencyOK int `json:"latency_ok"` // 延迟达标
	RegionOK  int `json:"region_ok"`  // 地区解析成功
	Usable    int `json:"usable"`     // 最终可用
}

// TaskState 是任务状态快照。
//
// 首连 / 断线重连时由服务端全量下发，前端据此恢复完整视图，
// 保证切页、刷新、断线重连都不丢进度。
type TaskState struct {
	Phase     string  `json:"phase"`      // idle | scan | speed
	Status    string  `json:"status"`     // idle | running | done | aborted | failed
	Done      int     `json:"done"`       // 已完成数
	Total     int     `json:"total"`      // 总数
	Elapsed   float64 `json:"elapsed_s"`  // 已耗时（秒）
	ETA       float64 `json:"eta_s"`      // 预计剩余（秒）
	Funnel    Funnel  `json:"funnel"`     // 实时漏斗
	Preset    string  `json:"preset"`     // 当前档位名
	StartedAt int64   `json:"started_at"` // Unix 秒；0 表示尚未开始
	ErrorMsg  string  `json:"error,omitempty"`
}

// IdleState 返回空闲态快照，用于服务启动时以及任务结束后复位。
func IdleState() TaskState {
	return TaskState{
		Phase:  PhaseIdle,
		Status: StatusIdle,
	}
}

// IsRunning 报告任务是否正在执行。
func (s TaskState) IsRunning() bool {
	return s.Status == StatusRunning
}

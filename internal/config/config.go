package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"

	"cloudtrace/internal/model"
)

// 并发与采样的上限口径，四层含义必须区分：
//
//   - MaxWorkersCeiling 是 **net.max_workers 自身能取到的最大值**。它只是防手滑的
//     护栏（填个天文数字会真的去开那么多协程），不是产品意义上的上限——真正决定
//     能开多少并发的是 net.max_workers，而那个值用户自己可以调。
//   - MaxWorkersDefault 是 **net.max_workers 的默认值**，同时就是默认能填到的
//     最大并发。它不能取内置档位的 200：档位上限是「一键填充时最多给多少」，
//     与「用户自己最多能填多少」是两件事，混在一起会让一份本来合法的配置
//     被判成非法。
//   - MaxWorkersPreset 是**内置档位上限**（「不再提供 400 档」的原意）。
//   - WarnWorkers 是**警示阈值**：允许手填，但界面显示警示色、配置体检告警。
//
// 并发这件事刻意不设死上限：网卡、路由器、运营商各不相同，能跑多少只有用户
// 自己知道。界面的职责是把他选的代价说清楚（警示色 + 后果说明），不是替他决定。
const (
	MaxWorkersCeiling = 10000
	MaxWorkersDefault = 2000
	MaxWorkersPreset  = 200
	WarnWorkers       = 300
	MaxSampleMax      = 5000
	WarnLatency       = 100
)

// 默认值。
const (
	DefaultPort          = 17443
	DefaultKeepCount     = 20
	DefaultSessionTTLMin = 720
)

// Config 是全部可配置项的强类型值对象。
//
// 它是**纯数据**，不携带锁与路径；需要并发读写时请使用 Store。
type Config struct {
	Scan     ScanConfig     `json:"scan"`
	Speed    SpeedConfig    `json:"speed"`
	Source   SourceConfig   `json:"source"`
	Net      NetConfig      `json:"net"`
	Geo      GeoConfig      `json:"geo"`
	History  HistoryConfig  `json:"history"`
	Data     DataConfig     `json:"data"`
	Export   ExportConfig   `json:"export"`
	UI       UIConfig       `json:"ui"`
	Server   ServerConfig   `json:"server"`
	Notify   NotifyConfig   `json:"notify"`
	Advanced AdvancedConfig `json:"advanced"`

	// Origins 记录每个参数的来源（default / preset / user），
	// 是自适应能否改动某参数的判定依据。
	Origins model.ParamOrigins `json:"origins,omitempty"`
}

// ScanConfig 是扫描相关配置（前缀 scan.）。
type ScanConfig struct {
	Mode             string   `json:"mode"`              // tcping | httping
	Workers          int      `json:"workers"`           // 并发 worker 数
	SampleMax        int      `json:"sample_max"`        // 采样上限，0 = 不限制
	LatencyThreshold int      `json:"latency_threshold"` // 延迟阈值 ms
	PingTimes        int      `json:"ping_times"`        // 每 IP 探测次数（精扫阶段）
	Port             int      `json:"port"`              // 默认端口
	SourceMode       string   `json:"source_mode"`       // official | custom | both
	CustomSource     string   `json:"custom_source"`     // 多形态来源文本
	PreFilterPorts   []int    `json:"pre_filter_ports"`  // 前置端口过滤
	AllowedRegions   []string `json:"allowed_regions"`   // 地区白名单
	BlockedRegions   []string `json:"blocked_regions"`   // 地区黑名单
	TwoPhase         bool     `json:"two_phase"`         // 两阶段扫描（默认开启）
	VerifyNodes      bool     `json:"verify_nodes"`      // 节点明细采集
	UsabilityCheck   bool     `json:"usability_check"`   // 测速前可用性校验
	TimeoutMS        int      `json:"timeout_ms"`        // 单次探测超时
	Retry            int      `json:"retry"`             // 探测失败重试次数
}

// HistoryConfig 是历史记录相关配置（前缀 history.）。
type HistoryConfig struct {
	KeepCount int    `json:"keep_count"` // 保留份数（默认 20）
	KeepMode  string `json:"keep_mode"`  // count | days
	KeepDays  int    `json:"keep_days"`  // keep_mode = days 时生效
	AutoDedup bool   `json:"auto_dedup"` // 同参数短时间内重复存档合并 / 跳过
	AutoSave  bool   `json:"auto_save"`  // 任务完成后自动存档
}

// DataConfig 是数据目录相关配置（前缀 data.）。
type DataConfig struct {
	Dir      string `json:"dir"`      // 空 = 按 Portable 决定
	Portable bool   `json:"portable"` // 便携模式（默认 true，数据落在 exe 同级 data/）
}

// UIConfig 是界面相关配置（前缀 ui.）。
type UIConfig struct {
	Theme               string `json:"theme"`                 // dark | light | system
	Lang                string `json:"lang"`                  // zh | en
	FontScale           string `json:"font_scale"`            // small | medium | large
	Density             string `json:"density"`               // auto | simple | advanced
	TableDensity        string `json:"table_density"`         // compact | normal | comfortable
	PageSize            int    `json:"page_size"`             // 每页条数
	TimeFormat          string `json:"time_format"`           // local | utc
	StartPage           string `json:"start_page"`            // 启动页面
	Animation           bool   `json:"animation"`             // 动效开关（可访问性）
	Contrast            bool   `json:"contrast"`              // 高对比度（可访问性，与深浅正交）
	RememberState       bool   `json:"remember_state"`        // 状态记忆
	AdaptiveEnabled     bool   `json:"adaptive_enabled"`      // 自适应总开关
	AdaptiveAllowPreset bool   `json:"adaptive_allow_preset"` // 是否允许调整内置档位填入的值
	// CloseToTray 决定关掉窗口是收进托盘还是退出。
	//
	// 默认收进托盘：扫描要跑几分钟，用户关窗口多半是想让它去后台跑，而不是
	// 把跑到一半的任务掐掉。真要退出，托盘菜单里有「退出」。
	CloseToTray bool `json:"close_to_tray"`
}

// ServerConfig 是服务面板相关配置（前缀 server.）。
type ServerConfig struct {
	Port          int    `json:"port"`
	Bind          string `json:"bind"`            // 127.0.0.1 | 0.0.0.0
	Token         string `json:"token"`           // 访问 Token（首次启动自动生成）
	SessionTTLMin int    `json:"session_ttl_min"` // 会话 TTL（分钟）
	OpenBrowser   bool   `json:"open_browser"`
	Autostart     bool   `json:"autostart"`
}

// AdvancedConfig 是高级与调试相关配置（前缀 advanced.）。
type AdvancedConfig struct {
	LogLevel     string          `json:"log_level"` // debug | info | warn | error
	LogKeepDays  int             `json:"log_keep_days"`
	Experimental map[string]bool `json:"experimental"`
	CheckUpdate  bool            `json:"check_update"`
}

// Default 返回内置默认配置。
//
// 硬性要求：这份默认值必须能直接跑出结果——用户不做任何设置，
// 点一下就能拿到可用 IP。
func Default() Config {
	return Config{
		Scan: ScanConfig{
			Mode:             "tcping",
			Workers:          200,
			SampleMax:        MaxSampleMax,
			LatencyThreshold: 230,
			PingTimes:        4,
			Port:             443,
			SourceMode:       "official",
			CustomSource:     "",
			PreFilterPorts:   []int{},
			AllowedRegions:   []string{},
			BlockedRegions:   []string{},
			TwoPhase:         true,
			VerifyNodes:      true,
			UsabilityCheck:   true,
			TimeoutMS:        1000,
			Retry:            0,
		},
		Speed:  defaultSpeed(),
		Source: defaultSource(),
		Net:    defaultNet(),
		Geo:    defaultGeo(),
		History: HistoryConfig{
			KeepCount: DefaultKeepCount,
			KeepMode:  "count",
			KeepDays:  90,
			AutoDedup: true,
			AutoSave:  true,
		},
		Data: DataConfig{
			Dir:      "",
			Portable: true,
		},
		Export: defaultExport(),
		UI: UIConfig{
			Theme:               "system",
			Lang:                "zh",
			FontScale:           "medium",
			Density:             "auto",
			TableDensity:        "normal",
			PageSize:            50,
			TimeFormat:          "local",
			StartPage:           "scan",
			Animation:           true,
			RememberState:       true,
			CloseToTray:         true,
			AdaptiveEnabled:     true,
			AdaptiveAllowPreset: true,
		},
		Server: ServerConfig{
			Port:          DefaultPort,
			Bind:          "127.0.0.1",
			Token:         "",
			SessionTTLMin: DefaultSessionTTLMin,
			OpenBrowser:   true,
			Autostart:     false,
		},
		Notify: defaultNotify(),
		Advanced: AdvancedConfig{
			LogLevel:     "info",
			LogKeepDays:  7,
			Experimental: map[string]bool{},
			CheckUpdate:  false,
		},
		Origins: model.ParamOrigins{},
	}
}

// normalize 补齐零值：保证切片与 map 非 nil，枚举落到默认值。
//
// 目的：让 JSON 往返稳定（nil 与 [] 的差异会导致「读→写→读」不相等），
// 并让手写的不完整配置文件也能正常工作。
func (c *Config) normalize() {
	d := Default()

	if c.Scan.PreFilterPorts == nil {
		c.Scan.PreFilterPorts = []int{}
	}
	if c.Scan.AllowedRegions == nil {
		c.Scan.AllowedRegions = []string{}
	}
	if c.Scan.BlockedRegions == nil {
		c.Scan.BlockedRegions = []string{}
	}
	if c.Scan.Mode == "" {
		c.Scan.Mode = d.Scan.Mode
	}
	if c.Scan.SourceMode == "" {
		c.Scan.SourceMode = d.Scan.SourceMode
	}
	if c.Scan.Port == 0 {
		c.Scan.Port = d.Scan.Port
	}
	if c.Scan.PingTimes == 0 {
		c.Scan.PingTimes = d.Scan.PingTimes
	}
	if c.Scan.TimeoutMS == 0 {
		c.Scan.TimeoutMS = d.Scan.TimeoutMS
	}

	c.Speed.normalize()
	c.Source.normalize()
	c.Net.normalize()
	c.Geo.normalize()

	if c.History.KeepMode == "" {
		c.History.KeepMode = d.History.KeepMode
	}
	if c.History.KeepDays == 0 {
		c.History.KeepDays = d.History.KeepDays
	}

	c.Export.normalize()

	if c.UI.Theme == "" {
		c.UI.Theme = d.UI.Theme
	}
	if c.UI.Lang == "" {
		c.UI.Lang = d.UI.Lang
	}
	if c.UI.FontScale == "" {
		c.UI.FontScale = d.UI.FontScale
	}
	if c.UI.Density == "" {
		c.UI.Density = d.UI.Density
	}
	if c.UI.TableDensity == "" {
		c.UI.TableDensity = d.UI.TableDensity
	}
	if c.UI.PageSize == 0 {
		c.UI.PageSize = d.UI.PageSize
	}
	if c.UI.TimeFormat == "" {
		c.UI.TimeFormat = d.UI.TimeFormat
	}
	if c.UI.StartPage == "" {
		c.UI.StartPage = d.UI.StartPage
	}

	if c.Server.Port == 0 {
		c.Server.Port = d.Server.Port
	}
	if c.Server.Bind == "" {
		c.Server.Bind = d.Server.Bind
	}
	if c.Server.SessionTTLMin == 0 {
		c.Server.SessionTTLMin = d.Server.SessionTTLMin
	}

	if c.Advanced.LogLevel == "" {
		c.Advanced.LogLevel = d.Advanced.LogLevel
	}
	if c.Advanced.LogKeepDays == 0 {
		c.Advanced.LogKeepDays = d.Advanced.LogKeepDays
	}
	if c.Advanced.Experimental == nil {
		c.Advanced.Experimental = map[string]bool{}
	}

	if c.Origins == nil {
		c.Origins = model.ParamOrigins{}
	}
}

// EnsureToken 在 Token 为空时生成一个 32 字节随机 hex 的访问令牌。
//
// 返回是否发生了生成。首次启动时调用。
func (c *ServerConfig) EnsureToken() (bool, error) {
	if c.Token != "" {
		return false, nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return false, err
	}
	c.Token = hex.EncodeToString(buf)
	return true, nil
}

// Clone 返回深拷贝，避免调用方意外共享切片与 map。
func (c Config) Clone() Config {
	out := c
	out.Speed = c.Speed.clone()
	out.Source = c.Source.clone()
	out.Net = c.Net.clone()
	out.Geo = c.Geo.clone()
	out.Export = c.Export.clone()
	out.Notify = c.Notify.clone()
	out.Scan.PreFilterPorts = append([]int(nil), c.Scan.PreFilterPorts...)
	out.Scan.AllowedRegions = append([]string(nil), c.Scan.AllowedRegions...)
	out.Scan.BlockedRegions = append([]string(nil), c.Scan.BlockedRegions...)
	if c.Advanced.Experimental != nil {
		out.Advanced.Experimental = make(map[string]bool, len(c.Advanced.Experimental))
		for k, v := range c.Advanced.Experimental {
			out.Advanced.Experimental[k] = v
		}
	}
	out.Origins = c.Origins.Clone()
	return out
}

// MarshalJSON 保证输出稳定（normalize 后再序列化）。
func (c Config) MarshalJSON() ([]byte, error) {
	type alias Config
	n := c.Clone()
	n.normalize()
	return json.Marshal(alias(n))
}

// UnmarshalJSON 保证读到缺失字段时补默认值。
func (c *Config) UnmarshalJSON(data []byte) error {
	type alias Config
	tmp := alias(Default())
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}
	*c = Config(tmp)
	c.normalize()
	return nil
}

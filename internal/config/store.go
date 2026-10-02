package config

import (
	"encoding/json"
	"errors"
	"os"
	"sync"

	"cloudtrace/internal/model"
)

// Store 是配置的唯一数据源。
//
// 职责：
//   - 持有当前 Config，提供并发安全的读写；
//   - 任何修改都走「校验 → 原子写 → 广播」三步，杜绝双端互相覆盖；
//   - 保留尚未实现的顶层分组（见 extra），保证读→写不丢数据。
type Store struct {
	mu      sync.RWMutex
	path    string
	exeDir  string
	cfg     Config
	extra   map[string]json.RawMessage
	warns   []string
	onSaved func(Config)
}

// knownSections 是当前 Config 已实现的顶层分组。
//
// 不在其中的分组会被原样保留在 extra 里，避免「尚未实现的分组被一次
// 保存抹掉」——例如后续才加入的 speed.* / geo.* 配置。
var knownSections = map[string]bool{
	"scan":     true,
	"speed":    true,
	"source":   true,
	"net":      true,
	"geo":      true,
	"history":  true,
	"data":     true,
	"export":   true,
	"ui":       true,
	"server":   true,
	"notify":   true,
	"advanced": true,
	"origins":  true,
}

// OpenStore 打开（或初始化）配置。
//
// 配置文件损坏时不报错，而是回退默认值并把原因记入 Warnings()。
func OpenStore(path, exeDir string) (*Store, error) {
	s := &Store{
		path:   path,
		exeDir: exeDir,
		extra:  map[string]json.RawMessage{},
	}

	cfg, err := LoadFile(path)
	if err != nil {
		var ce *CorruptedError
		if !errors.As(err, &ce) {
			return nil, err
		}
		s.warns = append(s.warns, ce.Error())
	}
	s.cfg = cfg

	// 读取原始 JSON，保存尚未实现的分组。
	if data, rerr := os.ReadFile(path); rerr == nil {
		var raw map[string]json.RawMessage
		if jerr := json.Unmarshal(data, &raw); jerr == nil {
			for k, v := range raw {
				if !knownSections[k] {
					s.extra[k] = v
				}
			}
		}
	}
	return s, nil
}

// Path 返回配置文件路径。
func (s *Store) Path() string { return s.path }

// ExeDir 返回可执行文件所在目录。
func (s *Store) ExeDir() string { return s.exeDir }

// Warnings 返回启动期的非致命问题（如配置文件损坏已回退）。
func (s *Store) Warnings() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.warns...)
}

// Get 返回当前配置的深拷贝，调用方可随意修改。
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.Clone()
}

// DataDir 返回解析后的数据目录绝对路径。
func (s *Store) DataDir() (string, error) {
	return ResolveDataDir(s.Get(), s.exeDir)
}

// OnSaved 注册保存后的回调（用于广播 settings 事件）。
func (s *Store) OnSaved(fn func(Config)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onSaved = fn
}

// Set 整体替换配置：校验 → 原子写 → 回调。
func (s *Store) Set(cfg Config) (Config, error) {
	next := cfg.Clone()
	next.normalize()
	if err := next.Validate(); err != nil {
		return Config{}, err
	}
	if err := s.persist(next); err != nil {
		return Config{}, err
	}
	return next.Clone(), nil
}

// Patch 局部更新配置。
//
// patch 是嵌套对象，例如 {"scan": {"workers": 100}}；
// origins 是参数来源表（点号路径 → default/preset/user），
// 只需包含本次修改的键，未列出的键保持原值。
func (s *Store) Patch(patch map[string]any, origins model.ParamOrigins) (Config, error) {
	if len(patch) == 0 && len(origins) == 0 {
		return s.Get(), nil
	}

	cur := s.Get()

	merged, err := mergePatch(cur, patch)
	if err != nil {
		return Config{}, err
	}
	if len(origins) > 0 {
		if merged.Origins == nil {
			merged.Origins = model.ParamOrigins{}
		}
		for k, v := range origins {
			merged.Origins.Set(k, v)
		}
	}
	return s.Set(merged)
}

// Reload 从磁盘重新读取配置（用于外部修改后刷新）。
func (s *Store) Reload() (Config, error) {
	cfg, err := LoadFile(s.path)
	if err != nil {
		var ce *CorruptedError
		if !errors.As(err, &ce) {
			return Config{}, err
		}
	}
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
	return cfg.Clone(), nil
}

func (s *Store) persist(cfg Config) error {
	if err := writeMerged(s.path, cfg, s.extraSnapshot()); err != nil {
		return err
	}
	s.mu.Lock()
	s.cfg = cfg
	fn := s.onSaved
	s.mu.Unlock()
	if fn != nil {
		fn(cfg.Clone())
	}
	return nil
}

func (s *Store) extraSnapshot() map[string]json.RawMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]json.RawMessage, len(s.extra))
	for k, v := range s.extra {
		out[k] = v
	}
	return out
}

// writeMerged 写出配置，并把 extra 中的未知分组一并保留。
func writeMerged(path string, cfg Config, extra map[string]json.RawMessage) error {
	n := cfg.Clone()
	n.normalize()

	base, err := json.Marshal(n)
	if err != nil {
		return err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(base, &top); err != nil {
		return err
	}
	for k, v := range extra {
		if _, exists := top[k]; !exists {
			top[k] = v
		}
	}

	data, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileAtomic(path, data, filePerm)
}

// mergePatch 把 patch 深度合并进 base 配置，返回新配置。
//
// 注意：不能直接把 patch 反序列化进 Config —— Config.UnmarshalJSON 会从
// 默认值起步，那样未出现在 patch 里的字段会被重置（旧项目「改一个键
// 把其他键打回默认」的经典 bug）。这里先在 JSON 对象层做深度合并。
func mergePatch(base Config, patch map[string]any) (Config, error) {
	baseBytes, err := json.Marshal(base)
	if err != nil {
		return Config{}, err
	}
	var obj map[string]any
	if err := json.Unmarshal(baseBytes, &obj); err != nil {
		return Config{}, err
	}
	deepMerge(obj, patch)

	merged, err := json.Marshal(obj)
	if err != nil {
		return Config{}, err
	}
	var out Config
	if err := json.Unmarshal(merged, &out); err != nil {
		// 归到校验错误而不是 IO 错误：补丁结构与配置对不上是调用方写错了，
		// 前端该做的是把它标在字段上，而不是提示「文件读写失败」。
		return Config{}, &ValidationError{Fields: []FieldError{
			{Key: "patch", Value: patch, Reason: "补丁与配置结构不符：" + err.Error()},
		}}
	}
	return out, nil
}

func deepMerge(dst, src map[string]any) {
	for k, sv := range src {
		if sm, ok := sv.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				deepMerge(dm, sm)
				continue
			}
		}
		dst[k] = sv
	}
}

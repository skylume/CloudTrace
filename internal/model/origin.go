package model

// ParamOrigin 是参数来源三态，决定自适应逻辑能否改动该参数。
//
// 规则（不可违反）：
//   - 自适应逻辑**只能修改** OriginDefault / OriginPreset 的参数；
//   - OriginUser 的参数一个字节都不改，只能产生「建议」；
//   - 用户保存的「我的档位」载入的参数一律标记为 OriginUser。
type ParamOrigin string

const (
	// OriginDefault 系统内置默认值，用户从未碰过。
	OriginDefault ParamOrigin = "default"
	// OriginPreset 由内置档位（快速 / 标准 / 精细）填入。
	OriginPreset ParamOrigin = "preset"
	// OriginUser 用户手改过，或来自「我的档位」。
	OriginUser ParamOrigin = "user"
)

// ParamOrigins 是参数来源表。
//
// key 为参数路径（点号分隔），例如 "scan.workers"、"speed.concurrency"。
type ParamOrigins map[string]ParamOrigin

// Clone 返回副本，避免调用方共享底层 map。
func (p ParamOrigins) Clone() ParamOrigins {
	if p == nil {
		return nil
	}
	out := make(ParamOrigins, len(p))
	for k, v := range p {
		out[k] = v
	}
	return out
}

// Get 读取来源；键不存在或为空时返回 OriginDefault（等价于「用户从未碰过」）。
func (p ParamOrigins) Get(key string) ParamOrigin {
	if v, ok := p[key]; ok && v != "" {
		return v
	}
	return OriginDefault
}

// CanAutoAdjust 判断自适应是否允许修改该参数。
//
// 这是「自动逻辑绝不覆盖用户的显式选择」的唯一判定入口，
// 任何自动调整参数的代码都必须先过这一关。
func (p ParamOrigins) CanAutoAdjust(key string) bool {
	return p.Get(key) != OriginUser
}

// Set 写入来源；value 为空时按 OriginDefault 处理。
func (p ParamOrigins) Set(key string, value ParamOrigin) {
	if key == "" {
		return
	}
	if value == "" {
		value = OriginDefault
	}
	p[key] = value
}

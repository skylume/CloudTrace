// Package adaptive 按网络环境调整参数，并守住一条红线：
// **绝不覆盖用户的显式选择**。
//
// 它只做判定，不碰配置——判定结果要么是「静默调整」（可还原、有徽标、有日志），
// 要么是「只给建议」，要么是「什么都不做」。把判定与写入分开，是为了让
// 「不许改用户的值」这条规则能被结构性保证：唯一能拿到写入内容的入口是
// Decision.Patch，而它对非调整动作返回空。
package adaptive

import (
	"fmt"
	"strings"

	"cloudtrace/internal/model"
)

// Signal 是触发自适应的网络信号。
type Signal string

const (
	// SignalMobileISP 是探测到出口属于移动宽带。
	SignalMobileISP Signal = "mobile_isp"
	// SignalHighTimeout 是扫描过程中超时率过高。
	SignalHighTimeout Signal = "high_timeout"
)

// Action 是对一个参数的处置。
type Action string

const (
	// ActionNone 表示不需要做任何事。
	ActionNone Action = "none"
	// ActionAdjust 表示静默调整：可以改，但必须有徽标、可还原、留日志。
	ActionAdjust Action = "adjust"
	// ActionSuggest 表示只给建议：值一个字节都不改，等用户自己点。
	ActionSuggest Action = "suggest"
)

// Options 是全局开关。
type Options struct {
	// Enabled 对应 ui.adaptive_enabled；关掉后只提示、不动手。
	Enabled bool
	// AllowPreset 对应 ui.adaptive_allow_preset；关掉后内置档位填的值也不改。
	AllowPreset bool
}

// Request 是一次判定所需的全部输入。
type Request struct {
	// Key 是配置里的点号路径，如 scan.workers。
	Key string
	// Current 是当前值。
	Current int
	// Origin 是这个值的来源。user 来源永不被调整。
	Origin  model.ParamOrigin
	Signal  Signal
	Options Options
}

// Decision 是判定结果。
type Decision struct {
	Action Action
	// From 是判定时的当前值，供事件与日志使用。
	From int
	// To 是建议或调整后的值；Action 为 None 时与 From 相同。
	To int
	// Key 是涉及的参数，便于调用方原样带进事件。
	Key string
	// Reason 是机器可读的原因标识，文案由界面层决定。
	Reason string
}

// 原因标识。
const (
	ReasonMobileISP   = "mobile_isp"
	ReasonHighTimeout = "high_timeout"
)

// rule 是一条自适应规则。
type rule struct {
	signal Signal
	key    string
	reason string
	// target 给出期望值；返回原值表示不需要改动。
	target func(current int) int
}

// rules 是规则表。
//
// 只收「不改会让用户拿到更差结果」的项。参数联动校验那种「改了可能更好」的
// 情形不在这里——它只提示，不参与判定。
var rules = []rule{
	{
		signal: SignalMobileISP,
		key:    "scan.workers",
		reason: ReasonMobileISP,
		// 移动宽带的上行通常更窄，并发拉满会把自己的连接压垮，结果反而更差。
		target: func(current int) int { return min(current, 80) },
	},
	{
		signal: SignalMobileISP,
		key:    "speed.concurrency",
		reason: ReasonMobileISP,
		target: func(current int) int { return min(current, 4) },
	},
	{
		signal: SignalHighTimeout,
		key:    "scan.workers",
		reason: ReasonHighTimeout,
		// 砍半而不是给固定值：原本设成 20 的用户不该被调到 80。
		target: func(current int) int { return max(50, current/2) },
	},
}

// Evaluate 判定一个参数该怎么处理。
func Evaluate(req Request) Decision {
	return evaluate(req, false)
}

/**
 * EvaluateExplicit 判定一次「用户主动要求的」调整。
 *
 * 与 Evaluate 的唯一区别是不受来源与全局开关限制：用户点了「智能推荐」，就是
 * 明确授权这一次改动，包括改他手填过的值。
 *
 * 单独一个函数而不是给 Evaluate 加个 bool 开关：它绕过的是「自动逻辑绝不覆盖
 * 用户的显式选择」那条红线，而那条红线的结构性保证正是靠 Evaluate 拿不到写入
 * 内容来兜底的。一个布尔值可能被顺手打开，一个叫 EvaluateExplicit 的调用点
 * 不会。
 */
func EvaluateExplicit(req Request) Decision {
	return evaluate(req, true)
}

// evaluate 是两者的共同实现。
func evaluate(req Request, explicit bool) Decision {
	base := Decision{Action: ActionNone, Key: req.Key, From: req.Current, To: req.Current}

	var matched *rule
	for i := range rules {
		if rules[i].signal == req.Signal && rules[i].key == req.Key {
			matched = &rules[i]
			break
		}
	}
	if matched == nil {
		return base
	}

	to := matched.target(req.Current)
	if to == req.Current {
		// 已经在合理范围内，什么都不用做——包括不提示。
		return base
	}
	base.To = to
	base.Reason = matched.reason

	if explicit {
		// 用户主动点的，一路放行。
		base.Action = ActionAdjust
		return base
	}

	// 红线：用户显式设过的值，只建议、不改。
	if req.Origin == model.OriginUser {
		base.Action = ActionSuggest
		return base
	}

	// 全局关掉之后只提示不动手。
	if !req.Options.Enabled {
		base.Action = ActionSuggest
		return base
	}

	// 内置档位填进去的值，是否允许被调整由用户单独决定。
	if req.Origin == model.OriginPreset && !req.Options.AllowPreset {
		base.Action = ActionSuggest
		return base
	}

	base.Action = ActionAdjust
	return base
}

/**
 * Patch 返回要写入配置的改动，形状与设置补丁一致（嵌套对象）。
 *
 * **唯一能拿到写入内容的入口**：非调整动作一律返回空。调用方就算把建议当成
 * 调整来处理，也拿不到可以写入的东西——「不覆盖用户的值」因此是结构性保证，
 * 而不是靠每个调用点记得判断。
 *
 * Key 是配置里的点号路径，必须在这里拆成嵌套结构：配置存储收的是嵌套对象，
 * 把 `scan.workers` 当顶层键塞进去会被静默丢掉——值一个字节没变，事件却已经
 * 发出去了，界面上于是出现「徽标显示已调整、实际没改」的假象。
 */
func (d Decision) Patch() map[string]any {
	if d.Action != ActionAdjust || d.Key == "" {
		return nil
	}

	parts := strings.Split(d.Key, ".")
	out := map[string]any{}
	cur := out
	for i, part := range parts {
		if part == "" {
			// 路径里有空段说明键写错了，宁可什么都不返回，也不要写进一个
			// 谁也认不出来的键。
			return nil
		}
		if i == len(parts)-1 {
			cur[part] = d.To
			break
		}
		next := map[string]any{}
		cur[part] = next
		cur = next
	}
	return out
}

// String 便于日志与调试。
func (d Decision) String() string {
	return fmt.Sprintf("%s %s %d->%d (%s)", d.Action, d.Key, d.From, d.To, d.Reason)
}

package adaptive

import (
	"testing"

	"cloudtrace/internal/model"
)

// 全开的开关，作为大多数用例的基线。
func allOn() Options { return Options{Enabled: true, AllowPreset: true} }

// **红线**：用户显式设过的值，只能建议、不能改。
//
// 用户原话：「移动的高级用户自定义的参数是否也会变？如果会那就不太符合设计
// 理念」。这条用例是整条红线的落点，改规则时它必须一直是绿的。
func TestUserOriginIsNeverAdjusted(t *testing.T) {
	req := Request{
		Key:     "scan.workers",
		Current: 200,
		Origin:  model.OriginUser,
		Signal:  SignalMobileISP,
		Options: allOn(),
	}

	got := Evaluate(req)

	if got.Action != ActionSuggest {
		t.Fatalf("动作 = %q，期望 %q（用户设过的值只能建议）", got.Action, ActionSuggest)
	}
	if got.To != 80 {
		t.Errorf("建议值 = %d，期望 80", got.To)
	}
	// 结构性保证：非调整动作拿不到可以写入的东西。
	if patch := got.Patch(); patch != nil {
		t.Fatalf("建议动作给出了可写入的改动：%v", patch)
	}
}

// 其他来源在开关全开时会被静默调整。
func TestDefaultOriginIsAdjusted(t *testing.T) {
	for _, origin := range []model.ParamOrigin{model.OriginDefault, model.OriginPreset} {
		got := Evaluate(Request{
			Key:     "scan.workers",
			Current: 200,
			Origin:  origin,
			Signal:  SignalMobileISP,
			Options: allOn(),
		})

		if got.Action != ActionAdjust {
			t.Errorf("%s 来源的动作 = %q，期望 %q", origin, got.Action, ActionAdjust)
		}
		if patch := got.Patch(); inner(patch)["workers"] != 80 {
			t.Errorf("%s 来源的改动 = %v，期望 scan.workers=80", origin, patch)
		}
	}
}

/**
 * 补丁必须是嵌套对象，不是点号扁平键。
 *
 * 配置存储收的是嵌套结构；把 `scan.workers` 当顶层键塞进去会被静默丢掉——
 * 值一个字节没变，事件却已经发出去了，界面上于是出现「徽标显示已调整、实际
 * 没改」的假象。这个错误真实存在过，而且看起来一切正常。
 */
func TestPatchIsNested(t *testing.T) {
	got := EvaluateExplicit(Request{
		Key:     "scan.workers",
		Current: 300,
		Signal:  SignalMobileISP,
		Options: allOn(),
	})

	patch := got.Patch()
	inner, ok := patch["scan"].(map[string]any)
	if !ok {
		t.Fatalf("补丁不是嵌套结构：%v", patch)
	}
	if inner["workers"] != 80 {
		t.Errorf("scan.workers = %v，期望 80", inner["workers"])
	}
	if _, flat := patch["scan.workers"]; flat {
		t.Error("补丁里不该出现点号扁平键：那样写不进配置")
	}
}

// inner 取出补丁里的分组，顺带把「不是嵌套结构」这件事在断言里写清楚。
func inner(patch map[string]any) map[string]any {
	group, _ := patch["scan"].(map[string]any)
	return group
}

// 关掉自适应之后只提示不动手。
func TestDisabledOnlySuggests(t *testing.T) {
	got := Evaluate(Request{
		Key:     "scan.workers",
		Current: 200,
		Origin:  model.OriginDefault,
		Signal:  SignalMobileISP,
		Options: Options{Enabled: false, AllowPreset: true},
	})

	if got.Action != ActionSuggest {
		t.Fatalf("动作 = %q，期望 %q", got.Action, ActionSuggest)
	}
	if got.Patch() != nil {
		t.Fatal("关掉自适应后不该给出可写入的改动")
	}
}

// 不许改内置档位的值时，档位来源的参数也只建议。
func TestPresetOriginRespectsAllowPreset(t *testing.T) {
	got := Evaluate(Request{
		Key:     "scan.workers",
		Current: 200,
		Origin:  model.OriginPreset,
		Signal:  SignalMobileISP,
		Options: Options{Enabled: true, AllowPreset: false},
	})

	if got.Action != ActionSuggest {
		t.Fatalf("动作 = %q，期望 %q", got.Action, ActionSuggest)
	}
	// default 来源不受这个开关影响——它本来就是「没人设过」的值。
	other := Evaluate(Request{
		Key:     "scan.workers",
		Current: 200,
		Origin:  model.OriginDefault,
		Signal:  SignalMobileISP,
		Options: Options{Enabled: true, AllowPreset: false},
	})
	if other.Action != ActionAdjust {
		t.Errorf("default 来源被 allow_preset 误伤：%q", other.Action)
	}
}

// 已经在合理范围内时什么都不做，连提示都不给。
func TestNoActionWhenAlreadyReasonable(t *testing.T) {
	got := Evaluate(Request{
		Key:     "scan.workers",
		Current: 60,
		Origin:  model.OriginDefault,
		Signal:  SignalMobileISP,
		Options: allOn(),
	})

	if got.Action != ActionNone {
		t.Fatalf("动作 = %q，期望 %q（60 已经低于建议上限）", got.Action, ActionNone)
	}
	if got.Patch() != nil {
		t.Error("不需要改动时不该给出改动")
	}
}

// 超时率过高时砍半，而不是给一个固定值。
//
// 原本设成 20 的用户不该被调到 80——那会让他的问题变得更糟。
func TestHighTimeoutHalvesInsteadOfPinning(t *testing.T) {
	got := Evaluate(Request{
		Key:     "scan.workers",
		Current: 20,
		Origin:  model.OriginDefault,
		Signal:  SignalHighTimeout,
		Options: allOn(),
	})

	if got.To != 50 {
		t.Errorf("调后 = %d，期望 50（有下限，不能砍到 10）", got.To)
	}

	big := Evaluate(Request{
		Key:     "scan.workers",
		Current: 200,
		Origin:  model.OriginDefault,
		Signal:  SignalHighTimeout,
		Options: allOn(),
	})
	if big.To != 100 {
		t.Errorf("调后 = %d，期望 100（砍半）", big.To)
	}
}

// 没有规则的信号与参数组合什么都不做。
func TestUnknownCombinationIsNoop(t *testing.T) {
	got := Evaluate(Request{
		Key:     "scan.port",
		Current: 443,
		Origin:  model.OriginDefault,
		Signal:  SignalMobileISP,
		Options: allOn(),
	})

	if got.Action != ActionNone {
		t.Fatalf("动作 = %q，期望 %q", got.Action, ActionNone)
	}
}

// 移动宽带也要压测速并发：测速是并发下载，比扫描更吃上行。
func TestMobileISPLowersSpeedConcurrency(t *testing.T) {
	got := Evaluate(Request{
		Key:     "speed.concurrency",
		Current: 16,
		Origin:  model.OriginDefault,
		Signal:  SignalMobileISP,
		Options: allOn(),
	})

	if got.Action != ActionAdjust || got.To != 4 {
		t.Fatalf("动作 = %q 调后 = %d，期望 adjust 到 4", got.Action, got.To)
	}
}

// 空来源按 default 处理：旧配置文件里没有来源标记，那些值本来就是默认值。
func TestEmptyOriginBehavesLikeDefault(t *testing.T) {
	got := Evaluate(Request{
		Key:     "scan.workers",
		Current: 200,
		Origin:  "",
		Signal:  SignalMobileISP,
		Options: allOn(),
	})

	if got.Action != ActionAdjust {
		t.Fatalf("动作 = %q，期望 %q", got.Action, ActionAdjust)
	}
}

/**
 * EvaluateExplicit 是唯一能改「用户手填过的值」的入口。
 *
 * 它对应界面上的「智能推荐」按钮：用户主动点了就是明确授权。这里把它的边界
 * 钉死——只放宽来源与开关，规则本身（什么信号该改哪个键、改成多少）与自动
 * 路径完全一致，不会因为「显式」就多改一项。
 */
func TestEvaluateExplicitOverridesUserOrigin(t *testing.T) {
	req := Request{
		Key:     "scan.workers",
		Current: 300,
		Origin:  model.OriginUser,
		Signal:  SignalMobileISP,
		Options: allOn(),
	}

	// 自动路径：只建议，拿不到可写入的内容。
	if got := Evaluate(req); got.Action != ActionSuggest || got.Patch() != nil {
		t.Fatalf("自动路径 = %q patch=%v，期望只建议", got.Action, got.Patch())
	}

	// 显式路径：放行，且拿得到写入内容。
	got := EvaluateExplicit(req)
	if got.Action != ActionAdjust {
		t.Fatalf("显式路径动作 = %q，期望 %q", got.Action, ActionAdjust)
	}
	if got.To != 80 {
		t.Errorf("显式路径调后 = %d，期望 80", got.To)
	}
	if patch := got.Patch(); inner(patch)["workers"] != 80 {
		t.Errorf("显式路径没给出可写入的内容：%v", patch)
	}
}

// 全局开关关掉之后，显式操作照样放行——用户点按钮就是授权。
func TestEvaluateExplicitIgnoresGlobalSwitch(t *testing.T) {
	got := EvaluateExplicit(Request{
		Key:     "scan.workers",
		Current: 200,
		Origin:  model.OriginPreset,
		Signal:  SignalMobileISP,
		Options: Options{Enabled: false, AllowPreset: false},
	})

	if got.Action != ActionAdjust {
		t.Fatalf("动作 = %q，期望 %q", got.Action, ActionAdjust)
	}
}

// 规则之外的东西一样不动：显式不等于「什么都能改」。
func TestEvaluateExplicitStillRespectsRules(t *testing.T) {
	cases := []struct {
		name string
		req  Request
	}{
		{"没有匹配的规则", Request{Key: "ui.page_size", Current: 100, Signal: SignalMobileISP, Options: allOn()}},
		{"信号没有对应规则", Request{Key: "scan.workers", Current: 300, Signal: Signal("unknown"), Options: allOn()}},
		{"已经在范围内", Request{Key: "scan.workers", Current: 80, Signal: SignalMobileISP, Options: allOn()}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluateExplicit(tt.req)
			if got.Action != ActionNone {
				t.Errorf("动作 = %q，期望 %q", got.Action, ActionNone)
			}
			if got.Patch() != nil {
				t.Errorf("不该给出可写入的内容：%v", got.Patch())
			}
		})
	}
}

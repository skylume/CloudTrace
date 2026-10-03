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
		patch := got.Patch()
		if patch == nil || patch["scan.workers"] != 80 {
			t.Errorf("%s 来源的改动 = %v，期望 scan.workers=80", origin, patch)
		}
	}
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

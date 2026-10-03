package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"cloudtrace/internal/model"
)

func testPresetStore(t *testing.T) *PresetStore {
	t.Helper()
	s, err := OpenPresets(filepath.Join(t.TempDir(), "presets.json"), nil)
	if err != nil {
		t.Fatalf("打开档位库失败：%v", err)
	}
	return s
}

// ---------------------------------------------------------------- 内置档位

func TestBuiltinPresetsAreThreeAndReadonly(t *testing.T) {
	builtin := BuiltinPresets()
	if len(builtin) != 3 {
		t.Fatalf("内置档位数量 = %d，期望 3", len(builtin))
	}
	for _, p := range builtin {
		if !p.Builtin {
			t.Errorf("档位 %q 的 builtin 标记应为 true", p.ID)
		}
		if len(p.Values) == 0 {
			t.Errorf("档位 %q 没有参数值", p.ID)
		}
	}
}

/**
 * 内置档位的取值不得突破上限。
 *
 * 并发只到 200、采样只到 5000——超过既没必要也拖时间，曾经还有 400 与 20000
 * 两档。
 */
func TestBuiltinPresetsStayWithinLimits(t *testing.T) {
	for _, p := range BuiltinPresets() {
		cfg := Default()
		obj, err := toObject(cfg)
		if err != nil {
			t.Fatalf("转对象失败：%v", err)
		}
		for key, value := range p.Values {
			n, ok := value.(int)
			if !ok {
				continue
			}
			switch key {
			case "scan.workers":
				if n > MaxWorkersPreset {
					t.Errorf("档位 %q 的并发 %d 超过内置档位上限 %d", p.ID, n, MaxWorkersPreset)
				}
			case "scan.sample_max":
				if n > MaxSampleMax {
					t.Errorf("档位 %q 的采样 %d 超过上限 %d", p.ID, n, MaxSampleMax)
				}
			}
			// 键本身必须存在，否则应用档位时会被静默丢掉。
			if _, ok := lookupPath(obj, splitKey(key)); !ok {
				t.Errorf("档位 %q 用了不存在的配置键 %q", p.ID, key)
			}
		}
	}
}

// 「标准」档的探测次数写的是「自动」，而「自动」就是不给这一项填值。
//
// 填 0 会被当成非法参数（曾经选中这一档一启动就失败），填任何实数又等于替
// 用户做了决定。
func TestStandardPresetLeavesPingTimesUnset(t *testing.T) {
	preset := BuiltinPresets()[1]
	if preset.ID != PresetStandard {
		t.Fatalf("第二个内置档位应为 standard，实际 %q", preset.ID)
	}
	if _, set := preset.Values["scan.ping_times"]; set {
		t.Error("标准档不应设置探测次数：那一列写的是「自动」")
	}
}

func TestBuiltinPresetsCannotBeDeletedOrOverwritten(t *testing.T) {
	s := testPresetStore(t)

	if err := s.Delete(PresetFast); err == nil {
		t.Error("删除内置档位应被拒绝")
	}
	if err := s.Save(Preset{ID: PresetFast, Name: "我的快速", Values: map[string]any{"scan.workers": 42}}); err == nil {
		t.Error("覆盖内置档位应被拒绝")
	}
}

// ---------------------------------------------------------------- 自定义档位

func TestSaveAndReloadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.json")
	s, err := OpenPresets(path, nil)
	if err != nil {
		t.Fatalf("打开档位库失败：%v", err)
	}

	want := Preset{
		ID:      "office",
		Name:    "公司网络",
		Note:    "上行窄，并发压低",
		Icon:    "building",
		Color:   "#4f46e5",
		Order:   3,
		Values:  map[string]any{"scan.workers": 60, "scan.latency_threshold": 260},
		Origins: model.ParamOrigins{"scan.workers": model.OriginUser},
	}
	if err := s.Save(want); err != nil {
		t.Fatalf("保存档位失败：%v", err)
	}

	reopened, err := OpenPresets(path, nil)
	if err != nil {
		t.Fatalf("重新打开档位库失败：%v", err)
	}
	got, ok := reopened.Get("office")
	if !ok {
		t.Fatal("重新打开后找不到保存的档位")
	}
	if got.Name != want.Name || got.Note != want.Note || got.Icon != want.Icon ||
		got.Color != want.Color || got.Order != want.Order {
		t.Errorf("档位内容丢失：%+v", got)
	}
	if got.Values["scan.workers"] != 60 || got.Values["scan.latency_threshold"] != 260 {
		t.Errorf("参数值丢失：%+v", got.Values)
	}
	if got.Origins["scan.workers"] != model.OriginUser {
		t.Errorf("来源标记丢失：%+v", got.Origins)
	}
}

func TestSaveRejectsEmptyNameAndUnknownKeys(t *testing.T) {
	s := testPresetStore(t)

	cases := []struct {
		name   string
		preset Preset
	}{
		{"没有标识", Preset{Name: "x", Values: map[string]any{"scan.workers": 10}}},
		{"没有名称", Preset{ID: "x", Values: map[string]any{"scan.workers": 10}}},
		{"没有参数", Preset{ID: "x", Name: "x"}},
		// 认不出的键写进去会被静默丢掉：保存完发现参数没生效，却不知道哪个键错了。
		{"键不存在", Preset{ID: "x", Name: "x", Values: map[string]any{"scan.wokers": 10}}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if err := s.Save(tt.preset); err == nil {
				t.Error("期望返回错误，实际为 nil")
			}
		})
	}
}

/**
 * 值的类型与范围要在保存时拦下。
 *
 * 只在应用档位时才检查的话，用户已经存了一份用不了的档位，而报错会出现在
 * 「切换档位」这个与保存毫不相干的动作上——他会把问题归到切换上。
 */
func TestSaveRejectsBadValues(t *testing.T) {
	s := testPresetStore(t)

	cases := []struct {
		name   string
		values map[string]any
	}{
		{"类型不对", map[string]any{"scan.workers": []int{1, 2}}},
		{"类型不对（字符串当数字）", map[string]any{"scan.workers": "60"}},
		{"超出硬上限", map[string]any{"scan.workers": MaxWorkersHard + 1}},
		{"低于下限", map[string]any{"scan.latency_threshold": 0}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if err := s.Save(Preset{ID: "x", Name: "x", Values: tt.values}); err == nil {
				t.Error("期望返回错误，实际为 nil")
			}
		})
	}
}

func TestDeleteUnknownPresetIsAnError(t *testing.T) {
	s := testPresetStore(t)
	if err := s.Delete("nope"); err == nil {
		t.Error("删除不存在的档位应返回错误")
	}
}

// ---------------------------------------------------------------- 来源标记

/**
 * 「我的档位」载入的参数必须一律标记为 user。
 *
 * 这是自适应那条红线的另一半：用户保存档位时表达的是「我就要这组值」，自适
 * 应逻辑不该再动它们。内置档位填进去的值才标记成 preset，允许按网络环境调整。
 */
func TestApplyMarksCustomPresetValuesAsUser(t *testing.T) {
	s := testPresetStore(t)
	if err := s.Save(Preset{
		ID:     "office",
		Name:   "公司网络",
		Values: map[string]any{"scan.workers": 60, "scan.latency_threshold": 260},
	}); err != nil {
		t.Fatalf("保存档位失败：%v", err)
	}

	_, origins, err := s.Apply("office")
	if err != nil {
		t.Fatalf("应用档位失败：%v", err)
	}
	for _, key := range []string{"scan.workers", "scan.latency_threshold"} {
		if origins[key] != model.OriginUser {
			t.Errorf("%s 的来源 = %q，期望 %q", key, origins[key], model.OriginUser)
		}
	}
	if origins.CanAutoAdjust("scan.workers") {
		t.Error("我的档位载入的参数不该允许自适应调整")
	}
}

func TestApplyMarksBuiltinPresetValuesAsPreset(t *testing.T) {
	s := testPresetStore(t)

	_, origins, err := s.Apply(PresetFast)
	if err != nil {
		t.Fatalf("应用档位失败：%v", err)
	}
	if origins["scan.workers"] != model.OriginPreset {
		t.Errorf("来源 = %q，期望 %q", origins["scan.workers"], model.OriginPreset)
	}
	if !origins.CanAutoAdjust("scan.workers") {
		t.Error("内置档位填入的参数应允许自适应调整")
	}
}

// 补丁必须是嵌套结构：settings/update 与 Store.Patch 收的都是嵌套对象。
func TestApplyProducesNestedPatch(t *testing.T) {
	s := testPresetStore(t)

	patch, _, err := s.Apply(PresetPrecise)
	if err != nil {
		t.Fatalf("应用档位失败：%v", err)
	}
	scan, ok := patch["scan"].(map[string]any)
	if !ok {
		t.Fatalf("补丁里没有 scan 分组：%+v", patch)
	}
	if scan["sample_max"] != 5000 {
		t.Errorf("scan.sample_max = %v，期望 5000", scan["sample_max"])
	}
	speed, ok := patch["speed"].(map[string]any)
	if !ok {
		t.Fatalf("补丁里没有 speed 分组：%+v", patch)
	}
	if speed["target_qualified"] != 20 {
		t.Errorf("speed.target_qualified = %v，期望 20", speed["target_qualified"])
	}
}

// ---------------------------------------------------------------- 启动档位

func TestDefaultFallsBackToBuiltin(t *testing.T) {
	s := testPresetStore(t)
	if got := s.Default(); got != PresetFast {
		t.Errorf("默认档位 = %q，期望 %q", got, PresetFast)
	}

	// 设成一个不存在的档位必须被拒，而不是留下一个空指向。
	if err := s.SetDefault("nope"); err == nil {
		t.Error("设为不存在的档位应返回错误")
	}
}

func TestSetDefaultPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.json")
	s, err := OpenPresets(path, nil)
	if err != nil {
		t.Fatalf("打开档位库失败：%v", err)
	}
	if err := s.Save(Preset{ID: "office", Name: "公司网络", Values: map[string]any{"scan.workers": 60}}); err != nil {
		t.Fatalf("保存档位失败：%v", err)
	}
	if err := s.SetDefault("office"); err != nil {
		t.Fatalf("设为启动档位失败：%v", err)
	}

	reopened, err := OpenPresets(path, nil)
	if err != nil {
		t.Fatalf("重新打开失败：%v", err)
	}
	if got := reopened.Default(); got != "office" {
		t.Errorf("启动档位 = %q，期望 office", got)
	}
}

// 删掉启动档位时退回内置默认：留一个指向不存在的档位的 id，界面上「当前档位」
// 会是空白。
func TestDeletingDefaultResetsToBuiltin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.json")
	s, err := OpenPresets(path, nil)
	if err != nil {
		t.Fatalf("打开档位库失败：%v", err)
	}
	if err := s.Save(Preset{ID: "office", Name: "公司网络", Values: map[string]any{"scan.workers": 60}}); err != nil {
		t.Fatalf("保存档位失败：%v", err)
	}
	if err := s.SetDefault("office"); err != nil {
		t.Fatalf("设为启动档位失败：%v", err)
	}
	if err := s.Delete("office"); err != nil {
		t.Fatalf("删除档位失败：%v", err)
	}
	if got := s.Default(); got != PresetFast {
		t.Errorf("删除启动档位后启动档位 = %q，期望 %q", got, PresetFast)
	}
}

// ---------------------------------------------------------------- 文件容错

// 文件损坏不能用「程序起不来」来表现：档位是辅助功能。
func TestOpenPresetsSurvivesCorruptedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("写入损坏文件失败：%v", err)
	}

	s, err := OpenPresets(path, nil)
	if err != nil {
		t.Fatalf("损坏文件不该让档位库打不开：%v", err)
	}
	if len(s.Warnings()) == 0 {
		t.Error("应记下一条警告，否则用户不知道自己的档位没加载")
	}
	if got := s.Default(); got != PresetFast {
		t.Errorf("启动档位 = %q，期望 %q", got, PresetFast)
	}
}

// 旧版本可能把内置档位也写进了文件；重新打开时要丢掉它们，否则代码改了内置
// 档位，用户那份过期的拷贝还在。
func TestOpenPresetsDropsPersistedBuiltins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.json")
	raw, err := json.Marshal(presetsFile{
		Default: PresetFast,
		Presets: []Preset{{ID: PresetFast, Name: "过期的快速", Builtin: true}},
	})
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("写入失败：%v", err)
	}

	s, err := OpenPresets(path, nil)
	if err != nil {
		t.Fatalf("打开失败：%v", err)
	}
	for _, p := range s.List() {
		if p.Builtin && p.Name == "过期的快速" {
			t.Error("文件里的内置档位副本应被丢弃")
		}
	}
}

func TestListPutsBuiltinsFirst(t *testing.T) {
	s := testPresetStore(t)
	if err := s.Save(Preset{ID: "a", Name: "我的", Values: map[string]any{"scan.workers": 60}}); err != nil {
		t.Fatalf("保存档位失败：%v", err)
	}

	list := s.List()
	if len(list) != 4 {
		t.Fatalf("档位数 = %d，期望 4", len(list))
	}
	for i := 0; i < 3; i++ {
		if !list[i].Builtin {
			t.Errorf("第 %d 项不是内置档位：%+v", i, list[i])
		}
	}
}

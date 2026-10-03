package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"cloudtrace/internal/config"
	"cloudtrace/internal/model"
)

// presetsEvent 是 presets 事件解析后的形状。
type presetsEvent struct {
	Presets []config.Preset `json:"presets"`
	Default string          `json:"default"`
}

func readPresets(t *testing.T, conn *websocket.Conn) presetsEvent {
	t.Helper()
	m := readUntil(t, conn, eventPresets, 3*time.Second)
	var p presetsEvent
	decode(t, m, &p)
	return p
}

// ---------------------------------------------------------------- 列表

func TestWSPresetsListReturnsBuiltins(t *testing.T) {
	st := newTestStack(t, nil)
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"presets/list"}`)

	got := readPresets(t, conn)
	if len(got.Presets) != 3 {
		t.Fatalf("档位数 = %d，期望 3（三个内置档位）", len(got.Presets))
	}
	if got.Default != config.PresetFast {
		t.Errorf("启动档位 = %q，期望 %q", got.Default, config.PresetFast)
	}
	for _, p := range got.Presets {
		if !p.Builtin {
			t.Errorf("档位 %q 的 builtin 标记应为 true", p.ID)
		}
	}
}

// ---------------------------------------------------------------- 保存

func TestWSPresetsSaveThenList(t *testing.T) {
	st := newTestStack(t, nil)
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"presets/save","data":{"id":"office","name":"公司网络",`+
		`"note":"上行窄","order":5,"values":{"scan.workers":60,"scan.latency_threshold":260}}}`)

	// 保存成功由广播告知，发起方不再单独回执。
	got := readPresets(t, conn)
	if len(got.Presets) != 4 {
		t.Fatalf("档位数 = %d，期望 4", len(got.Presets))
	}
	var saved config.Preset
	for _, p := range got.Presets {
		if p.ID == "office" {
			saved = p
		}
	}
	if saved.ID == "" {
		t.Fatal("保存的档位没有出现在列表里")
	}
	if saved.Builtin {
		t.Error("用户另存的档位不该带 builtin 标记")
	}
	if saved.Values["scan.workers"] != float64(60) {
		t.Errorf("scan.workers = %v，期望 60", saved.Values["scan.workers"])
	}
}

func TestWSPresetsSaveRejectsUnknownConfigKey(t *testing.T) {
	st := newTestStack(t, nil)
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	// 键写错时保存必须被拒：写进去会被静默丢掉，用户以为存好了却没有。
	send(t, conn, `{"type":"presets/save","data":{"id":"x","name":"x","values":{"scan.wokers":60}}}`)

	m := readUntil(t, conn, eventError, 3*time.Second)
	var p errorPayload
	decode(t, m, &p)
	if p.Code != CodeInvalidParam {
		t.Errorf("code = %q，期望 %q", p.Code, CodeInvalidParam)
	}
}

func TestWSPresetsSaveRejectsBuiltinID(t *testing.T) {
	st := newTestStack(t, nil)
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"presets/save","data":{"id":"fast","name":"我的快速","values":{"scan.workers":60}}}`)

	m := readUntil(t, conn, eventError, 3*time.Second)
	var p errorPayload
	decode(t, m, &p)
	if p.Code != CodeInvalidParam {
		t.Errorf("code = %q，期望 %q——内置档位的标识应保留", p.Code, CodeInvalidParam)
	}
}

// ---------------------------------------------------------------- 应用

/**
 * 应用档位要真的把参数写进配置，并按档位类型打上正确的来源标记。
 *
 * 这是整条链路上最容易出错的一步：标记错了，自适应就会开始动用户明确保存过
 * 的参数，而那种错误是静默的——值被改了，用户不知道为什么。
 */
func TestWSPresetsApplyBuiltinWritesPresetOrigin(t *testing.T) {
	st := newTestStack(t, nil)
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"presets/apply","data":{"id":"precise"}}`)

	// 应用成功后由配置存储广播新配置。
	m := readUntil(t, conn, eventSettings, 3*time.Second)
	var payload settingsPayload
	decode(t, m, &payload)
	if payload.Values.Scan.SampleMax != 5000 {
		t.Errorf("采样上限 = %d，期望 5000", payload.Values.Scan.SampleMax)
	}
	if got := payload.Values.Origins["scan.workers"]; got != model.OriginPreset {
		t.Errorf("来源 = %q，期望 %q", got, model.OriginPreset)
	}
}

func TestWSPresetsApplyCustomWritesUserOrigin(t *testing.T) {
	st := newTestStack(t, nil)
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"presets/save","data":{"id":"office","name":"公司网络",`+
		`"values":{"scan.workers":60}}}`)
	readPresets(t, conn)

	send(t, conn, `{"type":"presets/apply","data":{"id":"office"}}`)

	m := readUntil(t, conn, eventSettings, 3*time.Second)
	var payload settingsPayload
	decode(t, m, &payload)
	if payload.Values.Scan.Workers != 60 {
		t.Errorf("并发 = %d，期望 60", payload.Values.Scan.Workers)
	}
	if got := payload.Values.Origins["scan.workers"]; got != model.OriginUser {
		t.Errorf("来源 = %q，期望 %q——我的档位载入的参数必须标 user", got, model.OriginUser)
	}
	if payload.Values.Origins.CanAutoAdjust("scan.workers") {
		t.Error("我的档位载入的参数不该允许自适应调整")
	}
}

func TestWSPresetsApplyRejectsUnknownID(t *testing.T) {
	st := newTestStack(t, nil)
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"presets/apply","data":{"id":"nope"}}`)

	m := readUntil(t, conn, eventError, 3*time.Second)
	var p errorPayload
	decode(t, m, &p)
	if p.Code != CodeInvalidParam {
		t.Errorf("code = %q，期望 %q", p.Code, CodeInvalidParam)
	}
}

// ---------------------------------------------------------------- 删除

func TestWSPresetsDeleteRejectsBuiltin(t *testing.T) {
	st := newTestStack(t, nil)
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"presets/delete","data":{"id":"fast"}}`)

	m := readUntil(t, conn, eventError, 3*time.Second)
	var p errorPayload
	decode(t, m, &p)
	if p.Code != CodeInvalidParam {
		t.Errorf("code = %q，期望 %q——内置档位不可删", p.Code, CodeInvalidParam)
	}
}

func TestWSPresetsDeleteRoundTrip(t *testing.T) {
	st := newTestStack(t, nil)
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"presets/save","data":{"id":"office","name":"公司网络","values":{"scan.workers":60}}}`)
	readPresets(t, conn)

	send(t, conn, `{"type":"presets/delete","data":{"id":"office"}}`)
	got := readPresets(t, conn)
	if len(got.Presets) != 3 {
		t.Errorf("删除后档位数 = %d，期望 3", len(got.Presets))
	}
}

// ---------------------------------------------------------------- 启动档位

func TestWSPresetsSetDefaultRejectsUnknown(t *testing.T) {
	st := newTestStack(t, nil)
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"presets/set_default","data":{"id":"nope"}}`)

	m := readUntil(t, conn, eventError, 3*time.Second)
	var p errorPayload
	decode(t, m, &p)
	if p.Code != CodeInvalidParam {
		t.Errorf("code = %q，期望 %q", p.Code, CodeInvalidParam)
	}
}

func TestWSPresetsSetDefaultRoundTrip(t *testing.T) {
	st := newTestStack(t, nil)
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"presets/save","data":{"id":"office","name":"公司网络","values":{"scan.workers":60}}}`)
	readPresets(t, conn)

	send(t, conn, `{"type":"presets/set_default","data":{"id":"office"}}`)
	got := readPresets(t, conn)
	if got.Default != "office" {
		t.Errorf("启动档位 = %q，期望 office", got.Default)
	}
}

// ---------------------------------------------------------------- 应用档位

/**
 * 应用档位时，「我的档位」载入的参数必须标记成 user。
 *
 * 这条在存储层已有断言，但真正会被自适应逻辑读到的来源是配置里的 origins 表，
 * 因此这里把「应用档位 → 写进配置」整条链路走一遍：标记必须在配置里落地，
 * 否则自适应还是会把用户的档位值当成可调整的。
 */
func TestPresetApplyWritesUserOriginsIntoConfig(t *testing.T) {
	st := newTestStack(t, nil)

	store := st.svc.Presets
	if store == nil {
		t.Fatal("档位库不可用")
	}
	if err := store.Save(config.Preset{
		ID:     "office",
		Name:   "公司网络",
		Values: map[string]any{"scan.workers": 60},
	}); err != nil {
		t.Fatalf("保存档位失败：%v", err)
	}

	patch, origins, err := store.Apply("office")
	if err != nil {
		t.Fatalf("应用档位失败：%v", err)
	}
	if _, err := st.store.Patch(patch, origins); err != nil {
		t.Fatalf("写入配置失败：%v", err)
	}

	cfg := st.store.Get()
	if cfg.Scan.Workers != 60 {
		t.Errorf("并发 = %d，期望 60", cfg.Scan.Workers)
	}
	if got := cfg.Origins["scan.workers"]; got != model.OriginUser {
		t.Errorf("配置里的来源 = %q，期望 %q", got, model.OriginUser)
	}
	if cfg.Origins.CanAutoAdjust("scan.workers") {
		t.Error("我的档位载入的参数不该允许自适应调整")
	}
}

// 内置档位填进去的值标记成 preset，允许自适应按网络环境调整。
func TestPresetApplyWritesPresetOriginsIntoConfig(t *testing.T) {
	st := newTestStack(t, nil)

	patch, origins, err := st.svc.Presets.Apply(config.PresetFast)
	if err != nil {
		t.Fatalf("应用档位失败：%v", err)
	}
	if _, err := st.store.Patch(patch, origins); err != nil {
		t.Fatalf("写入配置失败：%v", err)
	}

	cfg := st.store.Get()
	if got := cfg.Origins["scan.workers"]; got != model.OriginPreset {
		t.Errorf("配置里的来源 = %q，期望 %q", got, model.OriginPreset)
	}
	if !cfg.Origins.CanAutoAdjust("scan.workers") {
		t.Error("内置档位填入的参数应允许自适应调整")
	}
}

// ---------------------------------------------------------------- 载荷

// 档位里的值必须是 JSON 标量，不能是数组或对象——那样写进配置会被拒。
func TestWSPresetsSaveRejectsNonScalarValue(t *testing.T) {
	st := newTestStack(t, nil)
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	body := map[string]any{
		"type": "presets/save",
		"data": map[string]any{
			"id":     "x",
			"name":   "x",
			"values": map[string]any{"scan.workers": []int{1, 2}},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	send(t, conn, string(raw))

	m := readUntil(t, conn, eventError, 3*time.Second)
	var p errorPayload
	decode(t, m, &p)
	if p.Code != CodeInvalidParam {
		t.Errorf("code = %q，期望 %q", p.Code, CodeInvalidParam)
	}
}

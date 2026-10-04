package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"cloudtrace/internal/config"
	"cloudtrace/internal/model"
)

// fastSourceStack 让体检里的测速源探测打向一个必然被拒绝的回环地址。
//
// 真实探测在单测里既慢又不可控；指向 1 号端口会立刻被拒绝，结果稳定且快。
func fastSourceStack(t *testing.T, mutate func(*config.Config)) *testStack {
	t.Helper()
	return newTestStack(t, func(c *config.Config) {
		c.Speed.URLMode = "custom"
		c.Speed.CustomURL = "http://127.0.0.1:1/"
		if mutate != nil {
			mutate(c)
		}
	})
}

// 读设置要给出全量配置，前端拿它整体替换本地状态。
func TestWSSettingsGet(t *testing.T) {
	st := newTestStack(t, nil)

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"settings/get"}`)
	m := readUntil(t, conn, eventSettings, 3*time.Second)

	var resp settingsPayload
	decode(t, m, &resp)
	if resp.Values.Scan.Workers != config.Default().Scan.Workers {
		t.Errorf("扫描并发 = %d，期望默认值 %d", resp.Values.Scan.Workers, config.Default().Scan.Workers)
	}
	if resp.Values.Export.DefaultFormat == "" {
		t.Error("导出格式没下发")
	}
	if len(resp.RestartRequired) != 0 {
		t.Errorf("刚启动就报「需重启」：%v", resp.RestartRequired)
	}
}

/**
 * 配置告警随设置一起下发。
 *
 * 「值合法但可能带来麻烦」的项（并发过高、阈值过低）要在改的那一刻就说清楚，
 * 而不是等用户自己想起来去点「配置体检」——那时他早就把这件事忘了。
 */
func TestWSSettingsCarriesWarnings(t *testing.T) {
	st := newTestStack(t, func(c *config.Config) { c.Scan.Workers = 800 })

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"settings/get"}`)
	m := readUntil(t, conn, eventSettings, 3*time.Second)

	var resp settingsPayload
	decode(t, m, &resp)

	var found bool
	for _, w := range resp.Warnings {
		if w.Key == "scan.workers" {
			found = true
			if w.Reason == "" {
				t.Error("告警没有说明原因，界面上只会显示一个没有解释的警示色")
			}
		}
	}
	if !found {
		t.Errorf("并发 800 应当带一条 scan.workers 的告警，实际 %+v", resp.Warnings)
	}
}

// 值都在合理范围内时不该报警告：满屏警示色等于没有警示。
func TestWSSettingsHasNoWarningsByDefault(t *testing.T) {
	st := newTestStack(t, nil)

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"settings/get"}`)
	m := readUntil(t, conn, eventSettings, 3*time.Second)

	var resp settingsPayload
	decode(t, m, &resp)
	if len(resp.Warnings) != 0 {
		t.Errorf("默认配置不该有告警，实际 %+v", resp.Warnings)
	}
}

// 写设置成功时靠广播回执，而不是单独再回一次。
func TestWSSettingsUpdateBroadcasts(t *testing.T) {
	st := newTestStack(t, nil)

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"settings/update","data":{"patch":{"scan":{"workers":120}},"origins":{"scan.workers":"user"}}}`)

	m := readUntil(t, conn, eventSettings, 3*time.Second)
	var resp settingsPayload
	decode(t, m, &resp)
	if resp.Values.Scan.Workers != 120 {
		t.Fatalf("广播里的并发 = %d，期望 120", resp.Values.Scan.Workers)
	}
	if got := resp.Values.Origins.Get("scan.workers"); got != model.OriginUser {
		t.Errorf("参数来源标记 = %q，期望 %q", got, model.OriginUser)
	}

	// 落盘也要是新值，否则重启就白改了。
	if st.store.Get().Scan.Workers != 120 {
		t.Errorf("存储里的并发 = %d，期望 120", st.store.Get().Scan.Workers)
	}
}

// 双端实时同步：一个连接改设置，另一个连接立刻看到。
func TestWSSettingsSyncAcrossConnections(t *testing.T) {
	st := newTestStack(t, nil)

	first := st.mustDial(t)
	second := st.mustDial(t)
	readUntil(t, first, eventState, 3*time.Second)
	readUntil(t, second, eventState, 3*time.Second)

	send(t, first, `{"type":"settings/update","data":{"patch":{"ui":{"theme":"dark"}}}}`)

	conns := []*websocket.Conn{first, second}
	names := []string{"第一个", "第二个"}
	for i, conn := range conns {
		m := readUntil(t, conn, eventSettings, 3*time.Second)
		var resp settingsPayload
		decode(t, m, &resp)
		if resp.Values.UI.Theme != "dark" {
			t.Errorf("%s连接收到的主题 = %q，期望 dark", names[i], resp.Values.UI.Theme)
		}
	}
}

// 校验失败要带上具体字段，前端才能高亮到那一项。
func TestWSSettingsUpdateRejectsInvalidValue(t *testing.T) {
	st := newTestStack(t, nil)

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"settings/update","data":{"patch":{"scan":{"workers":99999}}}}`)
	var p errorPayload
	decode(t, readUntil(t, conn, eventError, 3*time.Second), &p)

	if p.Code != CodeInvalidParam {
		t.Fatalf("错误码 = %q，期望 %q", p.Code, CodeInvalidParam)
	}
	if !strings.Contains(p.Msg, "scan.workers") {
		t.Errorf("错误信息没有点出字段：%q", p.Msg)
	}
	// 失败的更新不能留下痕迹。
	if st.store.Get().Scan.Workers != config.Default().Scan.Workers {
		t.Error("失败的更新把配置改掉了")
	}
}

func TestWSSettingsUpdateRejectsEmptyPatch(t *testing.T) {
	st := newTestStack(t, nil)

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"settings/update","data":{}}`)
	var p errorPayload
	decode(t, readUntil(t, conn, eventError, 3*time.Second), &p)
	if p.Code != CodeInvalidParam {
		t.Fatalf("错误码 = %q，期望 %q", p.Code, CodeInvalidParam)
	}
}

// 分组重置与单参数重置都要能通过命令走通。
func TestWSSettingsReset(t *testing.T) {
	st := newTestStack(t, func(c *config.Config) {
		c.Scan.Workers = 500
		c.Scan.Port = 8443
		c.UI.Theme = "dark"
	})

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	// 只重置一个参数，同组其它改动保留。
	send(t, conn, `{"type":"settings/reset","data":{"keys":["scan.workers"]}}`)
	var one settingsPayload
	decode(t, readUntil(t, conn, eventSettings, 3*time.Second), &one)

	def := config.Default()
	if one.Values.Scan.Workers != def.Scan.Workers {
		t.Errorf("scan.workers = %d，期望回到 %d", one.Values.Scan.Workers, def.Scan.Workers)
	}
	if one.Values.Scan.Port != 8443 {
		t.Errorf("同组的 scan.port 被一起重置了：%d", one.Values.Scan.Port)
	}

	// 整组重置。
	send(t, conn, `{"type":"settings/reset","data":{"keys":["scan"]}}`)
	var group settingsPayload
	decode(t, readUntil(t, conn, eventSettings, 3*time.Second), &group)
	if group.Values.Scan.Port != def.Scan.Port {
		t.Errorf("scan.port = %d，期望回到 %d", group.Values.Scan.Port, def.Scan.Port)
	}
	if group.Values.UI.Theme != "dark" {
		t.Errorf("重置 scan 把 ui.theme 也改了：%q", group.Values.UI.Theme)
	}

	// 整份重置。
	send(t, conn, `{"type":"settings/reset","data":{}}`)
	var all settingsPayload
	decode(t, readUntil(t, conn, eventSettings, 3*time.Second), &all)
	if all.Values.UI.Theme != def.UI.Theme {
		t.Errorf("整份重置后主题 = %q，期望 %q", all.Values.UI.Theme, def.UI.Theme)
	}
}

func TestWSSettingsResetRejectsUnknownKey(t *testing.T) {
	st := newTestStack(t, nil)

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"settings/reset","data":{"keys":["scan.nope"]}}`)
	var p errorPayload
	decode(t, readUntil(t, conn, eventError, 3*time.Second), &p)
	if p.Code != CodeInvalidParam {
		t.Fatalf("错误码 = %q，期望 %q", p.Code, CodeInvalidParam)
	}
}

// 新加的命令必须真的登记进命令表：漏登记时前端只会收到「未注册的命令」。
func TestNewCommandsAreRegistered(t *testing.T) {
	st := fastSourceStack(t, nil)

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	cases := []struct {
		cmd  string
		data string
	}{
		{"settings/get", `{}`},
		{"settings/update", `{"patch":{"ui":{"theme":"dark"}}}`},
		{"settings/reset", `{"keys":["ui.theme"]}`},
		{"export", `{"format":"csv"}`},
		{"health/check", `{}`},
	}
	for _, c := range cases {
		send(t, conn, fmt.Sprintf(`{"type":%q,"data":%s}`, c.cmd, c.data))
		msgs := readStream(t, conn, 5*time.Second, c.cmd, func(got []message) bool { return len(got) > 0 })

		m := msgs[0]
		if m.Type != eventError {
			continue
		}
		var p errorPayload
		decode(t, m, &p)
		if p.Code == CodeUnknown && strings.Contains(p.Msg, "未注册") {
			t.Errorf("命令 %s 没有登记到命令表：%s", c.cmd, p.Msg)
		}
	}
}

// 改了端口或监听地址要提示「重启才生效」——面板还在原端口上跑，不给提示
// 用户就会以为改动没保存。
func TestSettingsReportsRestartRequired(t *testing.T) {
	st := newTestStack(t, nil)
	s := &server{cfg: st.store, svc: st.svc, startup: st.store.Get(), listenPort: config.DefaultPort}

	// 只改无关项时不报。
	cur := st.store.Get()
	cur.UI.Theme = "dark"
	if got := s.restartRequired(cur); len(got) != 0 {
		t.Errorf("只改了主题却报了需重启：%v", got)
	}

	cur.Server.Port = 19999
	cur.Server.Bind = "0.0.0.0"
	cur.Data.Portable = false
	cur.Advanced.LogLevel = "debug"
	got := s.restartRequired(cur)
	want := []string{"server.port", "server.bind", "data.portable", "advanced.log_level"}
	if len(got) != len(want) {
		t.Fatalf("需重启项 = %v，期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("需重启项 = %v，期望 %v", got, want)
		}
	}

	// 端口未知时不报端口：拿不到就别说。
	s.listenPort = 0
	if got := s.restartRequired(cur); len(got) != len(want)-1 {
		t.Errorf("端口未知时的需重启项 = %v", got)
	}
}

// 设置变更也要落盘，重启后还在。
func TestSettingsUpdatePersistsToDisk(t *testing.T) {
	st := newTestStack(t, nil)

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"settings/update","data":{"patch":{"export":{"default_format":"json"}}}}`)
	readUntil(t, conn, eventSettings, 3*time.Second)

	reloaded, err := config.LoadFile(st.store.Path())
	if err != nil {
		t.Fatalf("重新加载配置失败：%v", err)
	}
	if reloaded.Export.DefaultFormat != "json" {
		t.Fatalf("落盘的导出格式 = %q，期望 json", reloaded.Export.DefaultFormat)
	}
}

// 设置载荷必须是前端能直接用的 JSON，字段名与契约一致。
func TestSettingsPayloadShape(t *testing.T) {
	st := newTestStack(t, nil)
	s := &server{cfg: st.store, svc: st.svc, startup: st.store.Get(), listenPort: config.DefaultPort}

	raw, err := json.Marshal(s.settingsPayload())
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("载荷不是合法 JSON：%v", err)
	}
	values, ok := obj["values"].(map[string]any)
	if !ok {
		t.Fatalf("载荷缺少 values 对象：%s", raw)
	}
	for _, key := range []string{"scan", "speed", "source", "net", "geo", "history", "data", "export", "ui", "server", "notify", "advanced"} {
		if _, ok := values[key]; !ok {
			t.Errorf("配置分组 %q 没有下发：%s", key, raw)
		}
	}
}

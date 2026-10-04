package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"cloudtrace/internal/history"
)

// 一份最小可用的旧配置与旧历史。
const oldSettingsJSON = `{"workers": 321, "http_port": 17443, "scan_mode": "tcping"}`
const oldHistoryJSON = `{"save_time": "2026-09-27 13:15:14", "ip_version": 4, "result_type": "scan",
  "count": 1, "results": [{"ip": "104.18.77.31", "port": 443, "use_tls": true, "latency": 187,
  "latency_avg": 187, "latency_max": 190, "chinese_name": "洛杉矶", "colo": "LAX",
  "samples": 3, "ok_count": 3, "success": true}]}`

// withLegacy 造一份旧数据目录，并把它接到服务端上。
func withLegacy(t *testing.T, st *testStack, settings, history string) string {
	t.Helper()
	dir := t.TempDir()

	if settings != "" {
		writeLegacy(t, filepath.Join(dir, "settings.json"), settings)
	}
	if history != "" {
		writeLegacy(t, filepath.Join(dir, "CloudTrace_history", "ipv4_scan_20260927_131514.json"), history)
	}
	st.srv.legacyRoot = func() string { return dir }
	return dir
}

func writeLegacy(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("写文件失败：%v", err)
	}
}

type migrateEvent struct {
	Found     bool `json:"found"`
	Settings  bool `json:"settings"`
	Histories int  `json:"histories"`
	Migrated  bool `json:"migrated"`
	Report    *struct {
		Settings bool     `json:"settings"`
		Imported int      `json:"imported"`
		Notes    []string `json:"notes"`
		Skipped  []string `json:"skipped"`
	} `json:"report"`
}

func readMigrate(t *testing.T, conn *websocket.Conn) migrateEvent {
	t.Helper()
	m := readUntil(t, conn, eventMigrate, 3*time.Second)
	var payload migrateEvent
	decode(t, m, &payload)
	return payload
}

// ---------------------------------------------------------------- 状态

func TestWSMigrateStatusFindsLegacy(t *testing.T) {
	st := newTestStack(t, nil)
	withLegacy(t, st, oldSettingsJSON, oldHistoryJSON)

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"migrate/status"}`)
	got := readMigrate(t, conn)

	if !got.Found {
		t.Fatal("没检测到旧数据")
	}
	if !got.Settings {
		t.Error("没认出旧配置")
	}
	if got.Histories != 1 {
		t.Errorf("旧历史数 = %d，期望 1", got.Histories)
	}
	// 还没迁移过。
	if got.Migrated {
		t.Error("还没迁移就报已迁移")
	}
}

func TestWSMigrateStatusWithoutLegacy(t *testing.T) {
	st := newTestStack(t, nil)
	withLegacy(t, st, "", "")

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"migrate/status"}`)
	if got := readMigrate(t, conn); got.Found {
		t.Errorf("空目录不该检测到旧数据：%+v", got)
	}
}

// ---------------------------------------------------------------- 执行

/**
 * 迁移**不阻断启动**：它只是界面上的一条提示，用户点了才真的动数据。
 *
 * 悄悄搬东西比不搬更糟——用户会发现自己的旧配置在不知情的时候变了。
 */
func TestWSMigrateDoesNotRunUntilAsked(t *testing.T) {
	st := newTestStack(t, nil)
	withLegacy(t, st, oldSettingsJSON, "")

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"migrate/status"}`)
	readMigrate(t, conn)

	if got := st.store.Get().Scan.Workers; got == 321 {
		t.Error("只是读了一次状态就把旧配置搬过来了")
	}
}

func TestWSMigrateRunImportsData(t *testing.T) {
	st := newTestStack(t, nil)
	withLegacy(t, st, oldSettingsJSON, oldHistoryJSON)

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"migrate/run"}`)
	got := readMigrate(t, conn)

	if got.Report == nil {
		t.Fatal("没有回执")
	}
	if !got.Report.Settings {
		t.Error("配置没写进去")
	}
	if got.Report.Imported != 1 {
		t.Errorf("导入数 = %d，期望 1", got.Report.Imported)
	}
	if got := st.store.Get().Scan.Workers; got != 321 {
		t.Errorf("并发 = %d，期望沿用旧配置的 321", got)
	}

	entries, err := st.svc.History.List(history.Filter{})
	if err != nil || len(entries) != 1 {
		t.Fatalf("历史条目数 = %d（err=%v），期望 1", len(entries), err)
	}
	// 迁移之后状态要变成「已迁移」，界面据此收起横幅。
	if !got.Migrated {
		t.Error("迁移之后状态没更新")
	}
}

func TestWSMigrateRunWithoutLegacyIsAnError(t *testing.T) {
	st := newTestStack(t, nil)
	withLegacy(t, st, "", "")

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"migrate/run"}`)

	m := readUntil(t, conn, eventError, 3*time.Second)
	var p errorPayload
	decode(t, m, &p)
	if p.Code != CodeNotFound {
		t.Errorf("code = %q，期望 %q", p.Code, CodeNotFound)
	}
}

// 迁移结果要广播给所有连接：它改了配置与历史，别的面板上显示的东西跟着变了。
func TestWSMigrateRunBroadcasts(t *testing.T) {
	st := newTestStack(t, nil)
	withLegacy(t, st, oldSettingsJSON, "")

	first := st.mustDial(t)
	readUntil(t, first, eventState, 3*time.Second)
	second := st.mustDial(t)
	readUntil(t, second, eventState, 3*time.Second)

	send(t, first, `{"type":"migrate/run"}`)

	// 两个连接都应当收到。
	readMigrate(t, first)
	readMigrate(t, second)
}

package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestExportBytesOmitsCredentials 导出文件里不能出现凭据。
func TestExportBytesOmitsCredentials(t *testing.T) {
	cfg := Default()
	cfg.Server.Token = "pbkdf2-sha256$210000$c2FsdA==$aGFzaA=="
	cfg.Server.SessionTTLMin = 30
	cfg.Scan.Workers = 120

	data, err := ExportBytes(cfg)
	if err != nil {
		t.Fatalf("导出失败：%v", err)
	}
	text := string(data)
	if strings.Contains(text, "pbkdf2-sha256") {
		t.Fatalf("导出内容里出现了密码哈希：%s", text)
	}
	if strings.Contains(text, "session_ttl_min") {
		t.Fatalf("导出内容里出现了会话 TTL：%s", text)
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatalf("导出内容不是合法 JSON：%v", err)
	}
	var version int
	if err := json.Unmarshal(top["schema_version"], &version); err != nil {
		t.Fatalf("缺少 schema_version：%v", err)
	}
	if version != SchemaVersion {
		t.Fatalf("schema_version = %d，想要 %d", version, SchemaVersion)
	}

	var srv map[string]json.RawMessage
	if err := json.Unmarshal(top["server"], &srv); err != nil {
		t.Fatalf("server 段不是对象：%v", err)
	}
	if _, ok := srv["token"]; ok {
		t.Fatal("server 段里仍有 token")
	}
	if _, ok := srv["session_ttl_min"]; ok {
		t.Fatal("server 段里仍有 session_ttl_min")
	}
}

// TestImportBytesFillsDefaultsAndIgnoresUnknown 旧文件缺字段、新文件多字段都不该报错。
func TestImportBytesFillsDefaultsAndIgnoresUnknown(t *testing.T) {
	data := []byte(`{
	  "schema_version": 99,
	  "scan": {"workers": 120},
	  "future_section": {"enabled": true}
	}`)

	cfg, ignored, err := ImportBytes(data)
	if err != nil {
		t.Fatalf("导入失败：%v", err)
	}
	if len(ignored) != 0 {
		t.Fatalf("不该报忽略项：%v", ignored)
	}
	if cfg.Scan.Workers != 120 {
		t.Fatalf("scan.workers = %d，想要 120", cfg.Scan.Workers)
	}
	def := Default()
	if cfg.Speed.Concurrency != def.Speed.Concurrency {
		t.Fatalf("缺失字段没有补默认值：speed.concurrency = %d", cfg.Speed.Concurrency)
	}
	if cfg.UI.Theme != def.UI.Theme {
		t.Fatalf("缺失字段没有补默认值：ui.theme = %q", cfg.UI.Theme)
	}
}

// TestImportBytesDropsCredentials 文件里带的凭据要被丢弃并报出。
func TestImportBytesDropsCredentials(t *testing.T) {
	data := []byte(`{
	  "server": {"token": "pbkdf2-sha256$1$x$y", "session_ttl_min": 5, "port": 9090}
	}`)

	cfg, ignored, err := ImportBytes(data)
	if err != nil {
		t.Fatalf("导入失败：%v", err)
	}
	want := []string{"server.token", "server.session_ttl_min"}
	if len(ignored) != len(want) {
		t.Fatalf("忽略项 = %v，想要 %v", ignored, want)
	}
	for i := range want {
		if ignored[i] != want[i] {
			t.Fatalf("忽略项 = %v，想要 %v", ignored, want)
		}
	}
	if cfg.Server.Token != "" {
		t.Fatalf("密码哈希被带进了配置：%q", cfg.Server.Token)
	}
	if cfg.Server.SessionTTLMin != DefaultSessionTTLMin {
		t.Fatalf("session_ttl_min = %d，想要默认 %d", cfg.Server.SessionTTLMin, DefaultSessionTTLMin)
	}
	if cfg.Server.Port != 9090 {
		t.Fatalf("非凭据字段被误伤：server.port = %d", cfg.Server.Port)
	}
}

// TestExportImportBytesRoundTrip 导出再导入，非凭据字段应原样回来。
func TestExportImportBytesRoundTrip(t *testing.T) {
	want := Default()
	want.Scan.Workers = 120
	want.Scan.SampleMax = 5000
	want.Speed.Concurrency = 4
	want.Export.FieldAliases = map[string]string{"ip": "地址"}

	data, err := ExportBytes(want)
	if err != nil {
		t.Fatalf("导出失败：%v", err)
	}
	got, ignored, err := ImportBytes(data)
	if err != nil {
		t.Fatalf("导入失败：%v", err)
	}
	if len(ignored) != 0 {
		t.Fatalf("自己导出的文件不该有忽略项：%v", ignored)
	}
	if got.Scan.Workers != want.Scan.Workers || got.Scan.SampleMax != want.Scan.SampleMax {
		t.Fatalf("扫描参数往返不一致：%+v", got.Scan)
	}
	if got.Speed.Concurrency != want.Speed.Concurrency {
		t.Fatalf("测速参数往返不一致：%d", got.Speed.Concurrency)
	}
	if got.Export.FieldAliases["ip"] != "地址" {
		t.Fatalf("字段别名往返不一致：%v", got.Export.FieldAliases)
	}
}

// TestImportBytesRejectsBrokenJSON 坏文件要报错而不是静默变默认值。
func TestImportBytesRejectsBrokenJSON(t *testing.T) {
	if _, _, err := ImportBytes([]byte("{not json")); err == nil {
		t.Fatal("坏 JSON 应当报错")
	}
}

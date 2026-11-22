package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"cloudtrace/internal/model"
)

// 旧档位库还在时，横幅要报出来；导入后值成为默认参数、来源标成 user，
// 原文件留一份备份，标记文件挡住第二次提示。
func TestLegacyPresetsOfferAndImport(t *testing.T) {
	st := newTestStack(t, nil)
	srv := unitServer(t, st)
	dir, err := st.svc.DataDir()
	if err != nil {
		t.Fatalf("取数据目录失败：%v", err)
	}
	writeLegacyPresets(t, dir, `{
		"default": "mine",
		"presets": [
			{"id": "mine", "name": "我的档位", "values": {"scan.workers": 321, "scan.latency_threshold": 210}}
		]
	}`)

	status := srv.legacyPresetsStatus()
	if !status.Found || len(status.Presets) != 1 || status.Default != "mine" {
		t.Fatalf("状态 = %+v，期望发现一条自定义档位", status)
	}

	c := testConn()
	if err := srv.handleLegacyPresetsImport(c, json.RawMessage(`{"ids":["mine"]}`)); err != nil {
		t.Fatalf("导入失败：%v", err)
	}

	cfg := st.store.Get()
	if cfg.Scan.Workers != 321 || cfg.Scan.LatencyThreshold != 210 {
		t.Fatalf("导入后的参数 = workers %d / threshold %d，期望 321 / 210", cfg.Scan.Workers, cfg.Scan.LatencyThreshold)
	}
	if got := cfg.Origins.Get("scan.workers"); got != model.OriginUser {
		t.Errorf("来源 = %q，期望 user", got)
	}

	if _, err := os.Stat(filepath.Join(dir, legacyPresetsBackup)); err != nil {
		t.Errorf("没有留下备份：%v", err)
	}
	if !srv.legacyPresetsStatus().Migrated {
		t.Error("导入后应标记为已迁移")
	}

	m := nextEvent(t, c)
	if m.Type != eventLegacyPresets {
		t.Fatalf("事件 = %q，期望 %q", m.Type, eventLegacyPresets)
	}
	var got legacyPresetsStatus
	if err := json.Unmarshal(m.Data, &got); err != nil {
		t.Fatalf("事件数据不是合法 JSON：%v", err)
	}
	if len(got.Imported) != 1 || got.Imported[0] != "我的档位" || !got.Migrated {
		t.Fatalf("导入回执 = %+v", got)
	}
}

// 新版已删掉的参数不能让整次导入失败：能搬的搬，搬不动的列出来，且不写标记，
// 横幅下次还在。
func TestLegacyPresetsSkipUnknownValues(t *testing.T) {
	st := newTestStack(t, nil)
	srv := unitServer(t, st)
	dir, _ := st.svc.DataDir()
	writeLegacyPresets(t, dir, `{"presets": [{"id": "old", "name": "老档位", "values": {"scan.nonsense": 1}}]}`)

	c := testConn()
	if err := srv.handleLegacyPresetsImport(c, json.RawMessage(`{}`)); err == nil {
		t.Fatal("全部参数都不认识时应报错")
	}
	if srv.legacyPresetsStatus().Migrated {
		t.Error("没有成功导入任何一项时不应写迁移标记")
	}
	if _, err := os.Stat(filepath.Join(dir, legacyPresetsBackup)); err == nil {
		t.Error("没有成功导入时不应生成备份")
	}
}

func writeLegacyPresets(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, legacyPresetsFile), []byte(body), 0o600); err != nil {
		t.Fatalf("写入旧档位库失败：%v", err)
	}
}

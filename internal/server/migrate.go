package server

import (
	"encoding/json"

	"cloudtrace/internal/config"
	"cloudtrace/internal/migrate"
)

// 旧数据迁移相关的命令与事件名。
const (
	cmdMigrateStatus = "migrate/status"
	cmdMigrateRun    = "migrate/run"

	// eventMigrate 既是一次状态读取的结果，也是迁移完成后的广播。
	//
	// 与 settings 同一套做法：读与变更共用一个事件名，前端对两者的处理完全
	// 一样（拿全量状态替换本地展示）。
	eventMigrate = "migrate"
)

// migrateHandlers 返回迁移相关的命令表。
func (s *server) migrateHandlers() map[string]commandHandler {
	return map[string]commandHandler{
		cmdMigrateStatus: s.handleMigrateStatus,
		cmdMigrateRun:    s.handleMigrateRun,
	}
}

// migratePayload 是 migrate 事件的数据体。
type migratePayload struct {
	// Found 表示检测到旧版数据。
	Found bool `json:"found"`
	// Dir 是旧数据所在目录。
	Dir string `json:"dir,omitempty"`
	// Settings / Histories 是旧数据的构成。
	Settings  bool `json:"settings"`
	Histories int  `json:"histories"`
	// Migrated 表示这份旧数据已经迁移过。
	Migrated bool `json:"migrated"`
	// Report 是最近一次迁移的结果；还没跑过时为空。
	Report *migrate.Report `json:"report,omitempty"`
}

// legacyDir 返回旧数据可能在的位置。
//
// 只看程序目录：旧版本是便携的，数据就在 exe 同级。满盘搜是另一回事——用户
// 可能在别处放着一份备份，把那份导进来比不导入更糟。
func (s *server) legacyDir() string {
	if s.legacyRoot != nil {
		return s.legacyRoot()
	}
	exeDir, err := config.ExecutableDir()
	if err != nil {
		return ""
	}
	return exeDir
}

// handleMigrateStatus 下发迁移状态。
//
// 迁移**不阻断启动**：它只是扫描页上的一条横幅，用户点「导入」才会真的动数据。
// 悄悄搬东西比不搬更糟——用户会发现自己的旧配置在不知情的时候变了。
func (s *server) handleMigrateStatus(c *wsConn, _ json.RawMessage) error {
	c.sendEvent(eventMigrate, s.migratePayload())
	return nil
}

// handleMigrateRun 执行迁移。
//
// 结果广播给所有连接：迁移会改配置与历史，别的面板上显示的东西跟着变了。
func (s *server) handleMigrateRun(_ *wsConn, _ json.RawMessage) error {
	dir := s.legacyDir()
	if dir == "" {
		return fail(CodeIO, "定位程序目录失败，无法找到旧版数据")
	}
	legacy := migrate.Detect(dir)
	if !legacy.Found() {
		return fail(CodeNotFound, "没有检测到旧版数据")
	}

	dataDir, err := s.cfg.DataDir()
	if err != nil {
		return fail(CodeIO, "解析数据目录失败："+err.Error())
	}

	report, err := migrate.Run(legacy, migrate.Dest{
		Store:   s.cfg,
		History: s.svc.History,
		DataDir: dataDir,
	})
	if err != nil {
		return fail(CodeIO, "迁移失败："+err.Error())
	}

	// 把这次的结果留在内存里，随后广播出去；下次读状态时还能看到。
	s.mu.Lock()
	s.lastMigrate = &report
	s.mu.Unlock()

	s.hub.broadcast(eventMigrate, s.migratePayload())
	return nil
}

// migratePayload 组装当前迁移状态。
func (s *server) migratePayload() migratePayload {
	dir := s.legacyDir()
	out := migratePayload{Dir: dir}
	if dir == "" {
		return out
	}

	legacy := migrate.Detect(dir)
	out.Found = legacy.Found()
	out.Settings = legacy.SettingsPath != ""
	out.Histories = len(legacy.HistoryFiles)

	dataDir, err := s.cfg.DataDir()
	if err == nil {
		out.Migrated = migrate.AlreadyMigrated(dataDir, legacy)
	}

	s.mu.Lock()
	if s.lastMigrate != nil {
		report := *s.lastMigrate
		out.Report = &report
	}
	s.mu.Unlock()
	return out
}

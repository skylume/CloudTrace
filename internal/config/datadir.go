package config

import (
	"os"
	"path/filepath"
)

// 数据目录下的固定子路径。
const (
	dirHistory = "history"
	dirCache   = "cache"
	dirLogs    = "logs"
	dirBackup  = "backup"
	fileName   = "config.json"
)

// ExecutableDir 返回当前可执行文件所在目录。
//
// 注意：`go run` 场景下返回的是临时目录，因此命令行提供了 -data-dir 覆盖入口。
func ExecutableDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Dir(exe), nil
}

// ResolveDataDir 返回数据目录的绝对路径。
//
// 优先级：cfg.Data.Dir（非空）> 便携模式（exe 同级 data/，默认）> 标准模式（%APPDATA%）。
func ResolveDataDir(cfg Config, exeDir string) (string, error) {
	if dir := cfg.Data.Dir; dir != "" {
		return filepath.Abs(dir)
	}
	if cfg.Data.Portable {
		if exeDir == "" {
			return "", os.ErrInvalid
		}
		return filepath.Join(exeDir, "data"), nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "CloudTrace"), nil
}

// ConfigPath 返回数据目录下配置文件的路径。
func ConfigPath(dataDir string) string { return filepath.Join(dataDir, fileName) }

// HistoryDir 返回历史记录根目录。
func HistoryDir(dataDir string) string { return filepath.Join(dataDir, dirHistory) }

// CacheDir 返回缓存根目录。
func CacheDir(dataDir string) string { return filepath.Join(dataDir, dirCache) }

// LogsDir 返回日志目录。
func LogsDir(dataDir string) string { return filepath.Join(dataDir, dirLogs) }

// BackupDir 返回旧数据迁移的备份目录。
func BackupDir(dataDir string) string { return filepath.Join(dataDir, dirBackup) }

// EnsureDataDirs 创建数据目录骨架。
//
// 只创建目录，不写任何文件，因此可安全地在启动早期调用。
func EnsureDataDirs(dataDir string) error {
	for _, d := range []string{
		dataDir,
		HistoryDir(dataDir),
		CacheDir(dataDir),
		LogsDir(dataDir),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

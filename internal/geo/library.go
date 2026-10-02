package geo

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"cloudtrace/internal/config"
)

// 本文件是库文件的位置解析与「该不该更新」的判定，与生命周期分开：
// 前者是纯计算，可以直接单测，不需要构造一个管理器。

// libraryPath 返回按配置解析后的库位置。
//
// TSV 源给的是目录（两个文件放一起），mmdb 源给的是文件本身。
func (m *Manager) libraryPath(cfg config.GeoConfig) string {
	raw := strings.TrimSpace(cfg.ASNDBPath)
	if raw == "" {
		raw = filepath.Join(config.CacheDir(m.dataDir), "asn")
	}
	// 相对路径按数据目录解释：库文件跟着数据目录一起搬才合理，按当前工作
	// 目录解释会在「从别处启动」时突然找不到。配置里的默认值自带 data/
	// 前缀，先剥掉再拼，否则会拼成 data/data/。
	if !filepath.IsAbs(raw) {
		raw = filepath.ToSlash(raw)
		raw = strings.TrimPrefix(raw, "./")
		raw = strings.TrimPrefix(raw, "data/")
		raw = filepath.Join(m.dataDir, filepath.FromSlash(raw))
	}
	if isMMDB(cfg.ASNSource) {
		return resolveDBPath(raw, mmdbName)
	}
	return dirOf(raw)
}

// isMMDB 报告数据源是否是 mmdb。
func isMMDB(source string) bool {
	return strings.EqualFold(strings.TrimSpace(source), SourceMMDB)
}

// libraryTarget 返回某一族的库文件应当写到哪个路径。
//
// 优先沿用已经在用的那个文件：用户手动放的可能是未压缩的原名，而我们下载
// 的是压缩件，写到另一个名字上的话，加载时仍然会读旧的那一个，更新等于
// 没有发生。
func libraryTarget(dir, name string) string {
	if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
		return filepath.Join(dir, name)
	}
	return filepath.Join(dir, name+".gz")
}

// libraryFile 返回用于取文件时间与判断存在性的那个文件。
func (m *Manager) libraryFile(cfg config.GeoConfig, path string) string {
	if isMMDB(cfg.ASNSource) {
		return path
	}
	for _, name := range []string{iptoASNv4Name, iptoASNv6Name} {
		if file, ok := findLibrary(path, name); ok {
			return file
		}
	}
	return filepath.Join(path, iptoASNv4Name)
}

// libraryExists 报告库文件是否已经存在。
func (m *Manager) libraryExists(cfg config.GeoConfig) bool {
	if isMMDB(cfg.ASNSource) {
		info, err := os.Stat(m.libraryPath(cfg))
		return err == nil && !info.IsDir()
	}
	path := m.libraryPath(cfg)
	for _, name := range []string{iptoASNv4Name, iptoASNv6Name} {
		if _, ok := findLibrary(path, name); ok {
			return true
		}
	}
	return false
}

// stale 报告库是否已经超过配置的更新周期。
//
// 周期为 0 表示「仅手动更新」，因此这里直接返回 false —— 用户把周期设成 0
// 就是想自己控制什么时候更新。
func (m *Manager) stale(cfg config.GeoConfig) bool {
	if cfg.ASNUpdateIntervalDays <= 0 {
		return false
	}
	path := m.libraryFile(cfg, m.libraryPath(cfg))
	info, err := os.Stat(path)
	if err != nil {
		return true
	}
	deadline := info.ModTime().Add(time.Duration(cfg.ASNUpdateIntervalDays) * 24 * time.Hour)
	return m.now().After(deadline)
}

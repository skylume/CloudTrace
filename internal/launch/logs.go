package launch

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 日志文件的位置与命名。
const (
	logDirName    = "logs"
	logFilePrefix = "cloudtrace-"
	logFileSuffix = ".log"
	logTimeLayout = "20060102"
)

// logFileName 返回某一天的日志文件名。
func logFileName(day time.Time) string {
	return logFilePrefix + day.Format(logTimeLayout) + logFileSuffix
}

// openLogFile 打开当天的日志文件。
//
// 按天切分而不是按大小：用户翻日志多半是为了「昨天那次扫描到底出了什么事」，
// 按大小滚动会把同一天的事切到两个文件里。保留天数也是按这个粒度算的。
func openLogFile(dataDir string, day time.Time) (*os.File, error) {
	dir := filepath.Join(dataDir, logDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(
		filepath.Join(dir, logFileName(day)),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0o600,
	)
}

// pruneLogs 删掉超过保留天数的日志文件，返回删掉的个数。
//
// 只认本程序生成的那种文件名：数据目录是用户的目录，一个 *.log 通配符就能把
// 他自己放在那儿的东西一起删掉。
func pruneLogs(dataDir string, keepDays int, now time.Time) (int, error) {
	if keepDays <= 0 {
		return 0, nil
	}

	dir := filepath.Join(dataDir, logDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		// 目录还不存在是正常情况：第一次运行时它还没被创建。
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}

	cutoff := now.AddDate(0, 0, -keepDays)
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		day, ok := parseLogFileName(entry.Name())
		if !ok || !day.Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// parseLogFileName 从文件名里解出日期。
//
// 判日期用文件名而不是文件修改时间：时间戳会被复制、解压、备份工具改掉，
// 而文件名是写下去时定好的。认不出的名字返回 false，调用方据此跳过。
func parseLogFileName(name string) (time.Time, bool) {
	if !strings.HasPrefix(name, logFilePrefix) || !strings.HasSuffix(name, logFileSuffix) {
		return time.Time{}, false
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(name, logFilePrefix), logFileSuffix)
	day, err := time.ParseInLocation(logTimeLayout, raw, time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return day, true
}

package launch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseLogFileName(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"cloudtrace-20261005.log", true},
		// 别人的文件：一个 *.log 通配符就能把用户放在那儿的东西一起删掉。
		{"app.log", false},
		{"cloudtrace.log", false},
		{"cloudtrace-2026-10-05.log", false},
		{"cloudtrace-20261305.log", false},
		{"cloudtrace-20261005.log.bak", false},
		{"README.md", false},
	}
	for _, tt := range cases {
		if _, ok := parseLogFileName(tt.name); ok != tt.ok {
			t.Errorf("parseLogFileName(%q) = %v，期望 %v", tt.name, ok, tt.ok)
		}
	}
}

func TestOpenLogFileCreatesDirAndAppends(t *testing.T) {
	dir := t.TempDir()
	day := time.Date(2026, 10, 5, 12, 0, 0, 0, time.Local)

	file, err := openLogFile(dir, day)
	if err != nil {
		t.Fatalf("打开日志失败：%v", err)
	}
	if _, err := file.WriteString("第一行\n"); err != nil {
		t.Fatalf("写入失败：%v", err)
	}
	_ = file.Close()

	// 再开一次应当追加而不是截断：一天里程序可能被重启好几次。
	again, err := openLogFile(dir, day)
	if err != nil {
		t.Fatalf("再次打开失败：%v", err)
	}
	if _, err := again.WriteString("第二行\n"); err != nil {
		t.Fatalf("写入失败：%v", err)
	}
	_ = again.Close()

	body, err := os.ReadFile(filepath.Join(dir, logDirName, "cloudtrace-20261005.log"))
	if err != nil {
		t.Fatalf("读日志失败：%v", err)
	}
	if string(body) != "第一行\n第二行\n" {
		t.Errorf("内容 = %q，期望两行都在", body)
	}
}

/**
 * 超过保留天数的日志被删掉，其余原样留着。
 *
 * 只删本程序自己生成的那种文件名——数据目录是用户的目录。
 */
func TestPruneLogsRemovesOnlyExpiredOwnFiles(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, logDirName)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}

	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.Local)
	write := func(name string) {
		if err := os.WriteFile(filepath.Join(logDir, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("写 %s 失败：%v", name, err)
		}
	}

	write("cloudtrace-20261005.log") // 今天
	write("cloudtrace-20261002.log") // 3 天前，保留 7 天时还在
	write("cloudtrace-20260920.log") // 15 天前，该删
	write("cloudtrace-20260901.log") // 34 天前，该删
	write("notes.txt")               // 不是我们生成的
	write("cloudtrace-20260920.log.bak")

	removed, err := pruneLogs(dir, 7, now)
	if err != nil {
		t.Fatalf("清理失败：%v", err)
	}
	if removed != 2 {
		t.Errorf("删了 %d 个，期望 2", removed)
	}

	left, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("列目录失败：%v", err)
	}
	names := map[string]bool{}
	for _, entry := range left {
		names[entry.Name()] = true
	}
	for _, keep := range []string{"cloudtrace-20261005.log", "cloudtrace-20261002.log", "notes.txt", "cloudtrace-20260920.log.bak"} {
		if !names[keep] {
			t.Errorf("%s 不该被删", keep)
		}
	}
	if names["cloudtrace-20260920.log"] || names["cloudtrace-20260901.log"] {
		t.Errorf("过期的日志没删干净：%v", names)
	}
}

// 保留天数为 0 时什么都不删：那是「没配」而不是「全删」。
func TestPruneLogsKeepsEverythingWhenKeepDaysZero(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, logDirName)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "cloudtrace-20200101.log"), []byte("x"), 0o600); err != nil {
		t.Fatalf("写失败：%v", err)
	}

	removed, err := pruneLogs(dir, 0, time.Now())
	if err != nil {
		t.Fatalf("清理失败：%v", err)
	}
	if removed != 0 {
		t.Errorf("删了 %d 个，期望 0", removed)
	}
}

// 日志目录还不存在时不该报错：第一次运行时它本来就还没建。
func TestPruneLogsToleratesMissingDir(t *testing.T) {
	removed, err := pruneLogs(t.TempDir(), 7, time.Now())
	if err != nil {
		t.Fatalf("目录不存在不该报错：%v", err)
	}
	if removed != 0 {
		t.Errorf("删了 %d 个，期望 0", removed)
	}
}

package atomicfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteCreatesAndOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.json")

	if err := Write(path, []byte(`{"v":1}`), 0o600); err != nil {
		t.Fatalf("首次写入失败：%v", err)
	}
	if got := readFile(t, path); got != `{"v":1}` {
		t.Fatalf("内容 = %q，期望 %q", got, `{"v":1}`)
	}

	// 覆盖已存在的文件：Windows 上 os.Rename 会失败，这里是那条兜底路径。
	if err := Write(path, []byte(`{"v":2}`), 0o600); err != nil {
		t.Fatalf("覆盖写入失败：%v", err)
	}
	if got := readFile(t, path); got != `{"v":2}` {
		t.Fatalf("覆盖后内容 = %q，期望 %q", got, `{"v":2}`)
	}
}

func TestWriteCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deep", "nested", "a.json")

	if err := Write(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("写入失败：%v", err)
	}
	if got := readFile(t, path); got != "x" {
		t.Fatalf("内容 = %q，期望 %q", got, "x")
	}
}

func TestWriteLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	if err := Write(filepath.Join(dir, "a.json"), []byte("x"), 0o600); err != nil {
		t.Fatalf("写入失败：%v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取目录失败：%v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "a.json" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("目录残留 = %v，期望只有 a.json", names)
	}
}

// 失败时既不能留下临时文件，也不能动到目标 —— 后者是不可逆的数据丢失。
func TestWriteFailureKeepsTargetAndCleansTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target")

	// 目标是一个目录：最后的改名必然失败，而临时文件已经被创建出来了。
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("准备目标目录失败：%v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "keep.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatalf("准备内容失败：%v", err)
	}

	if err := Write(path, []byte("new"), 0o600); err == nil {
		t.Fatal("期望写入失败，实际成功")
	}

	// 目标目录与里面的内容都还在。
	if got := readFile(t, filepath.Join(path, "keep.txt")); got != "keep" {
		t.Fatalf("目标内容 = %q，期望 %q（失败时不该动到目标）", got, "keep")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取目录失败：%v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".target.tmp") {
			t.Fatalf("失败后残留了临时文件：%s", e.Name())
		}
	}
}

// 父路径上有一环是文件时，建目录就失败，应在写任何东西之前退出。
func TestWriteRejectsNonDirParent(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("准备失败：%v", err)
	}

	if err := Write(filepath.Join(blocker, "a.json"), []byte("y"), 0o600); err == nil {
		t.Fatal("期望写入失败，实际成功")
	}
}

func TestRenameOverwritesTarget(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")

	if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
		t.Fatalf("准备源失败：%v", err)
	}
	if err := os.WriteFile(dst, []byte("old"), 0o600); err != nil {
		t.Fatalf("准备目标失败：%v", err)
	}

	if err := Rename(src, dst); err != nil {
		t.Fatalf("改名失败：%v", err)
	}
	if got := readFile(t, dst); got != "new" {
		t.Fatalf("目标内容 = %q，期望 %q", got, "new")
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("源仍存在：%v", err)
	}
}

// 源不存在时必须原样报错，**绝不能**顺手把目标删掉。
func TestRenameMissingSourceKeepsTarget(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "dst")

	if err := os.WriteFile(dst, []byte("precious"), 0o600); err != nil {
		t.Fatalf("准备目标失败：%v", err)
	}

	if err := Rename(filepath.Join(dir, "missing"), dst); err == nil {
		t.Fatal("期望改名失败，实际成功")
	}
	if got := readFile(t, dst); got != "precious" {
		t.Fatalf("目标内容 = %q，期望 %q（源不存在时不该删目标）", got, "precious")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", path, err)
	}
	return string(data)
}

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cloudtrace/internal/launch"
)

// --version 只打印版本号并正常退出，不起任何服务。
func TestRunShowsVersion(t *testing.T) {
	var out, errB bytes.Buffer
	code := run([]string{"--version"}, &out, &errB)
	if code != 0 {
		t.Fatalf("--version 应返回 0，实际 %d", code)
	}
	if got := out.String(); !strings.Contains(got, "cloudtrace "+version) {
		t.Errorf("应打印版本号，实际 %q", got)
	}
	if errB.Len() != 0 {
		t.Errorf("版本输出不应写 stderr：%q", errB.String())
	}
}

// 未识别的命令行参数：flag 解析失败，按原 ExitOnError 行为以 2 退出。
func TestRunBadFlagReturnsTwo(t *testing.T) {
	var out, errB bytes.Buffer
	code := run([]string{"--不存在的参数"}, &out, &errB)
	if code != 2 {
		t.Fatalf("坏参数应返回 2，实际 %d", code)
	}
	if errB.Len() == 0 {
		t.Error("坏参数应在 stderr 打印用法说明")
	}
}

// 第二个实例：已有实例持锁，这里只打印提示并返回 0，不起新面板。
func TestRunDetectsExistingInstance(t *testing.T) {
	t.Setenv(launch.RestartEnv, "")
	lock, first, err := launch.AcquireSingleInstance()
	if err != nil {
		t.Fatalf("取单实例锁失败：%v", err)
	}
	if !first {
		t.Skip("测试环境已有实例持锁，无法构造【已存在】场景")
	}
	defer lock.Release()

	var out, errB bytes.Buffer
	code := run([]string{"--no-browser"}, &out, &errB)
	if code != 0 {
		t.Fatalf("已有实例时应返回 0，实际 %d", code)
	}
	if msg := errB.String(); !strings.Contains(msg, "已有实例在运行") {
		t.Errorf("应提示已有实例在运行，实际 %q", msg)
	}
}

// 数据目录路径被普通文件挡住时，Prepare 失败：打印启动失败并返回 1。
//
// 不持锁：本测试要以第一个实例的身份走到 Prepare 那一步，而不是撞上「已有实例」。
func TestRunPrepareFailure(t *testing.T) {
	t.Setenv(launch.RestartEnv, "")
	base := t.TempDir()
	blocker := filepath.Join(base, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("建挡路文件失败：%v", err)
	}

	var out, errB bytes.Buffer
	code := run([]string{"--data-dir", filepath.Join(blocker, "sub")}, &out, &errB)
	if code != 1 {
		t.Fatalf("Prepare 失败应返回 1，实际 %d", code)
	}
	if msg := errB.String(); !strings.Contains(msg, "启动失败") {
		t.Errorf("应打印启动失败，实际 %q", msg)
	}
}

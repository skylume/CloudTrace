//go:build windows

package platform

import (
	"os/exec"
	"testing"
)

// 单实例锁：第一次取到 first=true，第二个实例 first=false，名字与名字一致，释放后可再取。
func TestSingleInstanceLockRoundTrip(t *testing.T) {
	name := "CloudTrace.Test.SingleInstance." + t.Name()
	lock, first, err := AcquireLock(name)
	if err != nil {
		t.Fatalf("取锁失败：%v", err)
	}
	if !first {
		t.Fatal("第一次取锁应是第一个实例")
	}
	if got := lock.Name(); got != name {
		t.Errorf("Name() = %q，期望 %q", got, name)
	}

	second, first, err := AcquireLock(name)
	if err != nil {
		t.Fatalf("第二个实例取锁失败：%v", err)
	}
	if first {
		t.Error("锁未释放时第二个实例不应是第一个")
	}
	second.Release()

	lock.Release()
	// 释放后可再取回，视为新的第一个实例。
	third, first, err := AcquireLock(name)
	if err != nil {
		t.Fatalf("释放后取锁失败：%v", err)
	}
	if !first {
		t.Error("释放后应能重新成为第一个实例")
	}
	third.Release()
}

// DPI 感知是运行时设置，不应报错也不应 panic。
func TestSetDPIAwareRuns(t *testing.T) {
	SetDPIAware() // 只要求不 panic
}

// 深色主题查询读取注册表，返回布尔，不 panic。
func TestPrefersDarkThemeRuns(t *testing.T) {
	_ = PrefersDarkTheme()
}

// DetachProcess 给子进程挂上脱离标志：DETACHED_PROCESS 位必须存在。
func TestDetachProcessSetsFlags(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "exit")
	DetachProcess(cmd)
	if cmd.SysProcAttr == nil {
		t.Fatal("DetachProcess 未设置 SysProcAttr")
	}
	if cmd.SysProcAttr.CreationFlags&detachedProcess == 0 {
		t.Errorf("缺少 DETACHED_PROCESS 位：%#x", cmd.SysProcAttr.CreationFlags)
	}
}

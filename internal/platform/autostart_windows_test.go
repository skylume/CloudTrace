//go:build windows

package platform

import (
	"testing"

	"golang.org/x/sys/windows/registry"
)

// testAutostart 返回一个写在临时键下的自启项。
//
// **不能直接用默认的 Run 键**：跑一次用例就往用户的开机项里加一条 CloudTrace，
// 而开发者多半不希望在跑测试时被悄悄打开自启。临时键连值带键一起删干净。
func testAutostart(t *testing.T) Autostart {
	t.Helper()
	a := Autostart{
		KeyPath: `Software\CloudTraceTest\Autostart`,
		Value:   "CloudTraceTest",
	}
	t.Cleanup(func() {
		_ = a.Set("", false)
		// 值删掉之后键本身还留着，一并清理，免得在注册表里攒垃圾。
		_ = registry.DeleteKey(registry.CURRENT_USER, a.KeyPath)
	})
	return a
}

func TestAutostartRoundTrip(t *testing.T) {
	a := testAutostart(t)
	exe := `C:\Program Files\CloudTrace\cloudtrace-panel.exe`

	if on, err := a.Enabled(exe); err != nil || on {
		t.Fatalf("初始状态应为未开启：on=%v err=%v", on, err)
	}

	if err := a.Set(exe, true); err != nil {
		t.Fatalf("开启自启失败：%v", err)
	}
	if on, err := a.Enabled(exe); err != nil || !on {
		t.Fatalf("开启之后应显示已启用：on=%v err=%v", on, err)
	}

	if err := a.Set(exe, false); err != nil {
		t.Fatalf("关闭自启失败：%v", err)
	}
	if on, err := a.Enabled(exe); err != nil || on {
		t.Fatalf("关闭之后仍显示已启用：on=%v err=%v", on, err)
	}
}

/**
 * 路径不同就算「没开」。
 *
 * 程序被挪到别处之后，旧路径还留在注册表里：值存在，但指向的东西已经没了。
 * 只判断「值存不存在」的话，界面会一直显示已启用，而实际开机什么都不会发生。
 */
func TestAutostartDetectsMovedExecutable(t *testing.T) {
	a := testAutostart(t)
	oldExe := `C:\old\cloudtrace-panel.exe`

	if err := a.Set(oldExe, true); err != nil {
		t.Fatalf("写入自启失败：%v", err)
	}

	if on, err := a.Enabled(`D:\new\cloudtrace-panel.exe`); err != nil || on {
		t.Errorf("换路径之后应显示未启用：on=%v err=%v", on, err)
	}
}

// Sync 负责把这种「值还在、指向已失效」的状态修回来。
func TestAutostartSyncRepairsStaleEntry(t *testing.T) {
	a := testAutostart(t)
	exe := `C:\current\cloudtrace-panel.exe`

	if err := a.Set(`C:\old\cloudtrace-panel.exe`, true); err != nil {
		t.Fatalf("写入自启失败：%v", err)
	}
	if err := a.Sync(exe, true); err != nil {
		t.Fatalf("同步失败：%v", err)
	}
	if on, err := a.Enabled(exe); err != nil || !on {
		t.Errorf("同步之后应指向当前路径：on=%v err=%v", on, err)
	}
}

// 关闭一个本来就没开过的自启项不该报错：目标状态已经达成。
func TestAutostartDisableWhenAbsent(t *testing.T) {
	a := testAutostart(t)
	if err := a.Set(`C:\any\cloudtrace.exe`, false); err != nil {
		t.Errorf("关闭不存在的自启项报错了：%v", err)
	}
}

// 路径里的空格必须靠引号保住：不带引号时系统会把它拆成「程序 + 参数」两段，
// 自启静默失效。
func TestAutostartQuotesPathWithSpaces(t *testing.T) {
	a := testAutostart(t)
	exe := `C:\Program Files\Cloud Trace\cloudtrace.exe`

	if err := a.Set(exe, true); err != nil {
		t.Fatalf("写入自启失败：%v", err)
	}
	if on, err := a.Enabled(exe); err != nil || !on {
		t.Errorf("带空格的路径往返失败：on=%v err=%v", on, err)
	}
}

package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultWindowStateIsUsable(t *testing.T) {
	st := DefaultWindowState()
	if st.Width < minWindowWidth || st.Height < minWindowHeight {
		t.Fatalf("默认尺寸 %dx%d 比下限还小", st.Width, st.Height)
	}
	if got := st.Sanitize(); got != st {
		t.Errorf("默认值本身应当通过检查，实际被改成 %+v", got)
	}
}

/**
 * 尺寸为 0 会得到一个看不见的窗口，而用户只会以为程序没启动。
 *
 * 这类值多半来自一份损坏的记忆文件，所以要在读回来的时候挡住。
 */
func TestSanitizeReplacesUnusableSizes(t *testing.T) {
	def := DefaultWindowState()

	// 只替换不合理的那一维：用户把窗口调成窄而高是有意的，不该连高度一起重置。
	t.Run("宽为 0 时只换宽度", func(t *testing.T) {
		got := WindowState{Width: 0, Height: 800}.Sanitize()
		if got.Width != def.Width {
			t.Errorf("宽度 = %d，期望默认值 %d", got.Width, def.Width)
		}
		if got.Height != 800 {
			t.Errorf("高度被改成了 %d，用户选的 800 应当保留", got.Height)
		}
	})

	t.Run("高为 0 时只换高度", func(t *testing.T) {
		got := WindowState{Width: 1280, Height: 0}.Sanitize()
		if got.Height != def.Height {
			t.Errorf("高度 = %d，期望默认值 %d", got.Height, def.Height)
		}
		if got.Width != 1280 {
			t.Errorf("宽度被改成了 %d，用户选的 1280 应当保留", got.Width)
		}
	})

	t.Run("过窄过矮都换掉", func(t *testing.T) {
		got := WindowState{Width: 100, Height: 100}.Sanitize()
		if got.Width != def.Width || got.Height != def.Height {
			t.Errorf("过小的尺寸没被换掉：%+v", got)
		}
	})

	t.Run("尺寸离谱也换掉", func(t *testing.T) {
		got := WindowState{Width: 999999, Height: 800}.Sanitize()
		if got.Width != def.Width {
			t.Errorf("宽度 = %d，期望默认值", got.Width)
		}
	})
}

// 位置为 0 是合法的（主屏左上角），不能被当成「没设过」而重置。
func TestSanitizeKeepsOriginPosition(t *testing.T) {
	got := WindowState{X: 0, Y: 0, Width: 1280, Height: 800}.Sanitize()
	if got.X != 0 || got.Y != 0 {
		t.Errorf("原点的位置被改成了 %+v", got)
	}
}

// 多屏拔掉之后系统里可能残留一个远在屏幕外的坐标，照搬过去窗口就看不见了。
func TestSanitizeRejectsFarOffscreenPosition(t *testing.T) {
	got := WindowState{X: -50000, Y: 40000, Width: 1280, Height: 800}.Sanitize()
	if got.X != 0 || got.Y != 0 {
		t.Errorf("离谱的坐标没被清掉：%+v", got)
	}
}

func TestWindowStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "window.json")
	want := WindowState{X: 120, Y: 80, Width: 1440, Height: 900}

	if err := SaveWindowState(path, want); err != nil {
		t.Fatalf("保存失败：%v", err)
	}
	if got := LoadWindowState(path); got != want {
		t.Errorf("读回来 = %+v，期望 %+v", got, want)
	}
}

// 文件不存在、内容损坏都退回默认值，不报错：窗口位置是纯体验问题。
func TestLoadWindowStateFallsBack(t *testing.T) {
	dir := t.TempDir()

	if got := LoadWindowState(filepath.Join(dir, "nope.json")); got != DefaultWindowState() {
		t.Errorf("文件不存在时 = %+v，期望默认值", got)
	}

	broken := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(broken, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("写入失败：%v", err)
	}
	if got := LoadWindowState(broken); got != DefaultWindowState() {
		t.Errorf("文件损坏时 = %+v，期望默认值", got)
	}
}

// 存下去的必须是清理过的值：否则一份坏文件会被原样传下去，永远修不好。
func TestSaveWindowStateSanitizes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "window.json")
	if err := SaveWindowState(path, WindowState{Width: 0, Height: 0}); err != nil {
		t.Fatalf("保存失败：%v", err)
	}
	if got := LoadWindowState(path); got.Width < minWindowWidth {
		t.Errorf("存进去的坏尺寸没被清理：%+v", got)
	}
}

func TestSaveWindowStateRejectsEmptyPath(t *testing.T) {
	if err := SaveWindowState("", DefaultWindowState()); err == nil {
		t.Error("空路径应当报错")
	}
}

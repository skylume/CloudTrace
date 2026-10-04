package platform

import "testing"

func TestTrayMenuShape(t *testing.T) {
	calls := map[string]int{}
	deps := TrayDeps{
		Running: func() bool { return false },
		Show:    func() { calls[TrayShow]++ },
		Stop:    func() { calls[TrayStop]++ },
		Quit:    func() { calls[TrayQuit]++ },
	}
	items := TrayMenu(deps, TrayLabelsFor("zh"))

	if len(items) != 3 {
		t.Fatalf("菜单项数 = %d，期望 3", len(items))
	}
	for _, item := range items {
		if item.Label == "" {
			t.Errorf("菜单项 %q 没有文案", item.ID)
		}
		if item.Run == nil {
			t.Errorf("菜单项 %q 点了没反应", item.ID)
		}
	}
}

// 每项点下去要调到对应的能力，不能串。
func TestTrayMenuDispatches(t *testing.T) {
	calls := map[string]int{}
	deps := TrayDeps{
		Running: func() bool { return true },
		Show:    func() { calls[TrayShow]++ },
		Stop:    func() { calls[TrayStop]++ },
		Quit:    func() { calls[TrayQuit]++ },
	}

	for _, item := range TrayMenu(deps, TrayLabelsFor("zh")) {
		item.Run()
	}

	for _, id := range []string{TrayShow, TrayStop, TrayQuit} {
		if calls[id] != 1 {
			t.Errorf("%q 被调用了 %d 次，期望 1", id, calls[id])
		}
	}
}

/**
 * 「停止」只在有任务可停时才是可点的。
 *
 * 一个点了没反应的菜单项，用户会以为是程序卡住了——而托盘里本来就没有别的
 * 反馈渠道。
 */
func TestTrayStopEnabledFollowsRunning(t *testing.T) {
	running := false
	deps := TrayDeps{
		Running: func() bool { return running },
		Show:    func() {},
		Stop:    func() {},
		Quit:    func() {},
	}

	find := func(id string) TrayItem {
		for _, item := range TrayMenu(deps, TrayLabelsFor("zh")) {
			if item.ID == id {
				return item
			}
		}
		t.Fatalf("菜单里没有 %q", id)
		return TrayItem{}
	}

	stop := find(TrayStop)
	if stop.Clickable() {
		t.Error("没有任务在跑时「停止」不该可点")
	}
	running = true
	if !stop.Clickable() {
		t.Error("有任务在跑时「停止」应当可点")
	}

	// 「显示」与「退出」始终可点：无论有没有任务在跑，这两件事都得能做。
	for _, id := range []string{TrayShow, TrayQuit} {
		if !find(id).Clickable() {
			t.Errorf("%q 应当始终可点", id)
		}
	}
}

// 「启动」不在菜单里，理由见 TrayMenu 的注释。
func TestTrayMenuHasNoStart(t *testing.T) {
	items := TrayMenu(TrayDeps{}, TrayLabelsFor("zh"))
	for _, item := range items {
		if item.ID == "start" {
			t.Error("托盘里不该有「启动」：开一次扫描要用面板上那套参数")
		}
	}
}

func TestTrayLabelsFollowLanguage(t *testing.T) {
	zh := TrayLabelsFor("zh")
	en := TrayLabelsFor("en")

	if zh.Quit == en.Quit {
		t.Errorf("中英文案的退出项相同：%q", zh.Quit)
	}
	if zh.AppName != en.AppName {
		t.Error("应用名不该跟着语言变")
	}
	// 认不出的语言退回中文，而不是给出一套空文案。
	if got := TrayLabelsFor("de"); got.Quit != zh.Quit {
		t.Errorf("认不出的语言应退回中文，实际 %q", got.Quit)
	}
}

func TestTrayTooltip(t *testing.T) {
	zh := TrayLabelsFor("zh")

	cases := []struct {
		status  string
		percent int
		want    string
	}{
		{"idle", 0, "CloudTrace"},
		{"running", 45, "CloudTrace · 进行中 45%"},
		{"done", 100, "CloudTrace · 已完成"},
		{"failed", 0, "CloudTrace · 失败"},
		{"aborted", 0, "CloudTrace"},
	}
	for _, tt := range cases {
		if got := TrayTooltip(zh, tt.status, tt.percent); got != tt.want {
			t.Errorf("TrayTooltip(%q, %d) = %q，期望 %q", tt.status, tt.percent, got, tt.want)
		}
	}
}

// 进度可能越界（总数未知、或状态与计数不同步），提示里不该出现 -5% 或 300%。
func TestTrayTooltipClampsPercent(t *testing.T) {
	zh := TrayLabelsFor("zh")

	if got := TrayTooltip(zh, "running", -5); got != "CloudTrace · 进行中 0%" {
		t.Errorf("负数进度 = %q", got)
	}
	if got := TrayTooltip(zh, "running", 300); got != "CloudTrace · 进行中 100%" {
		t.Errorf("越界进度 = %q", got)
	}
}

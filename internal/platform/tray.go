package platform

import "strconv"

// TrayLabels 是托盘菜单与提示的文案。
//
// 由调用方传入而不是写死在这里：这个包不认识语言，而托盘和界面用的是同一套
// 语言设置——用户在设置里切到英文，托盘菜单却还是中文，是那种「说不上哪里不对
// 但就是没做完」的细节。
type TrayLabels struct {
	// AppName 是应用名，用于空闲时的托盘提示。
	AppName string
	Show    string
	Stop    string
	Quit    string
	// Running / Done / Failed 是托盘提示里的状态词。
	Running string
	Done    string
	Failed  string
}

// TrayLabelsFor 按语言取一套文案。
func TrayLabelsFor(lang string) TrayLabels {
	if lang == "en" {
		return TrayLabels{
			AppName: "CloudTrace",
			Show:    "Show window",
			Stop:    "Stop current task",
			Quit:    "Quit",
			Running: "running",
			Done:    "done",
			Failed:  "failed",
		}
	}
	return TrayLabels{
		AppName: "CloudTrace",
		Show:    "显示主窗口",
		Stop:    "停止当前任务",
		Quit:    "退出",
		Running: "进行中",
		Done:    "已完成",
		Failed:  "失败",
	}
}

// TrayDeps 是托盘菜单要用到的能力，由桌面壳注入。
type TrayDeps struct {
	// Running 报告当前是否有任务在跑。
	Running func() bool
	// Show 把主窗口调到前面。
	Show func()
	// Stop 中止当前任务。
	Stop func()
	// Quit 退出程序。
	Quit func()
}

// 托盘菜单项标识。
const (
	TrayShow = "show"
	TrayStop = "stop"
	TrayQuit = "quit"
)

// TrayItem 是托盘菜单里的一项。
//
// Enabled 为 nil 表示始终可点。
type TrayItem struct {
	ID      string
	Label   string
	Enabled func() bool
	Run     func()
}

// Clickable 报告这一项现在能不能点。
func (i TrayItem) Clickable() bool {
	return i.Enabled == nil || i.Enabled()
}

// TrayMenu 组装托盘菜单。
//
// **「启动」不在菜单里。** 开一次扫描要用面板上那套参数——档位、来源文本、
// 各项覆盖值——而托盘没有这些输入。放一个「用某个默认值开始扫描」的入口，
// 等于让用户用一组他没选过的参数去跑，比没有这个入口更糟：跑出来的结果他
// 不会知道为什么和上次不一样。
//
// 「停止」则不一样：它不需要任何参数，而且在后台跑着的任务正需要一个不用
// 把窗口翻出来的中止方式。
func TrayMenu(deps TrayDeps, labels TrayLabels) []TrayItem {
	return []TrayItem{
		{ID: TrayShow, Label: labels.Show, Run: deps.Show},
		{ID: TrayStop, Label: labels.Stop, Enabled: deps.Running, Run: deps.Stop},
		{ID: TrayQuit, Label: labels.Quit, Run: deps.Quit},
	}
}

// TrayTooltip 按任务状态算出托盘提示文案。
//
// 这就是「托盘通知」的落地：任务在后台跑的时候，用户把鼠标移到托盘图标上
// 就能看到进度，不必把窗口翻出来。
//
// 结束状态受「完成提醒 / 失败提醒」控制：关掉之后托盘回到应用名——**不提醒**
// 的含义就是别拿这件事打扰我，而托盘提示也是一次提醒。进行中的进度不受它们
// 影响：那是状态，不是提醒。
//
// 状态用字符串而不是某个结构体，是为了让这个函数不依赖业务包——它只做拼装。
func TrayTooltip(labels TrayLabels, status string, percent int, onDone, onFail bool) string {
	switch status {
	case "running":
		if percent < 0 {
			percent = 0
		}
		if percent > 100 {
			percent = 100
		}
		return labels.AppName + " · " + labels.Running + " " + strconv.Itoa(percent) + "%"
	case "done":
		if !onDone {
			return labels.AppName
		}
		return labels.AppName + " · " + labels.Done
	case "failed":
		if !onFail {
			return labels.AppName
		}
		return labels.AppName + " · " + labels.Failed
	default:
		return labels.AppName
	}
}

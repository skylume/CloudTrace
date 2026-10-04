package platform

import (
	"encoding/json"
	"errors"
	"os"

	"cloudtrace/internal/atomicfile"
)

// WindowState 是窗口位置与尺寸的记忆。
//
// 位置也记：用户把窗口挪到副屏、调到顺手的大小，下次打开又回到屏幕中央，
// 是那种「说不上哪里不对但就是烦」的问题。
type WindowState struct {
	X         int  `json:"x"`
	Y         int  `json:"y"`
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	Maximised bool `json:"maximised,omitempty"`
}

// 窗口尺寸的合理范围。
//
// 下限不是审美问题：宽高为 0 会得到一个看不见的窗口，而用户只会以为程序没启动。
const (
	minWindowWidth  = 480
	minWindowHeight = 360
	// maxWindowSpan 挡住明显不合理的坐标：多屏拔掉之后，系统里可能残留一个
	// 远在屏幕外的位置，照搬过去窗口就在可见区域之外了。
	maxWindowSpan = 20000
)

// DefaultWindowState 返回第一次打开时的窗口状态。
//
// 取函数而不是包级变量：包级变量会被任何一处调用方改掉，而它的语义是常量。
func DefaultWindowState() WindowState {
	return WindowState{Width: 1280, Height: 820}
}

// Sanitize 把明显不合理的值换成默认值。
//
// 只挡「明显不合理」，不去猜屏幕布局——那需要平台信息，而这个函数要能单测。
// 位置为 0 是合法的（主屏左上角），因此坐标只做范围检查。
func (s WindowState) Sanitize() WindowState {
	def := DefaultWindowState()

	if s.Width < minWindowWidth || s.Width > maxWindowSpan {
		s.Width = def.Width
	}
	if s.Height < minWindowHeight || s.Height > maxWindowSpan {
		s.Height = def.Height
	}
	if s.X < -maxWindowSpan || s.X > maxWindowSpan {
		s.X = 0
	}
	if s.Y < -maxWindowSpan || s.Y > maxWindowSpan {
		s.Y = 0
	}
	return s
}

// LoadWindowState 读回上次的窗口状态。
//
// 读不出来（文件不存在、内容损坏）时返回默认值且不报错：窗口位置是纯体验
// 问题，为它让程序起不来是本末倒置。
func LoadWindowState(path string) WindowState {
	data, err := os.ReadFile(path)
	if err != nil {
		return DefaultWindowState()
	}
	var st WindowState
	if err := json.Unmarshal(data, &st); err != nil {
		return DefaultWindowState()
	}
	return st.Sanitize()
}

// SaveWindowState 原子写回窗口状态。
//
// 原子写：退出时被强杀、磁盘满，都不该在文件里留下半截 JSON——下次读到的
// 会是一份解析不了的内容，窗口位置于是每次都重置。
func SaveWindowState(path string, st WindowState) error {
	if path == "" {
		return errors.New("platform: 窗口状态文件路径为空")
	}
	data, err := json.Marshal(st.Sanitize())
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicfile.Write(path, data, 0o600)
}

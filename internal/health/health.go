// Package health 做配置体检：把「配错了」翻译成一句人话 + 一条建议。
//
// 检查只依赖配置与少量可注入的探测函数，不反向依赖扫描 / 测速 / 历史等
// 上层包——体检要给整个程序做诊断，自己却先被上层依赖住就本末倒置了。
package health

import (
	"context"
	"sort"

	"cloudtrace/internal/config"
)

// Level 是问题等级。
type Level string

const (
	// LevelWarning 是「能跑但结果会受影响」，例如并发过高。
	LevelWarning Level = "warning"
	// LevelError 是「这一项已经不可用」，例如数据目录写不进去。
	LevelError Level = "error"
)

// Fix 是一键修复动作：把哪个配置键改成什么。
type Fix struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
	Label string `json:"label"`
}

// Issue 是一处体检发现。
type Issue struct {
	// Key 是检查项标识，与检查一一对应，前端据此定位到设置项。
	Key string `json:"key"`
	// Level 是问题等级。
	Level Level `json:"level"`
	// Problem 是「出了什么事」的人话描述。
	Problem string `json:"problem"`
	// Suggestion 是「该怎么办」。
	Suggestion string `json:"suggestion"`
	// Field 是相关的配置键（可为空），供前端高亮。
	Field string `json:"field,omitempty"`
	// Fixable 表示是否提供一键修复。
	Fixable bool `json:"fixable"`
	// Fix 在 Fixable 为真时给出具体动作。
	Fix *Fix `json:"fix,omitempty"`
}

// Report 是一次体检的结果。
type Report struct {
	Issues []Issue `json:"issues"`
	// Checked 是本次实际跑过的检查项数量。
	//
	// 有些检查依赖运行时信息（出口地区、测速源可达性），拿不到就跳过，
	// 因此数量不一定是 7。前端据此区分「全通过」与「只查了三项」。
	Checked int `json:"checked"`
}

// Options 是体检需要的运行时输入。
//
// 想测的东西全部可注入：真实环境下跑真探测，测试里换成假实现，就能让
// 七项检查各自被稳定触发。
type Options struct {
	// DataDir 是解析后的数据目录，为空则跳过数据目录检查。
	DataDir string
	// ASNDBPath 是解析后的 ASN 库路径，为空则跳过 ASN 检查。
	ASNDBPath string
	// ListeningPort 是本进程当前占用的端口。
	//
	// 要排除它：面板正在跑时端口当然是被占用的——占用者就是自己，报「端口
	// 冲突」是误报。
	ListeningPort int
	// ExitCountry 是最近一次探测到的出口国家码，为空表示还不知道。
	ExitCountry string
	// PortInUse 判断端口是否被别人占用，为 nil 时尝试真实绑定。
	PortInUse func(port int) bool
	// DirWritable 判断目录是否可写，为 nil 时真实写一个临时文件再删掉。
	DirWritable func(dir string) error
	// SourceReachable 判断测速源是否可达，为 nil 时跳过该项。
	SourceReachable func(ctx context.Context) error
}

// Check 跑一遍体检。
//
// 单项检查失败（例如探测本身出错）不会让整份报告失败：体检的价值在于把
// 能查到的都报出来，缺一项比全盘皆无好。
func Check(ctx context.Context, cfg config.Config, opts Options) Report {
	rep := Report{}
	for _, fn := range []func(context.Context, config.Config, Options) *Issue{
		checkPort,
		checkWorkers,
		checkLatencyThreshold,
		checkDataDir,
		checkASNDB,
		checkProxyEnv,
		checkSpeedSource,
	} {
		issue := fn(ctx, cfg, opts)
		if issue == nil {
			// 返回 nil 表示「没查出问题」。
			rep.Checked++
			continue
		}
		if issue == skipIssue {
			// 拿不到输入、这一项没法查，不计入已查数量。
			continue
		}
		rep.Checked++
		rep.Issues = append(rep.Issues, *issue)
	}

	sort.SliceStable(rep.Issues, func(i, j int) bool {
		// 错误排在警告前面，同级别按检查项标识排序，保证输出稳定。
		if rep.Issues[i].Level != rep.Issues[j].Level {
			return rep.Issues[i].Level == LevelError
		}
		return rep.Issues[i].Key < rep.Issues[j].Key
	})
	return rep
}

// skipIssue 是「这一项没法查」的哨兵。
var skipIssue = &Issue{}

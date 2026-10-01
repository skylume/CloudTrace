package scan

import (
	"context"

	"cloudtrace/internal/model"
)

// Reporter 是扫描流程向编排层汇报进度的入口。
//
// 这里只声明扫描真正用到的几个方法，不绑定具体编排实现：任何具备这个
// 形状的载体都能传进来，测试里也就不必拉起整套事件总线。
type Reporter interface {
	// Context 返回任务级 context；中止信号由它传递。
	Context() context.Context
	// SetTotal 设置总步数。各阶段的工作量取决于前一阶段的结果，因此这个
	// 值会在流程中变大，进度条只会往前走。
	SetTotal(total int)
	// SetDone 设置已完成步数。
	SetDone(done int)
	// SetFunnel 更新漏斗计数。
	SetFunnel(f model.Funnel)
	// Emit 发布一条业务事件。
	Emit(topic string, payload any)
}

// stages 累积各阶段的进度。
//
// 总步数没法一次定死：阶段二的目标数要等阶段一跑完才知道，校验阶段的
// 工作量又取决于前面的结果。按阶段累加，进度条只会往前走；一次定死只能
// 按最坏情况估，进度条会先冲到顶再退回。
type stages struct {
	rep    Reporter
	offset int // 已结束阶段累计的步数
	total  int // 当前已知的总步数
}

// begin 进入一个新阶段，把它的步数加进总数。
func (s *stages) begin(n int) {
	s.offset = s.total
	s.total += n
	s.rep.SetTotal(s.total)
	s.rep.SetDone(s.offset)
}

// advance 报告当前阶段已完成 n 步。
func (s *stages) advance(n int) { s.rep.SetDone(s.offset + n) }

// finish 把进度推到终点。
func (s *stages) finish() { s.rep.SetDone(s.total) }

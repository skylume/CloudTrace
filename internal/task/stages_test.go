package task

import (
	"context"
	"testing"

	"cloudtrace/internal/model"
)

// 阶段累加必须让总步数与已完成数都只增不减。
//
// 后一阶段的目标数要等前一阶段跑完才知道，一次定死总步数会让进度条先冲到
// 顶再退回，用户看到的是「进度倒着走」。
func TestStagesAccumulateMonotonically(t *testing.T) {
	rep := &stagesRecorder{}
	st := NewStages(rep)

	st.Begin(4)
	rep.want(t, 0, 4, "进入阶段一")

	st.Advance(2)
	rep.want(t, 2, 4, "阶段一推进 2 步")

	// 新阶段：总步数从 4 涨到 7，已完成数停在阶段起点 4，不回退也不跳过。
	st.Begin(3)
	rep.want(t, 4, 7, "进入阶段二")

	st.Advance(1)
	rep.want(t, 5, 7, "阶段二推进 1 步")

	st.Finish()
	rep.want(t, 7, 7, "收尾")
}

// stagesRecorder 记录最近一次的 total / done 取值。
type stagesRecorder struct {
	total int
	done  int
}

func (r *stagesRecorder) Context() context.Context { return context.Background() }
func (r *stagesRecorder) SetTotal(total int)       { r.total = total }
func (r *stagesRecorder) SetDone(done int)         { r.done = done }
func (r *stagesRecorder) SetFunnel(model.Funnel)   {}
func (r *stagesRecorder) SetPreset(string)         {}
func (r *stagesRecorder) Emit(string, any)         {}

func (r *stagesRecorder) want(t *testing.T, done, total int, what string) {
	t.Helper()
	if r.done != done || r.total != total {
		t.Errorf("%s 后进度 = %d/%d，期望 %d/%d", what, r.done, r.total, done, total)
	}
}

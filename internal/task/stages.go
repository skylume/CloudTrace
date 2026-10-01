package task

// Stages 把流水线各阶段的进度累加成一条单调递增的进度条。
//
// 总步数没法一次定死：后一阶段的目标数往往要等前一阶段跑完才知道（先粗筛
// 才知道要精测几个、先测可用性才知道要下载几个）。按阶段累加，进度条只会
// 往前走；一次按最坏情况定死，进度条会先冲到顶再退回。
//
// 各阶段都要显式 Begin：Begin 之前的 SetDone 不归它管，混着用会让进度
// 跳变。
type Stages struct {
	rep    Reporter
	offset int // 已结束阶段累计的步数
	total  int // 当前已知的总步数
}

// NewStages 构造阶段累加器。
func NewStages(rep Reporter) *Stages { return &Stages{rep: rep} }

// Begin 进入一个新阶段，把它的步数加进总数，并把进度推到该阶段起点。
func (s *Stages) Begin(n int) {
	s.offset = s.total
	s.total += n
	s.rep.SetTotal(s.total)
	s.rep.SetDone(s.offset)
}

// Advance 报告当前阶段已完成 n 步。
func (s *Stages) Advance(n int) { s.rep.SetDone(s.offset + n) }

// Finish 把进度推到当前已知的终点。
func (s *Stages) Finish() { s.rep.SetDone(s.total) }

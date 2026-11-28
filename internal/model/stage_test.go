package model

import "testing"

func TestNewStageStatDoneIncludesSkipped(t *testing.T) {
	st := NewStageStat(StagePool, 10, 6, 1, 3, 0.5)
	if st.Done != 10 {
		t.Errorf("Done = %d，期望 10（OK+Failed+Skipped）", st.Done)
	}
	if st.Done != st.OK+st.Failed+st.Skipped {
		t.Errorf("Done = %d，与三类结果之和不一致", st.Done)
	}
	if st.Elapsed != 0.5 {
		t.Errorf("Elapsed = %v，期望 0.5", st.Elapsed)
	}
}

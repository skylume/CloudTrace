package scan

import (
	"context"
	"sync"
	"testing"

	"cloudtrace/internal/model"
)

// 注入的归属地补齐要作用到每条产出上，且补齐的内容要能进最终结果。
func TestRunAppliesEnrichToEveryRecord(t *testing.T) {
	params := scanParams("1.1.1.1\n1.1.1.2\n1.1.1.3")
	params.VerifyNodes = false
	params.Workers = 2

	var mu sync.Mutex
	var seen []string

	d := &deps{
		prober: &fakeProber{fallback: 50},
		enrich: func(rec *model.IPRecord) {
			mu.Lock()
			seen = append(seen, rec.IP)
			mu.Unlock()
			rec.RegionName = "香港"
			rec.ASN = 13335
			rec.ASOrg = "CLOUDFLARENET"
		},
	}
	runner := newRunnerForTest(t, params, d)

	var got []model.IPRecord
	runner.SetOnDone(func(res model.TaskResult) { got = res.Records })

	if _, err, _ := runScan(t, runner, context.Background()); err != nil {
		t.Fatalf("Run 返回错误：%v", err)
	}

	mu.Lock()
	calls := len(seen)
	mu.Unlock()
	if calls != 3 {
		t.Fatalf("补齐被调用了 %d 次，期望每个节点一次（3 次）", calls)
	}
	if len(got) != 3 {
		t.Fatalf("结果数 = %d，期望 3", len(got))
	}
	for _, rec := range got {
		if rec.RegionName != "香港" || rec.ASN != 13335 || rec.ASOrg != "CLOUDFLARENET" {
			t.Errorf("结果 %s 没有带上补齐的归属信息：%+v", rec.IP, rec)
		}
		// 补齐不能改动探测本身的结果。
		if rec.Latency != 50 {
			t.Errorf("补齐改动了延迟：%v", rec.Latency)
		}
	}
}

// 不注入补齐时一切照旧：这是关掉 ASN 查询时的状态。
func TestRunWithoutEnrich(t *testing.T) {
	params := scanParams("1.1.1.1\n1.1.1.2")
	params.VerifyNodes = false

	runner := newRunnerForTest(t, params, &deps{prober: &fakeProber{fallback: 50}})

	var got []model.IPRecord
	runner.SetOnDone(func(res model.TaskResult) { got = res.Records })

	if _, err, _ := runScan(t, runner, context.Background()); err != nil {
		t.Fatalf("Run 返回错误：%v", err)
	}
	if len(got) != 2 {
		t.Fatalf("结果数 = %d，期望 2", len(got))
	}
	for _, rec := range got {
		if rec.RegionName != "" || rec.ASN != 0 {
			t.Errorf("没有注入补齐却出现了归属信息：%+v", rec)
		}
	}
}

// 补齐回调里 panic 不能把整轮扫描带走：它由外部注入，出错的面比探测层大。
func TestRunSurvivesEnrichPanic(t *testing.T) {
	params := scanParams("1.1.1.1")
	params.VerifyNodes = false

	d := &deps{
		prober: &fakeProber{fallback: 50},
		enrich: func(*model.IPRecord) { panic("补齐炸了") },
	}

	// 走明细采集那条路径：它自带 panic 兜底。
	params.VerifyNodes = true
	runner := newRunnerForTest(t, params, d)

	var got []model.IPRecord
	runner.SetOnDone(func(res model.TaskResult) { got = res.Records })

	count, err, _ := runScan(t, runner, context.Background())
	if err != nil {
		t.Fatalf("补齐回调 panic 不该让整轮扫描失败：%v", err)
	}
	// 丢一条记录可以接受，整轮崩掉不行。
	if count != 0 || len(got) != 0 {
		t.Fatalf("panic 的节点应当被丢弃，实际 count=%d records=%d", count, len(got))
	}
}

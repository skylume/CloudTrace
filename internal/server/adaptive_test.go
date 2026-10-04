package server

import (
	"context"
	"testing"
	"time"

	"cloudtrace/internal/adaptive"
	"cloudtrace/internal/model"
	"cloudtrace/internal/speed"
)

// 固定的出口探测结果，避免用例碰真实网络。
type fixedProbe struct {
	info speed.ISPInfo
	err  error
}

func (p fixedProbe) probe(context.Context) (speed.ISPInfo, error) { return p.info, p.err }

// mobileExit 造一个「出口是中国移动」的服务端。
func mobileExit(t *testing.T, st *testStack, mobile bool) {
	t.Helper()
	info := speed.ISPInfo{IP: "1.1.1.1", ASN: 13335, Org: "CLOUDFLARENET"}
	if mobile {
		info = speed.ISPInfo{IP: "100.64.0.1", ASN: 9808, Org: "CHINA MOBILE"}
	}
	st.srv.speedSource = speed.NewSourceResolver(fixedProbe{info: info}.probe, time.Now, speed.DefaultSourceTTL)
}

// 先把手填的并发设成一个偏高值，模拟「用户自己调过」。
func setUserWorkers(t *testing.T, st *testStack, workers int) {
	t.Helper()
	if _, err := st.store.Patch(
		map[string]any{"scan": map[string]any{"workers": workers}},
		model.ParamOrigins{"scan.workers": model.OriginUser},
	); err != nil {
		t.Fatalf("写入配置失败：%v", err)
	}
}

/**
 * 智能推荐是显式操作：它能改用户手填过的值。
 *
 * 这是它与自动自适应唯一的区别，也是规格里「显式操作，不受限制」那句话的全部
 * 含义。自动路径碰到 user 来源只会给建议，一个字节都不改。
 */
func TestWSAdaptiveRecommendOverridesUserValue(t *testing.T) {
	st := newTestStack(t, nil)
	mobileExit(t, st, true)
	setUserWorkers(t, st, 300)

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"adaptive/recommend"}`)

	// 改动走 adaptive/applied 回来，因此徽标与「还原」都还在。
	m := readUntil(t, conn, eventAdaptiveApplied, 3*time.Second)
	var p adaptivePayload
	decode(t, m, &p)
	if p.Key != "scan.workers" {
		t.Fatalf("调整的是 %q，期望 scan.workers", p.Key)
	}
	if p.From != 300 || p.To != 80 {
		t.Errorf("调整 %d → %d，期望 300 → 80", p.From, p.To)
	}

	cfg := st.store.Get()
	if cfg.Scan.Workers != 80 {
		t.Errorf("配置里的并发 = %d，期望 80", cfg.Scan.Workers)
	}
}

// 出口不是移动宽带时没有触发条件，要明确报错，而不是让按钮看起来没反应。
func TestWSAdaptiveRecommendWithoutSignal(t *testing.T) {
	st := newTestStack(t, nil)
	mobileExit(t, st, false)

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"adaptive/recommend"}`)

	m := readUntil(t, conn, eventError, 3*time.Second)
	var p errorPayload
	decode(t, m, &p)
	if p.Code != CodeInvalidParam {
		t.Errorf("code = %q，期望 %q", p.Code, CodeInvalidParam)
	}
}

// 参数已经在推荐范围内时同样要说清楚，而不是静默成功。
func TestWSAdaptiveRecommendWhenAlreadyInRange(t *testing.T) {
	st := newTestStack(t, nil)
	mobileExit(t, st, true)
	setUserWorkers(t, st, 80)

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"adaptive/recommend"}`)

	m := readUntil(t, conn, eventError, 3*time.Second)
	var p errorPayload
	decode(t, m, &p)
	if p.Code != CodeInvalidParam {
		t.Errorf("code = %q，期望 %q", p.Code, CodeInvalidParam)
	}
	if cfg := st.store.Get(); cfg.Scan.Workers != 80 {
		t.Errorf("不该改动已经在范围内的值，实际 = %d", cfg.Scan.Workers)
	}
}

/**
 * 自动路径对默认来源是真的写进配置的。
 *
 * 这条曾经是假的：补丁用了点号扁平键，而配置存储收的是嵌套对象，于是值一个
 * 字节没变、事件却已经发出去——徽标亮了、日志写了「已按网络环境自动调整参数」，
 * 参数纹丝不动。断言「事件发了」是查不出来的，必须断言配置里的值。
 */
func TestWSAdaptiveAutoActuallyWrites(t *testing.T) {
	st := newTestStack(t, nil)
	mobileExit(t, st, true)

	if applied := st.srv.applyAdaptive(adaptive.SignalMobileISP, false); !applied {
		t.Fatal("默认来源的参数应当被调整")
	}

	cfg := st.store.Get()
	if cfg.Scan.Workers != 80 {
		t.Errorf("并发 = %d，期望 80", cfg.Scan.Workers)
	}
	if got := cfg.Origins["scan.workers"]; got != model.OriginDefault {
		t.Errorf("来源 = %q，期望 %q", got, model.OriginDefault)
	}
}

/**
 * 自动路径碰到 user 来源一个字节都不改——与上面那条形成对照。
 *
 * 两条一起看才说明「显式」这个区分是真的在起作用，而不是碰巧都改了。
 */
func TestWSAdaptiveAutoKeepsUserValue(t *testing.T) {
	st := newTestStack(t, nil)
	mobileExit(t, st, true)
	setUserWorkers(t, st, 300)

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	// 触发自动路径：这里直接调判定，绕开测速源解析（那一步另有覆盖）。
	if applied := st.srv.applyAdaptive("mobile_isp", false); applied {
		t.Error("自动路径不该改动 user 来源的参数")
	}

	if cfg := st.store.Get(); cfg.Scan.Workers != 300 {
		t.Errorf("user 来源的值被改成了 %d", cfg.Scan.Workers)
	}
}

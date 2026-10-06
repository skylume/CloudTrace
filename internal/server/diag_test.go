package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"cloudtrace/internal/diag"
)

// fakeDiag 造一组可控的诊断探测，避免用例真的去连外网。
func fakeDiag() diag.Options {
	return diag.Options{
		Resolve:    func(context.Context, string) ([]string, error) { return []string{"104.16.132.229"}, nil },
		Dial:       func(context.Context, string) error { return nil },
		FetchTrace: func(context.Context) (string, error) { return "ip=203.0.113.7\nloc=JP\ncolo=NRT\n", nil },
		Now:        time.Now,
	}
}

// 诊断命令要回四项检查，且出口看着像代理时总体结论不是 ok。
func TestWSDiagRun(t *testing.T) {
	st := newTestStack(t, nil)
	st.srv.diagOptions = fakeDiag
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"diag/run"}`)

	m := readUntil(t, conn, eventDiag, 3*time.Second)
	var report diag.Report
	decode(t, m, &report)

	if len(report.Items) != 4 {
		t.Fatalf("检查项数 = %d，期望 4", len(report.Items))
	}
	// loc=JP 不在免提示名单里，出口那一项应当判 warn。
	if report.Status != diag.StatusWarn {
		t.Errorf("总体结论 = %q，期望 warn", report.Status)
	}
	for _, item := range report.Items {
		if item.Key == diag.KeyEgress && item.Status != diag.StatusWarn {
			t.Errorf("出口状态 = %q，期望 warn", item.Status)
		}
	}
}

// 诊断包走下载中转，且内容里的敏感项必须已经打码。
func TestWSDiagExportRedactsAndServes(t *testing.T) {
	st := newTestStack(t, nil)
	st.srv.diagOptions = fakeDiag
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	// 把 Token 换成一个一眼能认出来的值，导出后不该出现在内容里。
	cfg := st.store.Get()
	cfg.Server.Token = "token-should-not-leak"
	if _, err := st.store.Set(cfg); err != nil {
		t.Fatalf("写配置失败：%v", err)
	}

	send(t, conn, `{"type":"diag/run"}`)
	readUntil(t, conn, eventDiag, 3*time.Second)

	send(t, conn, `{"type":"diag/export","data":{"report":{"status":"ok","items":[],"checked_at":"2026-10-06T15:04:05+08:00"}}}`)
	m := readUntil(t, conn, cmdDiagExport, 3*time.Second)
	var resp diagExportResp
	decode(t, m, &resp)

	if resp.URL == "" || resp.Name == "" {
		t.Fatalf("导出响应缺少地址或文件名：%+v", resp)
	}
	if !strings.HasSuffix(resp.Name, ".txt") {
		t.Errorf("诊断包文件名 = %q，期望 .txt", resp.Name)
	}

	// 走真实的下载路由取内容：这条链路（登记 → 取走）本身也要验。
	body := fetchDownload(t, st, resp.URL)
	if strings.Contains(body, "token-should-not-leak") {
		t.Error("诊断包里出现了访问 Token")
	}
	if !strings.Contains(body, "网络诊断") {
		t.Error("诊断包缺少诊断段落")
	}
	if !strings.Contains(body, "配置（已脱敏）") {
		t.Error("诊断包缺少配置段落")
	}
}

// 没跑过诊断也能导出，包里要写明这一点而不是留一段空白。
func TestWSDiagExportWithoutReport(t *testing.T) {
	st := newTestStack(t, nil)
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"diag/export"}`)
	m := readUntil(t, conn, cmdDiagExport, 3*time.Second)
	var resp diagExportResp
	decode(t, m, &resp)

	body := fetchDownload(t, st, resp.URL)
	if !strings.Contains(body, "本次没有跑诊断") {
		t.Error("没跑过诊断时应当在包里说明")
	}
}

// 诊断失败不该让整条链路报错：一项不通也要把结果给出来。
func TestWSDiagRunReportsFailures(t *testing.T) {
	st := newTestStack(t, nil)
	st.srv.diagOptions = func() diag.Options {
		return diag.Options{
			Resolve:    func(context.Context, string) ([]string, error) { return nil, errors.New("no such host") },
			Dial:       func(context.Context, string) error { return errors.New("i/o timeout") },
			FetchTrace: func(context.Context) (string, error) { return "", errors.New("connection reset") },
			Now:        time.Now,
		}
	}
	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"diag/run"}`)
	m := readUntil(t, conn, eventDiag, 3*time.Second)

	var report diag.Report
	decode(t, m, &report)
	if report.Status != diag.StatusBad {
		t.Errorf("全不通时总体结论 = %q，期望 bad", report.Status)
	}
	if len(report.Items) != 4 {
		t.Errorf("检查项数 = %d，期望 4（失败也要跑完）", len(report.Items))
	}
}

// fetchDownload 从下载路由取回内容。
func fetchDownload(t *testing.T, st *testStack, path string) string {
	t.Helper()
	return bodyOf(t, get(t, st.ts.URL+path))
}

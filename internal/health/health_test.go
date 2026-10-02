package health

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cloudtrace/internal/config"
)

// baseOptions 造一份「一切正常」的运行时输入：端口空闲、目录可写、
// 库文件存在、出口在国内、测速源可达。
func baseOptions(t *testing.T) (config.Config, Options) {
	t.Helper()
	dir := t.TempDir()
	db := filepath.Join(dir, "asn.tsv")
	if err := os.WriteFile(db, []byte("x"), 0o600); err != nil {
		t.Fatalf("准备库文件失败：%v", err)
	}
	cfg := config.Default()
	return cfg, Options{
		DataDir:         dir,
		ASNDBPath:       db,
		ListeningPort:   cfg.Server.Port,
		ExitCountry:     "CN",
		PortInUse:       func(int) bool { return false },
		DirWritable:     func(string) error { return nil },
		SourceReachable: func(context.Context) error { return nil },
	}
}

func find(rep Report, key string) (Issue, bool) {
	for _, i := range rep.Issues {
		if i.Key == key {
			return i, true
		}
	}
	return Issue{}, false
}

// 一切正常时不该报出任何问题。
func TestHealthyConfigReportsNothing(t *testing.T) {
	cfg, opts := baseOptions(t)
	rep := Check(context.Background(), cfg, opts)
	if len(rep.Issues) != 0 {
		t.Fatalf("健康配置报出了问题：%+v", rep.Issues)
	}
	if rep.Checked != 7 {
		t.Fatalf("已查项数 = %d，期望 7", rep.Checked)
	}
}

// 七项检查必须各自都能被触发——体检的价值就在于「配错了能被说出来」。
func TestEveryCheckIsTriggerable(t *testing.T) {
	cfg, opts := baseOptions(t)

	// ① 端口冲突：配置的端口不是本进程在用的那个，且已被占用。
	cfg.Server.Port = 19999
	opts.ListeningPort = 17443
	opts.PortInUse = func(int) bool { return true }

	// ② ③ 危险值
	cfg.Scan.Workers = 480
	cfg.Scan.LatencyThreshold = 50

	// ④ 数据目录不可写
	opts.DirWritable = func(string) error { return errors.New("拒绝访问") }

	// ⑤ ASN 库缺失
	opts.ASNDBPath = filepath.Join(t.TempDir(), "missing.tsv")

	// ⑥ 代理环境：出口不在国内
	opts.ExitCountry = "SG"

	// ⑦ 测速源不可达
	opts.SourceReachable = func(context.Context) error { return errors.New("超时") }

	rep := Check(context.Background(), cfg, opts)
	if len(rep.Issues) != 7 {
		t.Fatalf("问题数 = %d，期望 7，实际 %+v", len(rep.Issues), rep.Issues)
	}
	if rep.Checked != 7 {
		t.Fatalf("已查项数 = %d，期望 7", rep.Checked)
	}

	want := []string{
		KeyPort, KeyWorkers, KeyLatency, KeyDataDir, KeyASNDB, KeyProxyEnv, KeySpeedSource,
	}
	for _, k := range want {
		issue, ok := find(rep, k)
		if !ok {
			t.Fatalf("未报出 %s", k)
		}
		if issue.Problem == "" || issue.Suggestion == "" {
			t.Errorf("%s 的问题或建议为空：%+v", k, issue)
		}
	}

	// 只有端口与数据目录是「已经不可用」，其余是「能跑但结果受影响」。
	for _, k := range []string{KeyPort, KeyDataDir} {
		if issue, _ := find(rep, k); issue.Level != LevelError {
			t.Errorf("%s 的等级 = %q，期望 error", k, issue.Level)
		}
	}
	for _, k := range []string{KeyWorkers, KeyLatency, KeyASNDB, KeyProxyEnv, KeySpeedSource} {
		if issue, _ := find(rep, k); issue.Level != LevelWarning {
			t.Errorf("%s 的等级 = %q，期望 warning", k, issue.Level)
		}
	}

	// 错误排在警告前面，便于前端把「必须处理」的放在最上面。
	first := rep.Issues[0]
	if first.Level != LevelError {
		t.Errorf("首条 = %+v，期望 error 级", first)
	}
}

// 本进程正在用的端口不算冲突——占用者就是自己。
func TestOwnListeningPortIsNotAConflict(t *testing.T) {
	cfg, opts := baseOptions(t)
	opts.PortInUse = func(int) bool { return true } // 无论什么端口都报占用

	rep := Check(context.Background(), cfg, opts)
	if _, ok := find(rep, KeyPort); ok {
		t.Fatal("把本进程占用的端口当成了冲突")
	}
}

// 并发与阈值的一键修复要给出具体动作，前端才能直接按下按钮。
func TestDangerousValuesOfferFix(t *testing.T) {
	cfg, opts := baseOptions(t)
	cfg.Scan.Workers = 500
	cfg.Scan.LatencyThreshold = 30

	rep := Check(context.Background(), cfg, opts)

	w, ok := find(rep, KeyWorkers)
	if !ok || !w.Fixable || w.Fix == nil {
		t.Fatalf("并发过高未提供一键修复：%+v", w)
	}
	if w.Fix.Key != "scan.workers" || w.Fix.Value != fixedWorkers {
		t.Errorf("修复动作 = %+v，期望 scan.workers → %d", w.Fix, fixedWorkers)
	}
	if w.Fix.Label == "" {
		t.Error("修复按钮缺少文案")
	}

	l, ok := find(rep, KeyLatency)
	if !ok || !l.Fixable || l.Fix == nil {
		t.Fatalf("阈值过低未提供一键修复：%+v", l)
	}
	if l.Fix.Key != "scan.latency_threshold" || l.Fix.Value != fixedLatency {
		t.Errorf("修复动作 = %+v，期望 scan.latency_threshold → %d", l.Fix, fixedLatency)
	}
}

// 配了代理但开了强制直连就不算问题，反之要提示。
func TestProxyEnvTwoTriggers(t *testing.T) {
	t.Run("配了代理且未强制直连", func(t *testing.T) {
		cfg, opts := baseOptions(t)
		cfg.Net.Proxy = "http://127.0.0.1:7890"
		cfg.Net.ForceDirect = false
		rep := Check(context.Background(), cfg, opts)
		issue, ok := find(rep, KeyProxyEnv)
		if !ok {
			t.Fatal("未报出代理环境")
		}
		if issue.Field != "net.proxy" || !issue.Fixable {
			t.Errorf("代理问题 = %+v，期望指向 net.proxy 且可一键修复", issue)
		}
	})

	t.Run("配了代理但强制直连", func(t *testing.T) {
		cfg, opts := baseOptions(t)
		cfg.Net.Proxy = "http://127.0.0.1:7890"
		cfg.Net.ForceDirect = true
		rep := Check(context.Background(), cfg, opts)
		if _, ok := find(rep, KeyProxyEnv); ok {
			t.Error("强制直连时不该报代理环境")
		}
	})

	t.Run("出口地区大小写与空白不影响判定", func(t *testing.T) {
		cfg, opts := baseOptions(t)
		opts.ExitCountry = " sg "
		rep := Check(context.Background(), cfg, opts)
		issue, ok := find(rep, KeyProxyEnv)
		if !ok {
			t.Fatal("未报出代理环境")
		}
		if !strings.Contains(issue.Problem, "SG") {
			t.Errorf("问题描述 = %q，期望带上规范化后的地区码", issue.Problem)
		}
	})
}

// 拿不到输入的检查应当跳过，既不报问题也不计入已查数量。
//
// 端口、并发、阈值这三项不需要运行时输入就能查（端口自己试绑定即可）；
// 其余四项依赖数据目录、库路径、出口地区与测速源，没有输入就跳过。
func TestUnavailableChecksAreSkipped(t *testing.T) {
	cfg := config.Default()
	// 端口探测结果取决于机器占用情况，这里固定掉，否则用例会随机失败。
	rep := Check(context.Background(), cfg, Options{PortInUse: func(int) bool { return false }})
	if len(rep.Issues) != 0 {
		t.Fatalf("空输入报出了问题：%+v", rep.Issues)
	}
	if rep.Checked != 3 {
		t.Fatalf("已查项数 = %d，期望端口 / 并发 / 阈值三项", rep.Checked)
	}
}

// 关闭 ASN 查询后，库文件缺失不该再报。
func TestASNDisabledSkipsDBCheck(t *testing.T) {
	cfg, opts := baseOptions(t)
	cfg.Geo.ASNSource = "off"
	opts.ASNDBPath = filepath.Join(t.TempDir(), "missing.tsv")

	rep := Check(context.Background(), cfg, opts)
	if _, ok := find(rep, KeyASNDB); ok {
		t.Error("已关闭 ASN 查询，不该再报库文件缺失")
	}
}

// 真实端口探测：自己占住的端口必须被判为占用。
func TestRealPortInUse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("绑定端口失败：%v", err)
	}
	defer ln.Close()

	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("地址类型 = %T，期望 *net.TCPAddr", ln.Addr())
	}
	if !portInUse(addr.Port) {
		t.Errorf("本进程占用的端口 %d 未被判为占用", addr.Port)
	}
}

// 真实目录探测：临时目录应当可写，不存在的目录应当不可写。
func TestRealProbes(t *testing.T) {
	dir := t.TempDir()
	if err := dirWritable(dir); err != nil {
		t.Errorf("临时目录应当可写，实际：%v", err)
	}
	if err := dirWritable(filepath.Join(dir, "no", "such", "dir")); err == nil {
		t.Error("不存在的目录应判定为不可写")
	}
}

func TestDirWritableRemovesTestFile(t *testing.T) {
	dir := t.TempDir()
	if err := dirWritable(dir); err != nil {
		t.Fatalf("写入测试失败：%v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取目录失败：%v", err)
	}
	if len(entries) != 0 {
		t.Errorf("写入测试留下了文件：%+v", entries)
	}
}

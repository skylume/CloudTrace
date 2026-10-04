package migrate

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cloudtrace/internal/config"
	"cloudtrace/internal/history"
	"cloudtrace/internal/model"
)

// oldSettings 是一份贴近真实的旧配置。
const oldSettings = `{
  "tray_on_close": false,
  "cidr_mode": "仅官方",
  "scan_mode": "tcping",
  "sample_max": 5000,
  "workers": 200,
  "latency_threshold": 230,
  "ping_times": 0,
  "pre_filter_ports": "80,443",
  "allowed_regions": "HKG, NRT",
  "blocked_regions": "",
  "use_ip_cache": true,
  "use_remote_sources": false,
  "remote_sources": [
    {"name": "聚合列表", "url": "https://example.com/all.txt", "enabled": true}
  ],
  "source_retries": 3,
  "source_retry_delay": 2.0,
  "source_timeout": 8.0,
  "speed_url": "auto",
  "min_speed": 0.0,
  "verify_nodes": true,
  "download_interval": 3,
  "speed_workers": 2,
  "per_region_topn": 3,
  "score_speed_weight": 2.0,
  "score_latency_weight": 1.5,
  "http_enabled": true,
  "http_port": 17443,
  "allow_lan": false,
  "http_token": "abc123"
}`

const oldHistory = `{
  "save_time": "2026-09-27 13:15:14",
  "ip_version": 4,
  "result_type": "scan",
  "count": 1,
  "results": [
    {
      "chinese_name": "洛杉矶",
      "colo": "LAX",
      "ip": "104.18.77.31",
      "port": 443,
      "use_tls": true,
      "latency": 187.0,
      "latency_avg": 187.0,
      "latency_max": 190.0,
      "jitter": 1.5,
      "loss": 0.0,
      "loc": "CN",
      "ok_count": 3,
      "samples": 3,
      "success": true
    }
  ]
}`

// legacyDir 造一份旧数据目录。
func legacyDir(t *testing.T, settings string, histories map[string]string) string {
	t.Helper()
	dir := t.TempDir()

	if settings != "" {
		write(t, filepath.Join(dir, legacySettingsFile), settings)
	}
	if len(histories) > 0 {
		for name, body := range histories {
			write(t, filepath.Join(dir, legacyHistoryDir, name), body)
		}
	}
	return dir
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("写文件失败：%v", err)
	}
}

// dest 造一个新版的目标。
func dest(t *testing.T) (Dest, *config.Store, *history.Store) {
	t.Helper()
	dir := t.TempDir()

	store, err := config.OpenStore(config.ConfigPath(dir), dir)
	if err != nil {
		t.Fatalf("打开配置失败：%v", err)
	}
	hist, err := history.New(history.Options{
		Dir:    config.HistoryDir(dir),
		Config: func() config.HistoryConfig { return store.Get().History },
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("打开历史失败：%v", err)
	}
	t.Cleanup(func() { _ = hist.Close() })

	return Dest{Store: store, History: hist, DataDir: dir}, store, hist
}

// ---------------------------------------------------------------- 检测

func TestDetectFindsBothParts(t *testing.T) {
	dir := legacyDir(t, oldSettings, map[string]string{
		"ipv4_scan_20260927_131514.json": oldHistory,
		"ipv4_scan_latest.json":          oldHistory,
	})

	got := Detect(dir)
	if !got.Found() {
		t.Fatal("没检测到旧数据")
	}
	if got.SettingsPath == "" {
		t.Error("没找到旧配置")
	}
	// `*_latest.json` 是带时间戳那份的快捷方式，导入两次会在历史里出现一对
	// 一模一样的记录。
	if len(got.HistoryFiles) != 1 {
		t.Errorf("历史文件数 = %d，期望 1（latest 不算）", len(got.HistoryFiles))
	}
}

func TestDetectEmptyDir(t *testing.T) {
	if got := Detect(t.TempDir()); got.Found() {
		t.Errorf("空目录不该检测到旧数据：%+v", got)
	}
}

// ---------------------------------------------------------------- 映射

func TestBuildMapsSettings(t *testing.T) {
	plan, err := Build(Detect(legacyDir(t, oldSettings, nil)))
	if err != nil {
		t.Fatalf("算计划失败：%v", err)
	}

	cases := map[string]any{
		"server.port":              config.DefaultPort,
		"server.token":             "abc123",
		"server.bind":              "127.0.0.1",
		"scan.mode":                "tcping",
		"scan.sample_max":          5000,
		"scan.workers":             200,
		"scan.latency_threshold":   230,
		"scan.ping_times":          0,
		"scan.source_mode":         "official",
		"scan.verify_nodes":        true,
		"speed.concurrency":        2,
		"speed.url_mode":           "auto",
		"speed.per_region_topn":    3,
		"speed.weight_speed":       2.0,
		"speed.weight_latency":     1.5,
		"source.retry":             3,
		"source.retry_interval_ms": 2000,
		"source.timeout_ms":        8000,
		// 旧版间隔是秒，新版是毫秒。
		"speed.interval_ms": 3000,
	}
	for path, want := range cases {
		if got := valueAt(plan.Patch, path); got != want {
			t.Errorf("%s = %v（%T），期望 %v", path, got, got, want)
		}
	}
}

// 逗号分隔的文本要变成数组：旧版是输入框，新版是列表。
func TestBuildConvertsTextLists(t *testing.T) {
	plan, err := Build(Detect(legacyDir(t, oldSettings, nil)))
	if err != nil {
		t.Fatalf("算计划失败：%v", err)
	}

	ports, ok := valueAt(plan.Patch, "scan.pre_filter_ports").([]int)
	if !ok || len(ports) != 2 || ports[0] != 80 || ports[1] != 443 {
		t.Errorf("前置端口 = %v，期望 [80 443]", valueAt(plan.Patch, "scan.pre_filter_ports"))
	}
	regions, ok := valueAt(plan.Patch, "scan.allowed_regions").([]string)
	if !ok || len(regions) != 2 || regions[0] != "HKG" {
		t.Errorf("地区白名单 = %v，期望 [HKG NRT]", valueAt(plan.Patch, "scan.allowed_regions"))
	}
}

func TestBuildConvertsRemoteSources(t *testing.T) {
	plan, err := Build(Detect(legacyDir(t, oldSettings, nil)))
	if err != nil {
		t.Fatalf("算计划失败：%v", err)
	}

	got, ok := valueAt(plan.Patch, "source.remote_urls").([]config.RemoteSource)
	if !ok || len(got) != 1 {
		t.Fatalf("远程源 = %v，期望 1 个", valueAt(plan.Patch, "source.remote_urls"))
	}
	if got[0].URL != "https://example.com/all.txt" || !got[0].Enabled {
		t.Errorf("远程源内容不对：%+v", got[0])
	}
	// 旧版的 name 在新版里是备注。
	if got[0].Note != "聚合列表" {
		t.Errorf("备注 = %q，期望沿用旧版的名称", got[0].Note)
	}
}

// 旧版早期的端口要统一，并且必须告诉用户——地址变了。
func TestBuildNotesPortChange(t *testing.T) {
	body := strings.Replace(oldSettings, `"http_port": 17443`, `"http_port": 18543`, 1)
	plan, err := Build(Detect(legacyDir(t, body, nil)))
	if err != nil {
		t.Fatalf("算计划失败：%v", err)
	}

	if got := valueAt(plan.Patch, "server.port"); got != config.DefaultPort {
		t.Errorf("端口 = %v，期望统一为 %d", got, config.DefaultPort)
	}
	var noted bool
	for _, n := range plan.Notes {
		if strings.Contains(n, "18543") {
			noted = true
		}
	}
	if !noted {
		t.Errorf("端口被改过却没提示用户：%v", plan.Notes)
	}
}

// 迁移过来的值一律标 user：旧配置里分不清哪些是用户改的、哪些是默认值。
func TestBuildMarksOriginsAsUser(t *testing.T) {
	plan, err := Build(Detect(legacyDir(t, oldSettings, nil)))
	if err != nil {
		t.Fatalf("算计划失败：%v", err)
	}
	if got := plan.Origins.Get("scan.workers"); got != model.OriginUser {
		t.Errorf("来源 = %q，期望 %q", got, model.OriginUser)
	}
}

// 没搬的旧键要列出来。悄悄漏掉一项的话，新界面里那一项显示的是默认值，
// 看起来很正常，用户不会发现。
func TestBuildListsSkippedKeys(t *testing.T) {
	body := strings.Replace(oldSettings, `"tray_on_close": false,`, `"tray_on_close": false, "unknown_future_key": 1,`, 1)
	plan, err := Build(Detect(legacyDir(t, body, nil)))
	if err != nil {
		t.Fatalf("算计划失败：%v", err)
	}

	joined := strings.Join(plan.Skipped, "\n")
	for _, want := range []string{"tray_on_close", "unknown_future_key", "use_ip_cache"} {
		if !strings.Contains(joined, want) {
			t.Errorf("没登记 %q：\n%s", want, joined)
		}
	}
}

// ---------------------------------------------------------------- 历史

func TestBuildConvertsHistory(t *testing.T) {
	dir := legacyDir(t, "", map[string]string{"ipv4_scan_20260927_131514.json": oldHistory})

	plan, err := Build(Detect(dir))
	if err != nil {
		t.Fatalf("算计划失败：%v", err)
	}
	if len(plan.Histories) != 1 {
		t.Fatalf("记录数 = %d，期望 1", len(plan.Histories))
	}

	rec := plan.Histories[0]
	// ID 必须符合新版格式（时间戳 + 8 位十六进制），否则读回来时会被当成
	// 非法 ID 拒掉——记录明明写进去了，却怎么也打不开。
	if len(rec.ID) != 24 || !strings.HasPrefix(rec.ID, "20260927_131514_") {
		t.Errorf("ID = %q，期望形如 20260927_131514_xxxxxxxx", rec.ID)
	}
	if rec.Type != history.TypeScan || rec.IPVersion != 4 {
		t.Errorf("类型/地址族不对：%s %d", rec.Type, rec.IPVersion)
	}
	if rec.CreatedAt.Format(legacyTimeLayout) != "2026-09-27 13:15:14" {
		t.Errorf("时间 = %v", rec.CreatedAt)
	}
	if len(rec.Results) != 1 {
		t.Fatalf("结果数 = %d", len(rec.Results))
	}

	got := rec.Results[0]
	if got.IP != "104.18.77.31" || got.Port != 443 || !got.UseTLS {
		t.Errorf("地址信息不对：%+v", got)
	}
	if got.Latency != 187 || got.LatencyMax != 190 || got.Jitter != 1.5 {
		t.Errorf("延迟统计不对：%+v", got)
	}
	// 旧版的 chinese_name 就是新版的地区中文名。
	if got.RegionName != "洛杉矶" || got.Colo != "LAX" {
		t.Errorf("地区信息不对：%+v", got)
	}
	// 旧版用「发送样本数 / 成功数」表示探测结果。
	if got.Sent != 3 || got.Recv != 3 {
		t.Errorf("探测计数不对：sent=%d recv=%d", got.Sent, got.Recv)
	}
	// 摘要要现算：旧文件里没有，列表页那一栏空着用户会以为记录坏了。
	if rec.Summary.MinLatency != 187 {
		t.Errorf("摘要里的最低延迟 = %v", rec.Summary.MinLatency)
	}
	if rec.Summary.Total != 1 {
		t.Errorf("摘要条数 = %d", rec.Summary.Total)
	}
}

// 单份历史读不出来不该让整次迁移失败：其余的照样能搬。
func TestBuildKeepsGoingOnBrokenHistory(t *testing.T) {
	dir := legacyDir(t, "", map[string]string{
		"ipv4_scan_20260927_131514.json": oldHistory,
		"ipv4_scan_broken.json":          "{not json",
	})

	plan, err := Build(Detect(dir))
	if err != nil {
		t.Fatalf("一份坏文件不该让整次迁移失败：%v", err)
	}
	if len(plan.Histories) != 1 {
		t.Errorf("可用记录数 = %d，期望 1", len(plan.Histories))
	}
	if len(plan.Skipped) == 0 {
		t.Error("坏文件没登记进跳过清单")
	}
}

// ---------------------------------------------------------------- 执行

func TestRunMigratesAndBacksUp(t *testing.T) {
	old := legacyDir(t, oldSettings, map[string]string{"ipv4_scan_20260927_131514.json": oldHistory})
	target, store, hist := dest(t)

	report, err := Run(Detect(old), target)
	if err != nil {
		t.Fatalf("迁移失败：%v", err)
	}

	if !report.Settings {
		t.Error("配置没写进去")
	}
	if report.Imported != 1 {
		t.Errorf("导入数 = %d，期望 1", report.Imported)
	}
	if got := store.Get().Scan.Workers; got != 200 {
		t.Errorf("并发 = %d，期望 200", got)
	}
	if got := store.Get().Server.Token; got != "abc123" {
		t.Errorf("访问 Token = %q", got)
	}

	entries, err := hist.List(history.Filter{})
	if err != nil {
		t.Fatalf("列历史失败：%v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("历史条目数 = %d，期望 1", len(entries))
	}

	// 备份必须存在：出问题时用户能翻回去。
	if report.BackupDir == "" {
		t.Fatal("没有做备份")
	}
	if _, err := os.Stat(filepath.Join(report.BackupDir, legacySettingsFile)); err != nil {
		t.Errorf("备份里没有旧配置：%v", err)
	}
	if _, err := os.Stat(filepath.Join(report.BackupDir, legacyHistoryDir)); err != nil {
		t.Errorf("备份里没有旧历史：%v", err)
	}
}

// **复制而非移动**：旧目录原样留着，用户看完新界面觉得不合适还能开旧版本。
func TestRunLeavesLegacyDirIntact(t *testing.T) {
	old := legacyDir(t, oldSettings, map[string]string{"ipv4_scan_20260927_131514.json": oldHistory})
	target, _, _ := dest(t)

	if _, err := Run(Detect(old), target); err != nil {
		t.Fatalf("迁移失败：%v", err)
	}

	if _, err := os.Stat(filepath.Join(old, legacySettingsFile)); err != nil {
		t.Errorf("旧配置被搬走了：%v", err)
	}
	if _, err := os.Stat(filepath.Join(old, legacyHistoryDir, "ipv4_scan_20260927_131514.json")); err != nil {
		t.Errorf("旧历史被搬走了：%v", err)
	}
}

/**
 * 幂等：连续跑两次不会导入两份。
 *
 * 这是最容易发生的一种误操作——用户不确定上次跑没跑成，于是再点一次。
 */
func TestRunIsIdempotent(t *testing.T) {
	old := legacyDir(t, oldSettings, map[string]string{
		"ipv4_scan_20260927_131514.json": oldHistory,
		"ipv4_speed_20260927_131607.json": strings.Replace(
			oldHistory, `"result_type": "scan"`, `"result_type": "speed"`, 1),
	})
	target, store, hist := dest(t)

	legacy := Detect(old)
	if _, err := Run(legacy, target); err != nil {
		t.Fatalf("第一次迁移失败：%v", err)
	}
	second, err := Run(legacy, target)
	if err != nil {
		t.Fatalf("第二次迁移失败：%v", err)
	}

	entries, _ := hist.List(history.Filter{})
	if len(entries) != 2 {
		t.Fatalf("历史条目数 = %d，期望 2（重复执行不该翻倍）", len(entries))
	}
	if got := store.Get().Scan.Workers; got != 200 {
		t.Errorf("第二次执行把配置改坏了：workers=%d", got)
	}
	// 第二次仍然要如实报告「又导入了一遍」——它确实重新写了一遍同样的记录。
	if second.Imported != 2 {
		t.Errorf("第二次导入数 = %d，期望 2", second.Imported)
	}
}

func TestAlreadyMigrated(t *testing.T) {
	old := legacyDir(t, oldSettings, nil)
	target, _, _ := dest(t)

	legacy := Detect(old)
	if AlreadyMigrated(target.DataDir, legacy) {
		t.Error("还没迁移就报已迁移")
	}
	if _, err := Run(legacy, target); err != nil {
		t.Fatalf("迁移失败：%v", err)
	}
	if !AlreadyMigrated(target.DataDir, legacy) {
		t.Error("迁移之后没记下标记，用户再点一次会重做")
	}
}

// 越界的旧值不该让整份配置都被拒——那样用户会以为什么都没搬过来。
func TestRunKeepsOtherValuesWhenOneIsRejected(t *testing.T) {
	body := strings.Replace(oldSettings, `"workers": 200,`, `"workers": 99999,`, 1)
	old := legacyDir(t, body, nil)
	target, store, _ := dest(t)

	report, err := Run(Detect(old), target)
	if err != nil {
		t.Fatalf("迁移失败：%v", err)
	}

	if got := store.Get().Scan.SampleMax; got != 5000 {
		t.Errorf("一个值被拒就丢了其余项：sample_max=%d", got)
	}
	if got := store.Get().Scan.Workers; got == 99999 {
		t.Error("越界的值居然写进去了")
	}
	var noted bool
	for _, s := range report.Skipped {
		if strings.Contains(s, "scan.workers") {
			noted = true
		}
	}
	if !noted {
		t.Errorf("被拒的那一项没报出来：%v", report.Skipped)
	}
}

func TestRunRejectsWhenNoLegacyData(t *testing.T) {
	target, _, _ := dest(t)
	if _, err := Run(Detect(t.TempDir()), target); err == nil {
		t.Error("没有旧数据时应当报错，而不是静默成功")
	}
}

// 迁移过来的记录能被新版正常读出来——写进去只是第一步。
func TestRunRecordsAreReadable(t *testing.T) {
	old := legacyDir(t, "", map[string]string{"ipv4_scan_20260927_131514.json": oldHistory})
	target, _, hist := dest(t)

	if _, err := Run(Detect(old), target); err != nil {
		t.Fatalf("迁移失败：%v", err)
	}

	entries, err := hist.List(history.Filter{})
	if err != nil || len(entries) != 1 {
		t.Fatalf("历史条目数 = %d（err=%v），期望 1", len(entries), err)
	}
	rec, err := hist.Load(entries[0].ID)
	if err != nil {
		t.Fatalf("读不回迁移过来的记录：%v", err)
	}
	if len(rec.Results) != 1 || rec.Results[0].IP != "104.18.77.31" {
		t.Errorf("记录内容不对：%+v", rec.Results)
	}
	// 参数快照是空的——旧文件里没有。它必须是个合法 JSON，否则下次读取会失败。
	if !json.Valid(rec.Params) {
		t.Errorf("参数快照不是合法 JSON：%q", rec.Params)
	}
}

/**
 * 同一个旧文件每次都要算出同一个 ID。
 *
 * 幂等全靠这一点：ID 若是随机的，重复执行会导入两份一模一样的记录，而用户在
 * 历史列表里看到的就是两条他分不清的记录。
 */
func TestBuildProducesStableIDs(t *testing.T) {
	dir := legacyDir(t, "", map[string]string{"ipv4_scan_20260927_131514.json": oldHistory})
	legacy := Detect(dir)

	first, err := Build(legacy)
	if err != nil {
		t.Fatalf("算计划失败：%v", err)
	}
	second, err := Build(legacy)
	if err != nil {
		t.Fatalf("算计划失败：%v", err)
	}

	if first.Histories[0].ID != second.Histories[0].ID {
		t.Errorf("两次算出的 ID 不同：%q vs %q", first.Histories[0].ID, second.Histories[0].ID)
	}
}

// 两份不同的旧文件不能算出同一个 ID，否则后一份会覆盖前一份。
func TestBuildIDsAreDistinctPerFile(t *testing.T) {
	other := strings.Replace(oldHistory, `"save_time": "2026-09-27 13:15:14"`, `"save_time": "2026-09-27 13:17:54"`, 1)
	dir := legacyDir(t, "", map[string]string{
		"ipv4_scan_20260927_131514.json": oldHistory,
		"ipv4_scan_20260927_131754.json": other,
	})

	plan, err := Build(Detect(dir))
	if err != nil {
		t.Fatalf("算计划失败：%v", err)
	}
	if len(plan.Histories) != 2 {
		t.Fatalf("记录数 = %d", len(plan.Histories))
	}
	if plan.Histories[0].ID == plan.Histories[1].ID {
		t.Errorf("两份文件算出了同一个 ID：%q", plan.Histories[0].ID)
	}
}

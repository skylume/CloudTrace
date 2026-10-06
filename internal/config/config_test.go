package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cloudtrace/internal/model"
)

func TestDefaultIsUsable(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("内置默认值必须能直接通过校验，实际：%v", err)
	}
	if cfg.Scan.Workers != 200 {
		t.Fatalf("默认并发应为 200，实际 %d", cfg.Scan.Workers)
	}
	if cfg.Scan.SampleMax != MaxSampleMax {
		t.Fatalf("默认采样上限应为 %d，实际 %d", MaxSampleMax, cfg.Scan.SampleMax)
	}
	if !cfg.Scan.TwoPhase {
		t.Fatal("两阶段扫描应默认开启")
	}
	if !cfg.Data.Portable {
		t.Fatal("数据目录应默认为便携模式")
	}
	if cfg.UI.Density != "auto" {
		t.Fatalf("默认密度应为 auto，实际 %q", cfg.UI.Density)
	}
	if cfg.Server.Port != DefaultPort {
		t.Fatalf("默认端口应为 %d，实际 %d", DefaultPort, cfg.Server.Port)
	}
	if cfg.History.KeepCount != DefaultKeepCount {
		t.Fatalf("默认保留份数应为 %d，实际 %d", DefaultKeepCount, cfg.History.KeepCount)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	cfg := Default()
	cfg.Scan.Workers = 123
	cfg.Scan.PreFilterPorts = []int{443, 8443}
	cfg.UI.Theme = "dark"
	cfg.Origins.Set("scan.workers", model.OriginUser)

	if err := SaveFile(path, cfg); err != nil {
		t.Fatalf("保存失败：%v", err)
	}
	got, err := LoadFile(path)
	if err != nil {
		t.Fatalf("读取失败：%v", err)
	}

	want, _ := json.Marshal(cfg)
	have, _ := json.Marshal(got)
	if string(want) != string(have) {
		t.Fatalf("往返不一致：\n写：%s\n读：%s", want, have)
	}
	if got.Origins.Get("scan.workers") != model.OriginUser {
		t.Fatal("参数来源未持久化")
	}
}

func TestLoadFileMissingReturnsDefault(t *testing.T) {
	cfg, err := LoadFile(filepath.Join(t.TempDir(), "不存在.json"))
	if err != nil {
		t.Fatalf("首次运行不应报错：%v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("首次运行应拿到可用默认值：%v", err)
	}
}

func TestSaveFileLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	if err := SaveFile(path, Default()); err != nil {
		t.Fatalf("首次保存失败：%v", err)
	}
	// 覆盖写：Windows 上 rename 不能直接覆盖已存在的文件，这条专门盯它。
	if err := SaveFile(path, Default()); err != nil {
		t.Fatalf("覆盖保存失败：%v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("目录应只剩配置文件，实际 %v", names)
	}
	if strings.Contains(entries[0].Name(), ".tmp") {
		t.Fatalf("残留临时文件：%s", entries[0].Name())
	}
}

func TestLoadFileCorruptedFallsBackAndBacksUp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{ 这不是合法 JSON"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFile(path)
	var ce *CorruptedError
	if !errors.As(err, &ce) {
		t.Fatalf("应返回 CorruptedError，实际 %v", err)
	}
	if cfg.Scan.Workers != 200 {
		t.Fatalf("应回退默认值，实际并发 %d", cfg.Scan.Workers)
	}
	if ce.Backup == "" {
		t.Fatal("应记录备份路径")
	}
	if _, serr := os.Stat(ce.Backup); serr != nil {
		t.Fatalf("备份文件不存在：%v", serr)
	}
	if _, serr := os.Stat(path); !errors.Is(serr, fs.ErrNotExist) {
		t.Fatal("损坏的原文件应已移走，避免下次启动再次报错")
	}
}

/**
 * 并发上限由用户自己定，不是写死的。
 *
 * 网卡、路由器、运营商各不相同，能跑多少只有用户自己知道。这里的上限是
 * 「面板允许设到多大」，默认值就是默认能填到的最大值——它刻意不取内置档位的
 * 200，那会把一份本来合法的配置判成非法。
 */
func TestScanWorkersFollowsGlobalCeiling(t *testing.T) {
	t.Run("调低上限后并发不能超过它", func(t *testing.T) {
		cfg := Default()
		cfg.Net.MaxWorkers = 100
		cfg.Scan.Workers = 101

		if err := cfg.Validate(); err == nil {
			t.Fatal("期望校验失败，实际通过")
		}
	})

	t.Run("上限之内照常通过", func(t *testing.T) {
		cfg := Default()
		cfg.Net.MaxWorkers = 100
		cfg.Scan.Workers = 100

		if err := cfg.Validate(); err != nil {
			t.Fatalf("上限之内的值不该被拒：%v", err)
		}
	})

	t.Run("调高上限后能填到更大", func(t *testing.T) {
		cfg := Default()
		cfg.Net.MaxWorkers = 8000
		cfg.Scan.Workers = 8000

		if err := cfg.Validate(); err != nil {
			t.Fatalf("调高上限之后不该被拒：%v", err)
		}
	})

	t.Run("上限本身不能超过代码护栏", func(t *testing.T) {
		cfg := Default()
		cfg.Net.MaxWorkers = MaxWorkersCeiling + 1

		if err := cfg.Validate(); err == nil {
			t.Fatal("期望校验失败，实际通过")
		}
	})
}

// 默认配置必须自洽：并发默认值不能超过默认的全局上限，整份配置也要能过校验。
func TestDefaultConfigIsSelfConsistent(t *testing.T) {
	cfg := Default()

	if cfg.Scan.Workers > cfg.Net.MaxWorkers {
		t.Fatalf("并发默认值 %d 超过了全局上限默认值 %d", cfg.Scan.Workers, cfg.Net.MaxWorkers)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("默认配置不合法：%v", err)
	}
}

func TestValidateRejectsOutOfRange(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantKey string
	}{
		{"并发超过全局上限", func(c *Config) { c.Scan.Workers = c.Net.MaxWorkers + 1 }, "scan.workers"},
		{"并发为 0", func(c *Config) { c.Scan.Workers = 0 }, "scan.workers"},
		{"采样超上限", func(c *Config) { c.Scan.SampleMax = MaxSampleMax + 1 }, "scan.sample_max"},
		{"延迟阈值为 0", func(c *Config) { c.Scan.LatencyThreshold = 0 }, "scan.latency_threshold"},
		{"端口越界", func(c *Config) { c.Scan.Port = 70000 }, "scan.port"},
		{"探测次数为 0", func(c *Config) { c.Scan.PingTimes = 0 }, "scan.ping_times"},
		{"来源模式非法", func(c *Config) { c.Scan.SourceMode = "whatever" }, "scan.source_mode"},
		{"自定义来源为空", func(c *Config) { c.Scan.SourceMode = "custom"; c.Scan.CustomSource = "   " }, "scan.custom_source"},
		{"保留份数越界", func(c *Config) { c.History.KeepCount = 0 }, "history.keep_count"},
		{"保留策略非法", func(c *Config) { c.History.KeepMode = "weekly" }, "history.keep_mode"},
		{"密度非法", func(c *Config) { c.UI.Density = "huge" }, "ui.density"},
		{"主题非法", func(c *Config) { c.UI.Theme = "neon" }, "ui.theme"},
		{"启动页面非法", func(c *Config) { c.UI.StartPage = "nope" }, "ui.start_page"},
		{"监听地址非法", func(c *Config) { c.Server.Bind = "10.0.0.1" }, "server.bind"},
		{"局域网未设 Token", func(c *Config) { c.Server.Bind = "0.0.0.0"; c.Server.Token = "" }, "server.token"},
		{"日志级别非法", func(c *Config) { c.Advanced.LogLevel = "trace" }, "advanced.log_level"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.mutate(&cfg)

			err := cfg.Validate()
			if err == nil {
				t.Fatal("期望校验失败，实际通过")
			}
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("应为 ValidationError，实际 %T", err)
			}
			if !containsKey(ve.Keys(), tt.wantKey) {
				t.Fatalf("期望包含 %s，实际 %v", tt.wantKey, ve.Keys())
			}
		})
	}
}

func TestValidateReportsAllProblems(t *testing.T) {
	cfg := Default()
	cfg.Scan.Workers = 0
	cfg.UI.Theme = "neon"

	err := cfg.Validate()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("应为 ValidationError，实际 %T", err)
	}
	// 一次返回全部问题，前端才能把每个出错字段都高亮出来。
	if len(ve.Fields) < 2 {
		t.Fatalf("应汇总全部问题，实际 %v", ve.Keys())
	}
}

func TestDangerousValuesWarnButPass(t *testing.T) {
	cfg := Default()
	cfg.Scan.Workers = WarnWorkers + 1
	cfg.Scan.LatencyThreshold = WarnLatency - 1

	if err := cfg.Validate(); err != nil {
		t.Fatalf("超过警示阈值不应导致校验失败：%v", err)
	}
	warns := cfg.Warnings()
	if len(warns) != 2 {
		t.Fatalf("应产生 2 条警示，实际 %d 条：%+v", len(warns), warns)
	}
}

// 访问密码：落盘的是加盐哈希，同一个密码两次哈希不同，但都能校验通过。
func TestHashPasswordRoundTrip(t *testing.T) {
	first, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatalf("哈希失败：%v", err)
	}
	second, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatalf("哈希失败：%v", err)
	}

	if first == second {
		t.Error("同一个密码两次哈希应当不同（盐不同）")
	}
	if !IsHashedPassword(first) {
		t.Errorf("落盘格式认不出来：%q", first)
	}
	if strings.Contains(first, "correct horse") {
		t.Error("哈希里出现了明文")
	}
	if !VerifyPassword(first, "correct horse battery") {
		t.Error("正确密码未通过")
	}
	if VerifyPassword(first, "wrong password") {
		t.Error("错误密码通过了")
	}
	if VerifyPassword(first, "") {
		t.Error("空密码通过了")
	}
}

// 空密码不生成哈希。
func TestHashPasswordRejectsEmpty(t *testing.T) {
	if _, err := HashPassword("   "); err == nil {
		t.Error("空密码应当被拒")
	}
}

// 升级前那版自动生成的明文 Token 必须继续可用。
func TestVerifyPasswordAcceptsLegacyPlaintext(t *testing.T) {
	legacy := strings.Repeat("ab", 32)
	if !VerifyPassword(legacy, legacy) {
		t.Error("旧版明文 Token 未通过")
	}
	if VerifyPassword(legacy, legacy+"x") {
		t.Error("旧版明文 Token 的错误值通过了")
	}
	if IsHashedPassword(legacy) {
		t.Error("明文不该被认成哈希")
	}
}

// 坏掉或空掉的存储值一律不通过，不能因为解析失败就放行。
func TestVerifyPasswordRejectsMalformed(t *testing.T) {
	cases := []string{
		"",
		"pbkdf2-sha256$notanumber$c2FsdA$aGFzaA",
		"pbkdf2-sha256$1000$!!!$aGFzaA",
		"pbkdf2-sha256$1000$c2FsdA",
		"pbkdf2-sha256$0$c2FsdA$aGFzaA",
	}
	for _, stored := range cases {
		if VerifyPassword(stored, "anything") {
			t.Errorf("存储值 %q 不该通过校验", stored)
		}
	}
}

func TestStorePatchKeepsUntouchedKeys(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "config.json"), dir)
	if err != nil {
		t.Fatal(err)
	}

	base := store.Get()
	base.Scan.LatencyThreshold = 111
	base.UI.Theme = "dark"
	if _, err := store.Set(base); err != nil {
		t.Fatal(err)
	}

	got, err := store.Patch(
		map[string]any{"scan": map[string]any{"workers": 88}},
		model.ParamOrigins{"scan.workers": model.OriginUser},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.Scan.Workers != 88 {
		t.Fatalf("补丁未生效：workers = %d", got.Scan.Workers)
	}
	// 关键回归：补丁里没出现的键绝不能被重置为默认值。
	if got.Scan.LatencyThreshold != 111 {
		t.Fatalf("未被补丁涉及的键被重置了：latency_threshold = %d", got.Scan.LatencyThreshold)
	}
	if got.UI.Theme != "dark" {
		t.Fatalf("未被补丁涉及的分组被重置了：theme = %q", got.UI.Theme)
	}
	if got.Origins.Get("scan.workers") != model.OriginUser {
		t.Fatal("参数来源未随补丁写入")
	}
}

func TestStoreRejectsInvalidPatch(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "config.json"), dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Patch(map[string]any{"scan": map[string]any{"workers": 0}}, nil); err == nil {
		t.Fatal("非法补丁应被拒绝")
	}
	// 拒绝后磁盘上的配置不应被破坏。
	if _, err := LoadFile(filepath.Join(dir, "config.json")); err != nil {
		var ce *CorruptedError
		if errors.As(err, &ce) {
			t.Fatal("被拒绝的补丁不应写坏配置文件")
		}
	}
}

func TestStorePreservesUnknownSections(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	raw := `{"scan":{"workers":50},"speed":{"concurrency":4},"geo":{"asn_source":"iptoasn"}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := OpenStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := store.Get().Scan.Workers; got != 50 {
		t.Fatalf("应读到既有配置，实际 workers = %d", got)
	}

	if _, err := store.Patch(map[string]any{"scan": map[string]any{"workers": 60}}, nil); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatal(err)
	}
	for _, section := range []string{"speed", "geo"} {
		if _, ok := top[section]; !ok {
			t.Fatalf("尚未实现的分组 %q 被一次保存抹掉了", section)
		}
	}
}

func TestResolveDataDir(t *testing.T) {
	exeDir := t.TempDir()

	cfg := Default()
	cfg.Data.Portable = true

	portable, err := ResolveDataDir(cfg, exeDir)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(exeDir, "data"); portable != want {
		t.Fatalf("便携模式应落在 exe 同级 data/，实际 %s", portable)
	}

	cfg.Data.Dir = filepath.Join(t.TempDir(), "custom")
	explicit, err := ResolveDataDir(cfg, exeDir)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(explicit) {
		t.Fatalf("显式指定的目录应转为绝对路径，实际 %s", explicit)
	}
	if filepath.Base(explicit) != "custom" {
		t.Fatalf("应使用显式目录，实际 %s", explicit)
	}
}

func TestEnsureDataDirs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := EnsureDataDirs(dir); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{dir, HistoryDir(dir), CacheDir(dir), LogsDir(dir)} {
		info, err := os.Stat(sub)
		if err != nil {
			t.Fatalf("目录未创建：%s（%v）", sub, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s 不是目录", sub)
		}
	}
}

func containsKey(keys []string, want string) bool {
	for _, k := range keys {
		if k == want {
			return true
		}
	}
	return false
}

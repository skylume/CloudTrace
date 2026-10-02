package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cloudtrace/internal/model"
)

// 默认值必须能直接跑：用户不做任何设置就该拿到结果。
func TestNewGroupDefaults(t *testing.T) {
	d := Default()

	if d.Speed.URLMode != "auto" || d.Speed.Concurrency != 1 || d.Speed.TargetQualified != 10 {
		t.Errorf("测速默认 = %+v，期望 auto/1/10", d.Speed)
	}
	if d.Speed.IntervalMS != 1200 || d.Speed.DownloadDurationS != 10 || d.Speed.Breaker429 != 3 {
		t.Errorf("测速默认 = %+v，期望 1200/10/3", d.Speed)
	}
	if d.Speed.WeightSpeed != 1 || d.Speed.WeightLatency != 1 || d.Speed.WeightJitter != 0 {
		t.Errorf("评分权重默认 = %v/%v/%v，期望 1/1/0",
			d.Speed.WeightSpeed, d.Speed.WeightLatency, d.Speed.WeightJitter)
	}
	if d.Source.MergeStrategy != "union" || d.Source.Retry != 2 || d.Source.TimeoutMS != 10000 {
		t.Errorf("数据源默认 = %+v，期望 union/2/10000", d.Source)
	}
	if d.Net.UseTLS != "auto" || d.Net.IPVersion != "auto" || d.Net.UserAgent == "" {
		t.Errorf("网络默认 = %+v，期望 auto/auto/非空 UA", d.Net)
	}
	if !d.Net.DNSFallback || !d.Net.ForceDirect {
		t.Errorf("网络默认 = %+v，期望回退与直连都开", d.Net)
	}
	if d.Geo.ASNSource != "iptoasn" || d.Geo.ASNUpdateIntervalDays != 7 || !d.Geo.GeoWarnEnabled {
		t.Errorf("地理默认 = %+v，期望 iptoasn/7/true", d.Geo)
	}
	if d.Export.DefaultFormat != FormatCSV || d.Export.DefaultFields != FieldsAll || !d.Export.CSVBOM {
		t.Errorf("导出默认 = %+v，期望 csv/all/带 BOM", d.Export)
	}
	if !d.Notify.Tray || !d.Notify.OnDone || !d.Notify.OnFail || d.Notify.Web || d.Notify.Sound {
		t.Errorf("通知默认 = %+v，期望托盘+完成+失败开、Web 与声音关", d.Notify)
	}
}

// 零值只在「零值必然无效」时才补默认值。
//
// 反过来更危险：把用户显式设的 0 补成默认值，等于悄悄改掉设置。权重 0
// 是「不惩罚抖动」、合格线 0 是「不设合格线」、更新周期 0 是「仅手动更新」
// —— 这些都必须是合法取值。
func TestNormalizeKeepsMeaningfulZeros(t *testing.T) {
	var c Config
	c.Speed.WeightJitter = 0
	c.Speed.MinSpeed = 0
	c.Speed.PerRegionTopN = 0
	c.Speed.MaxDownloadMB = 0
	c.Geo.ASNUpdateIntervalDays = 0
	c.normalize()

	if c.Speed.WeightJitter != 0 || c.Speed.MinSpeed != 0 {
		t.Errorf("权重与合格线的 0 被补掉了：%+v", c.Speed)
	}
	if c.Speed.PerRegionTopN != 0 || c.Speed.MaxDownloadMB != 0 {
		t.Errorf("TopN 与最大下载量的 0 被补掉了：%+v", c.Speed)
	}
	if c.Geo.ASNUpdateIntervalDays != 0 {
		t.Errorf("更新周期 0（仅手动）被补成 %d", c.Geo.ASNUpdateIntervalDays)
	}

	// 这些字段的 0 是无效值，必须补齐。
	if c.Speed.Concurrency != 1 {
		t.Errorf("并发 0 应补成 1，实际 %d", c.Speed.Concurrency)
	}
	if c.Speed.IntervalMS != 1200 || c.Speed.DownloadDurationS != 10 || c.Speed.Breaker429 != 3 {
		t.Errorf("测速零值未补齐：%+v", c.Speed)
	}
	if c.Speed.UsabilityTimeoutMS == 0 {
		t.Error("可用性校验超时未补齐")
	}
	if c.Net.MaxWorkers == 0 || c.Net.ConnectTimeoutMS == 0 {
		t.Errorf("网络零值未补齐：%+v", c.Net)
	}
	if c.Source.Retry == 0 || c.Source.TimeoutMS == 0 || c.Source.MergeStrategy == "" {
		t.Errorf("数据源零值未补齐：%+v", c.Source)
	}
	if c.Export.DefaultFormat == "" || c.Export.FilenameTemplate == "" {
		t.Errorf("导出零值未补齐：%+v", c.Export)
	}
}

// 新增分组要能原样往返，不能读回来就变。
func TestNewGroupsRoundTrip(t *testing.T) {
	src := Default()
	src.Speed.Concurrency = 8
	src.Speed.URLMode = "custom"
	src.Speed.CustomURL = "https://speed.example.com/100MB"
	src.Speed.WeightJitter = 0.5
	src.Source.RemoteURLs = []RemoteSource{
		{URL: "https://a.example.com/ips.txt", Enabled: true, Note: "主源"},
		{URL: "https://b.example.com/ips.txt", Enabled: false},
	}
	src.Net.Proxy = "socks5://127.0.0.1:1080"
	src.Net.CustomDNS = []string{"1.1.1.1", "8.8.8.8"}
	src.Geo.FilterASN = []string{"AS9808"}
	src.Export.FieldAliases = map[string]string{"ip": "地址", "port": "端口"}
	src.Notify.Sound = true

	data, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	var got Config
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("反序列化失败：%v", err)
	}

	if got.Speed != src.Speed {
		t.Errorf("测速往返不一致：\n got=%+v\nwant=%+v", got.Speed, src.Speed)
	}
	if len(got.Source.RemoteURLs) != 2 || got.Source.RemoteURLs[0].Note != "主源" || got.Source.RemoteURLs[1].Enabled {
		t.Errorf("数据源往返不一致：%+v", got.Source.RemoteURLs)
	}
	if got.Net.Proxy != src.Net.Proxy || len(got.Net.CustomDNS) != 2 {
		t.Errorf("网络往返不一致：%+v", got.Net)
	}
	if len(got.Geo.FilterASN) != 1 || got.Geo.FilterASN[0] != "AS9808" {
		t.Errorf("地理往返不一致：%+v", got.Geo)
	}
	if got.Export.FieldAliases["ip"] != "地址" {
		t.Errorf("导出往返不一致：%+v", got.Export.FieldAliases)
	}
	if !got.Notify.Sound {
		t.Error("通知往返不一致")
	}
}

// 校验要一次报出全部问题，而不是遇到第一个就返回。
func TestValidateNewGroups(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Config)
		want string // 期望出现的配置键
	}{
		{"测速源模式非法", func(c *Config) { c.Speed.URLMode = "nope" }, "speed.url_mode"},
		{"测速并发越界", func(c *Config) { c.Speed.Concurrency = 99 }, "speed.concurrency"},
		{"收敛目标为 0", func(c *Config) { c.Speed.TargetQualified = 0 }, "speed.target_qualified"},
		{"下载时长越界", func(c *Config) { c.Speed.DownloadDurationS = 0 }, "speed.download_duration_s"},
		{"熔断阈值为 0", func(c *Config) { c.Speed.Breaker429 = 0 }, "speed.breaker_429"},
		{"权重为负", func(c *Config) { c.Speed.WeightSpeed = -1 }, "speed.weight_speed"},
		{"自定义测速地址非法", func(c *Config) {
			c.Speed.URLMode = "custom"
			c.Speed.CustomURL = "ftp://example.com/x"
		}, "speed.custom_url"},
		{"合并策略非法", func(c *Config) { c.Source.MergeStrategy = "xor" }, "source.merge_strategy"},
		{"启用的来源地址为空", func(c *Config) {
			c.Source.RemoteURLs = []RemoteSource{{URL: "", Enabled: true}}
		}, "source.remote_urls"},
		{"TLS 设置非法", func(c *Config) { c.Net.UseTLS = "maybe" }, "net.use_tls"},
		{"IP 版本非法", func(c *Config) { c.Net.IPVersion = "v5" }, "net.ip_version"},
		{"代理地址非法", func(c *Config) { c.Net.Proxy = "127.0.0.1:8080" }, "net.proxy"},
		{"ASN 来源非法", func(c *Config) { c.Geo.ASNSource = "maxmind" }, "geo.asn_source"},
		{"导出格式非法", func(c *Config) { c.Export.DefaultFormat = "xlsx" }, "export.default_format"},
		{"字段预设非法", func(c *Config) { c.Export.DefaultFields = "some" }, "export.default_fields"},
		{"文件名模板只有占位符", func(c *Config) { c.Export.FilenameTemplate = "{type}{ts}" }, "export.filename_template"},
		{"时间格式非法", func(c *Config) { c.UI.TimeFormat = "iso" }, "ui.time_format"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			tc.mut(&c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("应当拒绝 %s", tc.name)
			}
			ve, ok := err.(*ValidationError)
			if !ok {
				t.Fatalf("错误类型 = %T，期望 *ValidationError", err)
			}
			if !containsKey(ve.Keys(), tc.want) {
				t.Fatalf("报错键 = %v，期望包含 %q", ve.Keys(), tc.want)
			}
		})
	}
}

// 合法取值必须放行，尤其是那些「0 有意义」的。
func TestValidateAllowsMeaningfulValues(t *testing.T) {
	c := Default()
	c.Speed.WeightJitter = 0
	c.Speed.MinSpeed = 0
	c.Speed.PerRegionTopN = 0
	c.Speed.MaxDownloadMB = 0
	c.Geo.ASNUpdateIntervalDays = 0
	c.Net.Proxy = ""
	c.Speed.URLMode = "custom"
	c.Speed.CustomURL = "https://speed.example.com/50MB"

	if err := c.Validate(); err != nil {
		t.Fatalf("合法配置被拒绝：%v", err)
	}
}

func TestValidSpeedURL(t *testing.T) {
	cases := map[string]bool{
		"https://speed.cloudflare.com/__down?bytes=10000000": true,
		"http://example.com/100MB":                           true,
		"ftp://example.com/100MB":                            false,
		"example.com/100MB":                                  false,
		"":                                                   false,
	}
	for raw, want := range cases {
		if got := validSpeedURL(raw); got != want {
			t.Errorf("validSpeedURL(%q) = %v，期望 %v", raw, got, want)
		}
	}
}

func TestValidProxy(t *testing.T) {
	cases := map[string]bool{
		"http://127.0.0.1:8080":   true,
		"https://proxy.example":   true,
		"socks5://127.0.0.1:1080": true,
		"127.0.0.1:1080":          false,
		"ftp://proxy.example":     false,
		"":                        false,
	}
	for raw, want := range cases {
		if got := validProxy(raw); got != want {
			t.Errorf("validProxy(%q) = %v，期望 %v", raw, got, want)
		}
	}
}

// 停用是「这次不拉」，不是「删掉配置」。
func TestEnabledURLsSkipsDisabledAndEmpty(t *testing.T) {
	c := defaultSource()
	c.RemoteURLs = []RemoteSource{
		{URL: "https://a.example.com", Enabled: true},
		{URL: "https://b.example.com", Enabled: false},
		{URL: "", Enabled: true},
		{URL: "https://c.example.com", Enabled: true},
	}
	got := c.EnabledURLs()
	if len(got) != 2 || got[0] != "https://a.example.com" || got[1] != "https://c.example.com" {
		t.Fatalf("启用的来源 = %v，期望跳过停用与空地址", got)
	}
}

// 深拷贝：改副本不能动到原值。
func TestCloneDeepCopiesNewGroups(t *testing.T) {
	c := Default()
	c.Source.RemoteURLs = []RemoteSource{{URL: "https://a", Enabled: true}}
	c.Net.CustomDNS = []string{"1.1.1.1"}
	c.Geo.FilterASN = []string{"AS9808"}
	c.Export.FieldAliases = map[string]string{"ip": "地址"}

	got := c.Clone()
	got.Source.RemoteURLs[0].URL = "污染"
	got.Net.CustomDNS[0] = "污染"
	got.Geo.FilterASN[0] = "污染"
	got.Export.FieldAliases["ip"] = "污染"
	got.Notify.Sound = true

	if c.Source.RemoteURLs[0].URL == "污染" || c.Net.CustomDNS[0] == "污染" ||
		c.Geo.FilterASN[0] == "污染" || c.Export.FieldAliases["ip"] == "污染" {
		t.Fatalf("Clone 返回了共享引用：%+v %+v %+v %+v",
			c.Source.RemoteURLs, c.Net.CustomDNS, c.Geo.FilterASN, c.Export.FieldAliases)
	}
	if c.Notify.Sound {
		t.Error("Clone 后原值的通知设置被改")
	}
}

// 每个顶层分组都必须在 knownSections 里登记。
//
// 漏登记的分组不会被丢（会原样保留在 extra 里重写出），但它从此不再参与
// 默认值补齐与校验，等于退化成一段无人管理的 JSON。
func TestAllSectionsAreKnown(t *testing.T) {
	data, err := json.Marshal(Default())
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	for k := range top {
		if !knownSections[k] {
			t.Errorf("配置分组 %q 未登记到 knownSections，将不参与校验与补默认值", k)
		}
	}
}

// 导入导出必须往返一致，否则「备份一份配置再恢复」就是假的。
func TestExportImportRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	dst := filepath.Join(dir, "backup.json")

	st, err := OpenStore(path, dir)
	if err != nil {
		t.Fatalf("打开配置失败：%v", err)
	}
	want := Default()
	want.Scan.Workers = 120
	want.Speed.Concurrency = 4
	want.Export.FieldAliases = map[string]string{"ip": "地址"}
	want.Origins = model.ParamOrigins{"scan.workers": model.OriginUser}
	if _, err := st.Set(want); err != nil {
		t.Fatalf("保存失败：%v", err)
	}

	if err := st.ExportTo(dst); err != nil {
		t.Fatalf("导出失败：%v", err)
	}
	got, err := ImportFile(dst)
	if err != nil {
		t.Fatalf("导入失败：%v", err)
	}
	if got.Scan.Workers != 120 || got.Speed.Concurrency != 4 {
		t.Errorf("导入结果 = workers %d / concurrency %d，期望 120 / 4",
			got.Scan.Workers, got.Speed.Concurrency)
	}
	if got.Export.FieldAliases["ip"] != "地址" {
		t.Errorf("字段别名丢了：%+v", got.Export.FieldAliases)
	}
	if got.Origins["scan.workers"] != model.OriginUser {
		t.Errorf("参数来源标记丢了：%+v", got.Origins)
	}
}

// 导入一个不存在的文件要报错，不能静默变成默认值。
func TestImportMissingFileFails(t *testing.T) {
	_, err := ImportFile(filepath.Join(t.TempDir(), "nope.json"))
	if err == nil {
		t.Fatal("导入不存在的文件应当报错")
	}
	if !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("错误信息 = %q，期望说明文件不存在", err.Error())
	}
}

// 一键重置：回到默认值，但不动服务相关的三项。
func TestResetKeepsServiceSettings(t *testing.T) {
	dir := t.TempDir()
	st, err := OpenStore(filepath.Join(dir, "settings.json"), dir)
	if err != nil {
		t.Fatalf("打开配置失败：%v", err)
	}

	cur := Default()
	cur.Server.Token = "keep-me"
	cur.Server.Port = 19999
	cur.Server.Bind = "0.0.0.0"
	cur.Scan.Workers = 480
	cur.Origins = model.ParamOrigins{"scan.workers": model.OriginUser}
	if _, err := st.Set(cur); err != nil {
		t.Fatalf("保存失败：%v", err)
	}

	got, err := st.Reset()
	if err != nil {
		t.Fatalf("重置失败：%v", err)
	}
	if got.Scan.Workers != Default().Scan.Workers {
		t.Errorf("重置后并发 = %d，期望回到默认 %d", got.Scan.Workers, Default().Scan.Workers)
	}
	if len(got.Origins) != 0 {
		t.Errorf("重置后来源标记应清空，实际 %+v", got.Origins)
	}
	if got.Server.Token != "keep-me" {
		t.Errorf("重置把 Token 换掉了，会把已登录会话踢下线：%q", got.Server.Token)
	}
	if got.Server.Port != 19999 || got.Server.Bind != "0.0.0.0" {
		t.Errorf("重置改动了端口或监听地址：%d / %s", got.Server.Port, got.Server.Bind)
	}
}

// 尚未实现的分组要原样保留，不能一次保存就抹掉。
func TestNewGroupsAreNotDroppedOnSave(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/settings.json"

	raw := `{"scan":{"workers":120},"future_feature":{"alpha":1}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("准备配置失败：%v", err)
	}

	st, err := OpenStore(path, dir)
	if err != nil {
		t.Fatalf("打开配置失败：%v", err)
	}
	if _, err := st.Set(Default()); err != nil {
		t.Fatalf("保存失败：%v", err)
	}

	raw2, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取失败：%v", err)
	}
	saved := string(raw2)
	if !strings.Contains(saved, "future_feature") {
		t.Errorf("未知分组被抹掉了：%s", saved)
	}
	// 新分组必须出现在落盘结果里，否则前端读了拿不到。
	for _, want := range []string{`"speed"`, `"source"`, `"net"`, `"geo"`, `"export"`, `"notify"`} {
		if !strings.Contains(saved, want) {
			t.Errorf("落盘配置缺少 %s：%s", want, saved)
		}
	}
}

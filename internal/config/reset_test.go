package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cloudtrace/internal/model"
)

// newResetStore 造一个落在临时目录里的配置存储。
func newResetStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	st, err := OpenStore(filepath.Join(dir, "settings.json"), dir)
	if err != nil {
		t.Fatalf("打开配置失败：%v", err)
	}
	return st
}

// 重置一个分组：组内改动全部回到默认，其它分组不受影响。
func TestResetKeysSectionRestoresDefaults(t *testing.T) {
	st := newResetStore(t)

	if _, err := st.Patch(map[string]any{
		"scan": map[string]any{"workers": 500, "latency_threshold": 120},
		"ui":   map[string]any{"theme": "dark"},
	}, nil); err != nil {
		t.Fatalf("改配置失败：%v", err)
	}

	got, err := st.ResetKeys([]string{"scan"})
	if err != nil {
		t.Fatalf("重置失败：%v", err)
	}

	def := Default()
	if got.Scan.Workers != def.Scan.Workers || got.Scan.LatencyThreshold != def.Scan.LatencyThreshold {
		t.Errorf("scan 分组没有回到默认：%+v", got.Scan)
	}
	if got.UI.Theme != "dark" {
		t.Errorf("重置 scan 把 ui.theme 也改了：%q", got.UI.Theme)
	}
}

// 只重置单个参数时，同组其它参数必须保留用户的值。
func TestResetKeysSingleParameter(t *testing.T) {
	st := newResetStore(t)

	if _, err := st.Patch(map[string]any{
		"scan": map[string]any{"workers": 500, "latency_threshold": 120, "port": 8443},
	}, nil); err != nil {
		t.Fatalf("改配置失败：%v", err)
	}

	got, err := st.ResetKeys([]string{"scan.workers"})
	if err != nil {
		t.Fatalf("重置失败：%v", err)
	}

	def := Default()
	if got.Scan.Workers != def.Scan.Workers {
		t.Errorf("scan.workers = %d，期望回到 %d", got.Scan.Workers, def.Scan.Workers)
	}
	if got.Scan.LatencyThreshold != 120 {
		t.Errorf("同组的 scan.latency_threshold 被一起重置了：%d", got.Scan.LatencyThreshold)
	}
	if got.Scan.Port != 8443 {
		t.Errorf("同组的 scan.port 被一起重置了：%d", got.Scan.Port)
	}
}

// 重置之后不该再留着「用户手改过」的标记，否则自适应永远不敢碰它。
func TestResetKeysClearsOrigins(t *testing.T) {
	st := newResetStore(t)

	origins := model.ParamOrigins{
		"scan.workers": model.OriginUser,
		"scan.port":    model.OriginUser,
		"ui.theme":     model.OriginUser,
	}
	if _, err := st.Patch(map[string]any{
		"scan": map[string]any{"workers": 500},
		"ui":   map[string]any{"theme": "dark"},
	}, origins); err != nil {
		t.Fatalf("改配置失败：%v", err)
	}

	got, err := st.ResetKeys([]string{"scan"})
	if err != nil {
		t.Fatalf("重置失败：%v", err)
	}

	if !got.Origins.CanAutoAdjust("scan.workers") {
		t.Error("重置后 scan.workers 的用户标记还在")
	}
	if !got.Origins.CanAutoAdjust("scan.port") {
		t.Error("重置分组时应当连下级参数的用户标记一起清掉")
	}
	if got.Origins.Get("ui.theme") != model.OriginUser {
		t.Error("重置 scan 不该动 ui.theme 的用户标记")
	}
}

// 重置单个参数只清它自己的标记。
func TestResetKeysClearsOnlyItsOwnOrigin(t *testing.T) {
	st := newResetStore(t)

	origins := model.ParamOrigins{
		"scan.workers": model.OriginUser,
		"scan.port":    model.OriginUser,
	}
	if _, err := st.Patch(map[string]any{"scan": map[string]any{"workers": 500}}, origins); err != nil {
		t.Fatalf("改配置失败：%v", err)
	}

	got, err := st.ResetKeys([]string{"scan.workers"})
	if err != nil {
		t.Fatalf("重置失败：%v", err)
	}
	if !got.Origins.CanAutoAdjust("scan.workers") {
		t.Error("被重置的 scan.workers 标记还在")
	}
	if got.Origins.Get("scan.port") != model.OriginUser {
		t.Error("未被重置的 scan.port 标记被清掉了")
	}
}

// 服务相关三项一律保留：换了 Token 会把已登录会话踢下线，改了端口下次
// 启动找不到面板。
func TestResetKeysKeepsServiceSettings(t *testing.T) {
	st := newResetStore(t)

	cfg := st.Get()
	cfg.Server.Token = "keep-me"
	cfg.Server.Port = 19999
	cfg.Server.Bind = "0.0.0.0"
	cfg.Server.SessionTTLMin = 60
	if _, err := st.Set(cfg); err != nil {
		t.Fatalf("改配置失败：%v", err)
	}

	got, err := st.ResetKeys([]string{"server"})
	if err != nil {
		t.Fatalf("重置失败：%v", err)
	}

	if got.Server.Token != "keep-me" {
		t.Errorf("Token 被重置了：%q", got.Server.Token)
	}
	if got.Server.Port != 19999 || got.Server.Bind != "0.0.0.0" {
		t.Errorf("端口或监听地址被重置了：%d / %s", got.Server.Port, got.Server.Bind)
	}
	if got.Server.SessionTTLMin != Default().Server.SessionTTLMin {
		t.Errorf("会话时长应当被重置：%d", got.Server.SessionTTLMin)
	}
}

// 显式点名 server.port 也一样保留：这条规则只有一种口径，不因为写得更细
// 就换个行为。
func TestResetKeysKeepsServiceSettingsEvenWhenNamed(t *testing.T) {
	st := newResetStore(t)

	cfg := st.Get()
	cfg.Server.Port = 19999
	if _, err := st.Set(cfg); err != nil {
		t.Fatalf("改配置失败：%v", err)
	}

	got, err := st.ResetKeys([]string{"server.port"})
	if err != nil {
		t.Fatalf("重置失败：%v", err)
	}
	if got.Server.Port != 19999 {
		t.Errorf("显式点名 server.port 时端口被改了：%d", got.Server.Port)
	}
}

// map 类型的配置项必须整体替换，不能与用户已有的键合并。
func TestResetKeysReplacesMapsInsteadOfMerging(t *testing.T) {
	st := newResetStore(t)

	if _, err := st.Patch(map[string]any{
		"export": map[string]any{"field_aliases": map[string]any{"ip": "地址", "port": "端口"}},
	}, nil); err != nil {
		t.Fatalf("改配置失败：%v", err)
	}

	got, err := st.ResetKeys([]string{"export"})
	if err != nil {
		t.Fatalf("重置失败：%v", err)
	}
	if len(got.Export.FieldAliases) != 0 {
		t.Errorf("字段别名没有被清空：%v", got.Export.FieldAliases)
	}
}

// 重置结果必须落盘，重启后不能又变回改过的值。
func TestResetKeysPersists(t *testing.T) {
	st := newResetStore(t)

	if _, err := st.Patch(map[string]any{"scan": map[string]any{"workers": 500}}, nil); err != nil {
		t.Fatalf("改配置失败：%v", err)
	}
	if _, err := st.ResetKeys([]string{"scan.workers"}); err != nil {
		t.Fatalf("重置失败：%v", err)
	}

	raw, err := os.ReadFile(st.Path())
	if err != nil {
		t.Fatalf("读取落盘配置失败：%v", err)
	}
	want := `"workers": ` + itoa(Default().Scan.Workers)
	if !strings.Contains(string(raw), want) {
		t.Errorf("落盘配置里没有 %s：%s", want, string(raw))
	}
}

// 空列表等价于整份重置。
func TestResetKeysEmptyMeansFullReset(t *testing.T) {
	st := newResetStore(t)

	cfg := st.Get()
	cfg.Server.Token = "keep-me"
	cfg.UI.Theme = "dark"
	if _, err := st.Set(cfg); err != nil {
		t.Fatalf("改配置失败：%v", err)
	}

	got, err := st.ResetKeys(nil)
	if err != nil {
		t.Fatalf("重置失败：%v", err)
	}
	if got.UI.Theme != Default().UI.Theme {
		t.Errorf("整份重置没有回到默认主题：%q", got.UI.Theme)
	}
	if got.Server.Token != "keep-me" {
		t.Errorf("整份重置把 Token 换掉了：%q", got.Server.Token)
	}
}

// 认不出来的键必须报错：静默忽略会让用户以为「点了恢复默认」其实没生效。
func TestResetKeysUnknownKeyFails(t *testing.T) {
	st := newResetStore(t)

	for _, key := range []string{"nope", "scan.nope", "scan.workers.deeper", ""} {
		if _, err := st.ResetKeys([]string{key}); err == nil {
			t.Errorf("重置 %q 应当报错", key)
		}
	}

	// 报错之后配置不能被改坏。
	if got := st.Get().Scan.Workers; got != Default().Scan.Workers {
		t.Errorf("失败的调用改动了配置：workers = %d", got)
	}
}

// 一次点名多个键时逐个生效。
func TestResetKeysMultipleKeys(t *testing.T) {
	st := newResetStore(t)

	if _, err := st.Patch(map[string]any{
		"scan":   map[string]any{"workers": 500},
		"speed":  map[string]any{"concurrency": 8},
		"notify": map[string]any{"web": true},
	}, nil); err != nil {
		t.Fatalf("改配置失败：%v", err)
	}

	got, err := st.ResetKeys([]string{"scan.workers", "speed"})
	if err != nil {
		t.Fatalf("重置失败：%v", err)
	}

	def := Default()
	if got.Scan.Workers != def.Scan.Workers {
		t.Errorf("scan.workers 没回到默认：%d", got.Scan.Workers)
	}
	if got.Speed.Concurrency != def.Speed.Concurrency {
		t.Errorf("speed 分组没回到默认：%d", got.Speed.Concurrency)
	}
	if !got.Notify.Web {
		t.Error("没点名的 notify 分组被重置了")
	}
}

func TestSplitKey(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"scan", []string{"scan"}},
		{"scan.workers", []string{"scan", "workers"}},
		{" scan . workers ", []string{"scan", "workers"}},
		{"", nil},
		{".", nil},
	}
	for _, c := range cases {
		got := splitKey(c.in)
		if len(got) != len(c.want) {
			t.Errorf("splitKey(%q) = %v，期望 %v", c.in, got, c.want)
			continue
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Errorf("splitKey(%q) = %v，期望 %v", c.in, got, c.want)
				break
			}
		}
	}
}

func TestCoveredBy(t *testing.T) {
	keys := []string{"scan.workers", "ui"}
	cases := map[string]bool{
		"scan.workers":   true,
		"scan.workers.x": true,
		"scan.port":      false,
		"ui":             true,
		"ui.theme":       true,
		"uix":            false,
		"":               false,
	}
	for key, want := range cases {
		if got := coveredBy(key, keys); got != want {
			t.Errorf("coveredBy(%q) = %v，期望 %v", key, got, want)
		}
	}
}

func TestLookupAndSetPath(t *testing.T) {
	obj := map[string]any{"scan": map[string]any{"workers": 1}}

	if v, ok := lookupPath(obj, []string{"scan", "workers"}); !ok || v != 1 {
		t.Errorf("lookupPath 取值 = %v / %v", v, ok)
	}
	if _, ok := lookupPath(obj, []string{"scan", "nope"}); ok {
		t.Error("不存在的键不该被找到")
	}
	if _, ok := lookupPath(obj, []string{"scan", "workers", "deeper"}); ok {
		t.Error("路径穿过了非对象不该被找到")
	}

	setPath(obj, []string{"scan", "port"}, 8443)
	setPath(obj, []string{"new", "inner"}, true)
	if obj["scan"].(map[string]any)["port"] != 8443 {
		t.Error("setPath 没有写入已有层级")
	}
	if obj["new"].(map[string]any)["inner"] != true {
		t.Error("setPath 没有补出缺失层级")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

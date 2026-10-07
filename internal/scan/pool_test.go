package scan

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"

	"cloudtrace/internal/model"
)

// fakeResolver 用固定答案替换真实 DNS，让候选池生成完全离线可测。
type fakeResolver struct {
	answers map[string][]string
	// queried 记录被查过的域名，用来断言「解析确实只做一次」。
	queried []string
}

func (f *fakeResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	f.queried = append(f.queried, host)
	ips, ok := f.answers[host]
	if !ok {
		return nil, errors.New("域名不存在")
	}
	out := make([]net.IPAddr, 0, len(ips))
	for _, ip := range ips {
		addr, err := netip.ParseAddr(ip)
		if err != nil {
			return nil, err
		}
		out = append(out, net.IPAddr{IP: addr.AsSlice()})
	}
	return out, nil
}

func baseParams() model.ScanParams {
	return model.ScanParams{
		Mode:             "tcping",
		Workers:          4,
		SampleMax:        100,
		LatencyThreshold: 230,
		PingTimes:        2,
		Port:             443,
		IPVersion:        4,
		SourceMode:       "custom",
		TwoPhase:         true,
		VerifyNodes:      true,
		TimeoutMS:        500,
	}
}

func candidateKeys(candidates []Candidate) []string {
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, c.IP+":"+strconv.Itoa(c.Port))
	}
	return out
}

func TestMatchesIPVersion(t *testing.T) {
	tests := []struct {
		ip      string
		version int
		want    bool
	}{
		{ip: "1.1.1.1", version: 4, want: true},
		{ip: "1.1.1.1", version: 6, want: false},
		{ip: "2606:4700::1", version: 6, want: true},
		{ip: "2606:4700::1", version: 4, want: false},
		// 4-in-6 写法本质是 IPv4，不能算进 IPv6 那一档。
		{ip: "::ffff:1.1.1.1", version: 6, want: false},
		{ip: "::ffff:1.1.1.1", version: 4, want: false},
		{ip: "not-an-ip", version: 4, want: false},
		{ip: "", version: 4, want: false},
	}

	for _, tt := range tests {
		if got := matchesIPVersion(tt.ip, tt.version); got != tt.want {
			t.Errorf("matchesIPVersion(%q, %d) = %v，期望 %v", tt.ip, tt.version, got, tt.want)
		}
	}
}

func TestMakeCandidate(t *testing.T) {
	opts := poolOptions{Params: baseParams(), Host: "edge.example.com"}

	got := makeCandidate("1.1.1.1", 0, opts)
	if got.Port != 443 {
		t.Errorf("未指定端口时 Port = %d，期望默认端口 443", got.Port)
	}
	if !got.UseTLS {
		t.Error("443 端口应带上 TLS 标记")
	}
	if got.Host != "edge.example.com" {
		t.Errorf("Host = %q，期望 %q", got.Host, "edge.example.com")
	}

	got = makeCandidate("1.1.1.1", 8080, opts)
	if got.Port != 8080 {
		t.Errorf("指定端口时 Port = %d，期望 8080", got.Port)
	}
	if got.UseTLS {
		t.Error("8080 端口不应带上 TLS 标记")
	}
}

func TestBuildPoolCustomSources(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		params  func(*model.ScanParams)
		want    []string
		maxSize int
	}{
		{
			name:   "单个 IP 用默认端口",
			source: "1.1.1.1",
			want:   []string{"1.1.1.1:443"},
		},
		{
			name:   "IP 带端口",
			source: "1.1.1.1:8443",
			want:   []string{"1.1.1.1:8443"},
		},
		{
			name:   "重复地址只留一个",
			source: "1.1.1.1\n1.1.1.1\n1.1.1.1:443",
			want:   []string{"1.1.1.1:443"},
		},
		{
			name:   "同 IP 不同端口是两条",
			source: "1.1.1.1\n1.1.1.1:8443",
			want:   []string{"1.1.1.1:443", "1.1.1.1:8443"},
		},
		{
			name:   "地址区间展开",
			source: "1.1.1.1-1.1.1.4",
			want:   []string{"1.1.1.1:443", "1.1.1.2:443", "1.1.1.3:443", "1.1.1.4:443"},
		},
		{
			name:   "网段采样",
			source: "10.0.0.0/24",
			want:   nil, // 只断言条数
			params: func(p *model.ScanParams) { p.SampleMax = 3 },
		},
		{
			name:   "注释与空行被忽略",
			source: "# 注释\n\n  1.1.1.1  \n",
			want:   []string{"1.1.1.1:443"},
		},
		{
			name:   "地址族不符的条目被剔除",
			source: "1.1.1.1\n2606:4700::1",
			want:   []string{"1.1.1.1:443"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := baseParams()
			params.CustomSource = tt.source
			if tt.params != nil {
				tt.params(&params)
			}
			opts := poolOptions{Params: params, Host: "edge.example.com", SampleMax: params.SampleMax}

			got, err := buildPool(context.Background(), opts, nil, &fakeResolver{})
			if err != nil {
				t.Fatalf("buildPool 返回错误：%v", err)
			}
			if tt.want == nil {
				if len(got.Candidates) == 0 || len(got.Candidates) > params.SampleMax {
					t.Fatalf("候选数 = %d，期望落在 1..%d", len(got.Candidates), params.SampleMax)
				}
				return
			}
			if diff := diffKeys(candidateKeys(got.Candidates), tt.want); diff != "" {
				t.Errorf("候选集合不符：%s", diff)
			}
		})
	}
}

func TestBuildPoolResolvesHosts(t *testing.T) {
	params := baseParams()
	params.CustomSource = "example.com\nother.example.com:8443"
	resolver := &fakeResolver{answers: map[string][]string{
		"example.com":       {"1.1.1.1", "1.1.1.2"},
		"other.example.com": {"2.2.2.2"},
	}}

	got, err := buildPool(context.Background(), poolOptions{
		Params: params, Host: "edge.example.com", SampleMax: params.SampleMax,
	}, nil, resolver)
	if err != nil {
		t.Fatalf("buildPool 返回错误：%v", err)
	}

	want := []string{"1.1.1.1:443", "1.1.1.2:443", "2.2.2.2:8443"}
	if diff := diffKeys(candidateKeys(got.Candidates), want); diff != "" {
		t.Errorf("候选集合不符：%s", diff)
	}
	if got.Explicit != 3 {
		t.Errorf("显式条目数 = %d，期望 3", got.Explicit)
	}
}

func TestBuildPoolRecordsFailedHosts(t *testing.T) {
	params := baseParams()
	params.CustomSource = "missing.example.com"
	resolver := &fakeResolver{answers: map[string][]string{}}

	got, err := buildPool(context.Background(), poolOptions{
		Params: params, Host: "edge.example.com", SampleMax: params.SampleMax,
	}, nil, resolver)
	if err != nil {
		t.Fatalf("域名解析失败不应中断候选池生成，实际返回：%v", err)
	}
	if len(got.DroppedHosts) != 1 || got.DroppedHosts[0] != "missing.example.com" {
		t.Errorf("解析失败清单 = %v，期望 [missing.example.com]", got.DroppedHosts)
	}
}

func TestBuildPoolRemoteSources(t *testing.T) {
	params := baseParams()
	// 只留远程源：官方段会掺进采样候选，让断言看不清楚。
	params.SourceMode = "custom"
	params.CustomSource = ""
	remote := func(_ context.Context, urls []string) ([]model.IPRecord, []string, error) {
		if len(urls) != 2 {
			t.Errorf("远程源地址数 = %d，期望 2", len(urls))
		}
		return []model.IPRecord{
			{IP: "9.9.9.9", Port: 8443, Colo: "NRT", Loc: "JP"},
			{IP: "9.9.9.10"},
		}, []string{"https://bad.example.com"}, nil
	}

	got, err := buildPool(context.Background(), poolOptions{
		Params:     params,
		Host:       "edge.example.com",
		SampleMax:  100,
		RemoteURLs: []string{"https://a.example.com", "https://bad.example.com"},
	}, remote, &fakeResolver{})
	if err != nil {
		t.Fatalf("buildPool 返回错误：%v", err)
	}

	want := []string{"9.9.9.9:8443", "9.9.9.10:443"}
	if diff := diffKeys(candidateKeys(got.Candidates), want); diff != "" {
		t.Errorf("候选集合不符：%s", diff)
	}
	if len(got.RemoteFailed) != 1 || got.RemoteFailed[0] != "https://bad.example.com" {
		t.Errorf("失败清单 = %v，期望一个坏地址", got.RemoteFailed)
	}
	// 远程源自带的地区信息要带进候选，前置过滤才能用上它。
	for _, c := range got.Candidates {
		if c.IP == "9.9.9.9" && (c.Colo != "NRT" || c.Loc != "JP") {
			t.Errorf("远程源地区未带进候选：colo=%q loc=%q", c.Colo, c.Loc)
		}
	}
}

func TestBuildPoolNoRemoteURLsSkipsFetch(t *testing.T) {
	params := baseParams()
	params.SourceMode = "official"
	called := false
	remote := func(context.Context, []string) ([]model.IPRecord, []string, error) {
		called = true
		return nil, nil, nil
	}

	if _, err := buildPool(context.Background(), poolOptions{
		Params: params, Host: "edge.example.com", SampleMax: 5,
	}, remote, &fakeResolver{}); err != nil {
		t.Fatalf("buildPool 返回错误：%v", err)
	}
	if called {
		t.Error("没有配置远程源时不应发起拉取")
	}
}

// TestBuildPoolPrefersExplicitUnderCap 验证采样上限不够用时先保显式条目。
//
// 用户手写的一个 IP，比随机抽出来的一个 IP 更可能就是他想要的。
func TestBuildPoolPrefersExplicitUnderCap(t *testing.T) {
	params := baseParams()
	params.CustomSource = "1.1.1.1\n1.1.1.2\n1.1.1.3\n10.0.0.0/24"

	got, err := buildPool(context.Background(), poolOptions{
		Params: params, Host: "edge.example.com", SampleMax: 3,
	}, nil, &fakeResolver{})
	if err != nil {
		t.Fatalf("buildPool 返回错误：%v", err)
	}
	want := []string{"1.1.1.1:443", "1.1.1.2:443", "1.1.1.3:443"}
	if diff := diffKeys(candidateKeys(got.Candidates), want); diff != "" {
		t.Errorf("候选集合不符：%s", diff)
	}
}

func TestBuildPoolIsDeterministic(t *testing.T) {
	params := baseParams()
	params.SourceMode = "official"

	first, err := buildPool(context.Background(), poolOptions{
		Params: params, Host: "edge.example.com", SampleMax: 20, Seed: 42,
	}, nil, &fakeResolver{})
	if err != nil {
		t.Fatalf("buildPool 返回错误：%v", err)
	}
	second, err := buildPool(context.Background(), poolOptions{
		Params: params, Host: "edge.example.com", SampleMax: 20, Seed: 42,
	}, nil, &fakeResolver{})
	if err != nil {
		t.Fatalf("buildPool 返回错误：%v", err)
	}

	if len(first.Candidates) == 0 {
		t.Fatal("官方段没有产出任何候选")
	}
	if diff := diffKeys(candidateKeys(first.Candidates), candidateKeys(second.Candidates)); diff != "" {
		t.Errorf("同一种子两次结果不一致：%s", diff)
	}
	for _, c := range first.Candidates {
		if !matchesIPVersion(c.IP, 4) {
			t.Errorf("候选 %q 不是 IPv4", c.IP)
		}
		if c.Host != "edge.example.com" {
			t.Errorf("候选 %q 的 Host = %q", c.IP, c.Host)
		}
	}
}

func TestBuildPoolOfficialIPv6(t *testing.T) {
	params := baseParams()
	params.SourceMode = "official"
	params.IPVersion = 6

	got, err := buildPool(context.Background(), poolOptions{
		Params: params, Host: "edge.example.com", SampleMax: 10, Seed: 7,
	}, nil, &fakeResolver{})
	if err != nil {
		t.Fatalf("buildPool 返回错误：%v", err)
	}
	if len(got.Candidates) == 0 {
		t.Fatal("官方 IPv6 段没有产出任何候选")
	}
	for _, c := range got.Candidates {
		if !matchesIPVersion(c.IP, 6) {
			t.Errorf("候选 %q 不是 IPv6", c.IP)
		}
	}
}

func TestBuildPoolEmptySource(t *testing.T) {
	params := baseParams()
	params.CustomSource = ""

	got, err := buildPool(context.Background(), poolOptions{
		Params: params, Host: "edge.example.com", SampleMax: 10,
	}, nil, &fakeResolver{})
	if err != nil {
		t.Fatalf("空来源不应报错，实际：%v", err)
	}
	if len(got.Candidates) != 0 {
		t.Errorf("候选数 = %d，期望 0", len(got.Candidates))
	}
}

// TestBuildPoolBothModesMergesSources 验证「官方 + 自定义」两种来源都会进池子，
// 且合并后的总数仍受采样上限约束。
func TestBuildPoolBothModesMergesSources(t *testing.T) {
	params := baseParams()
	params.SourceMode = "both"
	params.CustomSource = "1.1.1.1"

	got, err := buildPool(context.Background(), poolOptions{
		Params: params, Host: "edge.example.com", SampleMax: 8, Seed: 3,
	}, nil, &fakeResolver{})
	if err != nil {
		t.Fatalf("buildPool 返回错误：%v", err)
	}
	if got.Explicit != 1 {
		t.Errorf("显式条目数 = %d，期望 1", got.Explicit)
	}
	if got.Sampled == 0 {
		t.Error("官方段应同时产出采样候选")
	}
	if len(got.Candidates) != 8 {
		t.Errorf("候选数 = %d，期望 8（上限约束的是合并后的总数）", len(got.Candidates))
	}
	// 上限吃紧时先保显式条目：用户手写的那个 IP 必须在。
	found := false
	for _, c := range got.Candidates {
		if c.IP == "1.1.1.1" {
			found = true
		}
	}
	if !found {
		t.Error("上限吃紧时显式条目被挤掉了")
	}
}

// diffKeys 比较两个 key 集合（与顺序无关），返回可读的差异描述。
func diffKeys(got, want []string) string {
	gotSet := make(map[string]bool, len(got))
	for _, k := range got {
		gotSet[k] = true
	}
	wantSet := make(map[string]bool, len(want))
	for _, k := range want {
		wantSet[k] = true
	}

	var missing, extra []string
	for k := range wantSet {
		if !gotSet[k] {
			missing = append(missing, k)
		}
	}
	for k := range gotSet {
		if !wantSet[k] {
			extra = append(extra, k)
		}
	}
	if len(missing) == 0 && len(extra) == 0 && len(got) != len(want) {
		return "条数不符（存在重复项）"
	}
	if len(missing) == 0 && len(extra) == 0 {
		return ""
	}
	var b strings.Builder
	if len(missing) > 0 {
		b.WriteString("缺少 " + strings.Join(missing, ","))
	}
	if len(extra) > 0 {
		if b.Len() > 0 {
			b.WriteString("；")
		}
		b.WriteString("多出 " + strings.Join(extra, ","))
	}
	return b.String()
}

// 远程源里写的域名要解析，不能静默丢掉。
//
// 解析层本来就支持域名（parseNodeLine 对主机名返回 KindHost），但候选池此前
// 一律把它当 IP 用，随后在 IP 版本过滤里因为 netip.ParseAddr 失败被丢掉——
// 用户只看到候选莫名变少，界面上没有任何提示。自定义来源那条路一直是解析的。
func TestBuildPoolRemoteHostnamesAreResolved(t *testing.T) {
	params := baseParams()
	params.SourceMode = "custom"
	params.CustomSource = ""

	remote := func(context.Context, []string) ([]model.IPRecord, []string, error) {
		return []model.IPRecord{
			// 域名带地区标注，解析出来的地址要继承它。
			{IP: "edge.example.com", Port: 8443, Colo: "NRT", Loc: "JP"},
			// 解析不出来的那个要进「跳过的域名」，让用户看得见。
			{IP: "gone.example.com", Port: 443},
		}, nil, nil
	}
	resolver := &fakeResolver{answers: map[string][]string{
		"edge.example.com": {"9.9.9.9", "9.9.9.10"},
	}}

	got, err := buildPool(context.Background(), poolOptions{
		Params:     params,
		Host:       "edge.example.com",
		SampleMax:  100,
		RemoteURLs: []string{"https://a.example.com"},
	}, remote, resolver)
	if err != nil {
		t.Fatalf("buildPool 返回错误：%v", err)
	}

	want := []string{"9.9.9.9:8443", "9.9.9.10:8443"}
	if diff := diffKeys(candidateKeys(got.Candidates), want); diff != "" {
		t.Errorf("候选集合不符：%s", diff)
	}
	for _, c := range got.Candidates {
		if c.Colo != "NRT" || c.Loc != "JP" {
			t.Errorf("解析出的地址没继承地区：ip=%s colo=%q loc=%q", c.IP, c.Colo, c.Loc)
		}
	}
	if len(got.DroppedHosts) != 1 || got.DroppedHosts[0] != "gone.example.com" {
		t.Errorf("跳过清单 = %v，期望记下解析不出来的那个域名", got.DroppedHosts)
	}
}

// 同一个域名在同一端口下重复出现时只解析一次，且不会产生重复候选。
func TestBuildPoolRemoteHostnamesAreDeduped(t *testing.T) {
	params := baseParams()
	params.SourceMode = "custom"
	params.CustomSource = ""

	remote := func(context.Context, []string) ([]model.IPRecord, []string, error) {
		return []model.IPRecord{
			{IP: "edge.example.com", Port: 443, Loc: "JP"},
			{IP: "edge.example.com", Port: 443, Loc: "JP"},
		}, nil, nil
	}
	resolver := &fakeResolver{answers: map[string][]string{"edge.example.com": {"9.9.9.9"}}}

	got, err := buildPool(context.Background(), poolOptions{
		Params: params, Host: "edge.example.com", SampleMax: 100,
		RemoteURLs: []string{"https://a.example.com"},
	}, remote, resolver)
	if err != nil {
		t.Fatalf("buildPool 返回错误：%v", err)
	}
	if len(resolver.queried) != 1 {
		t.Errorf("解析次数 = %d，期望 1（重复条目只查一次）", len(resolver.queried))
	}
	if len(got.Candidates) != 1 {
		t.Errorf("候选数 = %d，期望 1（去重后）", len(got.Candidates))
	}
}

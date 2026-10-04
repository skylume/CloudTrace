package speed

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"cloudtrace/internal/geo"
)

// 测速源模式（对应配置里的 speed.url_mode）。
const (
	URLModeAuto           = "auto"
	URLModeOfficial       = "official"
	URLModeMobileFriendly = "mobile_friendly"
	URLModeMobileOnly     = "mobile_only"
	URLModeCustom         = "custom"
)

// 测速源地址。前两个都不带协议前缀，由探测层按 use_tls 补全。
const (
	officialSpeedURL       = "speed.cloudflare.com/__down?bytes=99999999"
	mobileFriendlySpeedURL = "cf.090227.xyz/__down?bytes=99999999"
	mobileOnlySpeedURL     = "speed.okl.abrdns.com"
	ispProbeURL            = "https://cf.090227.xyz/cf.json"
)

// DefaultSourceTTL 是 auto 选源结果的缓存时长。
//
// 出口 ISP 几分钟内不会变，而每测一个目标探一次既慢又容易被对方限流。
const DefaultSourceTTL = 10 * time.Minute

// ispProbeTimeout 是出口 ISP 探测的超时。
const ispProbeTimeout = 5 * time.Second

// chinaMobileASNs 是中国移动的 AS 号。
//
// 关键词表会随运营商改组织名而失效，AS 号不会，所以两者都要认。
var chinaMobileASNs = map[int]bool{
	9808:  true, // 中国移动
	24400: true, // 中国移动国际
	56040: true,
	56041: true,
	56044: true,
}

// chinaMobileKeywords 是组织名里的中国移动关键词，全部按小写比较。
var chinaMobileKeywords = []string{
	"cmi", "cmnet", "chinamobile", "china mobile", "cmcc", "mobile communications", "移动",
}

// ISPInfo 是出口 ISP 探测的结果。
type ISPInfo struct {
	// IP 是本机出口地址；接口没给时为空，此时无法用本地库反查。
	IP  string `json:"ip"`
	ASN int    `json:"asn"`
	Org string `json:"asOrganization"`
}

// ISPProbe 探测当前出口的 ISP。
type ISPProbe func(ctx context.Context) (ISPInfo, error)

// SourceResolver 解析本次测速使用的下载地址。
//
// auto 模式需要一次网络探测，结果按 TTL 缓存：同一个任务里每个目标都探一次
// 既浪费又容易触发限流。
type SourceResolver struct {
	probe ISPProbe
	now   func() time.Time
	ttl   time.Duration
	// pick 从候选地址里挑一个，注入是为了让随机选择可测。
	pick func([]string) string
	// logf 记录回退原因；为 nil 时不记录。
	logf func(format string, args ...any)
	// asn 用本地 ASN 库反查出口地址的 AS 信息；为 nil 时只用探测接口给的。
	//
	// 本地库每小时更新，且不依赖那个探测接口是否还返回这两个字段，因此
	// 有它就优先用它。库不可用时留空，关键词与硬编码 AS 表那条回退路径
	// 照常工作。
	asn geo.LookupFunc

	mu sync.Mutex
	// cached 是上一次的完整判定，不只是选出的地址。
	//
	// 只缓存地址会丢掉「为什么选它」：缓存命中时说不出出口是不是移动宽带，
	// 而那个判断同时是自适应降并发的唯一信号来源。缓存整份判定，命中也答得
	// 上来。
	cached SourceDecision
	at     time.Time
}

// NewSourceResolver 构造选源器。probe 为 nil 时用真实的出口探测。
func NewSourceResolver(probe ISPProbe, now func() time.Time, ttl time.Duration) *SourceResolver {
	if probe == nil {
		probe = HTTPISPProbe
	}
	if now == nil {
		now = time.Now
	}
	if ttl <= 0 {
		ttl = DefaultSourceTTL
	}
	return &SourceResolver{probe: probe, now: now, ttl: ttl, pick: pickRandom}
}

// SetLogger 注入日志函数，便于观察回退与选源决策。
func (r *SourceResolver) SetLogger(logf func(format string, args ...any)) { r.logf = logf }

// SetASNLookup 注入本地 ASN 查询，用于补全出口的 AS 信息。
func (r *SourceResolver) SetASNLookup(fn geo.LookupFunc) { r.asn = fn }

// Resolve 返回本次测速要用的下载地址。
//
// auto 模式的探测失败**不算错误**：拿不到出口 ISP 只影响「要不要换移动源」，
// 换不成官方源照样能测。为这点信息让整轮测速失败是本末倒置。
// 选源原因。机器可读的短标识，前端据此选文案。
const (
	// ReasonPinned 是用户明确指定了测速源。
	ReasonPinned = "pinned"
	// ReasonMobile 是探测到出口属于中国移动，因而用移动测速源。
	ReasonMobile = "mobile"
	// ReasonNotMobile 是出口不属于中国移动，用官方源。
	ReasonNotMobile = "not_mobile"
	// ReasonProbeFailed 是出口探测失败，回退官方源。
	ReasonProbeFailed = "probe_failed"
)

// SourceDecision 是一次选源的结果与理由。
//
// 把「为什么选它」一起返回，是因为自动选源对用户是个黑箱：同一份配置在不同
// 网络下会选中不同的源，而用户看不到任何线索。理由本身比结果更值得展示。
type SourceDecision struct {
	URL string
	// Code 是原因标识，见上面的 Reason* 常量。
	Code string
	// Detail 是补充说明（如「AS9808 中国移动」），可为空。
	Detail string
}

// Resolve 只返回选中的地址，是 Decide 的薄包装。
func (r *SourceResolver) Resolve(ctx context.Context, mode, customURL string) (string, error) {
	decision, err := r.Decide(ctx, mode, customURL)
	return decision.URL, err
}

// Decide 选源并说明理由。
func (r *SourceResolver) Decide(ctx context.Context, mode, customURL string) (SourceDecision, error) {
	switch mode {
	case URLModeOfficial:
		return SourceDecision{URL: officialSpeedURL, Code: ReasonPinned}, nil
	case URLModeMobileFriendly:
		return SourceDecision{URL: mobileFriendlySpeedURL, Code: ReasonPinned}, nil
	case URLModeMobileOnly:
		return SourceDecision{URL: mobileOnlySpeedURL, Code: ReasonPinned}, nil
	case URLModeCustom:
		raw := strings.TrimSpace(customURL)
		if raw == "" {
			return SourceDecision{}, fmt.Errorf("选择了自定义测速源，但地址为空")
		}
		return SourceDecision{URL: raw, Code: ReasonPinned}, nil
	}

	// auto 与无法识别的取值都走自动选源：配置里写错一个词不该让测速失败。
	return r.decideAuto(ctx), nil
}

// decideAuto 探测出口 ISP 并据此选源，结果在 TTL 内复用。
func (r *SourceResolver) decideAuto(ctx context.Context) SourceDecision {
	if cached, ok := r.cachedDecision(); ok {
		return cached
	}

	info, err := r.probe(ctx)
	if err != nil {
		r.note("出口 ISP 探测失败，回退官方测速源：%v", err)
		return SourceDecision{URL: officialSpeedURL, Code: ReasonProbeFailed}
	}
	info = r.fillASN(info)

	decision := SourceDecision{URL: officialSpeedURL, Code: ReasonNotMobile}
	if isChinaMobile(info) {
		url := r.pick([]string{mobileFriendlySpeedURL, mobileOnlySpeedURL})
		decision = SourceDecision{URL: url, Code: ReasonMobile, Detail: describeISP(info)}
		r.note("出口 ISP 判定为中国移动（AS%d %s），使用移动测速源 %s", info.ASN, info.Org, url)
	} else {
		decision.Detail = describeISP(info)
	}

	r.store(decision)
	return decision
}

// describeISP 把出口的 AS 信息拼成一句可显示的说明。
//
// 探测没给 AS 信息时返回空串而不是「AS0」：0 是保留值，显示出来只会让人困惑。
func describeISP(info ISPInfo) string {
	if info.ASN == 0 && info.Org == "" {
		return ""
	}
	if info.Org == "" {
		return fmt.Sprintf("AS%d", info.ASN)
	}
	if info.ASN == 0 {
		return info.Org
	}
	return fmt.Sprintf("AS%d %s", info.ASN, info.Org)
}

// fillASN 用本地 ASN 库补全出口的 AS 信息。
//
// 判定中国移动靠的就是 AS 号与组织名这两项，探测接口哪天不再返回它们，
// 判定就会退化成纯关键词匹配。本地库能补上就补上；查不到时保留接口给的
// 值，什么都不改。
func (r *SourceResolver) fillASN(info ISPInfo) ISPInfo {
	if r.asn == nil {
		return info
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(info.IP))
	if err != nil {
		return info
	}
	got, ok := r.asn(addr)
	if !ok {
		return info
	}
	return ISPInfo{IP: info.IP, ASN: int(got.ASN), Org: got.Org}
}

func (r *SourceResolver) cachedDecision() (SourceDecision, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cached.URL == "" || r.now().Sub(r.at) >= r.ttl {
		return SourceDecision{}, false
	}
	return r.cached, true
}

func (r *SourceResolver) store(decision SourceDecision) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cached = decision
	r.at = r.now()
}

/**
 * MobileExit 判断出口是否属于移动宽带。
 *
 * 「智能推荐」要按网络环境给建议，而出口运营商只有这里知道。走 Decide 的自动
 * 分支，因此与选速测源共用同一份十分钟缓存：重复点不会反复发探测请求。
 *
 * 探测失败不算错误：拿不到结论时按「不是移动宽带」处理，也就是不给建议。
 */
func (r *SourceResolver) MobileExit(ctx context.Context) bool {
	return r.decideAuto(ctx).Code == ReasonMobile
}

// note 记录一次选源决策；日志函数未注入时静默跳过。
func (r *SourceResolver) note(format string, args ...any) {
	if r.logf != nil {
		r.logf(format, args...)
	}
}

// isChinaMobile 判断出口 ISP 是否为中国移动。
func isChinaMobile(info ISPInfo) bool {
	if chinaMobileASNs[info.ASN] {
		return true
	}
	org := strings.ToLower(info.Org)
	for _, keyword := range chinaMobileKeywords {
		if strings.Contains(org, keyword) {
			return true
		}
	}
	return false
}

// pickRandom 从候选地址里等概率挑一个。
//
// 用 crypto/rand 而不是全局的伪随机数发生器：本包不持有包级可变状态，
// 全局发生器正是那种状态。
func pickRandom(urls []string) string {
	if len(urls) == 0 {
		return officialSpeedURL
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(urls))))
	if err != nil {
		return urls[0]
	}
	return urls[int(n.Int64())]
}

// HTTPISPProbe 用公开接口探测当前出口的 AS 号与组织名。
//
// 不走代理：测速下载是直连目标 IP 的，出口 ISP 也必须按直连的结果判断，
// 否则会把代理的 ISP 当成自己的。
func HTTPISPProbe(ctx context.Context) (ISPInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, ispProbeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ispProbeURL, nil)
	if err != nil {
		return ISPInfo{}, fmt.Errorf("构造出口探测请求失败：%w", err)
	}

	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}

	resp, err := client.Do(req)
	if err != nil {
		return ISPInfo{}, fmt.Errorf("出口探测请求失败：%w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return ISPInfo{}, fmt.Errorf("出口探测返回状态码 %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxISProbeBody))
	if err != nil {
		return ISPInfo{}, fmt.Errorf("读出口探测响应失败：%w", err)
	}

	var info ISPInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return ISPInfo{}, fmt.Errorf("出口探测响应不是合法 JSON：%w", err)
	}
	return info, nil
}

// maxISProbeBody 是出口探测响应的大小上限。正常响应只有一百多字节，
// 设上限是为了挡住异常的大响应。
const maxISProbeBody = 64 << 10

// checkSpeedURL 校验自定义测速地址的形状。
//
// 只做形状检查（有主机名、协议合法），不发请求：地址不通是网络问题，
// 该在测速时报；地址本身写错是配置问题，该在启动前就拦住。
func checkSpeedURL(raw string) error {
	value := strings.TrimSpace(raw)
	if value == "" {
		return fmt.Errorf("测速地址为空")
	}
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("测速地址无法解析：%w", err)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("测速地址的协议 %q 不受支持", parsed.Scheme)
	}
	if strings.TrimSpace(parsed.Hostname()) == "" {
		return fmt.Errorf("测速地址缺少域名：%s", raw)
	}
	return nil
}

package scan

import "strings"

// Candidate 是候选池里的一项。
//
// Colo / Loc 是「已经知道的」地区信息，来自归属地缓存或远程源自带的
// 国家码。走到前置过滤这一步时还没有发过任何网络请求，所以大多数候选
// 的这两个字段是空的——这正是「地区未知保守保留」的由来。
type Candidate struct {
	IP     string
	Port   int
	Host   string // 测试域名，用于 Host 头与 TLS SNI
	UseTLS bool
	Colo   string // 已知的数据中心代码
	Loc    string // 已知的出口国家码
}

// PreFilterOptions 是前置过滤的输入。
type PreFilterOptions struct {
	// Ports 非空时只保留这些端口上的候选；为空表示不限制。
	Ports []int
	// BlockedRegions 命中的候选被排除；为空表示不限制。
	BlockedRegions []string
	// AllowedRegions 非空时只保留命中的候选；为空表示不限制。
	AllowedRegions []string
}

// PreFilterResult 是前置过滤的结果与各轮淘汰计数。
//
// 三组计数分开记：用户看到「候选全没了」时，需要知道是被端口、黑名单
// 还是白名单吃掉的，否则只能靠猜。
type PreFilterResult struct {
	Kept          []Candidate
	PortRejected  int
	BlockRejected int
	AllowRejected int
}

// Rejected 返回被淘汰的候选总数。
func (r PreFilterResult) Rejected() int {
	return r.PortRejected + r.BlockRejected + r.AllowRejected
}

// PreFilter 在 TCP 探测之前淘汰明显不合格的候选。
//
// 顺序固定为端口 → 黑名单 → 白名单，不可调换：
//   - 端口过滤不需要任何额外信息、最便宜，放最前面最省算力；
//   - 白名单必须放最后，语义是「先排除明确不要的，再从想要的里面挑」。
//     调换之后，一个既在白名单里又被拉黑的地区会被放行。
//
// 地区未知时保守保留：判不出来就放过去。多探一次只是浪费一点时间，
// 误杀却会让用户永远扫不到本该可用的节点。
//
// 这一步必须在任何 TCP 探测之前执行，否则「省掉无效探测」的意义就没了。
func PreFilter(candidates []Candidate, opts PreFilterOptions) PreFilterResult {
	ports := intSet(opts.Ports)
	blocked := regionSet(opts.BlockedRegions)
	allowed := regionSet(opts.AllowedRegions)

	out := PreFilterResult{Kept: make([]Candidate, 0, len(candidates))}
	for _, c := range candidates {
		if len(ports) > 0 {
			if _, ok := ports[c.Port]; !ok {
				out.PortRejected++
				continue
			}
		}

		if len(blocked) > 0 && matchesRegion(c, blocked) {
			out.BlockRejected++
			continue
		}

		if len(allowed) > 0 && knownRegion(c) && !matchesRegion(c, allowed) {
			out.AllowRejected++
			continue
		}

		out.Kept = append(out.Kept, c)
	}
	return out
}

func intSet(values []int) map[int]struct{} {
	if len(values) == 0 {
		return nil
	}
	set := make(map[int]struct{}, len(values))
	for _, v := range values {
		set[v] = struct{}{}
	}
	return set
}

// regionSet 把配置里的地区码归一化成大写集合，顺带丢掉空项。
func regionSet(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(values))
	for _, v := range values {
		if v = strings.ToUpper(strings.TrimSpace(v)); v != "" {
			set[v] = struct{}{}
		}
	}
	return set
}

// matchesRegion 判断候选的已知地区是否命中集合。
//
// colo（数据中心代码）与 loc（出口国家码）都参与比较，大小写不敏感：
// 用户写 "HKG" 指的是数据中心，写 "HK" 指的是出口地区，两个字段都可能
// 是用户想表达的那个，只比一个会漏。
func matchesRegion(c Candidate, set map[string]struct{}) bool {
	for _, code := range []string{c.Colo, c.Loc} {
		if code == "" {
			continue
		}
		if _, ok := set[strings.ToUpper(strings.TrimSpace(code))]; ok {
			return true
		}
	}
	return false
}

// knownRegion 报告候选是否带有任何已知地区信息。
func knownRegion(c Candidate) bool {
	return strings.TrimSpace(c.Colo) != "" || strings.TrimSpace(c.Loc) != ""
}

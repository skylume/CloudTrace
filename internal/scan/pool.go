package scan

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"cloudtrace/assets"
	"cloudtrace/internal/model"
	"cloudtrace/internal/probe"
	"cloudtrace/internal/source"
)

// poolOptions 是候选池生成的输入。
type poolOptions struct {
	Params     model.ScanParams
	Host       string // 测试域名，写入每个候选
	SampleMax  int    // 已经按阶段折算过的采样上限，0 表示不限制
	Seed       int64  // 采样随机种子
	RemoteURLs []string
}

// poolResult 是候选池与生成过程的统计。
//
// 显式条目与网段展开的候选分开计数：用户看到「候选怎么这么少」时，
// 需要知道是网段采样截断了，还是来源文本压根没解析出东西。
type poolResult struct {
	Candidates   []Candidate
	Explicit     int      // 显式点名的候选（单 IP / 域名 / 远程源）
	Sampled      int      // 网段展开并采样出的候选
	DroppedHosts []string // 解析失败的域名
	RemoteFailed []string // 拉取失败的远程源
}

// buildPool 生成候选池：官方段 + 自定义来源 + 远程源 → 合并 → 去重 → 采样。
//
// 顺序是有讲究的：网段要先合并去重再采样，否则同一个网段既出现在官方段
// 里又出现在用户文本里时会被抽两次，采样预算白白浪费在重复地址上。
//
// 显式点名的条目排在网段采样之前，采样上限不够用时先保它们：用户手写的
// 一个 IP，比随机抽出来的一个 IP 更可能就是他想要的。
func buildPool(ctx context.Context, opts poolOptions, remote RemoteFunc, resolver source.Resolver) (poolResult, error) {
	var out poolResult

	cidrs := make([]string, 0, 32)
	explicit := make([]Candidate, 0, 32)
	// 域名按「条目自带的端口」分组：同一段来源文本里可能出现
	// example.com:8443 与 example.com，两者的候选不是一回事。
	hostsByPort := make(map[int][]string)

	if opts.Params.UsesOfficial() {
		cidrs = append(cidrs, assets.OfficialRanges(opts.Params.IPVersion)...)
	}
	if opts.Params.UsesCustom() {
		parsed, err := source.ParseSourceText(opts.Params.CustomSource)
		if err != nil {
			return out, err
		}
		for _, entry := range parsed.Entries {
			switch entry.Kind {
			case source.KindCIDR:
				cidrs = append(cidrs, entry.Value)
			case source.KindRange:
				prefixes, ok := rangeToCIDRs(entry.Value)
				if !ok {
					return out, fmt.Errorf("无法展开地址区间 %q", entry.Value)
				}
				for _, prefix := range prefixes {
					cidrs = append(cidrs, prefix.String())
				}
			case source.KindIP:
				explicit = append(explicit, makeCandidate(entry.Value, entry.Port, opts))
			case source.KindHost:
				hostsByPort[entry.Port] = append(hostsByPort[entry.Port], entry.Value)
			}
		}
	}

	if len(hostsByPort) > 0 {
		// 端口按键排序：map 的遍历顺序是随机的，不排序就没法保证
		// 同一份输入每次得到同一批候选。
		ports := make([]int, 0, len(hostsByPort))
		for port := range hostsByPort {
			ports = append(ports, port)
		}
		sort.Ints(ports)

		for _, port := range ports {
			ips, failed, err := source.ResolveHosts(ctx, resolver, hostsByPort[port])
			if err != nil {
				return out, err
			}
			out.DroppedHosts = append(out.DroppedHosts, failed...)
			for _, ip := range ips {
				explicit = append(explicit, makeCandidate(ip, port, opts))
			}
		}
	}

	if len(opts.RemoteURLs) > 0 && remote != nil {
		// 远程源失败不中断扫描：某个源挂了只是少一批候选，
		// 因此单个地址的失败由 remote 收进 failed 清单，这里不报错。
		records, failed, err := remote(ctx, opts.RemoteURLs)
		if err != nil {
			return out, err
		}
		out.RemoteFailed = failed
		for _, rec := range records {
			cand := makeCandidate(rec.IP, rec.Port, opts)
			cand.Colo = rec.Colo
			cand.Loc = rec.Loc
			explicit = append(explicit, cand)
		}
	}
	out.Explicit = len(explicit)

	sampled := make([]Candidate, 0, 32)
	if len(cidrs) > 0 {
		ips, err := source.Sample(cidrs, perSlash24, opts.SampleMax, opts.Seed)
		if err != nil {
			return out, err
		}
		out.Sampled = len(ips)
		for _, ip := range ips {
			sampled = append(sampled, makeCandidate(ip, 0, opts))
		}
	}

	merged := make([]Candidate, 0, len(explicit)+len(sampled))
	merged = append(merged, explicit...)
	merged = append(merged, sampled...)

	seen := make(map[string]bool, len(merged))
	out.Candidates = make([]Candidate, 0, len(merged))
	for _, cand := range merged {
		if !matchesIPVersion(cand.IP, opts.Params.IPVersion) {
			continue
		}
		key := cand.IP + ":" + strconv.Itoa(cand.Port)
		if seen[key] {
			continue
		}
		seen[key] = true
		out.Candidates = append(out.Candidates, cand)
		if opts.SampleMax > 0 && len(out.Candidates) >= opts.SampleMax {
			break
		}
	}
	return out, nil
}

// makeCandidate 按扫描参数补全端口与 TLS 标记；port <= 0 时用默认端口。
func makeCandidate(ip string, port int, opts poolOptions) Candidate {
	if port <= 0 {
		port = opts.Params.Port
	}
	return Candidate{
		IP:     ip,
		Port:   port,
		Host:   opts.Host,
		UseTLS: probe.IsHTTPSPort(port),
	}
}

// matchesIPVersion 报告地址是否属于要扫的地址族。
//
// 来源文本是手写的，里面混进另一种族的地址很常见；直接拿去扫只会得到
// 一堆必然失败的结果，不如在生成候选池时就剔掉。
func matchesIPVersion(ip string, version int) bool {
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return false
	}
	if version == 6 {
		return !addr.Is4() && !addr.Is4In6()
	}
	return addr.Is4()
}

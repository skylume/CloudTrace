package source

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"sort"
	"strings"
)

const (
	// hostOctetLimit 是 /24 里可选末位段的上界（不含）。
	// 排除全 1 的末位：它在 /24 里是广播地址，不是可用主机。
	hostOctetLimit = 255

	// maxUnboundedCandidates 是「不限制」时的内部安全上限。
	//
	// 用户可能填一个 /8 甚至更大的段，真按「不限制」展开会瞬间吃光内存。
	// 这个上限远超档位允许的采样量，正常场景碰不到。
	maxUnboundedCandidates = 65536
)

// Sample 从网段列表里随机抽取候选 IP。
//
// perSlash24 是每个 /24 抽取的个数，不足 1 时按 1 处理；max 是结果上限，
// 0 表示不限制（内部仍有安全上限）；seed 是随机种子，同一种子与同一输入
// 必须得到逐字节一致的输出，测试靠这一点做断言。
//
// 采样规则：
//   - 比 /24 更长的前缀直接全取（地址太少，随机没有意义）；
//   - /24 与前缀更短的段按 /24 切分，每块随机取 perSlash24 个；
//   - IPv6 保持前缀稳定，只随机前缀之后的位。
//
// 结果会去重，并按首次出现的顺序排列。
func Sample(cidrs []string, perSlash24, max int, seed int64) ([]string, error) {
	if max < 0 {
		return nil, fmt.Errorf("采样上限 %d 不能为负", max)
	}
	if perSlash24 < 1 {
		perSlash24 = 1
	}
	limit := max
	if limit <= 0 {
		limit = maxUnboundedCandidates
	}

	rng := rand.New(rand.NewSource(seed))
	seen := make(map[string]bool)
	out := make([]string, 0, min(limit, 256))

	for _, raw := range cidrs {
		if len(out) >= limit {
			break
		}
		network, err := parseNetwork(raw)
		if err != nil {
			return nil, err
		}
		for _, ip := range sampleNetwork(network, perSlash24, limit-len(out), rng) {
			if seen[ip] {
				continue
			}
			seen[ip] = true
			out = append(out, ip)
		}
	}
	return out, nil
}

// parseNetwork 解析网段；不带掩码的单个 IP 按 /32 或 /128 处理。
func parseNetwork(raw string) (*net.IPNet, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("网段为空")
	}
	if !strings.Contains(raw, "/") {
		ip := net.ParseIP(raw)
		if ip == nil {
			return nil, fmt.Errorf("无法解析网段：%s", raw)
		}
		if ip.To4() != nil {
			raw += "/32"
		} else {
			raw += "/128"
		}
	}
	_, network, err := net.ParseCIDR(raw)
	if err != nil {
		return nil, fmt.Errorf("无法解析网段 %s：%w", raw, err)
	}
	return network, nil
}

// sampleNetwork 按地址族分发采样。
func sampleNetwork(network *net.IPNet, perSlash24, budget int, rng *rand.Rand) []string {
	if budget <= 0 {
		return nil
	}
	ones, bits := network.Mask.Size()
	if bits == 32 {
		return sampleIPv4(network, ones, perSlash24, budget, rng)
	}
	return sampleIPv6(network, ones, perSlash24, budget, rng)
}

// sampleIPv4 采样一个 IPv4 网段。
func sampleIPv4(network *net.IPNet, ones, perSlash24, budget int, rng *rand.Rand) []string {
	base := network.IP.To4()
	if base == nil {
		return nil
	}
	// 比 /24 还小的段直接全取：地址太少，随机反而可能漏掉可用地址。
	if ones > 24 {
		return enumerateIPv4(binary.BigEndian.Uint32(base), ones, budget)
	}

	blockCount := 1 << (24 - ones)
	needed := (budget + perSlash24 - 1) / perSlash24
	if needed > blockCount {
		needed = blockCount
	}

	prefix := binary.BigEndian.Uint32(base)
	prefixMask := ^uint32(0) << uint(32-ones)

	out := make([]string, 0, min(budget, blockCount*perSlash24))
	var raw [4]byte
	for _, index := range pickIndices(blockCount, needed, rng) {
		block := (prefix & prefixMask) | uint32(index)<<8
		for n := 0; n < perSlash24; n++ {
			binary.BigEndian.PutUint32(raw[:], block|uint32(rng.Intn(hostOctetLimit)))
			out = append(out, net.IP(raw[:]).String())
			if len(out) >= budget {
				return out
			}
		}
	}
	return out
}

// enumerateIPv4 展开一个前缀长于 /24 的网段，取其中全部地址。
func enumerateIPv4(base uint32, ones, budget int) []string {
	size := uint32(1) << uint(32-ones)
	out := make([]string, 0, min(int(size), budget))
	var raw [4]byte
	for i := uint32(0); i < size && len(out) < budget; i++ {
		binary.BigEndian.PutUint32(raw[:], base+i)
		out = append(out, net.IP(raw[:]).String())
	}
	return out
}

// sampleIPv6 采样一个 IPv6 网段，保持前缀稳定。
//
// 随机起点取「前缀长度」与 64 的较大者：/64 及更短的段只随机后 64 位，
// 前缀更长的段则只随机前缀之后的位，保证结果一定落在段内。
func sampleIPv6(network *net.IPNet, ones, perSlash24, budget int, rng *rand.Rand) []string {
	base := network.IP.To16()
	if base == nil {
		return nil
	}
	start := ones
	if start < 64 {
		start = 64
	}
	if start >= 128 {
		return []string{net.IP(base).String()}
	}

	out := make([]string, 0, min(budget, perSlash24))
	for len(out) < budget && len(out) < perSlash24 {
		candidate := make(net.IP, len(base))
		copy(candidate, base)
		for bit := start; bit < 128; bit++ {
			if rng.Intn(2) == 1 {
				candidate[bit/8] |= 1 << uint(7-bit%8)
			}
		}
		out = append(out, candidate.String())
	}
	return out
}

// pickIndices 从 [0, total) 里挑出 count 个下标，按升序返回。
//
// 需要挑的数量达到总数时直接全取，避免在 /8 这类大段上做无谓的随机重试。
func pickIndices(total, count int, rng *rand.Rand) []int {
	if count >= total {
		out := make([]int, total)
		for i := range out {
			out[i] = i
		}
		return out
	}
	seen := make(map[int]bool, count)
	out := make([]int, 0, count)
	for len(out) < count {
		index := rng.Intn(total)
		if seen[index] {
			continue
		}
		seen[index] = true
		out = append(out, index)
	}
	sort.Ints(out)
	return out
}

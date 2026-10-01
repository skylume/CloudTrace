package scan

import (
	"net/netip"
	"strings"
)

// rangeToCIDRs 把 "起始-结束" 形式的地址区间拆成一组对齐的网段。
//
// 为什么不直接展开成逐个 IP：用户填一个跨 /8 的区间，逐个展开会瞬间把
// 内存吃满。为什么不直接把区间交给采样器：采样器只认网段，区间不是网段。
// 按「从左侧起尽量大的对齐块」拆分，既保留了区间语义，又能让采样器照常
// 做规模控制。
//
// 拆不出来（格式不对、两端不同族、起始大于结束）时返回 false。
func rangeToCIDRs(spec string) ([]netip.Prefix, bool) {
	startText, endText, ok := strings.Cut(spec, "-")
	if !ok {
		return nil, false
	}
	start, err := netip.ParseAddr(strings.TrimSpace(startText))
	if err != nil {
		return nil, false
	}
	end, err := netip.ParseAddr(strings.TrimSpace(endText))
	if err != nil {
		return nil, false
	}
	if start.Is4() != end.Is4() || start.Compare(end) > 0 {
		return nil, false
	}

	bits := 128
	if start.Is4() {
		bits = 32
	}

	out := make([]netip.Prefix, 0, 4)
	for cur := start; cur.IsValid() && cur.Compare(end) <= 0; {
		// 起点必须是块首，否则拆出来的网段会覆盖到区间之外的地址。
		length := bits - trailingZeros(cur, bits)
		// 块尾越过区间末尾就缩小一块，直到装得下。
		for length < bits && blockEnd(cur, length, bits).Compare(end) > 0 {
			length++
		}
		out = append(out, netip.PrefixFrom(cur, length))

		next := blockEnd(cur, length, bits).Next()
		if !next.IsValid() {
			break
		}
		cur = next
	}
	return out, len(out) > 0
}

// blockEnd 返回以 addr 为起点、前缀长度为 length 的那一块的最后一个地址。
func blockEnd(addr netip.Addr, length, bits int) netip.Addr {
	raw := addr.As16()
	offset := 16 - bits/8
	for i := 0; i < bits/8; i++ {
		switch hostBits := (i+1)*8 - length; {
		case hostBits >= 8:
			raw[offset+i] = 0xff
		case hostBits > 0:
			raw[offset+i] |= byte(1<<hostBits - 1)
		}
	}
	if bits == 32 {
		var out [4]byte
		copy(out[:], raw[12:])
		return netip.AddrFrom4(out)
	}
	return netip.AddrFrom16(raw)
}

// trailingZeros 返回地址低位连续 0 的个数，也就是「以它开头还能对齐到多小
// 的前缀」。全零地址返回位宽。
func trailingZeros(addr netip.Addr, bits int) int {
	raw := addr.As16()
	offset := 16 - bits/8
	for i := bits/8 - 1; i >= 0; i-- {
		b := raw[offset+i]
		if b == 0 {
			continue
		}
		zeros := 0
		for b&1 == 0 {
			zeros++
			b >>= 1
		}
		return zeros + (bits/8-1-i)*8
	}
	return bits
}

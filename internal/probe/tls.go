package probe

// UseTLSMode 是「是否走 TLS」的三态设置。
//
// 布尔值表达不了「按端口自动判断」，而这个第三态正是旧实现踩坑的地方：
// 把 use_tls 一律当 true 处理，非 TLS 端口上的节点必然连不上，用户看到
// 的却是「节点不可用」。
const (
	// UseTLSAuto 按端口推断。
	UseTLSAuto = "auto"
	// UseTLSOn 强制走 TLS。
	UseTLSOn = "true"
	// UseTLSOff 强制不走 TLS。
	UseTLSOff = "false"
)

// IsHTTPSPort 报告端口是否按 HTTPS 处理。
//
// Cloudflare 除 443 之外还提供几个备用 HTTPS 端口；只认 443 会让这些端口
// 上的节点全部测不通。
func IsHTTPSPort(port int) bool {
	switch port {
	case 443, 8443, 2053, 2083, 2087, 2096:
		return true
	default:
		return false
	}
}

// ResolveUseTLS 把三态设置落到具体端口上。
//
// mode 不是 auto / true / false 时按 auto 处理：配置里写错一个词不该让整轮
// 探测失败，退到「按端口推断」是这里最不容易出错的行为。
func ResolveUseTLS(mode string, port int) bool {
	switch mode {
	case UseTLSOn:
		return true
	case UseTLSOff:
		return false
	default:
		return IsHTTPSPort(port)
	}
}

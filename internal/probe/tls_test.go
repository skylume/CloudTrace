package probe

import "testing"

// HTTPS 端口集合必须全覆盖：漏掉一个，那个端口上的节点就会被当成明文
// HTTP 去连，结果是「节点不可用」——正是要修掉的那个旧问题。
func TestIsHTTPSPort(t *testing.T) {
	for _, port := range []int{443, 8443, 2053, 2083, 2087, 2096} {
		if !IsHTTPSPort(port) {
			t.Errorf("端口 %d 应判定为 HTTPS", port)
		}
	}
	for _, port := range []int{0, 80, 8080, 22, 4443} {
		if IsHTTPSPort(port) {
			t.Errorf("端口 %d 不应判定为 HTTPS", port)
		}
	}
}

func TestResolveUseTLS(t *testing.T) {
	tests := []struct {
		name string
		mode string
		port int
		want bool
	}{
		{"auto 走 HTTPS 端口", UseTLSAuto, 443, true},
		{"auto 走备用 HTTPS 端口", UseTLSAuto, 8443, true},
		{"auto 走明文端口", UseTLSAuto, 80, false},
		{"空值等同 auto", "", 443, true},
		{"无法识别的取值退回 auto", "yes", 80, false},
		{"强制开启覆盖明文端口", UseTLSOn, 80, true},
		{"强制关闭覆盖 HTTPS 端口", UseTLSOff, 443, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveUseTLS(tc.mode, tc.port); got != tc.want {
				t.Errorf("ResolveUseTLS(%q, %d) = %v，期望 %v", tc.mode, tc.port, got, tc.want)
			}
		})
	}
}

package netx

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"
)

func TestNewDialerReturnsNilWithoutServers(t *testing.T) {
	if got := NewDialer(nil, true); got != nil {
		t.Error("没配自定义 DNS 时应当返回 nil，让调用方走默认路径")
	}
	if got := NewDialer([]string{"  ", ""}, true); got != nil {
		t.Error("全是空项时应当返回 nil")
	}
}

func TestNormalizeServers(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{[]string{"1.1.1.1"}, []string{"1.1.1.1:53"}},
		{[]string{" 8.8.8.8 "}, []string{"8.8.8.8:53"}},
		// 自建 DNS 常监听在非标准端口，带了端口的要原样保留。
		{[]string{"10.0.0.1:5353"}, []string{"10.0.0.1:5353"}},
		{[]string{"1.1.1.1", "", "1.0.0.1"}, []string{"1.1.1.1:53", "1.0.0.1:53"}},
		// IPv6 字面量要加方括号，否则 JoinHostPort 会拼出歧义地址。
		{[]string{"2606:4700:4700::1111"}, []string{"[2606:4700:4700::1111]:53"}},
	}
	for _, tt := range cases {
		got := normalizeServers(tt.in)
		if len(got) != len(tt.want) {
			t.Errorf("normalizeServers(%v) = %v，期望 %v", tt.in, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("normalizeServers(%v)[%d] = %q，期望 %q", tt.in, i, got[i], tt.want[i])
			}
		}
	}
}

/**
 * 目标是 IP 时不解析。
 *
 * 扫描、测速、CF 校验的目标全是 IP，走的就是这条路。要是这里也去问一遍 DNS，
 * 每个节点的探测都会多一次解析——自定义 DNS 一旦不可用，整轮扫描跟着卡住。
 * 用一个连不上的 DNS 服务器来证明它根本没被问过。
 */
func TestDialerBypassesResolutionForIPs(t *testing.T) {
	dial := NewDialer([]string{"127.0.0.1:1"}, false)
	if dial == nil {
		t.Fatal("应当返回可用的拨号函数")
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("起测试监听失败：%v", err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := dial(ctx, "tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("目标是 IP 时不该去解析：%v", err)
	}
	_ = conn.Close()
}

/**
 * 回退决策：先自定义、失败再系统。
 *
 * 这三条只有注入假实现才测得到——回环名字走的是 hosts 文件、根本不会去问
 * DNS，而拿真名字又必须联网。
 */
func TestPickLookupFallbackDecision(t *testing.T) {
	ok := func(ips ...string) lookupFunc {
		return func(context.Context, string) ([]string, error) { return ips, nil }
	}
	boom := func(context.Context, string) ([]string, error) {
		return nil, &net.DNSError{Err: "服务器不回包"}
	}
	empty := func(context.Context, string) ([]string, error) { return nil, nil }

	t.Run("自定义成功时不问系统", func(t *testing.T) {
		systemCalled := false
		system := func(ctx context.Context, host string) ([]string, error) {
			systemCalled = true
			return []string{"9.9.9.9"}, nil
		}

		ips, err := pickLookup(context.Background(), "a.example", ok("1.1.1.1"), system, true)
		if err != nil || len(ips) != 1 || ips[0] != "1.1.1.1" {
			t.Fatalf("ips = %v / err = %v", ips, err)
		}
		if systemCalled {
			t.Error("自定义成功时不该再问一遍系统解析")
		}
	})

	t.Run("自定义失败且允许回退时用系统结果", func(t *testing.T) {
		ips, err := pickLookup(context.Background(), "a.example", boom, ok("2.2.2.2"), true)
		if err != nil || len(ips) != 1 || ips[0] != "2.2.2.2" {
			t.Fatalf("ips = %v / err = %v", ips, err)
		}
	})

	t.Run("自定义返回空结果也算失败", func(t *testing.T) {
		ips, err := pickLookup(context.Background(), "a.example", empty, ok("2.2.2.2"), true)
		if err != nil || len(ips) != 1 {
			t.Fatalf("空结果应当触发回退：ips = %v / err = %v", ips, err)
		}
	})

	t.Run("关掉回退时直接失败", func(t *testing.T) {
		if _, err := pickLookup(context.Background(), "a.example", boom, ok("2.2.2.2"), false); err == nil {
			t.Error("关掉回退后不该用系统结果")
		}
	})

	// 两条路都失败时优先报自定义那条：那才是用户刚配的东西。
	t.Run("两条都失败时报自定义的错误", func(t *testing.T) {
		_, err := pickLookup(context.Background(), "a.example", boom, boom, true)
		if err == nil {
			t.Fatal("应当返回错误")
		}
		if dnsErr, ok := err.(*net.DNSError); !ok || dnsErr.Err != "服务器不回包" {
			t.Errorf("错误 = %v，期望自定义那条", err)
		}
	})
}

// 带端口的地址原样传给底层拨号：HTTP 客户端传进来的就是这种形状。
func TestDialerKeepsPortForIPTargets(t *testing.T) {
	dial := NewDialer([]string{"127.0.0.1:1"}, false)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// 127.0.0.1:1 上通常没有服务，重点是它不会去解析、也不会挂住。
	_, err := dial(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(1)))
	if err == nil {
		t.Skip("本机 1 号端口居然有服务，跳过")
	}
	if ctx.Err() != nil {
		t.Errorf("不该等到上下文超时才返回：%v", err)
	}
}

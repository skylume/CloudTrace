package server

import (
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"time"
)

// 服务状态相关的命令与事件名。
const (
	cmdServerStatus  = "server/status"
	cmdServerRestart = "server/restart"
	eventServer      = "server"
)

// serverStatusResp 回答「这台面板现在能从哪儿访问」。
//
// 桌面版必须把它显示在界面上而不是控制台：桌面版用 `-H=windowsgui` 构建，
// 根本没有控制台窗口，打在 stdout 上的地址用户永远看不到——而「局域网怎么访问」
// 恰恰是他在设置页里刚打开那个开关之后最想知道的事。
type serverStatusResp struct {
	Bind string `json:"bind"`
	Port int    `json:"port"`
	// LocalURL 是本机访问地址。
	LocalURL string `json:"local_url"`
	// LANURLs 是局域网内其他设备可用的地址；只绑回环时为空。
	LANURLs []string `json:"lan_urls"`
	// NeedsAuth 表示局域网访问需要登录。只绑回环时为 false——本机访问免鉴权。
	NeedsAuth bool `json:"needs_auth"`
	// PasswordSet 表示已经设过访问密码。
	PasswordSet bool `json:"password_set"`
	// Version 是构建版本号。
	Version string `json:"version"`
	// DataDir 是当前生效的数据目录。
	DataDir string `json:"data_dir"`
}

// serverHandlers 返回服务状态相关的命令表。
func (s *server) serverHandlers() map[string]commandHandler {
	return map[string]commandHandler{
		cmdServerStatus:  s.handleServerStatus,
		cmdServerRestart: s.handleServerRestart,
	}
}

// handleServerRestart 重启程序。
//
// 先把回执发出去再动手：重启会让当前连接断开，前端收不到任何后续响应，只能
// 靠这条回执知道「命令收到了，正在重启」——没有它，界面表现和「点了个没反应
// 的按钮」一模一样，用户会连点好几次。
func (s *server) handleServerRestart(c *wsConn, _ json.RawMessage) error {
	if s.svc.Restart == nil {
		return fail(CodeUnknown, "当前入口不支持自动重启，请手动关闭后重新打开")
	}

	c.sendEvent(cmdServerRestart, map[string]bool{"ok": true})
	s.logger.Info("收到重启请求")

	go func() {
		// 留一点时间让回执真的发出去。
		time.Sleep(300 * time.Millisecond)
		if err := s.svc.Restart(); err != nil {
			s.logger.Error("重启失败，请手动重新打开", "err", err)
		}
	}()
	return nil
}

func (s *server) handleServerStatus(c *wsConn, _ json.RawMessage) error {
	cfg := s.cfg.Get()
	port := cfg.Server.Port
	if s.listenPort > 0 {
		// 命令行参数可能覆盖了配置里的端口，实际监听的那个才算数。
		port = s.listenPort
	}

	dir, _ := s.cfg.DataDir()
	c.sendEvent(eventServer, serverStatusResp{
		Bind: cfg.Server.Bind,
		Port: port,
		// 本机地址固定用 127.0.0.1 而不是 bind：bind 可能是 0.0.0.0，
		// 那不是个能打开的地址。
		LocalURL:    "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		LANURLs:     lanURLs(cfg.Server.Bind, port),
		NeedsAuth:   !isLoopbackBind(cfg.Server.Bind),
		PasswordSet: cfg.Server.Token != "",
		Version:     s.svc.Version,
		DataDir:     dir,
	})
	return nil
}

// isLoopbackBind 报告监听地址是否只对本机开放。
func isLoopbackBind(bind string) bool {
	if bind == "" {
		return true
	}
	ip := net.ParseIP(bind)
	if ip == nil {
		// 主机名（如 localhost）当作本机处理：它解析到哪儿由系统决定，
		// 但用户写这个值时的意图就是「只给本机」。
		return strings.EqualFold(bind, "localhost")
	}
	return ip.IsLoopback()
}

// lanURLs 列出局域网内其他设备可用的访问地址。
//
// 只绑回环时返回空：那种情况下局域网根本连不上，给出地址等于骗人。
func lanURLs(bind string, port int) []string {
	if isLoopbackBind(bind) {
		return nil
	}

	// 绑定了具体地址就用它：用户指定了网卡，枚举出来的其他地址访问不到。
	if bind != "" && bind != "0.0.0.0" && bind != "::" {
		return []string{"http://" + net.JoinHostPort(bind, strconv.Itoa(port))}
	}

	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}

	out := make([]string, 0, 4)
	seen := map[string]bool{}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		// 只要 IPv4：局域网里拿手机访问时输 IPv6 字面量没人受得了，
		// 而且 `http://[fe80::...]` 里的链路本地地址还需要带 zone。
		ip4 := ipNet.IP.To4()
		if ip4 == nil || ip4.IsLinkLocalUnicast() {
			continue
		}
		url := "http://" + net.JoinHostPort(ip4.String(), strconv.Itoa(port))
		if seen[url] {
			continue
		}
		seen[url] = true
		out = append(out, url)
	}
	return out
}

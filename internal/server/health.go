package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"cloudtrace/internal/config"
	"cloudtrace/internal/health"
)

// 体检相关的命令与事件名。
const (
	cmdHealthCheck = "health/check"
	eventHealth    = "health"
)

// 体检的超时。
//
// 总超时管住整个检查：其中一项要真的去探测速源，没有上限的话，网络半死不活
// 时前端会一直停在「检查中」。
const (
	healthTimeout      = 8 * time.Second
	healthProbeTimeout = 5 * time.Second
)

// healthHandlers 返回体检相关的命令表。
func (s *server) healthHandlers() map[string]commandHandler {
	return map[string]commandHandler{
		cmdHealthCheck: s.handleHealthCheck,
	}
}

// handleHealthCheck 跑一遍配置体检并把结果回给调用方。
//
// 结果只回给发起方而不广播：体检是用户主动点出来的，把别人的面板也刷上一份
// 检查结果没有任何意义。
func (s *server) handleHealthCheck(c *wsConn, _ json.RawMessage) error {
	ctx, cancel := context.WithTimeout(context.Background(), healthTimeout)
	defer cancel()
	c.sendEvent(eventHealth, health.Check(ctx, s.cfg.Get(), s.healthOptions()))
	return nil
}

// healthOptions 收集体检需要的运行时输入。
func (s *server) healthOptions() health.Options {
	cfg := s.cfg.Get()
	opts := health.Options{
		// 本进程占着的端口不算冲突：占用者就是自己，报出来是纯误报。
		ListeningPort:   s.listenPort,
		PortInUse:       s.portInUse,
		DirWritable:     s.dirWritable,
		SourceReachable: s.sourceReachable,
	}
	if dir, err := s.cfg.DataDir(); err == nil {
		opts.DataDir = dir
		opts.ASNDBPath = resolveASNPath(dir, cfg.Geo.ASNDBPath)
	}
	return opts
}

// resolveASNPath 把配置里的 ASN 库位置解析成绝对路径。
//
// 相对路径按数据目录解释：库文件跟着数据目录一起搬才合理，按当前工作目录
// 解释会在「从别处启动」时突然找不到。默认值本身带着 data/ 前缀，先去掉
// 再拼，否则会变成 data/data/。
func resolveASNPath(dataDir, configured string) string {
	p := strings.TrimSpace(configured)
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		return p
	}
	p = filepath.ToSlash(p)
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "data/")
	return filepath.Join(dataDir, filepath.FromSlash(p))
}

// probeSpeedSource 探一次测速源是否可达。
//
// 只发一个 HEAD 请求，而且**任何 HTTP 响应都算可达**：这一项要回答的是
// 「这个源还活着吗」，不是「它有多快」，也不是「那个路径对不对」。用状态码
// 判定会把「不接受 HEAD」的源误报成不可达——那是个假故障，比漏报更费时间。
//
// 走配置里的代理：开了代理的用户如果这里直连，会看到一条假的「源不可达」。
func (s *server) probeSpeedSource(ctx context.Context) error {
	cfg := s.cfg.Get()
	target, err := s.speedSource.Resolve(ctx, cfg.Speed.URLMode, cfg.Speed.CustomURL)
	if err != nil {
		return err
	}
	if !strings.Contains(target, "://") {
		target = "https://" + target
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", cfg.Net.UserAgent)

	client := &http.Client{Timeout: healthProbeTimeout, Transport: healthTransport(cfg)}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

// healthTransport 按配置构造体检用的传输层。
func healthTransport(cfg config.Config) *http.Transport {
	tr, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Transport{}
	}
	out := tr.Clone()
	out.Proxy = nil
	if p := strings.TrimSpace(cfg.Net.Proxy); p != "" && !cfg.Net.ForceDirect {
		if u, err := url.Parse(p); err == nil {
			out.Proxy = http.ProxyURL(u)
		}
	}
	return out
}

package health

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"

	"cloudtrace/internal/config"
)

// 检查项标识。
const (
	KeyPort        = "port"
	KeyWorkers     = "workers"
	KeyLatency     = "latency_threshold"
	KeyDataDir     = "data_dir"
	KeyASNDB       = "asn_db"
	KeyProxyEnv    = "proxy_env"
	KeySpeedSource = "speed_source"
)

// 一键修复的目标值。
//
// 都取内置档位的口径而不是「刚好越过告警线」：修一次就该回到一个公认好用
// 的值，而不是停在临界点上、下次稍微一调又报警。
const (
	fixedWorkers = config.MaxWorkersPreset // 200
	fixedLatency = 200
)

// checkPort 检查面板端口是否被别的进程占用。
func checkPort(_ context.Context, cfg config.Config, opts Options) *Issue {
	if cfg.Server.Port == opts.ListeningPort {
		return nil // 占用者是自己
	}
	inUse := opts.PortInUse
	if inUse == nil {
		inUse = portInUse
	}
	if !inUse(cfg.Server.Port) {
		return nil
	}
	return &Issue{
		Key:        KeyPort,
		Level:      LevelError,
		Problem:    fmt.Sprintf("面板端口 %d 已被其他程序占用，启动后会打不开面板", cfg.Server.Port),
		Suggestion: "换一个端口，或者先结束占用该端口的程序",
		Field:      "server.port",
	}
}

// checkDataDir 检查数据目录能否写入。
//
// 目录写不进去意味着历史存不下来、配置改不动，是最该早发现的一类问题，
// 因此只有它能到 error 级别。
func checkDataDir(_ context.Context, _ config.Config, opts Options) *Issue {
	if opts.DataDir == "" {
		return skipIssue
	}
	probe := opts.DirWritable
	if probe == nil {
		probe = dirWritable
	}
	if err := probe(opts.DataDir); err == nil {
		return nil
	}
	return &Issue{
		Key:        KeyDataDir,
		Level:      LevelError,
		Problem:    fmt.Sprintf("数据目录 %s 无法写入，历史与配置都存不下来", opts.DataDir),
		Suggestion: "换一个数据目录，或检查该目录的读写权限",
		Field:      "data.dir",
	}
}

// checkWorkers 检查并发是否高到可能拖垮网络。
func checkWorkers(_ context.Context, cfg config.Config, _ Options) *Issue {
	if cfg.Scan.Workers <= config.WarnWorkers {
		return nil
	}
	return &Issue{
		Key:     KeyWorkers,
		Level:   LevelWarning,
		Problem: fmt.Sprintf("并发 %d 过高，可能导致断网或路由器过载", cfg.Scan.Workers),
		Suggestion: fmt.Sprintf("降到 %d 以内；想更快出结果可以提高延迟阈值，而不是一味加并发",
			config.MaxWorkersPreset),
		Field:   "scan.workers",
		Fixable: true,
		Fix:     &Fix{Key: "scan.workers", Value: fixedWorkers, Label: fmt.Sprintf("降到 %d", fixedWorkers)},
	}
}

// checkLatencyThreshold 检查延迟阈值是否低到几乎扫不出结果。
func checkLatencyThreshold(_ context.Context, cfg config.Config, _ Options) *Issue {
	if cfg.Scan.LatencyThreshold >= config.WarnLatency {
		return nil
	}
	return &Issue{
		Key:        KeyLatency,
		Level:      LevelWarning,
		Problem:    fmt.Sprintf("延迟阈值 %dms 过低，绝大多数节点都会被淘汰，可能一条结果都拿不到", cfg.Scan.LatencyThreshold),
		Suggestion: fmt.Sprintf("提高到 %d 左右", fixedLatency),
		Field:      "scan.latency_threshold",
		Fixable:    true,
		Fix:        &Fix{Key: "scan.latency_threshold", Value: fixedLatency, Label: fmt.Sprintf("提高到 %dms", fixedLatency)},
	}
}

// checkASNDB 检查 ASN 库是否存在。
func checkASNDB(_ context.Context, cfg config.Config, opts Options) *Issue {
	if cfg.Geo.ASNSource == "off" || opts.ASNDBPath == "" {
		return skipIssue
	}
	if _, err := os.Stat(opts.ASNDBPath); err == nil {
		return nil
	}
	auto := ""
	if cfg.Geo.ASNAutoUpdate {
		auto = "下次扫描时会自动下载；也可以"
	} else {
		auto = "自动更新已关闭；请"
	}
	return &Issue{
		Key:        KeyASNDB,
		Level:      LevelWarning,
		Problem:    fmt.Sprintf("ASN 库文件 %s 不存在，结果里看不到运营商归属", opts.ASNDBPath),
		Suggestion: auto + "手动放置库文件，或到设置里关闭 ASN 查询",
		Field:      "geo.asn_db_path",
	}
}

// checkProxyEnv 检查出口环境是否会干扰结果。
//
// 两条触发路径：出口地区不是中国内地（多半走了代理），或者用户显式配了
// 代理且没有强制直连。两者都只是「结果可能不准」，不是故障，因此是警告。
func checkProxyEnv(_ context.Context, cfg config.Config, opts Options) *Issue {
	if p := strings.TrimSpace(cfg.Net.Proxy); p != "" && !cfg.Net.ForceDirect {
		return &Issue{
			Key:        KeyProxyEnv,
			Level:      LevelWarning,
			Problem:    fmt.Sprintf("已配置代理 %s 且未强制直连，探测会经由代理，延迟与地区可能不准", p),
			Suggestion: "扫描时建议开启强制直连；若必须走代理，结果仅供参考",
			Field:      "net.proxy",
			Fixable:    true,
			Fix:        &Fix{Key: "net.force_direct", Value: true, Label: "开启强制直连"},
		}
	}

	loc := strings.ToUpper(strings.TrimSpace(opts.ExitCountry))
	if loc == "" {
		return skipIssue
	}
	if loc == "CN" {
		return nil
	}
	return &Issue{
		Key:        KeyProxyEnv,
		Level:      LevelWarning,
		Problem:    fmt.Sprintf("当前出口地区是 %s，看起来走的是代理，探测到的延迟与地区可能与实际不符", loc),
		Suggestion: "关闭代理后重新扫描，结果才代表你自己的网络",
	}
}

// checkSpeedSource 检查测速源是否可达。
func checkSpeedSource(ctx context.Context, _ config.Config, opts Options) *Issue {
	if opts.SourceReachable == nil {
		return skipIssue
	}
	if err := opts.SourceReachable(ctx); err == nil {
		return nil
	}
	return &Issue{
		Key:        KeySpeedSource,
		Level:      LevelWarning,
		Problem:    "测速源当前不可达，测速会拿不到速度",
		Suggestion: "换一个测速源，或过一会儿再试（多数是临时限流）",
		Field:      "speed.url_mode",
	}
}

// portInUse 尝试绑定端口来判断是否被占用。
//
// 绑定成功立刻关闭：这一下只是问一句「能不能占」，不是真的要监听。端口
// 刚被释放时会有短暂不可用的窗口，但体检是给用户看的提示，误报一次的成本
// 远低于漏报。
func portInUse(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return true
	}
	_ = ln.Close()
	return false
}

// dirWritable 通过写一个临时文件来验证目录可写。
func dirWritable(dir string) error {
	path := filepath.Join(dir, ".cloudtrace-write-test")
	f, err := os.Create(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("目录不存在：%w", err)
		}
		return err
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Remove(name)
}

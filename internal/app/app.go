package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"cloudtrace/internal/config"
	"cloudtrace/internal/event"
	"cloudtrace/internal/geo"
	"cloudtrace/internal/history"
	"cloudtrace/internal/model"
	"cloudtrace/internal/task"
)

// 事件 topic。
const (
	// TopicState 任务状态快照变更（全量下发）。
	TopicState = "state"
)

// Services 是应用的依赖容器。
//
// 当前承载配置、事件总线与任务编排；探测、测速、地理、历史等业务服务
// 随后续功能陆续接入这里。
type Services struct {
	// Config 是配置的唯一数据源。
	Config *config.Store
	// Bus 是全局事件总线。
	Bus *event.Bus
	// Tasks 是任务编排器，也是任务状态的唯一来源。
	Tasks *task.Manager
	// History 是历史记录的存储核心。
	History *history.Store
	// Presets 是自定义档位的存储核心。内置档位由代码定义，不在这里。
	Presets *config.PresetStore
	// Geo 是 ASN 库与归属地信息的入口。
	Geo *geo.Manager
	// Region 是扫描过程中顺手学到的归属地缓存。
	Region *geo.InfoCache
	// Version 是构建版本号。
	Version string
	// Logger 是结构化日志器。
	Logger *slog.Logger

	// cancel 结束编排器的父 context：进程退出时用它把在跑的任务一起停掉。
	cancel context.CancelFunc

	mu      sync.RWMutex
	started bool
	startAt time.Time
}

// New 装配应用服务。
func New(cfg *config.Store, version string, logger *slog.Logger) (*Services, error) {
	if cfg == nil {
		return nil, errors.New("app: Config 不能为空")
	}
	if logger == nil {
		logger = slog.Default()
	}

	bus := event.New(event.DefaultQueueSize)
	// 编排器的父 context 跟进程生命周期绑定：退出时在跑的任务要能收到
	// 取消信号，否则进程会等到它自己跑完才退。
	baseCtx, cancel := context.WithCancel(context.Background())
	tasks, err := task.NewManager(bus, logger, task.Options{BaseContext: baseCtx})
	if err != nil {
		cancel()
		return nil, err
	}

	hist, err := newHistory(cfg, bus, logger)
	if err != nil {
		cancel()
		return nil, err
	}

	region, geoMgr := newGeo(cfg, logger)
	presets := newPresets(cfg, logger)

	return &Services{
		Config:  cfg,
		Bus:     bus,
		Tasks:   tasks,
		History: hist,
		Presets: presets,
		Geo:     geoMgr,
		Region:  region,
		Version: version,
		Logger:  logger,
		cancel:  cancel,
	}, nil
}

/**
 * newPresets 装配自定义档位库。
 *
 * 与历史同样的取舍：数据目录解析不出来时退化成「只有内置档位」，不阻断启动。
 * 档位是辅助功能，缺了它用户只是不能另存档位，三个内置档位照样能用。
 */
func newPresets(cfg *config.Store, logger *slog.Logger) *config.PresetStore {
	dataDir, err := cfg.DataDir()
	if err != nil {
		logger.Warn("解析数据目录失败，自定义档位本次不可用", "err", err)
		return nil
	}
	store, err := config.OpenPresets(config.PresetsPath(dataDir), logger)
	if err != nil {
		logger.Warn("自定义档位库打不开，本次只有内置档位可用", "err", err)
		return nil
	}
	for _, w := range store.Warnings() {
		logger.Warn("档位库：" + w)
	}
	return store
}

// newGeo 装配 ASN 库与归属地缓存。
//
// 数据目录解析不出来时两者都退化成不可用：ASN 归属是锦上添花，不能因为它
// 让整个程序起不来。缓存不落盘，但仍然能在本次运行里省下重复查询。
func newGeo(cfg *config.Store, logger *slog.Logger) (*geo.InfoCache, *geo.Manager) {
	dataDir, err := cfg.DataDir()
	if err != nil {
		logger.Warn("解析数据目录失败，ASN 查询与归属地缓存本次不可用", "err", err)
		return geo.NewInfoCache("", 0), geo.NewManager(geo.Options{
			Config: func() config.GeoConfig { return cfg.Get().Geo },
			Logger: logger,
		})
	}

	cache := geo.NewInfoCache(filepath.Join(config.CacheDir(dataDir), "ipinfo.json"), 0)
	return cache, geo.NewManager(geo.Options{
		Config:  func() config.GeoConfig { return cfg.Get().Geo },
		DataDir: dataDir,
		Cache:   cache,
		Logger:  logger,
	})
}

// newHistory 装配历史存储。
//
// 数据目录解析失败时不阻断启动：历史用不了总比整个程序起不来强，缺目录
// 这件事会在日志里点名。
func newHistory(cfg *config.Store, bus *event.Bus, logger *slog.Logger) (*history.Store, error) {
	dataDir, err := cfg.DataDir()
	if err != nil {
		return nil, fmt.Errorf("app: 解析数据目录失败：%w", err)
	}
	return history.New(history.Options{
		Dir: config.HistoryDir(dataDir),
		// 现取配置而不是拷一份：用户把保留份数从 20 改成 3，下一次存档
		// 就该按 3 清理。
		Config: func() config.HistoryConfig { return cfg.Get().History },
		Logger: logger,
		OnChanged: func(id, action string) {
			bus.Publish(history.TopicChanged, history.Change{ID: id, Action: action})
		},
	})
}

// Startup 启动服务：记录启动时间并把空闲态广播出去。
//
// 后台任务（ASN 库更新、旧数据迁移检测等）也在这里挂载，
// 但它们一律不得阻塞启动。
func (s *Services) Startup(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	s.started = true
	s.startAt = time.Now()
	s.mu.Unlock()

	s.Logger.Info("服务已启动", "version", s.Version)
	s.Bus.Publish(TopicState, s.Snapshot())
	s.startGeo(ctx)
	return nil
}

// startGeo 在后台加载 ASN 库并探测本机出口地区。
//
// 一律不阻塞启动：库文件是几兆到十几兆的下载，出口探测要发网络请求，任何
// 一项都能让启动慢上十几秒。它们只是让结果里多两列信息，用户不该为这个等。
func (s *Services) startGeo(ctx context.Context) {
	if s.Geo == nil {
		return
	}
	s.Region.Load()

	go func() {
		// 库缺失时先下载再加载；这一整套失败都只记状态与日志，不影响任务。
		s.Geo.Ensure(ctx)
		s.Geo.CheckExit(ctx)
	}()
}

// Shutdown 优雅退出：先停任务，再关闭事件总线并等待订阅者 goroutine 结束。
//
// 顺序不能颠倒：任务结束时还要往外发终止事件，总线先关掉的话那条事件
// 就丢了，前端会一直停在「运行中」。
func (s *Services) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.started = false
	s.mu.Unlock()

	if s.cancel != nil {
		s.cancel()
	}
	if s.Tasks != nil {
		s.waitIdle(ctx)
	}

	if s.History != nil {
		// 关闭历史存储会把还挂在撤销窗口里的删除落定。放在关总线之前：
		// 落定过程会发 history/changed，总线已经关掉的话那条事件就丢了。
		if err := s.History.Close(); err != nil {
			s.Logger.Warn("关闭历史存储失败", "err", err)
		}
	}

	if s.Geo != nil {
		// 缓存里是本次运行学到的归属地，攒一次落盘就够了；不落盘只是下次
		// 要重新探测一遍，没有正确性问题。
		if err := s.Geo.SaveCache(); err != nil {
			s.Logger.Warn("保存归属地缓存失败", "err", err)
		}
		if err := s.Geo.Close(); err != nil {
			s.Logger.Warn("关闭 ASN 库失败", "err", err)
		}
	}

	if s.Bus != nil {
		done := make(chan struct{})
		go func() {
			s.Bus.Close()
			close(done)
		}()
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.Logger.Info("服务已停止")
	return nil
}

// shutdownWait 是等待在跑任务收尾的上限。
//
// 设上限而不是一直等：退出流程不能被一个卡住的网络请求无限期拖住。
const shutdownWait = 5 * time.Second

// waitIdle 等任务编排器退出运行态，最多等 shutdownWait，或 ctx 先到期。
func (s *Services) waitIdle(ctx context.Context) {
	deadline := time.Now().Add(shutdownWait)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()

	for s.Tasks.Running() && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Snapshot 返回当前任务状态快照。
//
// 这是 WS `state` 事件的数据来源：首连与断线重连时全量下发，
// 前端据此恢复完整视图。快照由任务编排器提供，它是任务状态的唯一来源；
// 已耗时与 ETA 在取快照时现算，所以重连后计时器不会停在旧值上。
func (s *Services) Snapshot() model.TaskState {
	if s.Tasks == nil {
		return model.IdleState()
	}
	return s.Tasks.Snapshot()
}

// Started 报告服务是否已启动。
func (s *Services) Started() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.started
}

// Uptime 返回服务已运行时长；未启动时返回 0。
func (s *Services) Uptime() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.started {
		return 0
	}
	return time.Since(s.startAt)
}

// DataDir 返回解析后的数据目录。
func (s *Services) DataDir() (string, error) {
	return s.Config.DataDir()
}

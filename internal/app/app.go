package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"cloudtrace/internal/config"
	"cloudtrace/internal/event"
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

	return &Services{
		Config:  cfg,
		Bus:     bus,
		Tasks:   tasks,
		Version: version,
		Logger:  logger,
		cancel:  cancel,
	}, nil
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
	return nil
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

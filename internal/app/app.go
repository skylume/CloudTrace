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
)

// 事件 topic。
const (
	// TopicState 任务状态快照变更（全量下发）。
	TopicState = "state"
)

// Services 是应用的依赖容器。
//
// 当前承载配置、事件总线与任务状态快照；探测、扫描、测速、地理、
// 历史等业务服务随后续功能陆续接入这里。
type Services struct {
	// Config 是配置的唯一数据源。
	Config *config.Store
	// Bus 是全局事件总线。
	Bus *event.Bus
	// Version 是构建版本号。
	Version string
	// Logger 是结构化日志器。
	Logger *slog.Logger

	mu      sync.RWMutex
	state   model.TaskState
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
	return &Services{
		Config:  cfg,
		Bus:     event.New(event.DefaultQueueSize),
		Version: version,
		Logger:  logger,
		state:   model.IdleState(),
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
	st := s.state
	s.mu.Unlock()

	s.Logger.Info("服务已启动", "version", s.Version)
	s.Bus.Publish(TopicState, st)
	return nil
}

// Shutdown 优雅退出：关闭事件总线并等待订阅者 goroutine 结束。
func (s *Services) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.started = false
	s.mu.Unlock()

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

// Snapshot 返回当前任务状态快照。
//
// 这是 WS `state` 事件的数据来源：首连与断线重连时全量下发，
// 前端据此恢复完整视图。
func (s *Services) Snapshot() model.TaskState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// SetState 更新任务状态并广播 state 事件。
//
// 当前只提供这条最基础的通道；任务状态机接管后，本方法仍是
// 「状态变更 → 事件总线」的唯一出口。
func (s *Services) SetState(st model.TaskState) {
	s.mu.Lock()
	s.state = st
	s.mu.Unlock()

	s.Bus.Publish(TopicState, st)
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

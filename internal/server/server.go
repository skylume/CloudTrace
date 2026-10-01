package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"cloudtrace/internal/app"
	"cloudtrace/internal/config"
	"cloudtrace/internal/model"
)

// StateProvider 提供任务状态快照。
//
// 由 app.Services 实现；任务状态机接管后仍由它对外提供同一接口。
type StateProvider interface {
	Snapshot() model.TaskState
}

// server 是 http.Handler 的实现。
type server struct {
	cfg    *config.Store
	svc    *app.Services
	logger *slog.Logger
	auth   *authStore
	hub    *wsHub
	static *staticHandler
}

// New 构造唯一的前后端入口 handler（静态资源 + REST + WebSocket）。
//
// 面板版把它交给 http.ListenAndServe；桌面版把它同时交给 Wails 的
// AssetServer 与一个本地监听，从而保证两个发行版行为完全一致。
func New(cfg *config.Store, svc *app.Services) (http.Handler, error) {
	if cfg == nil {
		return nil, errors.New("server: 配置不能为空")
	}
	if svc == nil {
		return nil, errors.New("server: 服务容器不能为空")
	}

	static, err := newStaticHandler()
	if err != nil {
		return nil, fmt.Errorf("server: 加载内嵌前端产物失败：%w", err)
	}

	s := &server{
		cfg:    cfg,
		svc:    svc,
		logger: svc.Logger,
		auth:   newAuthStore(time.Duration(cfg.Get().Server.SessionTTLMin) * time.Minute),
		hub:    newWSHub(svc.Logger),
		static: static,
	}

	// 任务状态变更 → 广播给所有 WS 连接。
	// 订阅的取消由服务关闭时的总线关闭统一完成，这里无需另行保存。
	if _, err := svc.Bus.Subscribe(app.TopicState, s.onStateChanged); err != nil {
		return nil, fmt.Errorf("server: 订阅状态事件失败：%w", err)
	}

	return s.routes(), nil
}

// onStateChanged 把状态快照广播给所有连接。
func (s *server) onStateChanged(payload any) {
	st, ok := payload.(model.TaskState)
	if !ok {
		s.logger.Warn("state 事件的载荷类型不符，已忽略")
		return
	}
	s.hub.broadcast(eventState, st)
}

// writeJSON 输出 JSON 响应。
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeError 输出带错误码的 JSON 错误响应。
func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, errorPayload{Code: code, Msg: msg})
}

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"cloudtrace/internal/app"
	"cloudtrace/internal/config"
	"cloudtrace/internal/model"
	"cloudtrace/internal/speed"
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

	// speedSource 解析测速源。它必须跨任务复用：出口 ISP 探测结果按十分钟
	// 缓存，每次测速新建一个解析器等于每次都要重新探测。
	speedSource *speed.SourceResolver

	// downloads 保存待下载的导出物。
	downloads *downloadStore

	// listenPort 是本进程实际监听的端口。
	//
	// 体检要拿它把「自己占着自己的端口」排除掉，重启提示也要拿它判断端口
	// 是否被改过。为 0 表示未知。
	listenPort int
	// startup 是启动时的配置快照，用于判断哪些改动要重启才生效。
	startup config.Config

	// 体检用的可注入探测函数，为 nil 时由 health 包做真实探测。
	portInUse       func(int) bool
	dirWritable     func(string) error
	sourceReachable func(context.Context) error
}

// New 构造唯一的前后端入口 handler（静态资源 + REST + WebSocket）。
//
// listenPort 是本进程即将监听的端口，只用于体检与重启提示：面板跑着的
// 时候那个端口当然是被占用的，占用者就是自己，不排除掉就必然误报冲突。
//
// 面板版把它交给 http.ListenAndServe；桌面版把它同时交给 Wails 的
// AssetServer 与一个本地监听，从而保证两个发行版行为完全一致。
func New(cfg *config.Store, svc *app.Services, listenPort int) (http.Handler, error) {
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
		cfg:         cfg,
		svc:         svc,
		logger:      svc.Logger,
		auth:        newAuthStore(time.Duration(cfg.Get().Server.SessionTTLMin) * time.Minute),
		hub:         newWSHub(svc.Logger),
		static:      static,
		speedSource: speed.NewSourceResolver(nil, time.Now, speed.DefaultSourceTTL),
		downloads:   newDownloadStore(),
		listenPort:  listenPort,
		startup:     cfg.Get(),
	}
	s.speedSource.SetLogger(func(format string, args ...any) {
		svc.Logger.Info(fmt.Sprintf(format, args...))
	})
	// 本地 ASN 库能补全出口的 AS 信息，让「是不是中国移动」的判定不依赖
	// 出口探测接口是否还返回那两个字段。库不可用时留空，回退路径照常工作。
	if svc.Geo != nil {
		s.speedSource.SetASNLookup(svc.Geo.LookupFunc())
	}
	s.sourceReachable = s.probeSpeedSource

	// 配置一旦落盘就广播给所有连接（包括发起方）：前端拿全量配置整体替换
	// 本地状态，两端因此永远一致，不存在谁覆盖谁的问题。
	cfg.OnSaved(func(config.Config) {
		s.hub.broadcast(eventSettings, s.settingsPayload())
	})

	// 任务状态与任务事件 → 广播给所有 WS 连接。
	// 订阅的取消由服务关闭时的总线关闭统一完成，这里无需另行保存。
	for _, sub := range s.taskSubscriptions() {
		if _, err := svc.Bus.Subscribe(sub.topic, sub.fn); err != nil {
			return nil, fmt.Errorf("server: 订阅 %s 事件失败：%w", sub.topic, err)
		}
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

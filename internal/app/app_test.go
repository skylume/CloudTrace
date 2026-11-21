package app

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"cloudtrace/internal/config"
	"cloudtrace/internal/model"
	"cloudtrace/internal/task"
)

// openTestStore 构造一份指向独立临时目录的配置存储。
//
// Geo.ASNSource 置 off：Startup 挂载的后台 goroutine 不会发任何网络请求，
// 测试就能在完全离线、确定性的前提下跑。
func openTestStore(t *testing.T) *config.Store {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Data.Dir = dir
	cfg.Geo.ASNSource = "off"
	if err := config.SaveFile(config.ConfigPath(dir), cfg); err != nil {
		t.Fatalf("保存测试配置失败：%v", err)
	}
	store, err := config.OpenStore(config.ConfigPath(dir), "")
	if err != nil {
		t.Fatalf("打开测试配置失败：%v", err)
	}
	return store
}

func TestNewRequiresConfig(t *testing.T) {
	if _, err := New(nil, "test", slog.Default()); err == nil {
		t.Fatal("传 nil 配置应当报错")
	}
}

// 数据目录解析不出来时（便携模式 + exeDir 为空）历史存储装不上，
// 装配必须失败而不是带着残缺依赖继续跑。
func TestNewFailsWhenDataDirUnresolvable(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Data.Dir = ""
	cfg.Data.Portable = true
	if err := config.SaveFile(config.ConfigPath(dir), cfg); err != nil {
		t.Fatalf("保存测试配置失败：%v", err)
	}
	// exeDir 传空，便携模式解析不出数据目录 → newHistory 报错。
	store, err := config.OpenStore(config.ConfigPath(dir), "")
	if err != nil {
		t.Fatalf("打开测试配置失败：%v", err)
	}
	if _, err := New(store, "test", slog.Default()); err == nil {
		t.Fatal("数据目录解析失败时装配应当报错")
	}
}

// 空 logger 不报错：装配内部退回 slog.Default()。
func TestNewAcceptsNilLogger(t *testing.T) {
	svc, err := New(openTestStore(t), "test", nil)
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	if svc.Logger == nil {
		t.Error("nil logger 未退回默认")
	}
	t.Cleanup(func() { _ = svc.Shutdown(context.Background()) })
}

// 装配成功时依赖容器齐全，数据目录与配置口径一致。
func TestNewAssemblesDependencies(t *testing.T) {
	store := openTestStore(t)
	svc, err := New(store, "1.2.3", slog.Default())
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	t.Cleanup(func() { _ = svc.Shutdown(context.Background()) })

	if svc.Config != store {
		t.Error("配置指针没有透传")
	}
	if svc.Bus == nil || svc.Tasks == nil || svc.History == nil || svc.Geo == nil {
		t.Error("依赖容器缺件")
	}
	if svc.Version != "1.2.3" {
		t.Errorf("版本 = %q", svc.Version)
	}
	got, err := svc.DataDir()
	if err != nil {
		t.Fatalf("解析数据目录失败：%v", err)
	}
	want, _ := store.DataDir()
	if got != want {
		t.Errorf("DataDir = %q，期望 %q", got, want)
	}
}

func TestStartupPublishesIdleState(t *testing.T) {
	svc, err := New(openTestStore(t), "test", slog.Default())
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	t.Cleanup(func() { _ = svc.Shutdown(context.Background()) })

	ch := make(chan any, 1)
	unsub, err := svc.Bus.Subscribe(TopicState, func(payload any) {
		select {
		case ch <- payload:
		default:
		}
	})
	if err != nil {
		t.Fatalf("订阅状态事件失败：%v", err)
	}
	defer unsub()

	if err := svc.Startup(context.Background()); err != nil {
		t.Fatalf("启动失败：%v", err)
	}
	select {
	case payload := <-ch:
		st, ok := payload.(model.TaskState)
		if !ok {
			t.Fatalf("载荷类型 = %T", payload)
		}
		if st.Status != model.StatusIdle {
			t.Errorf("启动态 status = %q，期望 idle", st.Status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("没有收到空闲态广播")
	}
}

// 已取消的上下文直接返回错误，不该把服务标成已启动。
func TestStartupRejectsCanceledContext(t *testing.T) {
	svc, err := New(openTestStore(t), "test", slog.Default())
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	t.Cleanup(func() { _ = svc.Shutdown(context.Background()) })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := svc.Startup(ctx); err == nil {
		t.Fatal("已取消的上下文应当让启动失败")
	}
	if svc.Uptime() != 0 {
		t.Error("启动失败后不应记录运行时长")
	}
}

// Uptime 在启动前是 0，启动后随真实时间增长。
func TestUptimeGrowsAfterStartup(t *testing.T) {
	svc, err := New(openTestStore(t), "test", slog.Default())
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	t.Cleanup(func() { _ = svc.Shutdown(context.Background()) })

	if uptime := svc.Uptime(); uptime != 0 {
		t.Errorf("未启动时 Uptime = %v，期望 0", uptime)
	}
	if err := svc.Startup(context.Background()); err != nil {
		t.Fatalf("启动失败：%v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if uptime := svc.Uptime(); uptime <= 0 {
		t.Errorf("启动后 Uptime = %v，应当为正", uptime)
	}
}

// 空闲时快照是空闲态；有任务在跑时快照带上进度字段。
func TestSnapshotReflectsIdleAndRunning(t *testing.T) {
	svc, err := New(openTestStore(t), "test", slog.Default())
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	t.Cleanup(func() { _ = svc.Shutdown(context.Background()) })

	idle := svc.Snapshot()
	if idle.Status != model.StatusIdle {
		t.Errorf("空闲快照 status = %q", idle.Status)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	if err := svc.Tasks.Start(model.PhaseScan, func(task.Reporter) (task.Outcome, error) {
		close(started)
		<-release
		return task.Outcome{}, nil
	}); err != nil {
		t.Fatalf("启动任务失败：%v", err)
	}
	<-started

	running := svc.Snapshot()
	if running.Status != model.StatusRunning {
		t.Errorf("运行中快照 status = %q", running.Status)
	}
	close(release)
}

// Shutdown 幂等：退出过一次之后再退一次也不报错。
func TestShutdownIsIdempotent(t *testing.T) {
	svc, err := New(openTestStore(t), "test", slog.Default())
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	if err := svc.Startup(context.Background()); err != nil {
		t.Fatalf("启动失败：%v", err)
	}
	if err := svc.Shutdown(context.Background()); err != nil {
		t.Fatalf("第一次退出失败：%v", err)
	}
	if err := svc.Shutdown(context.Background()); err != nil {
		t.Fatalf("第二次退出失败：%v", err)
	}
}

// 退出时旧订阅者不再收到事件：总线已停止投递，发布不再有效。
func TestShutdownClosesBus(t *testing.T) {
	svc, err := New(openTestStore(t), "test", slog.Default())
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	if err := svc.Startup(context.Background()); err != nil {
		t.Fatalf("启动失败：%v", err)
	}

	ch := make(chan any, 1)
	_, err = svc.Bus.Subscribe(TopicState, func(payload any) {
		select {
		case ch <- payload:
		default:
		}
	})
	if err != nil {
		t.Fatalf("订阅失败：%v", err)
	}

	if err := svc.Shutdown(context.Background()); err != nil {
		t.Fatalf("退出失败：%v", err)
	}
	svc.Bus.Publish(TopicState, "after-close")
	select {
	case p := <-ch:
		t.Fatalf("退出后旧订阅者仍收到事件：%v", p)
	case <-time.After(100 * time.Millisecond):
	}
}

// 退出带已取消的上下文时，退出流程不能因此卡住或报错。
func TestShutdownWithCanceledContext(t *testing.T) {
	svc, err := New(openTestStore(t), "test", slog.Default())
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	if err := svc.Startup(context.Background()); err != nil {
		t.Fatalf("启动失败：%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = svc.Shutdown(ctx)
}

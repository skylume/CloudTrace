package platform

// Lock 是单实例锁的句柄。
//
// 拿到锁的进程负责在退出时释放；进程被强杀时由操作系统回收，不会留下一个
// 再也拿不到的锁——这正是用系统级锁而不是「锁文件」的原因：锁文件在崩溃后
// 会一直留着，用户只能手动去删，而他会以为程序坏了。
type Lock struct {
	name string
	// handle 是平台相关的底层句柄；为 0 表示没有真的持有任何东西。
	handle uintptr
}

// SingleInstanceName 是单实例锁的名字。
//
// 不带 `Global\` 前缀：那个命名空间要求进程持有创建全局对象的权限，普通用户
// 运行时会直接失败——而失败的表现是「单实例保护莫名其妙不生效」。默认的
// `Local\`（当前登录会话）已经覆盖了真实场景：同一个人不会在两个会话里各开一份。
//
// 两个发行版共用同一个名字，因为它们写的是同一份配置与历史，同时跑两份会撞车。
const SingleInstanceName = "CloudTrace.SingleInstance"

// Name 返回锁的名字，便于日志里说清是哪一个实例。
func (l *Lock) Name() string { return l.name }

// AcquireLock 尝试取得名为 name 的单实例锁。
//
// 第二个返回值表示「本次是第一个实例」。**拿不到锁不算错误**：正常启动第二个
// 实例时就是拿不到，此时调用方应当把已有实例的窗口唤到前面，然后安静退出。
// 真的出错（权限、系统调用失败）才返回 error，那种情况下应当照常启动——
// 宁可多开一个实例，也不要因为一个辅助能力让程序起不来。
func AcquireLock(name string) (*Lock, bool, error) {
	return acquireLock(name)
}

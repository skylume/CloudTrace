//go:build !windows

package platform

// acquireLock 在非 Windows 平台上不做任何事，一律当作「第一个实例」。
//
// 这个产品只发布 Windows 版本，其它平台上的构建是为了让开发和 CI 能跑起来
// （`go build ./...` 与 `go vet ./...` 不该因为一个辅助能力失败）。在这里
// 假装成功是安全的：拿不到锁最多是多开一个实例，而阻塞启动是更糟的结果。
func acquireLock(name string) (*Lock, bool, error) {
	return &Lock{name: name}, true, nil
}

// Release 在非 Windows 平台上没有东西要释放。
func (l *Lock) Release() {}

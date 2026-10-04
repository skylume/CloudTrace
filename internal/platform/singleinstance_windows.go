//go:build windows

package platform

import (
	"errors"

	"golang.org/x/sys/windows"
)

// acquireLock 用命名互斥体实现单实例。
//
// 互斥体由内核持有：进程无论怎么退出（正常、崩溃、被任务管理器结束），句柄
// 一关就自动释放。锁文件做不到这一点——崩溃后留下一个死文件，用户只能手动删，
// 而他会以为程序坏了。
func acquireLock(name string) (*Lock, bool, error) {
	handle, err := windows.CreateMutex(nil, false, windows.StringToUTF16Ptr(name))
	switch {
	case errors.Is(err, windows.ERROR_ALREADY_EXISTS):
		// 已经有实例拿着它了。句柄要关掉，否则这次调用自己也留了一份引用。
		if handle != 0 {
			_ = windows.CloseHandle(handle)
		}
		return &Lock{name: name}, false, nil
	case err != nil:
		return nil, false, err
	}
	return &Lock{name: name, handle: uintptr(handle)}, true, nil
}

// Release 释放锁。
//
// 重复调用是安全的：句柄清零之后什么也不做。
func (l *Lock) Release() {
	if l == nil || l.handle == 0 {
		return
	}
	_ = windows.CloseHandle(windows.Handle(l.handle))
	l.handle = 0
}

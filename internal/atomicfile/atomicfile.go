// Package atomicfile 提供「要么完整可见、要么完全不存在」的文件写入。
//
// 崩溃在写文件中途是常态（断电、被杀进程、磁盘满），半截 JSON 会让下次
// 启动直接读不出来。这里把「临时文件 + fsync + 改名覆盖」收成一处实现，
// 配置文件、历史索引、历史记录都走它，避免每个调用方各写一遍改名兜底。
package atomicfile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Write 把 data 原子地写到 path。
//
// 流程：同目录建临时文件 → 写入 → fsync → 关闭 → 改名覆盖。
// 临时文件与目标同目录是必须的：跨目录改名不保证原子。
//
// fsync 不能省。只调 Write 的话数据还在页缓存里，此时断电会留下一个
// 长度为 0 的目标文件——比没有文件更糟，因为调用方会以为它有效。
func Write(path string, data []byte, perm fs.FileMode) (err error) {
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	// 只在失败时清理临时文件；成功改名后 tmp 已不存在。
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()

	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	// 临时文件默认权限受 umask 影响，显式设一次，避免出现「权限比预期宽」的窗口。
	if err = os.Chmod(tmp, perm); err != nil {
		return err
	}
	return Rename(tmp, path)
}

// Rename 以覆盖语义把 oldpath 改名为 newpath。
//
// Windows 上 os.Rename 在目标已存在时直接失败（POSIX 语义是静默覆盖），
// 所以先删目标再改名。这两步之间不是原子的，但目标文件在改名成功之前
// 始终是完整的旧内容或不存在，不会出现半截状态。
func Rename(oldpath, newpath string) error {
	if err := os.Rename(oldpath, newpath); err == nil {
		return nil
	}

	// 只有在**确认源存在**之后才去删目标：失败原因可能是源不存在、权限不足、
	// 盘满，而「为了覆盖一个根本不存在的源把目标删掉」是不可逆的数据丢失。
	if _, err := os.Stat(oldpath); err != nil {
		return err
	}
	if err := os.Remove(newpath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.Rename(oldpath, newpath)
}

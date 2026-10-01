package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// filePerm 是配置文件权限：仅当前用户可读写。
const filePerm fs.FileMode = 0o600

// CorruptedError 表示配置文件无法解析。
//
// 遇到它时调用方应当**继续启动**：默认值已回退、原文件已备份，
// 只需把它当作警告展示给用户即可。
type CorruptedError struct {
	Path   string
	Backup string
	Reason string
}

func (e *CorruptedError) Error() string {
	return fmt.Sprintf("配置文件 %s 无法解析（%s），已回退默认值并把原文件备份为 %s", e.Path, e.Reason, e.Backup)
}

// LoadFile 从 path 读取配置。
//
// 语义：
//   - 文件不存在 → 返回默认配置 + nil（首次运行：不设置就能跑）；
//   - 内容损坏   → 备份原文件 + 返回默认配置 + *CorruptedError（**不阻断启动**）；
//   - 其他 IO 错误 → 原样返回。
func LoadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Default(), nil
		}
		return Default(), err
	}

	cfg := Default()
	if err := json.Unmarshal(data, &cfg); err != nil {
		backup, berr := backupBroken(path)
		if berr != nil {
			return Default(), &CorruptedError{Path: path, Backup: "", Reason: err.Error()}
		}
		return Default(), &CorruptedError{Path: path, Backup: backup, Reason: err.Error()}
	}
	cfg.normalize()
	return cfg, nil
}

// SaveFile 以原子写方式把配置写到 path。
//
// 原子写流程：同目录临时文件 → 写入 → fsync → 关闭 → rename 覆盖。
// ⚠️ Windows 上 os.Rename 无法覆盖已存在的文件，因此失败时先删除目标再重试。
func SaveFile(path string, cfg Config) error {
	n := cfg.Clone()
	n.normalize()
	data, err := json.MarshalIndent(n, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileAtomic(path, data, filePerm)
}

// writeFileAtomic 是原子写的唯一实现，配置与档位等文件共用。
func writeFileAtomic(path string, data []byte, perm fs.FileMode) (err error) {
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	// 只在失败时清理临时文件；成功 rename 后 tmp 已不存在。
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
	if err = os.Chmod(tmp, perm); err != nil {
		return err
	}

	if err = os.Rename(tmp, path); err != nil {
		// Windows：目标已存在时 rename 会失败，先移除再重试。
		if rmErr := os.Remove(path); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			return err
		}
		if err = os.Rename(tmp, path); err != nil {
			return err
		}
	}
	return nil
}

// backupBroken 把无法解析的文件重命名为 <name>.broken-<ts>。
func backupBroken(path string) (string, error) {
	ts := time.Now().Format("20060102_150405")
	backup := fmt.Sprintf("%s.broken-%s", path, ts)
	if err := os.Rename(path, backup); err != nil {
		return "", err
	}
	return backup, nil
}

// Package migrate 把旧版（Python 版）的数据搬到新版。
//
// 迁移只做三件事：找到旧数据、把能映射的搬过来、把结果如实报出来。几条硬性
// 约束都来自「用户的数据只有一份」这个前提：
//
//   - **复制而非移动**：旧目录原样留着。用户看完新界面觉得不合适，旧版本还能开。
//   - **先备份再写**：写新版之前把旧目录整个复制到备份区，出问题能翻回去。
//   - **幂等**：迁移记录用旧文件名做 ID，重复执行是覆盖而不是再导入一份；
//     另有一份标记文件挡住「跑第二遍」这种最常见的误操作。
//   - **不阻断启动**：迁移失败不该让程序起不来——旧数据还在原地，用户随时能重来。
package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cloudtrace/internal/config"
	"cloudtrace/internal/history"
	"cloudtrace/internal/model"
)

// 旧版的数据位置与命名。
const (
	legacySettingsFile = "settings.json"
	legacyHistoryDir   = "CloudTrace_history"
	// legacyPort 是旧版早期的默认端口。统一到新端口，并在报告里说明。
	legacyPort = 18543
)

// Legacy 描述一份检测到的旧数据。
type Legacy struct {
	// Dir 是旧数据所在目录。
	Dir string
	// SettingsPath 是旧 settings.json；不存在时为空。
	SettingsPath string
	// HistoryDir 是旧历史目录；不存在时为空。
	HistoryDir string
	// HistoryFiles 是旧历史文件的路径，已排序。
	HistoryFiles []string
}

// Found 报告是否检测到任何旧数据。
func (l Legacy) Found() bool {
	return l.SettingsPath != "" || len(l.HistoryFiles) > 0
}

// Detect 在 dir 下寻找旧版数据。
//
// 只看这两个位置：旧版本是便携的，数据就在程序目录下。满盘搜是另一回事——
// 用户可能在别处放着一份备份，把那份导进来比不导入更糟。
func Detect(dir string) Legacy {
	out := Legacy{Dir: dir}

	settings := filepath.Join(dir, legacySettingsFile)
	if info, err := os.Stat(settings); err == nil && !info.IsDir() {
		out.SettingsPath = settings
	}

	historyDir := filepath.Join(dir, legacyHistoryDir)
	entries, err := os.ReadDir(historyDir)
	if err != nil {
		return out
	}
	out.HistoryDir = historyDir
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		// `*_latest.json` 是「最新一份」的快捷方式，与带时间戳的那份是同一批
		// 数据。导入两次会让历史里出现一对一模一样的记录。
		if strings.HasSuffix(entry.Name(), "_latest.json") {
			continue
		}
		out.HistoryFiles = append(out.HistoryFiles, filepath.Join(historyDir, entry.Name()))
	}
	sort.Strings(out.HistoryFiles)
	return out
}

// Plan 是一次迁移会做什么，供用户确认后再执行。
type Plan struct {
	// Patch 是映射出来的配置补丁（嵌套，形状与设置补丁一致）。
	Patch map[string]any
	// Origins 是参数来源标记。迁移过来的值一律标成 user。
	Origins model.ParamOrigins
	// Histories 是待导入的历史记录。
	Histories []history.HistoryRecord
	// Notes 是要告诉用户的调整与取舍。
	Notes []string
	// Skipped 是认不出来、或新版没有对应项的旧键。
	Skipped []string
}

// Build 读旧数据并算出迁移计划。**它不写任何东西**。
func Build(legacy Legacy) (Plan, error) {
	plan := Plan{Patch: map[string]any{}, Origins: model.ParamOrigins{}}

	if legacy.SettingsPath != "" {
		if err := buildSettings(legacy.SettingsPath, &plan); err != nil {
			return Plan{}, err
		}
	}
	for _, path := range legacy.HistoryFiles {
		rec, err := buildRecord(path)
		if err != nil {
			// 单份历史读不出来不该让整次迁移失败：其余的照样能搬。
			plan.Skipped = append(plan.Skipped, filepath.Base(path)+"（"+err.Error()+"）")
			continue
		}
		plan.Histories = append(plan.Histories, rec)
	}
	return plan, nil
}

// Dest 是迁移的落地位置。
type Dest struct {
	// Store 是新版配置。
	Store *config.Store
	// History 是新版历史。
	History *history.Store
	// DataDir 是数据目录：备份与迁移标记都放在它下面。
	DataDir string
}

// Report 是一次迁移的结果。
type Report struct {
	// BackupDir 是备份目录；为空表示没有做备份。
	BackupDir string
	// Settings 表示配置已经写入。
	Settings bool
	// Imported / Failed 是历史的导入结果。
	Imported int
	Failed   int
	// Notes 是要告诉用户的调整与取舍。
	Notes []string
	// Skipped 是没能搬过来的项。
	Skipped []string
}

// markerName 是「这份旧数据已经迁移过」的标记文件。
const markerName = ".legacy-migrated.json"

// marker 是标记文件的内容。
type marker struct {
	Source   string    `json:"source"`
	Migrated time.Time `json:"migrated_at"`
	Imported int       `json:"imported"`
}

// AlreadyMigrated 报告这份旧数据是否已经迁移过。
//
// 挡的是「跑第二遍」这种最常见的误操作。真正保证不重复导入的是记录 ID（用旧
// 文件名），标记只是让第二次执行时能给出「已经迁移过」这句话，而不是默默重做。
func AlreadyMigrated(dataDir string, legacy Legacy) bool {
	data, err := os.ReadFile(filepath.Join(dataDir, markerName))
	if err != nil {
		return false
	}
	var m marker
	if err := json.Unmarshal(data, &m); err != nil {
		return false
	}
	return samePath(m.Source, legacy.Dir)
}

// Run 执行迁移。
//
// 顺序是「先备份，再写新版」：写到一半失败时，旧数据还在原地、备份也在，
// 用户随时能翻回去重来。
func Run(legacy Legacy, dest Dest) (Report, error) {
	if !legacy.Found() {
		return Report{}, errors.New("migrate: 没有检测到旧版数据")
	}
	if dest.Store == nil || dest.History == nil {
		return Report{}, errors.New("migrate: 目标未装配完整")
	}

	plan, err := Build(legacy)
	if err != nil {
		return Report{}, err
	}
	report := Report{Notes: plan.Notes, Skipped: plan.Skipped}

	backup, err := backupLegacy(legacy, dest.DataDir)
	if err != nil {
		return report, fmt.Errorf("备份旧数据失败，未做任何改动：%w", err)
	}
	report.BackupDir = backup

	if report.Settings, err = applyPatch(dest.Store, plan.Patch, plan.Origins, &report); err != nil {
		return report, err
	}

	for _, rec := range plan.Histories {
		if _, err := dest.History.Save(rec); err != nil {
			report.Failed++
			report.Skipped = append(report.Skipped, rec.ID+"（写入失败："+err.Error()+"）")
			continue
		}
		report.Imported++
	}

	if err := writeMarker(dest.DataDir, legacy, report.Imported); err != nil {
		// 标记写不上不算迁移失败：数据已经搬过来了，只是下次会重做一遍
		// （而重做是幂等的）。
		report.Notes = append(report.Notes, "迁移完成，但标记文件没写成："+err.Error())
	}
	return report, nil
}

// applyPatch 把补丁逐项写进配置，返回是否有任何一项写成功。
//
// **逐项写而不是整份写**：整份补丁里只要有一个值越界，配置存储就会把整份拒掉，
// 用户看到的是「什么都没搬过来」——而其实只有那一项有问题。逐项写能把失败的
// 那一项单独报出来，其余的照常生效。
func applyPatch(store *config.Store, patch map[string]any, origins model.ParamOrigins, report *Report) (bool, error) {
	leaves := flatten(patch, "")
	sort.Strings(leaves)

	applied := false
	for _, path := range leaves {
		one := map[string]any{}
		setPath(one, path, valueAt(patch, path))

		keyOrigins := model.ParamOrigins{}
		if origin := origins.Get(path); origin != model.OriginDefault {
			keyOrigins.Set(path, origin)
		}
		if _, err := store.Patch(one, keyOrigins); err != nil {
			report.Skipped = append(report.Skipped, path+"（新版不接受这个值："+err.Error()+"）")
			continue
		}
		applied = true
	}
	return applied, nil
}

// flatten 把嵌套补丁摊成点号路径。
func flatten(patch map[string]any, prefix string) []string {
	out := make([]string, 0, len(patch))
	for key, value := range patch {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		if nested, ok := value.(map[string]any); ok {
			out = append(out, flatten(nested, path)...)
			continue
		}
		out = append(out, path)
	}
	return out
}

// valueAt 按点号路径取值。
func valueAt(patch map[string]any, path string) any {
	var cur any = patch
	for _, part := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = obj[part]
	}
	return cur
}

// backupLegacy 把旧数据整份复制到数据目录下的备份区。
//
// 复制而不是移动：用户看完新界面觉得不合适，旧版本还能开起来。备份目录名里
// 带时间戳，因此连续迁移两次不会互相覆盖。
func backupLegacy(legacy Legacy, dataDir string) (string, error) {
	if dataDir == "" {
		return "", errors.New("migrate: 数据目录为空，无法备份")
	}
	dest := filepath.Join(config.BackupDir(dataDir), "legacy-"+time.Now().Format("20060102_150405"))
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", err
	}

	if legacy.SettingsPath != "" {
		if err := copyFile(legacy.SettingsPath, filepath.Join(dest, legacySettingsFile)); err != nil {
			return "", err
		}
	}
	if legacy.HistoryDir != "" {
		target := filepath.Join(dest, legacyHistoryDir)
		if err := copyDir(legacy.HistoryDir, target); err != nil {
			return "", err
		}
	}
	return dest, nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o600)
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

func writeMarker(dataDir string, legacy Legacy, imported int) error {
	data, err := json.MarshalIndent(marker{
		Source:   legacy.Dir,
		Migrated: time.Now(),
		Imported: imported,
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataDir, markerName), append(data, '\n'), 0o600)
}

// samePath 比较两个路径是否指向同一处，忽略大小写与末尾斜杠。
func samePath(a, b string) bool {
	clean := func(p string) string {
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		return strings.TrimRight(strings.ToLower(filepath.ToSlash(abs)), "/")
	}
	return clean(a) == clean(b)
}

package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

/**
 * 每个配置项都得有人读，或者被明确登记为「只有界面在读」「还没生效」。
 *
 * 一条没人读的配置项看起来完全正常：能改、能存、下次打开还在，就是什么都不
 * 发生——而用户只会以为是自己没配对。这一轮就撞见过好几个：两个界面开关
 * （记住界面状态、启动页面）从来没有代码读它们，还有一个与扫描分组重复的
 * 可用性开关。靠人记得去核对是行不通的，这里让它变成一条会红的用例。
 *
 * 判定方式是「字段名在 config 包之外出现过」。它不完美——同名巧合会让它漏判
 * （把没人读的当成有人读），但足以挡住「加了一项却忘了接线」这个最常见的失误。
 */

// frontendOnly 是只由界面读的配置项：后端存下来给前端渲染，自己不解释它。
//
// 加进来之前先确认前端真的在用。写进这份名单比漏判更危险：「前端在读」和
// 「谁都没读」在源码里看起来一模一样。
var frontendOnly = map[string]string{
	"ui.lang":           "前端渲染界面语言",
	"ui.font_scale":     "前端换算字号",
	"ui.density":        "前端决定披露程度",
	"ui.table_density":  "前端换算行高",
	"ui.time_format":    "前端格式化准确时间",
	"ui.start_page":     "前端决定首次落在哪一页",
	"ui.animation":      "前端贴 data-animation",
	"ui.contrast":       "前端贴 data-contrast",
	"ui.remember_state": "前端决定要不要恢复上次的页面与密度",
}

// configOnly 是只被配置包自己读的配置项：它们约束别的项，或者只影响校验。
//
// 单独一类而不是混进 pending：这类项看着像死键，改它其实是有后果的——把它当成
// 「还没生效」会误导人以为可以随便改。
var configOnly = map[string]string{
	"net.max_workers": "约束 scan.workers 能填到多大；本身没有别的消费方",
}

// pending 是还没有任何代码读的配置项，等对应功能落地时从这里删掉。
//
// 它们在设置页里带着「尚未生效」的标记并被禁用：一个改了什么都不会发生的开关，
// 比没有这个开关更伤信任——用户会以为是自己没配对。
var pending = map[string]string{
	"ui.page_size":           "结果表还没有翻页",
	"server.autostart":       "开机自启属于桌面版",
	"notify.on_done":         "通知渠道还没做",
	"notify.on_fail":         "通知渠道还没做",
	"notify.web":             "通知渠道还没做",
	"notify.tray":            "托盘通知属于桌面版",
	"notify.sound":           "通知渠道还没做",
	"advanced.log_keep_days": "日志目前只写控制台，没有落盘的文件可清理",
	"advanced.check_update":  "还没有更新检查",
	"advanced.experimental":  "实验开关还没有消费方",

	// 下面这些是「协议层还留着的旋钮」：探测与测速的超时、DNS、并发上限目前
	// 全部由请求参数决定，配置里这几项没有接到任何地方。
	"net.connect_timeout_ms":   "探测超时由请求参数决定，配置项还没接上",
	"net.custom_dns":           "探测目前走系统 DNS",
	"net.dns_fallback":         "探测目前走系统 DNS",
	"source.merge_strategy":    "多源合并目前固定取并集",
	"source.retry_interval_ms": "来源拉取还没有重试",
	"speed.max_download_mb":    "测速目前只按时长停止，没有按下载量",
}

// configField 是一个配置项：json 名与 Go 字段名。
type configField struct {
	jsonName string
	goName   string
}

// allConfigFields 用反射列出配置的全部叶子字段，含嵌套分组。
func allConfigFields() []configField {
	var out []configField
	var walk func(prefix string, t reflect.Type)
	walk = func(prefix string, t reflect.Type) {
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "" || name == "-" {
				continue
			}
			full := name
			if prefix != "" {
				full = prefix + "." + name
			}
			if field.Type.Kind() == reflect.Struct {
				walk(full, field.Type)
				continue
			}
			out = append(out, configField{jsonName: full, goName: field.Name})
		}
	}
	walk("", reflect.TypeOf(Config{}))
	sort.Slice(out, func(i, j int) bool { return out[i].jsonName < out[j].jsonName })
	return out
}

// anyMatches 判断有没有哪份源码里出现过这个模式。
func anyMatches(pattern string, sources []string) bool {
	re := regexp.MustCompile(pattern)
	for _, source := range sources {
		if re.MatchString(source) {
			return true
		}
	}
	return false
}

// readersOutsideConfig 收集 config 包之外的全部 Go 源码。
func readersOutsideConfig(t *testing.T) []string {
	t.Helper()

	var sources []string
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(path)
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "dist", "docs", "Screenshots", "__pycache__":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.Contains(slash, "/internal/config/") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		sources = append(sources, string(data))
		return nil
	})
	if err != nil {
		t.Fatalf("遍历源码失败：%v", err)
	}
	if len(sources) < 20 {
		t.Fatalf("只读到 %d 个文件，遍历范围可能不对", len(sources))
	}
	return sources
}

func TestEveryConfigKeyHasAReader(t *testing.T) {
	sources := readersOutsideConfig(t)

	var unaccounted []string
	for _, field := range allConfigFields() {
		if _, ok := frontendOnly[field.jsonName]; ok {
			continue
		}
		if _, ok := configOnly[field.jsonName]; ok {
			continue
		}
		if _, ok := pending[field.jsonName]; ok {
			continue
		}
		if !anyMatches(`\.`+regexp.QuoteMeta(field.goName)+`\b`, sources) {
			unaccounted = append(unaccounted, field.jsonName)
		}
	}

	if len(unaccounted) > 0 {
		t.Errorf("这些配置项没有任何代码读它们：\n  %s\n要么接上，要么写进 frontendOnly / configOnly / pending 并说明原因",
			strings.Join(unaccounted, "\n  "))
	}
}

// 名单本身也要保持干净：删掉的配置项不该在名单里留一条。
func TestKeyListsHaveNoStaleEntries(t *testing.T) {
	known := map[string]bool{}
	for _, field := range allConfigFields() {
		known[field.jsonName] = true
	}
	for _, list := range keyLists {
		for key := range list {
			if !known[key] {
				t.Errorf("名单里有配置里已经不存在的项：%s", key)
			}
		}
	}
}

// keyLists 是三条名单，名字带进去便于报错时指认。
var keyLists = map[string]map[string]string{
	"frontendOnly": frontendOnly,
	"configOnly":   configOnly,
	"pending":      pending,
}

// 名单之间不该重叠：一个项只属于一类。
func TestKeyListsDoNotOverlap(t *testing.T) {
	names := make([]string, 0, len(keyLists))
	for name := range keyLists {
		names = append(names, name)
	}
	sort.Strings(names)

	for i, first := range names {
		for _, second := range names[i+1:] {
			for key := range keyLists[first] {
				if _, both := keyLists[second][key]; both {
					t.Errorf("%s 同时出现在 %s 与 %s 里", key, first, second)
				}
			}
		}
	}
}

package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

/**
 * 设置页声明的配置键必须与配置结构对得上。
 *
 * 为什么要读前端的源码文件：两边各写一份清单是「字段悄悄漂移」的根源，而漂
 * 移后的表现最难查——写进去的键不在配置结构里，反序列化时被静默丢掉，界面上
 * 却像是保存成功了，等下次拉到全量配置，那个开关又变回原样。用户看到的是
 * 「改了没用」，而不是一条报错。
 *
 * 曾经设置页里就有八个这样的键，范围也有两处比配置宽松（能填出必定被拒的
 * 值）。这里一次性挡住：新增设置项必须先在这里落一个字段。
 */

// frontendSchemaFile 是设置页的配置项声明表。
const frontendSchemaFile = "../../web/src/i18n/settingsSchema.ts"

// skipWithoutFrontend 只在「整个前端都不在」时跳过。
//
// 前端在而文件不在，说明路径或文件名改了——那种情况必须报错，静默跳过会让
// 这个用例变成永远通过的摆设。
func skipWithoutFrontend(t *testing.T) []byte {
	t.Helper()

	if _, err := os.Stat(filepath.Dir(frontendSchemaFile)); err != nil {
		t.Skipf("前端源码不在（可能是后端单独检出），跳过：%v", err)
	}
	data, err := os.ReadFile(frontendSchemaFile)
	if err != nil {
		t.Fatalf("读不到设置页声明表 %s：%v", frontendSchemaFile, err)
	}
	return data
}

var pathRE = regexp.MustCompile(`path:\s*'([^']+)'`)
var rangeRE = regexp.MustCompile(`path:\s*'([^']+)'[^\n]*min:\s*(-?\d+),\s*max:\s*(-?\d+)`)
var enumRE = regexp.MustCompile(`path:\s*'([^']+)',\s*kind:\s*'enum',\s*options:\s*\[([^\]]*)\]`)

/** frontendEnumOption 是设置页给某个枚举项列出的一个可选值。 */
type frontendEnumOption struct {
	path   string
	option string
}

/** frontendEnumOptions 解析出声明表里全部枚举项的取值。 */
func frontendEnumOptions(t *testing.T) []frontendEnumOption {
	t.Helper()
	matches := enumRE.FindAllStringSubmatch(string(skipWithoutFrontend(t)), -1)
	if len(matches) < 10 {
		t.Fatalf("只解析出 %d 个枚举项，声明表的写法可能变了", len(matches))
	}
	out := make([]frontendEnumOption, 0, len(matches))
	for _, m := range matches {
		for _, raw := range strings.Split(m[2], ",") {
			option := strings.Trim(strings.TrimSpace(raw), "'\"")
			if option != "" {
				out = append(out, frontendEnumOption{path: m[1], option: option})
			}
		}
	}
	return out
}

/** frontendPaths 解析出声明表里出现的全部路径。 */
func frontendPaths(t *testing.T) []string {
	t.Helper()
	matches := pathRE.FindAllStringSubmatch(string(skipWithoutFrontend(t)), -1)
	// 解析不出东西说明格式变了，而不是「一个键都没有」。
	if len(matches) < 50 {
		t.Fatalf("只解析出 %d 个路径，声明表的写法可能变了", len(matches))
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}

/** frontendRange 是声明表里某个数值项的取值范围。 */
type frontendRange struct {
	path string
	min  int
	max  int
}

func frontendRanges(t *testing.T) []frontendRange {
	t.Helper()
	matches := rangeRE.FindAllStringSubmatch(string(skipWithoutFrontend(t)), -1)
	out := make([]frontendRange, 0, len(matches))
	for _, m := range matches {
		min, _ := strconv.Atoi(m[2])
		max, _ := strconv.Atoi(m[3])
		out = append(out, frontendRange{path: m[1], min: min, max: max})
	}
	return out
}

/**
 * configKeys 用反射列出配置结构认识的全部点号路径。
 *
 * 走反射而不是手写一份清单：手写清单等于又多一处会与结构走散的地方，而这个
 * 用例存在的意义正是消灭这种清单。
 */
func configKeys() map[string]bool {
	out := map[string]bool{}
	var cfg Config
	v := reflect.ValueOf(cfg)

	for i := 0; i < v.NumField(); i++ {
		name := strings.Split(v.Type().Field(i).Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		fv := v.Field(i)
		if fv.Kind() != reflect.Struct {
			out[name] = true
			continue
		}
		for j := 0; j < fv.NumField(); j++ {
			subName := strings.Split(fv.Type().Field(j).Tag.Get("json"), ",")[0]
			if subName == "" || subName == "-" {
				continue
			}
			out[name+"."+subName] = true
		}
	}
	return out
}

/**
 * 设置页列为可选的枚举值，配置校验必须真的接受。
 *
 * 用 Validate 本身当权威而不是另抄一份清单：抄来的那份一样会与校验走散，而
 * 走散的表现是「界面允许选、保存时被拒」，看起来像程序的错。
 *
 * 反方向（后端接受但界面没列出）不在这里管：界面刻意少给几个选项是合理的
 * 取舍，没法自动判断。
 */
func TestFrontendEnumOptionsAreAccepted(t *testing.T) {
	for _, item := range frontendEnumOptions(t) {
		obj, err := toObject(Default())
		if err != nil {
			t.Fatalf("转对象失败：%v", err)
		}
		path := splitKey(item.path)
		if _, ok := lookupPath(obj, path); !ok {
			t.Errorf("设置页声明了配置里没有的枚举项 %q", item.path)
			continue
		}
		setPath(obj, path, item.option)

		next, err := fromObject(obj)
		if err != nil {
			t.Errorf("%s 设成 %q 后配置解析不了：%v", item.path, item.option, err)
			continue
		}
		err = next.Validate()
		if err == nil || !complainsAbout(err, item.path) {
			// 报的是别的键（如选了自定义来源就必须填自定义地址）属于跨字段约束，
			// 不是这个取值本身不被接受，这里不管。
			continue
		}
		t.Errorf("设置页把 %s 的 %q 列为可选，配置校验却拒绝：%v", item.path, item.option, err)
	}
}

// complainsAbout 判断校验失败里有没有指向这个键的。
//
// 认不出形状的失败一律算相关：宁可误报一条，也不要放过真正的取值问题。
func complainsAbout(err error, key string) bool {
	var ve *ValidationError
	if !errors.As(err, &ve) {
		return true
	}
	for _, field := range ve.Fields {
		if field.Key == key {
			return true
		}
	}
	return false
}

func TestFrontendDeclaresOnlyKnownConfigKeys(t *testing.T) {
	known := configKeys()

	for _, path := range frontendPaths(t) {
		if !known[path] {
			t.Errorf("设置页声明了配置里没有的键 %q：改它会被静默丢弃，界面上表现为「保存成功了又变回去」", path)
		}
	}
}

// hardLimits 是配置校验真正接受的边界，与 Validate 里的判断一一对应。
//
// 只登记「设置页曾经写错过」和「容易写错」的几项：全量登记等于把 validate.go
// 抄一遍，而抄来的表一样会走散。
var hardLimits = map[string]frontendRange{
	"speed.concurrency":      {min: 1, max: 16},
	"speed.breaker_429":      {min: 1, max: 100},
	"server.session_ttl_min": {min: 1, max: 10080},
	"scan.workers":           {min: 1, max: MaxWorkersHard},
	"net.max_workers":        {min: 1, max: MaxWorkersHard},
	"history.keep_count":     {min: 1, max: 1000},
	"ui.page_size":           {min: 1, max: 1000},
	"scan.ping_times":        {min: 1, max: 100},
}

/**
 * 设置页允许填的范围不能超出配置接受的边界。
 *
 * 超出那一侧就是「用户能填出一个必定被拒的值」——保存时报错，而界面明明允许
 * 他这么填，看起来像程序的错。
 *
 * 注意 ping_times：声明表里下限是 0 是有意的（0 表示「不指定」，由补默认值那
 * 一步换成真实次数），因此这一项只查上限。
 */
func TestFrontendRangesStayWithinConfigLimits(t *testing.T) {
	for _, declared := range frontendRanges(t) {
		limit, ok := hardLimits[declared.path]
		if !ok {
			continue
		}
		if declared.path == "scan.ping_times" {
			if declared.max > limit.max {
				t.Errorf("%s 的上限 %d 超出配置接受的 %d", declared.path, declared.max, limit.max)
			}
			continue
		}
		if declared.min < limit.min || declared.max > limit.max {
			t.Errorf("%s 的范围 %d–%d 超出配置接受的 %d–%d",
				declared.path, declared.min, declared.max, limit.min, limit.max)
		}
	}
}

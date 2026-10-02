package exporter

import (
	"fmt"
	"strings"
	"time"

	"cloudtrace/internal/model"
)

// Options 是一次导出的参数。
type Options struct {
	// Format 取值 csv / json / txt。
	Format string
	// Fields 是要导出的字段键；空 = 用默认选中的那批。
	Fields []string
	// Aliases 覆盖表头 / 键名（原始键 → 自定义名）。
	Aliases map[string]string
	// BOM 仅对 CSV 生效：带上字节序标记，Excel 打开才不乱码。
	BOM bool
}

// Export 按格式渲染结果集，同时给出内容类型与文件扩展名。
//
// 内容类型与扩展名一起返回而不是让调用方再查一次表：格式名到这两个值的
// 映射只有本包知道，散到调用方就必然出现「下载下来后缀不对」。
func Export(records []model.IPRecord, opts Options) (data []byte, contentType, ext string, err error) {
	switch opts.Format {
	case "csv":
		fields := ResolveFields(opts.Fields)
		return CSV(records, fields, opts.Aliases, opts.BOM), "text/csv; charset=utf-8", "csv", nil
	case "json":
		fields := ResolveFields(opts.Fields)
		out, err := JSON(records, fields, opts.Aliases)
		if err != nil {
			return nil, "", "", err
		}
		return out, "application/json; charset=utf-8", "json", nil
	case "txt":
		return TXT(records), "text/plain; charset=utf-8", "txt", nil
	default:
		return nil, "", "", fmt.Errorf("导出格式 %q 无法识别，只能是 csv / json / txt", opts.Format)
	}
}

// Filename 按模板生成文件名（不含目录）。
//
// 支持的占位符：{type} 结果类型、{ts} 完整时间戳、{date} 日期、{time} 时间。
// 模板里没有扩展名时补上，重复的就去掉，避免出现 result.csv.csv。
func Filename(template, typ, format string, ts time.Time) string {
	tmpl := strings.TrimSpace(template)
	if tmpl == "" {
		tmpl = "cloudtrace_{type}_{ts}"
	}

	name := strings.NewReplacer(
		"{type}", sanitize(typ),
		"{ts}", ts.Format("20060102_150405"),
		"{date}", ts.Format("20060102"),
		"{time}", ts.Format("150405"),
	).Replace(tmpl)

	name = sanitize(name)
	if name == "" {
		name = "cloudtrace"
	}

	ext := "." + format
	if !strings.EqualFold(name[len(name)-len(ext):], ext) {
		name += ext
	}
	return name
}

// sanitize 去掉文件名里的非法字符。
//
// Windows 不允许 \ / : * ? " < > | 与控制字符；结尾的点与空格在保存时会
// 被静默去掉，不如这里就处理掉，避免「保存成功但文件名不对」。
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\', '/', ':', '*', '?', '"', '<', '>', '|':
			b.WriteRune('_')
		default:
			if r < 0x20 || r == 0x7f {
				continue
			}
			b.WriteRune(r)
		}
	}
	return strings.TrimRight(b.String(), " .")
}

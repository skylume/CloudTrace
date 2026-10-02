package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"cloudtrace/internal/config"
	"cloudtrace/internal/exporter"
	"cloudtrace/internal/history"
	"cloudtrace/internal/model"
)

// 导出相关的命令与事件名。
//
// 响应的 type 与命令同名，与既有协议一致：前端可以用同一套「发什么命令等
// 什么事件」的写法。
const cmdExport = "export"

// exportFieldsRoute 是字段清单的下发地址。
const exportFieldsRoute = "/api/export/fields"

// exportFieldsResp 是 GET /api/export/fields 的响应体。
//
// 字段、预设、格式三者一起下发：前端硬编码其中任何一份都会漂移，而「后端
// 加了字段、前端还不知道」这种不同步没有地方能发现。
type exportFieldsResp struct {
	Fields  []exporter.FieldDef `json:"fields"`
	Presets []exporter.Preset   `json:"presets"`
	Formats []string            `json:"formats"`
}

// handleExportFields 下发导出字段清单。
func (s *server) handleExportFields(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, CodeInvalidParam, "只支持 GET")
		return
	}
	writeJSON(w, http.StatusOK, exportFieldsResp{
		Fields:  exporter.Fields(),
		Presets: exporter.Presets(),
		Formats: []string{config.FormatCSV, config.FormatJSON, config.FormatTXT},
	})
}

// exportReq 是 export 命令的载荷。
type exportReq struct {
	// Type 是结果类型（scan / speed）；为空表示最新一份，优先测速结果。
	Type string `json:"type"`
	// ID 指定某一份历史；为空时按 Type 取最新一份。
	ID string `json:"id"`
	// Format 取值 csv / json / txt；为空表示用配置里的默认格式。
	Format string `json:"format"`
	// Fields 是字段键；为空表示用配置里的默认字段预设。
	Fields []string `json:"fields"`
	// Filter 缩小导出范围。
	Filter exportFilter `json:"filter"`
	// Sort 是排序维度；为空表示默认（丢包升序 → 延迟升序）。
	Sort string `json:"sort"`
	// Desc 为空时按该维度的自然方向（速度与评分降序，其余升序）。
	Desc *bool `json:"desc"`
}

// exportFilter 缩小导出范围。
type exportFilter struct {
	// Regions 只保留这些数据中心代码（大小写不敏感）；为空表示不按地区过滤。
	Regions []string `json:"regions"`
	// MaxLatency 是延迟上限（毫秒）；<= 0 表示不限。
	MaxLatency float64 `json:"max_latency"`
	// IncludeUnreached 是否连探测失败的节点一起导出；默认排除。
	IncludeUnreached bool `json:"include_unreached"`
}

// exportResp 是 export 命令的响应。
type exportResp struct {
	// ID 是下载标识，拼上 /api/download/ 就是下载地址。
	ID string `json:"id"`
	// URL 是下载地址。
	URL string `json:"url"`
	// Name 是建议的文件名。
	Name string `json:"name"`
	// Count 是实际导出的条数，Total 是筛选前的条数。
	Count int `json:"count"`
	Total int `json:"total"`
}

// handleExport 生成一份导出文件并返回下载地址。
func (s *server) handleExport(c *wsConn, data json.RawMessage) error {
	var req exportReq
	if err := decodeReq(data, &req, "导出参数"); err != nil {
		return err
	}

	cfg := s.cfg.Get()

	format := strings.ToLower(strings.TrimSpace(req.Format))
	if format == "" {
		format = cfg.Export.DefaultFormat
	}
	switch format {
	case config.FormatCSV, config.FormatJSON, config.FormatTXT:
	default:
		return fail(CodeInvalidParam,
			fmt.Sprintf("导出格式 %q 无法识别，只能是 %s / %s / %s",
				req.Format, config.FormatCSV, config.FormatJSON, config.FormatTXT))
	}

	// 维度认不出来就报错而不是兜底：按别的维度排完，用户只会觉得「点了没反应」。
	key, ok := model.ParseSortKey(req.Sort)
	if !ok {
		return fail(CodeInvalidParam, fmt.Sprintf("排序维度 %q 无法识别", req.Sort))
	}
	desc := model.NaturalDesc(key)
	if req.Desc != nil {
		desc = *req.Desc
	}

	rec, err := s.sourceRecord(req.ID, req.Type)
	if err != nil {
		return err
	}

	total := len(rec.Results)
	records := filterRecords(rec.Results, req.Filter)
	// 排序在服务端做：几千条记录交给前端排会让点击列头明显卡顿，而导出的
	// 顺序本来就该由导出参数决定，不该取决于前端此刻是怎么排的。
	model.SortRecords(records, key, desc)

	fields := req.Fields
	if len(fields) == 0 {
		fields = exporter.PresetKeys(cfg.Export.DefaultFields)
	}

	blob, contentType, ext, err := exporter.Export(records, exporter.Options{
		Format:  format,
		Fields:  fields,
		Aliases: cfg.Export.FieldAliases,
		BOM:     cfg.Export.CSVBOM,
	})
	if err != nil {
		return fail(CodeInvalidParam, err.Error())
	}

	name := exporter.Filename(cfg.Export.FilenameTemplate, rec.Type, ext, time.Now())
	id, err := s.downloads.put(name, contentType, blob)
	if err != nil {
		return fail(CodeIO, err.Error())
	}

	c.sendEvent(cmdExport, exportResp{
		ID:    id,
		URL:   downloadRoute + id,
		Name:  name,
		Count: len(records),
		Total: total,
	})
	return nil
}

// sourceRecord 找出这次导出要用的结果集。
//
// 指定了 ID 就用那一份；否则取该类最新一份，类型为空时优先测速结果——测速
// 结果是优选过的，比扫描结果更接近用户真正想导出的东西。
func (s *server) sourceRecord(id, typ string) (history.HistoryRecord, error) {
	store := s.history()
	if store == nil {
		return history.HistoryRecord{}, fail(CodeIO, "历史存储不可用，无法导出")
	}

	if id != "" {
		rec, err := store.Load(id)
		if err != nil {
			return history.HistoryRecord{}, s.historyError(err)
		}
		return rec, nil
	}

	types := []string{history.TypeSpeed, history.TypeScan}
	if t := strings.ToLower(strings.TrimSpace(typ)); t != "" {
		if t != history.TypeScan && t != history.TypeSpeed {
			return history.HistoryRecord{}, fail(CodeInvalidParam,
				fmt.Sprintf("结果类型 %q 无法识别，只能是 %s 或 %s", typ, history.TypeScan, history.TypeSpeed))
		}
		types = []string{t}
	}

	for _, t := range types {
		rec, err := s.latestRecord(t)
		if err == nil {
			return rec, nil
		}
		if !errors.Is(err, history.ErrNotFound) {
			return history.HistoryRecord{}, s.historyError(err)
		}
	}
	return history.HistoryRecord{}, fail(CodeNotFound, "还没有任何结果可以导出，先跑一次扫描或测速")
}

// latestRecord 取某类最新一份记录。
//
// 两个 IP 版本都试：用户切了 IP 版本之后，「上一次的结果」往往还留在另一个
// 版本的目录下，为此让导出直接失败没有道理。
func (s *server) latestRecord(typ string) (history.HistoryRecord, error) {
	store := s.history()
	var lastErr error
	for _, v := range s.ipVersionOrder() {
		rec, err := store.LoadLatest(typ, v)
		if err == nil {
			return rec, nil
		}
		lastErr = err
	}
	return history.HistoryRecord{}, lastErr
}

// ipVersionOrder 返回查找「最新一份」时的 IP 版本顺序，当前配置的版本优先。
func (s *server) ipVersionOrder() []int {
	if s.ipVersion() == 6 {
		return []int{6, 4}
	}
	return []int{4, 6}
}

// ipVersion 返回当前配置使用的 IP 版本号。
func (s *server) ipVersion() int {
	switch strings.ToLower(strings.TrimSpace(s.cfg.Get().Net.IPVersion)) {
	case "v6", "6", "ipv6":
		return 6
	default:
		return 4
	}
}

// filterRecords 按筛选条件挑出要导出的记录。
//
// 默认排除探测失败的节点：它们的延迟是哨兵值、速度是 0，导出去只会让下游
// 拿到一行看不懂的空数据。用户显式要求时才带上。
func filterRecords(records []model.IPRecord, f exportFilter) []model.IPRecord {
	regions := normalizeRegions(f.Regions)
	out := make([]model.IPRecord, 0, len(records))
	for _, r := range records {
		if !f.IncludeUnreached && !r.Reachable() {
			continue
		}
		// 延迟上限只管可达的节点：不可达节点该不该出现由 IncludeUnreached
		// 单独决定，拿哨兵值 -1 去比上限是两回事。
		if f.MaxLatency > 0 && r.Reachable() && r.Latency > f.MaxLatency {
			continue
		}
		if len(regions) > 0 && !regions[strings.ToUpper(strings.TrimSpace(r.Colo))] {
			continue
		}
		out = append(out, r)
	}
	return out
}

// normalizeRegions 把地区代码归一成大写集合，顺带丢掉空项。
func normalizeRegions(in []string) map[string]bool {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]bool, len(in))
	for _, r := range in {
		if code := strings.ToUpper(strings.TrimSpace(r)); code != "" {
			out[code] = true
		}
	}
	return out
}

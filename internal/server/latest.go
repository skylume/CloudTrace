package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"cloudtrace/internal/exporter"
	"cloudtrace/internal/geo"
	"cloudtrace/internal/model"
)

// 给脚本与 DDNS 拉取的本地结果地址。
//
// 单独开这两个地址而不是让脚本去读数据目录：数据目录的布局属于内部实现，
// 而「从本机拉一份当前可用的 IP 列表」是用户能依赖的稳定接口。
const (
	latestRoute     = "/latest"
	latestJSONRoute = "/latest.json"
)

// handleLatest 输出最新优选结果的 ip:port 清单，每行一条。
func (s *server) handleLatest(w http.ResponseWriter, r *http.Request) {
	records, err := s.latestResults()
	if err != nil {
		writeLatestError(w, err)
		return
	}

	var buf bytes.Buffer
	for _, rec := range records {
		buf.WriteString(exporter.HostPort(rec.IP, rec.Port))
		buf.WriteByte('\n')
	}

	writeLatestBody(w, r, "text/plain; charset=utf-8", buf.Bytes())
}

// handleLatestJSON 输出同一批结果的完整结构。
//
// 给的是完整记录而不是只有 ip/port：脚本想按地区挑、按延迟筛的时候，
// 不必再去别处找这些字段。
func (s *server) handleLatestJSON(w http.ResponseWriter, r *http.Request) {
	records, err := s.latestResults()
	if err != nil {
		writeLatestError(w, err)
		return
	}

	body, err := json.Marshal(records)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeUnknown, "结果序列化失败")
		return
	}
	writeLatestBody(w, r, "application/json; charset=utf-8", body)
}

// latestResults 取最新一次结果的可用节点，按默认维度排好序。
//
// 只留可达节点：这份列表的用途是直接喂给下游，混进一条探测失败的地址，
// 下游拿到的是没法用的东西。排序与结果页一致，脚本拿到的第一条就是最优选。
func (s *server) latestResults() ([]model.IPRecord, error) {
	rec, err := s.sourceRecord("", "")
	if err != nil {
		return nil, err
	}
	out := filterRecords(rec.Results, exportFilter{})
	// 按运营商过滤在这里生效：配置里的条件筛的是「哪些节点算优选」，
	// 而这两个地址给出的正是优选结果。
	out = geo.FilterByASN(out, s.cfg.Get().Geo.FilterASN)
	model.SortRecords(out, model.DefaultSortKey, false)
	return out, nil
}

// writeLatestBody 输出响应体，HEAD 请求只要响应头。
func writeLatestBody(w http.ResponseWriter, r *http.Request, contentType string, body []byte) {
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Content-Length", strconv.Itoa(len(body)))
	h.Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// writeLatestError 把命令层的错误翻成 HTTP 响应。
//
// 没有结果时给 404 而不是 200 + 空内容：脚本靠状态码就能判断「还没跑过任务」，
// 不必去猜空响应到底是没结果还是服务出了别的问题。
func writeLatestError(w http.ResponseWriter, err error) {
	var ce *cmdError
	if errors.As(err, &ce) {
		switch ce.Code {
		case CodeNotFound:
			writeError(w, http.StatusNotFound, ce.Code, ce.Msg)
			return
		case CodeInvalidParam:
			writeError(w, http.StatusBadRequest, ce.Code, ce.Msg)
			return
		}
	}
	writeError(w, http.StatusInternalServerError, CodeUnknown, err.Error())
}

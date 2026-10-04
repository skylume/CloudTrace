package migrate

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"time"

	"cloudtrace/internal/history"
	"cloudtrace/internal/model"
)

// 旧的存档时间格式。
const legacyTimeLayout = "2006-01-02 15:04:05"

// legacyFile 是旧历史文件的形状。
type legacyFile struct {
	SaveTime   string           `json:"save_time"`
	IPVersion  int              `json:"ip_version"`
	ResultType string           `json:"result_type"`
	Count      int              `json:"count"`
	Results    []map[string]any `json:"results"`
}

// buildRecord 把一份旧历史文件转成新记录。
func buildRecord(path string) (history.HistoryRecord, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return history.HistoryRecord{}, err
	}
	var file legacyFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return history.HistoryRecord{}, errors.New("不是合法 JSON")
	}

	rec := history.HistoryRecord{
		Type:      file.ResultType,
		IPVersion: file.IPVersion,
	}
	if rec.Type != history.TypeScan && rec.Type != history.TypeSpeed {
		return history.HistoryRecord{}, fmt.Errorf("认不出的记录类型 %q", file.ResultType)
	}
	if rec.IPVersion != 4 && rec.IPVersion != 6 {
		return history.HistoryRecord{}, fmt.Errorf("认不出的地址族 %d", file.IPVersion)
	}

	// 旧版存的是本地时间的字符串，没有时区。按本地时区解释——用户在哪个时区
	// 跑的，那个时间就是那个时区的。
	if file.SaveTime != "" {
		at, err := time.ParseInLocation(legacyTimeLayout, file.SaveTime, time.Local)
		if err != nil {
			return history.HistoryRecord{}, fmt.Errorf("认不出的存档时间 %q", file.SaveTime)
		}
		rec.CreatedAt = at
	}
	if rec.CreatedAt.IsZero() {
		return history.HistoryRecord{}, errors.New("旧文件里没有存档时间")
	}
	rec.ID = legacyID(filepath.Base(path), rec.CreatedAt)

	for _, item := range file.Results {
		rec.Results = append(rec.Results, convertRecord(item))
	}
	rec.Count = len(rec.Results)
	if rec.Count == 0 {
		rec.Count = file.Count
	}
	// 旧文件没有参数快照，留一个空对象：记录结构要求它是个合法 JSON。
	rec.Params = json.RawMessage("{}")
	rec.Summary = model.Summarize(rec.Results)
	return rec, nil
}

// legacyID 由旧文件名算出新版格式的记录 ID。
//
// 新版 ID 的形状是固定的（`时间戳_8 位十六进制`），因此不能直接沿用旧文件名
// （`ipv4_scan_20260927_131514`）。后缀取文件名的摘要而不是随机数：**迁移必须
// 幂等**，同一个旧文件每次都要算出同一个 ID，否则重复执行会导入两份。
//
// 时间戳沿用旧文件里的存档时间，这样历史列表按时间排序时顺序与旧版一致。
func legacyID(fileName string, at time.Time) string {
	sum := fnv.New32a()
	_, _ = sum.Write([]byte(fileName))
	return at.Format("20060102_150405") + "_" + hex.EncodeToString(sum.Sum(nil))
}

// convertRecord 把一条旧结果转成新记录。
//
// 认不出的字段直接丢掉：新版记录结构是固定的，硬塞进去只会在下次读出时
// 变成一个解析不了的字段。
func convertRecord(item map[string]any) model.IPRecord {
	rec := model.IPRecord{
		IP:         str(item["ip"]),
		Port:       intOr(item["port"], 443),
		UseTLS:     boolOr(item["use_tls"], true),
		Latency:    floatOr(item["latency"], model.Unreachable),
		LatencyAvg: floatOr(item["latency_avg"], model.Unreachable),
		LatencyMax: floatOr(item["latency_max"], model.Unreachable),
		Jitter:     floatOr(item["jitter"], 0),
		Loss:       floatOr(item["loss"], 0),
		Colo:       str(item["colo"]),
		Loc:        str(item["loc"]),
		RegionName: str(item["chinese_name"]),
		SpeedMBps:  floatOr(item["download_speed"], 0),
		Score:      floatOr(item["score"], 0),
	}
	// 旧版用「发送样本数 / 成功数」表示探测结果。
	rec.Sent = intOr(item["samples"], 0)
	rec.Recv = intOr(item["ok_count"], 0)

	// 旧版把「探测成功」单独存了一个字段；新版按「收到过响应」判断。
	// 旧数据里 success=false 而 recv>0 的情况存在（部分成功），以 recv 为准。
	if success, ok := item["success"].(bool); ok && !success && rec.Recv == 0 {
		rec.Latency = model.Unreachable
		rec.LatencyAvg = model.Unreachable
		rec.LatencyMax = model.Unreachable
		rec.Loss = 1
	}
	return rec
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func intOr(v any, fallback int) int {
	if n, ok := toInt(v); ok {
		return n
	}
	return fallback
}

func floatOr(v any, fallback float64) float64 {
	if n, ok := toFloat(v); ok {
		return n
	}
	return fallback
}

func boolOr(v any, fallback bool) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return fallback
}

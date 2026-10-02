package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 导出物的保留参数。
//
// 导出物留在内存里而不写进数据目录：它是一次性的下载中转，落盘之后没人负责
// 清理，用户的 data/ 会慢慢堆满自己都不记得导出过的文件。一次导出最多也就
// 几 MB（几千条记录），放内存里代价很低。
const (
	downloadTTL     = 15 * time.Minute
	maxDownloads    = 32
	downloadIDBytes = 16
	downloadRoute   = "/api/download/"
)

// downloadFile 是一份待下载的导出物。
type downloadFile struct {
	name        string
	contentType string
	data        []byte
	createdAt   time.Time
}

// downloadStore 保存待下载的导出物，带过期与数量上限。
type downloadStore struct {
	mu    sync.Mutex
	files map[string]downloadFile
	// order 按登记顺序记录 ID，用于按数量淘汰最旧的一份。
	order []string
	now   func() time.Time
}

func newDownloadStore() *downloadStore {
	return &downloadStore{
		files: map[string]downloadFile{},
		now:   time.Now,
	}
}

// put 登记一份导出物并返回下载标识。
func (d *downloadStore) put(name, contentType string, data []byte) (string, error) {
	buf := make([]byte, downloadIDBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成下载标识失败：%w", err)
	}
	id := hex.EncodeToString(buf)

	d.mu.Lock()
	defer d.mu.Unlock()
	d.gcLocked()
	d.files[id] = downloadFile{
		name:        name,
		contentType: contentType,
		data:        data,
		createdAt:   d.now(),
	}
	d.order = append(d.order, id)
	d.evictLocked()
	return id, nil
}

// get 取出导出物。
func (d *downloadStore) get(id string) (downloadFile, bool) {
	if id == "" {
		return downloadFile{}, false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.gcLocked()
	f, ok := d.files[id]
	return f, ok
}

// count 返回当前保留的导出物数量。
func (d *downloadStore) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.files)
}

// gcLocked 清掉过期的导出物。
func (d *downloadStore) gcLocked() {
	deadline := d.now().Add(-downloadTTL)
	for id, f := range d.files {
		if f.createdAt.Before(deadline) {
			delete(d.files, id)
		}
	}
}

// evictLocked 按登记顺序淘汰超出上限的导出物。
//
// 淘汰最旧的一份而不是「最久没被下载的」：下载是点一下就走的一次性动作，
// 记录访问时间换不来什么，只会让状态多一处会错的地方。
func (d *downloadStore) evictLocked() {
	for len(d.order) > 0 && len(d.files) > maxDownloads {
		oldest := d.order[0]
		d.order = d.order[1:]
		delete(d.files, oldest)
	}
	// order 里会残留已经过期或被删掉的 ID，攒多了顺手压一次，别让它无限长。
	if len(d.order) > 2*maxDownloads {
		live := d.order[:0]
		for _, id := range d.order {
			if _, ok := d.files[id]; ok {
				live = append(live, id)
			}
		}
		d.order = live
	}
}

// handleDownload 处理 GET /api/download/{id}。
func (s *server) handleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, CodeInvalidParam, "只支持 GET")
		return
	}

	id := strings.TrimPrefix(r.URL.Path, downloadRoute)
	f, ok := s.downloads.get(id)
	if !ok {
		// 过期与「本来就不存在」给同一个回复：对用户来说要做的事完全一样，
		// 区分开只会多一句没有用处的提示。
		writeError(w, http.StatusNotFound, CodeNotFound, "导出文件不存在或已过期，请重新导出")
		return
	}

	h := w.Header()
	h.Set("Content-Type", f.contentType)
	h.Set("Content-Length", strconv.Itoa(len(f.data)))
	h.Set("Content-Disposition", contentDisposition(f.name))
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(f.data)
}

// contentDisposition 拼出下载响应头。
//
// 同时给 filename 与 RFC 5987 的 filename*：前者是给老客户端的纯 ASCII 兜底，
// 后者才是带中文的文件名的正确表达。只给前者会让中文名变成一串下划线。
func contentDisposition(name string) string {
	return fmt.Sprintf("attachment; filename=%q; filename*=UTF-8''%s",
		asciiFallback(name), rfc5987Escape(name))
}

// asciiFallback 把文件名压成纯 ASCII。
//
// 引号与反斜杠必须去掉：它们能提前结束响应头里的字符串，让文件名里的内容
// 变成额外的响应头。
func asciiFallback(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c < 0x20 || c > 0x7e || c == '"' || c == '\\' {
			b.WriteByte('_')
			continue
		}
		b.WriteByte(c)
	}
	if out := strings.TrimSpace(b.String()); out != "" {
		return out
	}
	return "export"
}

// rfc5987Escape 按 RFC 5987 的 attr-char 规则做百分号编码。
func rfc5987Escape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if c := s[i]; isAttrChar(c) {
			b.WriteByte(c)
			continue
		}
		b.WriteString(fmt.Sprintf("%%%02X", s[i]))
	}
	return b.String()
}

// isAttrChar 判断字节是否属于 RFC 5987 允许直接出现的字符集。
func isAttrChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '!', '#', '$', '&', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}

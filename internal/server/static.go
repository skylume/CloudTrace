package server

import (
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"

	webassets "cloudtrace/web"
)

// staticHandler 提供内嵌的前端产物，并对未知路径回退到 index.html。
//
// 回退是单页应用的必要行为：前端路由（/history、/settings 等）并不对应
// 真实文件，刷新页面时必须仍能拿到 index.html。
type staticHandler struct {
	root  fs.FS
	files http.Handler
}

// newStaticHandler 从内嵌产物构造静态资源处理器。
func newStaticHandler() (*staticHandler, error) {
	sub, err := fs.Sub(webassets.Dist, "dist")
	if err != nil {
		return nil, err
	}
	return &staticHandler{
		root:  sub,
		files: http.FileServer(http.FS(sub)),
	}, nil
}

func (h *staticHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		h.files.ServeHTTP(w, r)
		return
	}
	if f, err := h.root.Open(name); err == nil {
		_ = f.Close()
		h.files.ServeHTTP(w, r)
		return
	}
	h.serveIndex(w)
}

// serveIndex 输出 index.html。
func (h *staticHandler) serveIndex(w http.ResponseWriter) {
	if !h.serveNamed(w, "index.html") {
		http.Error(w, "前端产物缺失", http.StatusInternalServerError)
	}
}

// serveNamed 直接输出产物中的指定文件；文件不存在时返回 false。
func (h *staticHandler) serveNamed(w http.ResponseWriter, name string) bool {
	data, err := fs.ReadFile(h.root, name)
	if err != nil {
		return false
	}
	contentType := mime.TypeByExtension(path.Ext(name))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if strings.HasPrefix(contentType, "text/html") {
		contentType = "text/html; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
	return true
}

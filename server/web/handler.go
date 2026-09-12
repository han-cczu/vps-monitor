package web

import (
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

func init() {
	// Go 内置的 mime 表没有字体类型；scratch 容器里也没有 /etc/mime.types 可查。
	for ext, typ := range map[string]string{
		".woff":  "font/woff",
		".woff2": "font/woff2",
		".ttf":   "font/ttf",
		".ico":   "image/x-icon",
	} {
		_ = mime.AddExtensionType(ext, typ)
	}
}

// Handler 提供内嵌前端：命中静态文件就返回文件，否则回退到 index.html 交给前端路由。
//
// 带 hash 的 assets/* 打上一年的 immutable 缓存；index.html 永远 no-cache，
// 这样发新版后浏览器一刷新就拿到新的资源清单。
func Handler() http.Handler {
	return handlerFor(FS())
}

func handlerFor(dist fs.FS) http.Handler {
	index, indexErr := fs.ReadFile(dist, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name != "" && name != "index.html" {
			if st, err := fs.Stat(dist, name); err == nil && !st.IsDir() {
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				http.ServeFileFS(w, r, dist, name)
				return
			}
		}

		if indexErr != nil {
			http.Error(w, "frontend not built", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(index)
	})
}

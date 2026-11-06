// Package web 嵌入管理后台的前端静态资源并提供 HTTP 处理。
package web

import (
	"embed"
	"io"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed dist/*
var DistFS embed.FS

// Handler 返回用于提供 Web 管理后台静态资源和 SPA 路由支持的 http.Handler。
func Handler() http.Handler {
	sub, err := fs.Sub(DistFS, "dist")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Web UI 静态资源未找到", http.StatusInternalServerError)
		})
	}

	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/admin")
		cleanPath := strings.TrimPrefix(p, "/")

		// 如果访问的是根路径或 index.html，直接输出 index.html 内容，避免 http.FileServer 重定向循环
		if cleanPath == "" || cleanPath == "index.html" {
			serveIndex(w, r, sub)
			return
		}

		// 检查静态资源是否存在
		f, err := sub.Open(cleanPath)
		if err != nil {
			// 文件不存在时，SPA 路由回退输出 index.html
			serveIndex(w, r, sub)
			return
		}
		_ = f.Close()

		// 静态资源文件存在（如 js/css 等），重写路径后由 fileServer 正常服务
		r2 := new(http.Request)
		*r2 = *r
		r2.URL.Path = "/" + cleanPath
		fileServer.ServeHTTP(w, r2)
	})
}

// serveIndex 负责直接输出 index.html，不经过 http.FileServer 的重定向规则。
func serveIndex(w http.ResponseWriter, r *http.Request, fsys fs.FS) {
	f, err := fsys.Open("index.html")
	if err != nil {
		http.Error(w, "index.html not found", http.StatusNotFound)
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if rs, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, "index.html", fi.ModTime(), rs)
		return
	}

	data, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

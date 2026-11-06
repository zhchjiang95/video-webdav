// Package server 组装 HTTP 服务：Web 管理后台 -> 访问日志 -> 认证 -> 方法过滤 -> WebDAV。
package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/webdav"

	"video-server/internal/auth"
	"video-server/internal/config"
	"video-server/internal/tlscert"
	"video-server/internal/vfs"
	"video-server/web"
)

// Version 可以在启动时由 main 模块注入
var Version = "dev"

// allowedMethods 只读服务允许的 HTTP 方法
const allowedMethods = "OPTIONS, GET, HEAD, PROPFIND"

// Server 视频 WebDAV 与 Web 控制台复合服务。
type Server struct {
	cfgPath   string
	mu        sync.RWMutex
	cfg       *config.Config
	logger    *slog.Logger
	http      *http.Server
	guard     *auth.Guard
	fsProxy   *dynamicFS
	cancel    context.CancelFunc
	startTime time.Time
}

// New 根据配置创建服务（尚未开始监听）。
func New(cfg *config.Config, cfgPath string, logger *slog.Logger) (*Server, error) {
	guard := auth.New(cfg.Auth, cfg.Security, logger)
	fsProxy := newDynamicFS(vfs.New(cfg, logger))

	dav := &webdav.Handler{
		FileSystem: fsProxy,
		LockSystem: webdav.NewMemLS(),
		Logger: func(r *http.Request, err error) {
			if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, os.ErrPermission) {
				logger.Debug("WebDAV 处理出错", "method", r.Method, "path", r.URL.Path, "err", err)
			}
		},
	}

	s := &Server{
		cfgPath:   cfgPath,
		cfg:       cfg,
		logger:    logger,
		guard:     guard,
		fsProxy:   fsProxy,
		startTime: time.Now(),
	}

	// 组装 WebDAV 管道
	var davHandler http.Handler = dav
	davHandler = readOnly(davHandler)
	davHandler = guard.Middleware(davHandler)

	// 顶层多路复用路由器
	mux := http.NewServeMux()

	// 1. Web UI 静态资源处理
	mux.Handle("/admin", http.RedirectHandler("/admin/", http.StatusMovedPermanently))
	mux.Handle("/admin/", web.Handler())

	// 2. Web 管理后台 REST API
	mux.HandleFunc("/api/admin/", s.handleAdminAPI)

	// 3. WebDAV 业务处理与根路径贴心跳转
	rootHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 如果访问根路径 / 且是浏览器（Accept: text/html）未携带 Basic Auth，重定向到管理后台
		if r.URL.Path == "/" && r.Method == http.MethodGet &&
			strings.Contains(r.Header.Get("Accept"), "text/html") &&
			r.Header.Get("Authorization") == "" {
			http.Redirect(w, r, "/admin/", http.StatusFound)
			return
		}
		davHandler.ServeHTTP(w, r)
	})
	mux.Handle("/", rootHandler)

	var h http.Handler = mux
	if cfg.Log.AccessLog {
		h = accessLog(h, logger)
	}

	srv := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           h,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelDebug),
	}

	if cfg.Server.TLS.Enabled {
		reloader, err := tlscert.New(cfg.Server.TLS.CertFile, cfg.Server.TLS.KeyFile, logger)
		if err != nil {
			return nil, err
		}
		srv.TLSConfig = &tls.Config{
			MinVersion:     tls.VersionTLS12,
			GetCertificate: reloader.GetCertificate,
		}
	}

	s.http = srv
	return s, nil
}

// HTTPHandler 返回底层 HTTP 处理链，供测试或挂载使用。
func (s *Server) HTTPHandler() http.Handler {
	return s.http.Handler
}

// Start 开始监听端口（同步返回端口占用等错误），请求处理在后台进行。
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.cfg.Server.Listen)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go s.guard.RunCleanup(ctx)

	tlsOn := s.cfg.Server.TLS.Enabled
	go func() {
		var err error
		if tlsOn {
			err = s.http.ServeTLS(ln, "", "")
		} else {
			err = s.http.Serve(ln)
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("服务异常退出", "err", err)
		}
	}()

	scheme := "http"
	if tlsOn {
		scheme = "https"
	} else {
		s.logger.Warn("未启用 HTTPS，账号密码将以明文传输，不建议暴露到公网")
	}

	s.logger.Info("服务已启动", "listen", s.cfg.Server.Listen, "scheme", scheme)
	s.logger.Info("Web管理后台入口", "url", fmt.Sprintf("%s://localhost%s/admin/", scheme, s.cfg.Server.Listen))
	for _, d := range s.cfg.Dirs {
		s.logger.Info("视频目录", "name", d.Name, "path", d.Path)
	}
	return nil
}

// Shutdown 优雅关闭；超时后强制断开仍在播放的连接。
func (s *Server) Shutdown(ctx context.Context) error {
	if s.cancel != nil {
		s.cancel()
	}
	err := s.http.Shutdown(ctx)
	if errors.Is(err, context.DeadlineExceeded) {
		return s.http.Close()
	}
	return err
}

// readOnly 只放行只读方法，并限制 PROPFIND 深度。
func readOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead:
		case http.MethodOptions:
			w.Header().Set("Allow", allowedMethods)
			w.Header().Set("DAV", "1")
			w.Header().Set("MS-Author-Via", "DAV")
			w.WriteHeader(http.StatusOK)
			return
		case "PROPFIND":
			d := strings.TrimSpace(r.Header.Get("Depth"))
			if d == "" || strings.EqualFold(d, "infinity") {
				r.Header.Set("Depth", "1")
			}
		default:
			w.Header().Set("Allow", allowedMethods)
			http.Error(w, "Method Not Allowed (read-only)", http.StatusMethodNotAllowed)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// accessLog 记录每个请求。
func accessLog(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if sw.status == 0 {
			sw.status = http.StatusOK
		}

		// 管理后台静态资源请求（/admin/assets）无需繁琐刷屏 access_log
		if strings.HasPrefix(r.URL.Path, "/admin/assets") {
			return
		}

		attrs := []any{
			"ip", auth.ClientIP(r),
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"bytes", sw.bytes,
			"dur", time.Since(start).Round(time.Millisecond),
		}
		if rg := r.Header.Get("Range"); rg != "" {
			attrs = append(attrs, "range", rg)
		}
		attrs = append(attrs, "ua", r.UserAgent())
		logger.Info("请求", attrs...)
	})
}

// statusWriter 记录响应状态码和字节数。
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

func (w *statusWriter) ReadFrom(r io.Reader) (int64, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := io.Copy(w.ResponseWriter, r)
	w.bytes += n
	return n, err
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

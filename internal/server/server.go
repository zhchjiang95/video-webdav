// Package server 组装 HTTP 服务：访问日志 -> 认证 -> 方法过滤 -> WebDAV。
package server

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/net/webdav"

	"video-server/internal/auth"
	"video-server/internal/config"
	"video-server/internal/tlscert"
	"video-server/internal/vfs"
)

// allowedMethods 只读服务允许的 HTTP 方法
const allowedMethods = "OPTIONS, GET, HEAD, PROPFIND"

// Server 视频 WebDAV 服务。
type Server struct {
	cfg    *config.Config
	logger *slog.Logger
	http   *http.Server
	guard  *auth.Guard
	cancel context.CancelFunc
}

// New 根据配置创建服务（尚未开始监听）。
func New(cfg *config.Config, logger *slog.Logger) (*Server, error) {
	guard := auth.New(cfg.Auth, cfg.Security, logger)

	dav := &webdav.Handler{
		FileSystem: vfs.New(cfg, logger),
		LockSystem: webdav.NewMemLS(),
		Logger: func(r *http.Request, err error) {
			if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, os.ErrPermission) {
				logger.Debug("WebDAV 处理出错", "method", r.Method, "path", r.URL.Path, "err", err)
			}
		},
	}

	var h http.Handler = dav
	h = readOnly(h)
	h = guard.Middleware(h)
	if cfg.Log.AccessLog {
		h = accessLog(h, logger)
	}

	srv := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           h,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		// 公网上大量扫描器会产生 TLS 握手错误，降级为 debug 日志避免刷屏
		ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelDebug),
		// 注意：不设置 WriteTimeout，否则长时间播放视频会被强制断开
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

	return &Server{cfg: cfg, logger: logger, http: srv, guard: guard}, nil
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
			// 自行响应 OPTIONS，只声明只读能力（DAV 等级 1，不支持锁）
			w.Header().Set("Allow", allowedMethods)
			w.Header().Set("DAV", "1")
			w.Header().Set("MS-Author-Via", "DAV")
			w.WriteHeader(http.StatusOK)
			return
		case "PROPFIND":
			// 不带 Depth 或 Depth: infinity 会递归遍历整个目录树，开销巨大，统一降为 1
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

// ReadFrom 透传给底层 ResponseWriter，保留非 TLS 场景下的零拷贝发送能力。
func (w *statusWriter) ReadFrom(r io.Reader) (int64, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := io.Copy(w.ResponseWriter, r)
	w.bytes += n
	return n, err
}

// Unwrap 供 http.ResponseController 获取底层 ResponseWriter。
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

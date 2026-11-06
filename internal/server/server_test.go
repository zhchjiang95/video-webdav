package server_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"video-server/internal/config"
	"video-server/internal/server"
)

func TestRootAndAdminRouting(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.Dirs = []config.DirConfig{
		{Name: "test", Path: tmpDir},
	}
	cfg.Auth.Username = "admin"
	cfg.Auth.Password = "strongpassword123"

	srv, err := server.New(cfg, "", slog.New(slog.NewTextHandler(os.Stdout, nil)))
	if err != nil {
		t.Fatalf("创建服务失败: %v", err)
	}

	handler := srv.HTTPHandler()

	// 1. 浏览器访问根路径 / 应该 302 重定向到 /admin/
	reqRoot := httptest.NewRequest(http.MethodGet, "/", nil)
	reqRoot.Header.Set("Accept", "text/html,application/xhtml+xml")
	recRoot := httptest.NewRecorder()
	handler.ServeHTTP(recRoot, reqRoot)

	if recRoot.Code != http.StatusFound {
		t.Fatalf("根路径访问预期 302，实际返回 %d", recRoot.Code)
	}
	if loc := recRoot.Header().Get("Location"); loc != "/admin/" {
		t.Fatalf("重定向目标预期 /admin/，实际为 %s", loc)
	}

	// 2. 访问 /admin/ 必须返回 200 OK 且不能再重定向
	reqAdmin := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	recAdmin := httptest.NewRecorder()
	handler.ServeHTTP(recAdmin, reqAdmin)

	if recAdmin.Code != http.StatusOK {
		t.Fatalf("/admin/ 访问预期 200 OK，实际返回 %d (Location: %s)", recAdmin.Code, recAdmin.Header().Get("Location"))
	}
}


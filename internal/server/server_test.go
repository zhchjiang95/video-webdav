package server_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestAdminTestPathByTypes(t *testing.T) {
	tmpDir := t.TempDir()
	// 创建不同类型的文件（包含子目录）
	if err := os.WriteFile(filepath.Join(tmpDir, "a.mp4"), []byte("v"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmpDir, "Album"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "Album", "01.flac"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "Album", "02.mp3"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "Album", "01.lrc"), []byte("l"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "cover.png"), []byte("i"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Dirs = []config.DirConfig{{Name: "test", Path: tmpDir}}
	cfg.Auth.Username = "admin"
	cfg.Auth.Password = "strongpassword123"
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}

	srv, err := server.New(cfg, "", slog.New(slog.NewTextHandler(os.Stdout, nil)))
	if err != nil {
		t.Fatalf("创建服务失败: %v", err)
	}
	handler := srv.HTTPHandler()

	// 先登录获取管理员 Token
	loginBody, _ := json.Marshal(map[string]string{"username": "admin", "password": "strongpassword123"})
	loginReq := httptest.NewRequest(http.MethodPost, "/api/admin/login", bytes.NewReader(loginBody))
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("登录失败: %d %s", loginRec.Code, loginRec.Body.String())
	}
	var loginResp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginResp); err != nil || loginResp.Token == "" {
		t.Fatalf("解析登录 Token 失败: %v", err)
	}

	testCases := []struct {
		name      string
		types     []string
		wantCount int
	}{
		{name: "仅视频", types: []string{"video"}, wantCount: 1},
		{name: "仅音频（不含歌词计数）", types: []string{"audio"}, wantCount: 2},
		{name: "仅图片", types: []string{"image"}, wantCount: 1},
		{name: "音频+图片", types: []string{"audio", "image"}, wantCount: 3},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{"path": tmpDir, "types": tc.types})
			req := httptest.NewRequest(http.MethodPost, "/api/admin/test-path", bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+loginResp.Token)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("请求失败: %d %s", rec.Code, rec.Body.String())
			}
			var resp struct {
				Exists     bool     `json:"exists"`
				MatchCount int      `json:"match_count"`
				Samples    []string `json:"sample_files"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("解析响应失败: %v", err)
			}
			if !resp.Exists || resp.MatchCount != tc.wantCount {
				t.Fatalf("预期 match_count=%d，实际 %+v", tc.wantCount, resp)
			}
		})
	}
}


// Package server 内部实现 Web 管理后台的 REST API。
package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"video-server/internal/auth"
	"video-server/internal/config"
	"video-server/internal/vfs"
)

// adminLoginReq 登录请求结构
type adminLoginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// testPathReq 路径测试请求结构
type testPathReq struct {
	Path  string   `json:"path"`
	Types []string `json:"types"`
}

// unbanReq 解封 IP 请求结构
type unbanReq struct {
	IP string `json:"ip"`
}

// handleAdminAPI 分发管理后台的 API 请求。
func (s *Server) handleAdminAPI(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/admin")

	switch path {
	case "/login":
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		s.apiLogin(w, r)

	case "/logout":
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		s.apiLogout(w, r)

	case "/auth/check":
		s.apiAuthCheck(w, r)

	default:
		// 其余 API 均需要 Token 鉴权
		if !s.checkAdminAuth(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "未登录或登录已过期"})
			return
		}

		switch path {
		case "/status":
			s.apiStatus(w, r)
		case "/config":
			if r.Method == http.MethodGet {
				s.apiGetConfig(w, r)
			} else if r.Method == http.MethodPost {
				s.apiSaveConfig(w, r)
			} else {
				http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			}
		case "/test-path":
			if r.Method != http.MethodPost {
				http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
				return
			}
			s.apiTestPath(w, r)
		case "/banned-ips":
			s.apiBannedIPs(w, r)
		case "/unban":
			if r.Method != http.MethodPost {
				http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
				return
			}
			s.apiUnban(w, r)
		case "/logs":
			s.apiLogs(w, r)
		case "/restart":
			if r.Method != http.MethodPost {
				http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
				return
			}
			s.apiRestart(w, r)
		default:
			http.NotFound(w, r)
		}
	}
}

// checkAdminAuth 检查请求头或 Cookie 是否包含有效的管理员 Token。
func (s *Server) checkAdminAuth(r *http.Request) bool {
	// 优先检查 Authorization Header: Bearer <token>
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if s.guard.VerifyAdminToken(token) {
			return true
		}
	}

	// 其次检查 Cookie
	if cookie, err := r.Cookie("admin_token"); err == nil && cookie.Value != "" {
		if s.guard.VerifyAdminToken(cookie.Value) {
			return true
		}
	}

	return false
}

func (s *Server) apiLogin(w http.ResponseWriter, r *http.Request) {
	ip := auth.ClientIP(r)

	if until, banned := s.guard.BannedUntil(ip); banned {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error": fmt.Sprintf("登录失败次数过多，IP已被限制访问至 %s", until.Format("15:04:05")),
		})
		return
	}

	var req adminLoginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求参数格式错误"})
		return
	}

	if !s.guard.CheckCredentials(req.Username, req.Password) {
		banned := s.guard.RecordFailure(ip)
		if banned {
			s.logger.Warn("管理后台密码连续错误，已封禁 IP", "ip", ip, "user", req.Username)
			writeJSON(w, http.StatusTooManyRequests, map[string]any{
				"error": "密码错误次数过多，您的 IP 已被临时封禁！",
			})
		} else {
			s.logger.Warn("管理后台登录失败", "ip", ip, "user", req.Username)
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error": "用户名或密码错误",
			})
		}
		return
	}

	s.guard.RecordSuccess(ip)
	token := s.guard.CreateAdminToken(req.Username)

	http.SetCookie(w, &http.Cookie{
		Name:     "admin_token",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.Server.TLS.Enabled,
		MaxAge:   86400,
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"token":    token,
		"username": req.Username,
	})
}

func (s *Server) apiLogout(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "admin_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) apiAuthCheck(w http.ResponseWriter, r *http.Request) {
	authed := s.checkAdminAuth(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": authed,
	})
}

func (s *Server) apiStatus(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	uptimeSec := int(time.Since(s.startTime).Seconds())
	writeJSON(w, http.StatusOK, map[string]any{
		"version":       Version,
		"uptime":        uptimeSec,
		"uptime_str":    formatDuration(time.Since(s.startTime)),
		"listen":        s.cfg.Server.Listen,
		"tls":           s.cfg.Server.TLS.Enabled,
		"go_version":    runtime.Version(),
		"mem_alloc":     formatBytes(mem.Alloc),
		"mem_sys":       formatBytes(mem.Sys),
		"num_goroutine": runtime.NumGoroutine(),
		"dirs_count":    len(s.cfg.Dirs),
		"banned_count":  len(s.guard.BannedList()),
	})
}

func (s *Server) apiGetConfig(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 克隆配置，不向前端直接回传实际密码
	cfgCopy := *s.cfg
	cfgCopy.Auth.Password = ""

	writeJSON(w, http.StatusOK, cfgCopy)
}

func (s *Server) apiSaveConfig(w http.ResponseWriter, r *http.Request) {
	// 以默认配置作为底板反序列化，确保省略的新字段（如 audio_exts 等）不会变为空
	newCfg := *config.Default()
	if err := json.NewDecoder(r.Body).Decode(&newCfg); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "配置格式错误: " + err.Error()})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 如果密码留空，保留原有密码
	if strings.TrimSpace(newCfg.Auth.Password) == "" {
		newCfg.Auth.Password = s.cfg.Auth.Password
	}

	// 保持原配置所在目录
	newCfg.SetBaseDir(s.cfg.BaseDir())

	// 校验配置合法性
	if err := newCfg.Normalize(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "配置校验失败: " + err.Error()})
		return
	}

	// 检查是否修改了监听端口或 TLS 配置（这两个需要重启服务才能生效）
	restartRequired := newCfg.Server.Listen != s.cfg.Server.Listen ||
		newCfg.Server.TLS.Enabled != s.cfg.Server.TLS.Enabled ||
		newCfg.Server.TLS.CertFile != s.cfg.Server.TLS.CertFile ||
		newCfg.Server.TLS.KeyFile != s.cfg.Server.TLS.KeyFile

	// 持久化保存到配置文件
	if s.cfgPath != "" {
		if err := newCfg.Save(s.cfgPath); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存配置文件失败: " + err.Error()})
			return
		}
	}

	// 立即热更新内存中的配置、认证守卫与 VFS 文件系统
	s.cfg = &newCfg
	s.guard.Update(newCfg.Auth, newCfg.Security)
	s.fsProxy.Update(vfs.New(&newCfg, s.logger))

	s.logger.Info("管理员更新了服务配置", "restart_required", restartRequired)

	msg := "配置已成功保存并立即生效！"
	if restartRequired {
		msg = "配置已成功保存！由于修改了端口或HTTPS配置，需要重启服务后生效。"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"restart_required": restartRequired,
		"message":          msg,
	})
}

func (s *Server) apiTestPath(w http.ResponseWriter, r *http.Request) {
	var req testPathReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Path) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请提供有效的路径"})
		return
	}

	types, err := config.NormalizeFileTypes(req.Types)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "文件类型参数无效: " + err.Error()})
		return
	}

	cleanPath := filepath.Clean(req.Path)
	fi, err := os.Stat(cleanPath)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"exists": false,
			"error":  "路径不存在或不可访问: " + err.Error(),
		})
		return
	}
	if !fi.IsDir() {
		writeJSON(w, http.StatusOK, map[string]any{
			"exists": false,
			"error":  "该路径不是文件夹目录",
		})
		return
	}

	// 读取当前过滤配置（扩展名、排除规则、符号链接策略）
	s.mu.RLock()
	primaryExts, _ := s.cfg.Filter.MatchExts(types)
	excludes := append([]string(nil), s.cfg.Filter.ExcludeNames...)
	followSymlinks := s.cfg.Filter.FollowSymlinks
	s.mu.RUnlock()

	isExcluded := func(name string) bool {
		lower := strings.ToLower(name)
		for _, p := range excludes {
			if ok, _ := path.Match(p, lower); ok {
				return true
			}
		}
		return false
	}

	// 有界广度优先扫描（最多 3 层子目录、最多 100 个目录），既能发现子目录中的媒体文件，又避免大型 NAS 卡顿
	const (
		maxTestDepth = 3
		maxTestDirs  = 100
	)
	type dirTask struct {
		dir   string
		depth int
	}
	queue := []dirTask{{dir: cleanPath, depth: 0}}
	visitedDirs := 0
	matchCount := 0
	truncated := false
	samples := make([]string, 0, 5)

	for len(queue) > 0 {
		if visitedDirs >= maxTestDirs {
			truncated = true
			break
		}
		cur := queue[0]
		queue = queue[1:]
		visitedDirs++

		entries, readErr := os.ReadDir(cur.dir)
		if readErr != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if isExcluded(name) {
				continue
			}
			mode := e.Type()
			fullPath := filepath.Join(cur.dir, name)
			if mode&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
				if !followSymlinks {
					continue
				}
				resolved, statErr := os.Stat(fullPath)
				if statErr != nil {
					continue
				}
				mode = resolved.Mode()
			}
			if mode.IsDir() {
				if cur.depth < maxTestDepth {
					queue = append(queue, dirTask{dir: fullPath, depth: cur.depth + 1})
				} else {
					truncated = true
				}
				continue
			}
			if mode.IsRegular() {
				ext := strings.ToLower(filepath.Ext(name))
				if primaryExts[ext] {
					matchCount++
					if len(samples) < 5 {
						samples = append(samples, name)
					}
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"exists":       true,
		"is_dir":       true,
		"match_count":  matchCount,
		"video_count":  matchCount, // 兼容旧版前端字段名
		"truncated":    truncated,
		"types":        types,
		"sample_files": samples,
	})
}

func (s *Server) apiBannedIPs(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.guard.BannedList())
}

func (s *Server) apiUnban(w http.ResponseWriter, r *http.Request) {
	var req unbanReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.IP == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "参数无效"})
		return
	}

	s.guard.Unban(req.IP)
	s.logger.Info("管理员手动解封了 IP", "ip", req.IP)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) apiLogs(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	logFile := s.cfg.Log.File
	s.mu.RUnlock()

	if logFile == "" {
		writeJSON(w, http.StatusOK, map[string]any{"lines": []string{"未配置文件日志输出路径"}})
		return
	}

	file, err := os.Open(logFile)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"lines": []string{"日志文件尚不存在或无法打开: " + err.Error()}})
		return
	}
	defer file.Close()

	// 读取末尾 150 行
	lines := make([]string, 0)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > 150 {
			lines = lines[1:]
		}
	}

	// 逆序排列，让最新日志在最上方
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}

	writeJSON(w, http.StatusOK, map[string]any{"lines": lines})
}

func (s *Server) apiRestart(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"message": "重启指令已发出，服务将在 1 秒后重启...",
	})

	go func() {
		time.Sleep(800 * time.Millisecond)
		s.logger.Info("正在执行服务重启退出...")
		os.Exit(0)
	}()
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func formatDuration(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	secs := int(d.Seconds()) % 60

	if days > 0 {
		return fmt.Sprintf("%d天 %d小时 %d分", days, hours, mins)
	}
	if hours > 0 {
		return fmt.Sprintf("%d小时 %d分", hours, mins)
	}
	if mins > 0 {
		return fmt.Sprintf("%d分 %d秒", mins, secs)
	}
	return fmt.Sprintf("%d秒", secs)
}

func formatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}


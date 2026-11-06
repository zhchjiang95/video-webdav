// Package auth 提供 HTTP Basic 认证以及登录失败自动封禁 IP 的能力。
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"video-server/internal/config"
)

// BannedClient 包含被封禁客户端的信息。
type BannedClient struct {
	IP          string    `json:"ip"`
	BannedUntil time.Time `json:"banned_until"`
	DurationSec int       `json:"duration_sec"`
}

// Guard 认证守卫。
type Guard struct {
	userHash    [32]byte
	passHash    [32]byte
	rawUser     string
	maxFailures int
	window      time.Duration
	ban         time.Duration
	tokenSecret []byte
	logger      *slog.Logger

	mu      sync.RWMutex
	clients map[string]*clientState
}

type clientState struct {
	failures    int
	first       time.Time // 当前统计窗口的起始时间
	bannedUntil time.Time
}

// New 创建认证守卫。
func New(a config.AuthConfig, s config.SecurityConfig, logger *slog.Logger) *Guard {
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)

	return &Guard{
		userHash:    sha256.Sum256([]byte(a.Username)),
		passHash:    sha256.Sum256([]byte(a.Password)),
		rawUser:     a.Username,
		maxFailures: s.MaxFailures,
		window:      s.FailureWindow.Duration(),
		ban:         s.BanDuration.Duration(),
		tokenSecret: secret,
		logger:      logger,
		clients:     make(map[string]*clientState),
	}
}

// Update 更新认证凭据与安全策略。
func (g *Guard) Update(a config.AuthConfig, s config.SecurityConfig) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.userHash = sha256.Sum256([]byte(a.Username))
	g.passHash = sha256.Sum256([]byte(a.Password))
	g.rawUser = a.Username
	g.maxFailures = s.MaxFailures
	g.window = s.FailureWindow.Duration()
	g.ban = s.BanDuration.Duration()
}

// CheckCredentials 检查用户名和密码是否正确。
func (g *Guard) CheckCredentials(user, pass string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.check(user, pass)
}

// CreateAdminToken 为合法登录用户生成管理后台会话 Token（有效期 24 小时）。
func (g *Guard) CreateAdminToken(username string) string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	exp := time.Now().Add(24 * time.Hour).Unix()
	payload := fmt.Sprintf("%s:%d", username, exp)
	mac := hmac.New(sha256.New, g.tokenSecret)
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("%s:%s", payload, sig)
}

// VerifyAdminToken 校验管理后台会话 Token 是否有效。
func (g *Guard) VerifyAdminToken(token string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()

	parts := strings.Split(token, ":")
	if len(parts) != 3 {
		return false
	}
	user, expStr, sig := parts[0], parts[1], parts[2]
	if user != g.rawUser {
		return false
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}

	payload := fmt.Sprintf("%s:%s", user, expStr)
	mac := hmac.New(sha256.New, g.tokenSecret)
	mac.Write([]byte(payload))
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	return subtle.ConstantTimeCompare([]byte(sig), []byte(expectedSig)) == 1
}

// BannedList 返回当前所有正在被封禁的客户端 IP 列表。
func (g *Guard) BannedList() []BannedClient {
	g.mu.RLock()
	defer g.mu.RUnlock()

	now := time.Now()
	list := make([]BannedClient, 0)
	for ip, c := range g.clients {
		if now.Before(c.bannedUntil) {
			list = append(list, BannedClient{
				IP:          ip,
				BannedUntil: c.bannedUntil,
				DurationSec: int(time.Until(c.bannedUntil).Seconds()),
			})
		}
	}
	return list
}

// Unban 手动解封指定 IP。
func (g *Guard) Unban(ip string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.clients, ip)
}

// RunCleanup 定期清理过期的失败记录，直到 ctx 结束。
func (g *Guard) RunCleanup(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			g.mu.Lock()
			for ip, c := range g.clients {
				if now.After(c.bannedUntil) && now.Sub(c.first) > g.window {
					delete(g.clients, ip)
				}
			}
			g.mu.Unlock()
		}
	}
}

// Middleware 对所有请求做 Basic 认证。
func (g *Guard) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := ClientIP(r)

		if until, banned := g.BannedUntil(ip); banned {
			w.Header().Set("Retry-After", strconv.Itoa(int(time.Until(until).Seconds())+1))
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}

		user, pass, ok := r.BasicAuth()
		if !ok {
			// 未携带凭据：WebDAV 客户端通常先发一次匿名请求拿到 401 再带上凭据，不计入失败次数
			challenge(w)
			return
		}
		if !g.check(user, pass) {
			banned := g.RecordFailure(ip)
			if banned {
				g.logger.Warn("登录失败次数过多，已封禁 IP", "ip", ip, "user", user, "duration", g.ban)
			} else {
				g.logger.Warn("登录失败", "ip", ip, "user", user)
			}
			challenge(w)
			return
		}

		g.RecordSuccess(ip)
		next.ServeHTTP(w, r)
	})
}

// check 以常量时间比较用户名和密码，避免时序攻击。
func (g *Guard) check(user, pass string) bool {
	u := sha256.Sum256([]byte(user))
	p := sha256.Sum256([]byte(pass))
	uOK := subtle.ConstantTimeCompare(u[:], g.userHash[:])
	pOK := subtle.ConstantTimeCompare(p[:], g.passHash[:])
	return uOK&pOK == 1
}

// BannedUntil 查询 IP 是否正处于封禁状态。
func (g *Guard) BannedUntil(ip string) (time.Time, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	c, ok := g.clients[ip]
	if !ok || !time.Now().Before(c.bannedUntil) {
		return time.Time{}, false
	}
	return c.bannedUntil, true
}

// RecordFailure 记录一次登录失败，返回是否因此被封禁。
func (g *Guard) RecordFailure(ip string) bool {
	if g.maxFailures <= 0 {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	c, ok := g.clients[ip]
	if !ok || now.Sub(c.first) > g.window {
		c = &clientState{first: now}
		g.clients[ip] = c
	}
	c.failures++
	if c.failures >= g.maxFailures {
		c.bannedUntil = now.Add(g.ban)
		c.failures = 0
		c.first = now
		return true
	}
	return false
}

// RecordSuccess 记录一次登录成功，清除该 IP 的失败记录。
func (g *Guard) RecordSuccess(ip string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if c, ok := g.clients[ip]; ok && !time.Now().Before(c.bannedUntil) {
		delete(g.clients, ip)
	}
}

func challenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="Video Server", charset="UTF-8"`)
	http.Error(w, "Unauthorized", http.StatusUnauthorized)
}

// ClientIP 返回客户端 IP。
// 服务通过路由器端口转发直接暴露，不经过反向代理，因此只信任 TCP 连接的来源地址，
// 不读取 X-Forwarded-For 等可伪造的请求头。
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

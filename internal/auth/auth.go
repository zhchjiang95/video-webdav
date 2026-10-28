// Package auth 提供 HTTP Basic 认证以及登录失败自动封禁 IP 的能力。
package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"video-server/internal/config"
)

// Guard 认证守卫。
type Guard struct {
	userHash    [32]byte
	passHash    [32]byte
	maxFailures int
	window      time.Duration
	ban         time.Duration
	logger      *slog.Logger

	mu      sync.Mutex
	clients map[string]*clientState
}

type clientState struct {
	failures    int
	first       time.Time // 当前统计窗口的起始时间
	bannedUntil time.Time
}

// New 创建认证守卫。
func New(a config.AuthConfig, s config.SecurityConfig, logger *slog.Logger) *Guard {
	return &Guard{
		userHash:    sha256.Sum256([]byte(a.Username)),
		passHash:    sha256.Sum256([]byte(a.Password)),
		maxFailures: s.MaxFailures,
		window:      s.FailureWindow,
		ban:         s.BanDuration,
		logger:      logger,
		clients:     make(map[string]*clientState),
	}
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

		if until, banned := g.bannedUntil(ip); banned {
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
			banned := g.recordFailure(ip)
			if banned {
				g.logger.Warn("登录失败次数过多，已封禁 IP", "ip", ip, "user", user, "duration", g.ban)
			} else {
				g.logger.Warn("登录失败", "ip", ip, "user", user)
			}
			challenge(w)
			return
		}

		g.recordSuccess(ip)
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

func (g *Guard) bannedUntil(ip string) (time.Time, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	c, ok := g.clients[ip]
	if !ok || !time.Now().Before(c.bannedUntil) {
		return time.Time{}, false
	}
	return c.bannedUntil, true
}

// recordFailure 记录一次失败，返回是否因此被封禁。
func (g *Guard) recordFailure(ip string) bool {
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

func (g *Guard) recordSuccess(ip string) {
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

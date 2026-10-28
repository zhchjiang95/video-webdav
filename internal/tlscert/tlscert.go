// Package tlscert 负责加载 HTTPS 证书，并在证书文件更新后自动热加载。
package tlscert

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

// checkInterval 检查证书文件是否变化的最小间隔
const checkInterval = time.Minute

// expireWarnDays 证书剩余有效期少于该天数时输出警告
const expireWarnDays = 15

// Reloader 证书热加载器。
// 腾讯云免费证书有效期较短，替换证书文件后无需重启服务，一分钟内自动生效。
type Reloader struct {
	certFile string
	keyFile  string
	logger   *slog.Logger

	mu        sync.Mutex
	cert      *tls.Certificate
	certMod   time.Time
	keyMod    time.Time
	lastCheck time.Time
}

// New 加载证书，失败时返回错误。
func New(certFile, keyFile string, logger *slog.Logger) (*Reloader, error) {
	r := &Reloader{certFile: certFile, keyFile: keyFile, logger: logger, lastCheck: time.Now()}
	if err := r.load(); err != nil {
		return nil, err
	}
	return r, nil
}

// GetCertificate 供 tls.Config 使用。
func (r *Reloader) GetCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if time.Since(r.lastCheck) >= checkInterval {
		r.lastCheck = time.Now()
		if modTime(r.certFile) != r.certMod || modTime(r.keyFile) != r.keyMod {
			if err := r.load(); err != nil {
				// 新证书有问题时继续使用旧证书
				r.logger.Error("证书文件已变化但重新加载失败，继续使用旧证书", "err", err)
				r.certMod, r.keyMod = modTime(r.certFile), modTime(r.keyFile)
			}
		}
	}
	return r.cert, nil
}

// load 读取证书文件（调用方需持有锁或处于初始化阶段）。
func (r *Reloader) load() error {
	certMod, keyMod := modTime(r.certFile), modTime(r.keyFile)

	c, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return fmt.Errorf("加载证书失败: %w", err)
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		return fmt.Errorf("解析证书失败: %w", err)
	}
	c.Leaf = leaf

	r.cert = &c
	r.certMod, r.keyMod = certMod, keyMod

	left := time.Until(leaf.NotAfter)
	r.logger.Info("证书已加载",
		"domains", leaf.DNSNames,
		"not_after", leaf.NotAfter.Local().Format("2006-01-02 15:04:05"),
		"days_left", int(left.Hours()/24),
	)
	if left < 0 {
		r.logger.Error("证书已过期，请尽快更换", "not_after", leaf.NotAfter)
	} else if left < expireWarnDays*24*time.Hour {
		r.logger.Warn("证书即将过期，请尽快更换", "days_left", int(left.Hours()/24))
	}
	return nil
}

func modTime(p string) time.Time {
	fi, err := os.Stat(p)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

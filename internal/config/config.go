// Package config 负责加载、补全默认值并校验配置文件。
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 是服务的完整配置。
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Auth     AuthConfig     `yaml:"auth"`
	Security SecurityConfig `yaml:"security"`
	Dirs     []DirConfig    `yaml:"dirs"`
	Filter   FilterConfig   `yaml:"filter"`
	Log      LogConfig      `yaml:"log"`

	// baseDir 为配置文件所在目录，用于解析配置中的相对路径（不从 yaml 读取）
	baseDir string
}

// ServerConfig 监听与 TLS 配置。
type ServerConfig struct {
	// Listen 监听地址，例如 ":8443"
	Listen string    `yaml:"listen"`
	TLS    TLSConfig `yaml:"tls"`
}

// TLSConfig HTTPS 证书配置。
type TLSConfig struct {
	Enabled  bool   `yaml:"enabled"`
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// AuthConfig 固定的用户名密码。
type AuthConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// SecurityConfig 登录失败封禁策略。
type SecurityConfig struct {
	// MaxFailures 在 FailureWindow 时间窗口内允许的最大失败次数，<=0 表示不封禁
	MaxFailures   int           `yaml:"max_failures"`
	FailureWindow time.Duration `yaml:"failure_window"`
	BanDuration   time.Duration `yaml:"ban_duration"`
}

// DirConfig 一个对外暴露的视频目录。
type DirConfig struct {
	// Name 在 WebDAV 根目录下显示的文件夹名
	Name string `yaml:"name"`
	// Path 本地磁盘上的绝对路径
	Path string `yaml:"path"`
}

// FilterConfig 文件过滤规则。
type FilterConfig struct {
	VideoExts    []string `yaml:"video_exts"`
	SubtitleExts []string `yaml:"subtitle_exts"`
	// ExtraExts 额外允许暴露的扩展名（如 .nfo、.jpg），默认为空
	ExtraExts []string `yaml:"extra_exts"`
	// ExcludeNames 需要隐藏的文件/目录名（支持通配符，不区分大小写）
	ExcludeNames []string `yaml:"exclude_names"`
	// HideEmptyDirs 隐藏不包含任何视频文件的目录
	HideEmptyDirs bool `yaml:"hide_empty_dirs"`
	// EmptyDirCacheTTL “目录是否包含视频”判断结果的缓存时间
	EmptyDirCacheTTL time.Duration `yaml:"empty_dir_cache_ttl"`
	// FollowSymlinks 是否跟随符号链接/目录联接（junction），默认关闭以防越权访问
	FollowSymlinks bool `yaml:"follow_symlinks"`
}

// LogConfig 日志配置。
type LogConfig struct {
	// File 日志文件路径，相对路径基于配置文件所在目录，留空则只输出到控制台
	File string `yaml:"file"`
	// Level 日志级别：debug / info / warn / error
	Level string `yaml:"level"`
	// AccessLog 是否记录每个请求
	AccessLog bool `yaml:"access_log"`
}

// Default 返回带默认值的配置。
func Default() *Config {
	return &Config{
		Server: ServerConfig{Listen: ":8443"},
		Security: SecurityConfig{
			MaxFailures:   5,
			FailureWindow: 10 * time.Minute,
			BanDuration:   30 * time.Minute,
		},
		Filter: FilterConfig{
			VideoExts: []string{
				".mkv", ".mp4", ".m4v", ".avi", ".mov", ".wmv", ".flv", ".f4v", ".webm",
				".ts", ".m2ts", ".mts", ".mpg", ".mpeg", ".vob", ".rmvb", ".rm", ".3gp", ".iso",
			},
			SubtitleExts: []string{".srt", ".ass", ".ssa", ".sup", ".vtt", ".sub", ".idx"},
			ExcludeNames: []string{
				".*", "$*", "System Volume Information", "@eaDir", "#recycle", "#snapshot",
			},
			HideEmptyDirs:    true,
			EmptyDirCacheTTL: 2 * time.Minute,
		},
		Log: LogConfig{
			File:      "logs/video-server.log",
			Level:     "info",
			AccessLog: true,
		},
	}
}

// Load 读取并校验配置文件。
func Load(path string) (*Config, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("解析配置文件路径失败: %w", err)
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	// 先填充默认值，yaml 中未出现的字段保持默认
	cfg := Default()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}
	cfg.baseDir = filepath.Dir(absPath)

	if err := cfg.normalize(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// ResolvePath 将相对路径解析为相对配置文件所在目录的绝对路径。
func (c *Config) ResolvePath(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.baseDir, p)
}

// normalize 规范化并校验配置项。
func (c *Config) normalize() error {
	var errs []error

	if strings.TrimSpace(c.Server.Listen) == "" {
		errs = append(errs, errors.New("server.listen 不能为空"))
	}

	if c.Auth.Username == "" || c.Auth.Password == "" {
		errs = append(errs, errors.New("auth.username 和 auth.password 不能为空"))
	} else if len(c.Auth.Password) < 8 {
		errs = append(errs, errors.New("auth.password 长度至少 8 位（服务会暴露在公网，请使用强密码）"))
	}

	// TLS 证书路径
	if c.Server.TLS.Enabled {
		c.Server.TLS.CertFile = c.ResolvePath(c.Server.TLS.CertFile)
		c.Server.TLS.KeyFile = c.ResolvePath(c.Server.TLS.KeyFile)
		for _, f := range []string{c.Server.TLS.CertFile, c.Server.TLS.KeyFile} {
			if f == "" {
				errs = append(errs, errors.New("启用 TLS 时 cert_file 和 key_file 不能为空"))
				break
			}
			if _, err := os.Stat(f); err != nil {
				errs = append(errs, fmt.Errorf("证书文件不可用: %s (%v)", f, err))
			}
		}
	}

	// 视频目录
	if len(c.Dirs) == 0 {
		errs = append(errs, errors.New("dirs 至少需要配置一个视频目录"))
	}
	seen := make(map[string]bool)
	for i := range c.Dirs {
		d := &c.Dirs[i]
		d.Name = strings.TrimSpace(d.Name)
		if d.Name == "" {
			errs = append(errs, fmt.Errorf("dirs[%d].name 不能为空", i))
			continue
		}
		if d.Name == "." || d.Name == ".." || strings.ContainsAny(d.Name, `/\:*?"<>|`) {
			errs = append(errs, fmt.Errorf("dirs[%d].name 包含非法字符: %q", i, d.Name))
			continue
		}
		key := strings.ToLower(d.Name)
		if seen[key] {
			errs = append(errs, fmt.Errorf("dirs[%d].name 重复: %q", i, d.Name))
			continue
		}
		seen[key] = true

		if !filepath.IsAbs(d.Path) {
			errs = append(errs, fmt.Errorf("dirs[%d].path 必须是绝对路径: %q", i, d.Path))
			continue
		}
		d.Path = filepath.Clean(d.Path)
		fi, err := os.Stat(d.Path)
		if err != nil {
			errs = append(errs, fmt.Errorf("dirs[%d].path 不可访问: %q (%v)", i, d.Path, err))
			continue
		}
		if !fi.IsDir() {
			errs = append(errs, fmt.Errorf("dirs[%d].path 不是目录: %q", i, d.Path))
		}
	}

	// 扩展名统一为小写并带点
	c.Filter.VideoExts = normalizeExts(c.Filter.VideoExts)
	c.Filter.SubtitleExts = normalizeExts(c.Filter.SubtitleExts)
	c.Filter.ExtraExts = normalizeExts(c.Filter.ExtraExts)
	if len(c.Filter.VideoExts) == 0 {
		errs = append(errs, errors.New("filter.video_exts 不能为空"))
	}
	for i, p := range c.Filter.ExcludeNames {
		c.Filter.ExcludeNames[i] = strings.ToLower(strings.TrimSpace(p))
	}
	if c.Filter.EmptyDirCacheTTL <= 0 {
		c.Filter.EmptyDirCacheTTL = 2 * time.Minute
	}

	if c.Security.FailureWindow <= 0 {
		c.Security.FailureWindow = 10 * time.Minute
	}
	if c.Security.BanDuration <= 0 {
		c.Security.BanDuration = 30 * time.Minute
	}

	c.Log.File = c.ResolvePath(c.Log.File)

	return errors.Join(errs...)
}

// normalizeExts 将扩展名转为小写、补全前导点并去重。
func normalizeExts(exts []string) []string {
	out := make([]string, 0, len(exts))
	seen := make(map[string]bool)
	for _, e := range exts {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	return out
}

// Package config 负责加载、补全默认值并校验配置文件。
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration 包装 time.Duration，兼容在 JSON/YAML 中以友好字符串（如 "10m", "30m"）读写。
type Duration time.Duration

func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		td, err := time.ParseDuration(s)
		if err != nil {
			return err
		}
		*d = Duration(td)
		return nil
	}
	var num float64
	if err := json.Unmarshal(b, &num); err == nil {
		*d = Duration(time.Duration(num))
		return nil
	}
	return errors.New("无效的时长格式")
}

func (d Duration) MarshalYAML() (any, error) {
	return time.Duration(d).String(), nil
}

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err == nil {
		td, err := time.ParseDuration(s)
		if err != nil {
			return err
		}
		*d = Duration(td)
		return nil
	}
	var num int64
	if err := node.Decode(&num); err == nil {
		*d = Duration(time.Duration(num))
		return nil
	}
	return errors.New("无法解析时长格式")
}

// Config 是服务的完整配置。
type Config struct {
	Server   ServerConfig   `yaml:"server" json:"server"`
	Auth     AuthConfig     `yaml:"auth" json:"auth"`
	Security SecurityConfig `yaml:"security" json:"security"`
	Dirs     []DirConfig    `yaml:"dirs" json:"dirs"`
	Filter   FilterConfig   `yaml:"filter" json:"filter"`
	Log      LogConfig      `yaml:"log" json:"log"`

	// baseDir 为配置文件所在目录，用于解析配置中的相对路径（不从 yaml 读取）
	baseDir string
}

// ServerConfig 监听与 TLS 配置。
type ServerConfig struct {
	// Listen 监听地址，例如 ":8443"
	Listen string    `yaml:"listen" json:"listen"`
	TLS    TLSConfig `yaml:"tls" json:"tls"`
}

// TLSConfig HTTPS 证书配置。
type TLSConfig struct {
	Enabled  bool   `yaml:"enabled" json:"enabled"`
	CertFile string `yaml:"cert_file" json:"cert_file"`
	KeyFile  string `yaml:"key_file" json:"key_file"`
}

// AuthConfig 固定的用户名密码。
type AuthConfig struct {
	Username string `yaml:"username" json:"username"`
	Password string `yaml:"password" json:"password"`
}

// SecurityConfig 登录失败封禁策略。
type SecurityConfig struct {
	// MaxFailures 在 FailureWindow 时间窗口内允许的最大失败次数，<=0 表示不封禁
	MaxFailures   int      `yaml:"max_failures" json:"max_failures"`
	FailureWindow Duration `yaml:"failure_window" json:"failure_window"`
	BanDuration   Duration `yaml:"ban_duration" json:"ban_duration"`
}

// 支持的目录文件类型常量。
const (
	FileTypeVideo = "video"
	FileTypeAudio = "audio"
	FileTypeImage = "image"
)

// ValidFileTypes 所有受支持的文件类型（按展示顺序排列）。
var ValidFileTypes = []string{FileTypeVideo, FileTypeAudio, FileTypeImage}

// DirConfig 一个对外暴露的媒体目录。
type DirConfig struct {
	// Name 在 WebDAV 根目录下显示的文件夹名
	Name string `yaml:"name" json:"name"`
	// Path 本地磁盘上的绝对路径
	Path string `yaml:"path" json:"path"`
	// Types 该目录只扫描并暴露的文件类型（支持 video / audio / image，多选；留空默认 ["video"]）
	Types []string `yaml:"types" json:"types"`
}

// FilterConfig 文件过滤规则。
type FilterConfig struct {
	VideoExts    []string `yaml:"video_exts" json:"video_exts"`
	SubtitleExts []string `yaml:"subtitle_exts" json:"subtitle_exts"`
	AudioExts    []string `yaml:"audio_exts" json:"audio_exts"`
	LyricExts    []string `yaml:"lyric_exts" json:"lyric_exts"`
	ImageExts    []string `yaml:"image_exts" json:"image_exts"`
	// ExtraExts 额外允许暴露的扩展名（如 .nfo），对所有目录生效，默认为空
	ExtraExts []string `yaml:"extra_exts" json:"extra_exts"`
	// ExcludeNames 需要隐藏的文件/目录名（支持通配符，不区分大小写）
	ExcludeNames []string `yaml:"exclude_names" json:"exclude_names"`
	// HideEmptyDirs 隐藏不包含任何所选主媒体文件的目录
	HideEmptyDirs bool `yaml:"hide_empty_dirs" json:"hide_empty_dirs"`
	// EmptyDirCacheTTL “目录是否包含媒体文件”判断结果的缓存时间
	EmptyDirCacheTTL Duration `yaml:"empty_dir_cache_ttl" json:"empty_dir_cache_ttl"`
	// FollowSymlinks 是否跟随符号链接/目录联接（junction），默认关闭以防越权访问
	FollowSymlinks bool `yaml:"follow_symlinks" json:"follow_symlinks"`
}

// LogConfig 日志配置。
type LogConfig struct {
	// File 日志文件路径，相对路径基于配置文件所在目录，留空则只输出到控制台
	File string `yaml:"file" json:"file"`
	// Level 日志级别：debug / info / warn / error
	Level string `yaml:"level" json:"level"`
	// AccessLog 是否记录每个请求
	AccessLog bool `yaml:"access_log" json:"access_log"`
}

// Default 返回带默认值的配置。
func Default() *Config {
	return &Config{
		Server: ServerConfig{Listen: ":8443"},
		Security: SecurityConfig{
			MaxFailures:   5,
			FailureWindow: Duration(10 * time.Minute),
			BanDuration:   Duration(30 * time.Minute),
		},
		Filter: FilterConfig{
			VideoExts: []string{
				".mkv", ".mp4", ".m4v", ".avi", ".mov", ".wmv", ".flv", ".f4v", ".webm",
				".ts", ".m2ts", ".mts", ".mpg", ".mpeg", ".vob", ".rmvb", ".rm", ".3gp", ".iso",
			},
			SubtitleExts: []string{".srt", ".ass", ".ssa", ".sup", ".vtt", ".sub", ".idx"},
			AudioExts: []string{
				".mp3", ".flac", ".wav", ".aac", ".m4a", ".ogg", ".opus", ".wma",
				".ape", ".alac", ".aiff", ".aif", ".dsf", ".dff", ".wv", ".mka",
			},
			LyricExts: []string{".lrc", ".cue"},
			ImageExts: []string{
				".jpg", ".jpeg", ".png", ".webp", ".gif", ".bmp",
				".heic", ".heif", ".avif", ".tif", ".tiff", ".svg",
			},
			ExcludeNames: []string{
				".*", "$*", "System Volume Information", "@eaDir", "#recycle", "#snapshot",
			},
			HideEmptyDirs:    true,
			EmptyDirCacheTTL: Duration(2 * time.Minute),
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

	if err := cfg.Normalize(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// BaseDir 返回配置文件所在的基准目录。
func (c *Config) BaseDir() string {
	return c.baseDir
}

// SetBaseDir 设置配置文件所在的基准目录。
func (c *Config) SetBaseDir(dir string) {
	c.baseDir = dir
}

// Save 将当前配置保存到指定的文件路径中（原子写入）。
func (c *Config) Save(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("解析配置文件保存路径失败: %w", err)
	}

	c.baseDir = filepath.Dir(absPath)
	if err := c.Normalize(); err != nil {
		return fmt.Errorf("配置校验不通过: %w", err)
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}

	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}

	tmpFile := filepath.Join(dir, fmt.Sprintf(".config.tmp.%d", time.Now().UnixNano()))
	if err := os.WriteFile(tmpFile, data, 0o600); err != nil {
		return fmt.Errorf("写入临时配置文件失败: %w", err)
	}

	if err := os.Rename(tmpFile, absPath); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("保存配置文件失败: %w", err)
	}

	return nil
}

// EnsureDefaultConfig 若配置文件不存在，则在指定路径生成一份开箱即用的默认 HTTP 配置。
// 已存在时不做任何修改，返回 created=false。
func EnsureDefaultConfig(path string) (bool, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false, fmt.Errorf("解析配置文件路径失败: %w", err)
	}
	if _, err := os.Stat(absPath); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("检查配置文件失败: %w", err)
	}

	cfg := Default()
	cfg.Auth.Username = "admin"
	cfg.Auth.Password = "admin123456"
	cfg.Dirs = []DirConfig{}
	if err := cfg.Save(absPath); err != nil {
		return false, err
	}
	return true, nil
}

// ResolvePath 将相对路径解析为相对配置文件所在目录的绝对路径。
func (c *Config) ResolvePath(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.baseDir, p)
}

// Normalize 规范化并校验配置项。
func (c *Config) Normalize() error {
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

	// 媒体目录（允许初始为空，方便开箱启动后通过 Web 管理后台图形化添加）
	seen := make(map[string]bool)
	usedTypes := make(map[string]bool)
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
		lower := strings.ToLower(d.Name)
		if lower == "admin" || lower == "api" {
			errs = append(errs, fmt.Errorf("dirs[%d].name 不能使用系统保留名称 %q", i, d.Name))
			continue
		}
		key := lower
		if seen[key] {
			errs = append(errs, fmt.Errorf("dirs[%d].name 重复: %q", i, d.Name))
			continue
		}
		seen[key] = true

		// 规范化并校验目录文件类型（留空时向后兼容为 ["video"]）
		normTypes, typeErr := NormalizeFileTypes(d.Types)
		if typeErr != nil {
			errs = append(errs, fmt.Errorf("dirs[%d].types 无效: %v", i, typeErr))
		} else {
			d.Types = normTypes
			for _, t := range normTypes {
				usedTypes[t] = true
			}
		}

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
	c.Filter.AudioExts = normalizeExts(c.Filter.AudioExts)
	c.Filter.LyricExts = normalizeExts(c.Filter.LyricExts)
	c.Filter.ImageExts = normalizeExts(c.Filter.ImageExts)
	c.Filter.ExtraExts = normalizeExts(c.Filter.ExtraExts)
	if usedTypes[FileTypeVideo] && len(c.Filter.VideoExts) == 0 {
		errs = append(errs, errors.New("已启用视频类型目录，filter.video_exts 不能为空"))
	}
	if usedTypes[FileTypeAudio] && len(c.Filter.AudioExts) == 0 {
		errs = append(errs, errors.New("已启用音频类型目录，filter.audio_exts 不能为空"))
	}
	if usedTypes[FileTypeImage] && len(c.Filter.ImageExts) == 0 {
		errs = append(errs, errors.New("已启用图片类型目录，filter.image_exts 不能为空"))
	}
	for i, p := range c.Filter.ExcludeNames {
		c.Filter.ExcludeNames[i] = strings.ToLower(strings.TrimSpace(p))
	}
	if c.Filter.EmptyDirCacheTTL <= 0 {
		c.Filter.EmptyDirCacheTTL = Duration(2 * time.Minute)
	}

	if c.Security.FailureWindow <= 0 {
		c.Security.FailureWindow = Duration(10 * time.Minute)
	}
	if c.Security.BanDuration <= 0 {
		c.Security.BanDuration = Duration(30 * time.Minute)
	}

	c.Log.File = c.ResolvePath(c.Log.File)

	return errors.Join(errs...)
}

// NormalizeFileTypes 规范化并校验目录的文件类型列表；为空时默认返回 ["video"] 以兼容旧配置。
func NormalizeFileTypes(types []string) ([]string, error) {
	if len(types) == 0 {
		return []string{FileTypeVideo}, nil
	}
	out := make([]string, 0, len(types))
	seen := make(map[string]bool)
	for _, raw := range types {
		t := strings.ToLower(strings.TrimSpace(raw))
		if t == "" {
			continue
		}
		switch t {
		case FileTypeVideo, FileTypeAudio, FileTypeImage:
			if !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		default:
			return nil, fmt.Errorf("不支持的文件类型 %q（仅支持 video / audio / image）", raw)
		}
	}
	if len(out) == 0 {
		return []string{FileTypeVideo}, nil
	}
	return out, nil
}

// MatchExts 根据目录选择的文件类型，返回主媒体扩展名集合（用于隐藏空目录判断和扫描计数）
// 以及全部允许暴露的扩展名集合（主媒体 + 对应伴随文件 + 全局 extra_exts）。
func (f FilterConfig) MatchExts(types []string) (primary map[string]bool, allowed map[string]bool) {
	primary = make(map[string]bool)
	allowed = make(map[string]bool)

	normTypes, err := NormalizeFileTypes(types)
	if err != nil {
		normTypes = []string{FileTypeVideo}
	}

	addAll := func(dst map[string]bool, exts []string) {
		for _, e := range normalizeExts(exts) {
			dst[e] = true
		}
	}

	for _, t := range normTypes {
		switch t {
		case FileTypeVideo:
			addAll(primary, f.VideoExts)
			addAll(allowed, f.VideoExts)
			addAll(allowed, f.SubtitleExts)
		case FileTypeAudio:
			addAll(primary, f.AudioExts)
			addAll(allowed, f.AudioExts)
			addAll(allowed, f.LyricExts)
		case FileTypeImage:
			addAll(primary, f.ImageExts)
			addAll(allowed, f.ImageExts)
		}
	}

	// 全局 extra_exts 作为伴随/元数据文件对所有目录生效
	addAll(allowed, f.ExtraExts)
	return primary, allowed
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

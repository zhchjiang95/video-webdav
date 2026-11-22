package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"video-server/internal/config"
)

func TestConfigJSONMarshalling(t *testing.T) {
	cfg := config.Default()
	cfg.Dirs = []config.DirConfig{
		{Name: "Movies", Path: "/movies", Types: []string{config.FileTypeVideo}},
		{Name: "Music", Path: "/music", Types: []string{config.FileTypeAudio}},
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("序列化配置为 JSON 失败: %v", err)
	}

	jsonStr := string(data)

	// 确认字段名称均为小写驼峰/下划线，以符合前端契约
	expectedKeys := []string{
		`"server":`,
		`"auth":`,
		`"security":`,
		`"dirs":`,
		`"types":`,
		`"filter":`,
		`"audio_exts":`,
		`"lyric_exts":`,
		`"image_exts":`,
		`"log":`,
		`"max_failures":`,
		`"failure_window":"10m0s"`,
		`"ban_duration":"30m0s"`,
	}

	for _, k := range expectedKeys {
		if !strings.Contains(jsonStr, k) {
			t.Fatalf("JSON 输出缺少预期字段 %s: %s", k, jsonStr)
		}
	}

	// 测试从前端传入的 JSON 反序列化
	sampleInput := `{
		"server": {"listen": ":9000", "tls": {"enabled": false, "cert_file": "", "key_file": ""}},
		"auth": {"username": "admin", "password": "newpassword123"},
		"security": {"max_failures": 5, "failure_window": "15m", "ban_duration": "45m"},
		"dirs": [{"name": "Movies", "path": "/movies", "types": ["video", "audio"]}],
		"filter": {
			"video_exts": [".mkv", ".mp4"],
			"subtitle_exts": [".srt"],
			"audio_exts": [".flac", ".mp3"],
			"lyric_exts": [".lrc"],
			"image_exts": [".jpg", ".png"],
			"extra_exts": [],
			"exclude_names": [".*"],
			"hide_empty_dirs": true,
			"empty_dir_cache_ttl": "5m",
			"follow_symlinks": false
		},
		"log": {"file": "logs/test.log", "level": "info", "access_log": true}
	}`

	var unmarshaled config.Config
	if err := json.Unmarshal([]byte(sampleInput), &unmarshaled); err != nil {
		t.Fatalf("反序列化 JSON 配置失败: %v", err)
	}

	if unmarshaled.Security.FailureWindow.Duration() != 15*time.Minute {
		t.Fatalf("FailureWindow 解析有误: %v", unmarshaled.Security.FailureWindow)
	}
	if unmarshaled.Security.BanDuration.Duration() != 45*time.Minute {
		t.Fatalf("BanDuration 解析有误: %v", unmarshaled.Security.BanDuration)
	}
	if unmarshaled.Filter.EmptyDirCacheTTL.Duration() != 5*time.Minute {
		t.Fatalf("EmptyDirCacheTTL 解析有误: %v", unmarshaled.Filter.EmptyDirCacheTTL)
	}
	if len(unmarshaled.Dirs) != 1 || len(unmarshaled.Dirs[0].Types) != 2 {
		t.Fatalf("Dirs.Types 解析有误: %+v", unmarshaled.Dirs)
	}
}

func TestNormalizeDirTypesAndCompat(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()

	// 1. 旧配置未写 types 时，默认回填 ["video"]
	cfg := config.Default()
	cfg.Auth.Username = "admin"
	cfg.Auth.Password = "strongpass123"
	cfg.Dirs = []config.DirConfig{
		{Name: "Legacy", Path: dirA},
		{Name: "Mixed", Path: dirB, Types: []string{" AUDIO ", "image", "audio"}},
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatalf("合法配置校验失败: %v", err)
	}
	if len(cfg.Dirs[0].Types) != 1 || cfg.Dirs[0].Types[0] != config.FileTypeVideo {
		t.Fatalf("旧配置默认 types 预期 [video]，实际 %v", cfg.Dirs[0].Types)
	}
	if len(cfg.Dirs[1].Types) != 2 || cfg.Dirs[1].Types[0] != config.FileTypeAudio || cfg.Dirs[1].Types[1] != config.FileTypeImage {
		t.Fatalf("规范化 types 预期 [audio image]，实际 %v", cfg.Dirs[1].Types)
	}

	// 2. 非法类型应报错
	badCfg := config.Default()
	badCfg.Auth.Username = "admin"
	badCfg.Auth.Password = "strongpass123"
	badCfg.Dirs = []config.DirConfig{
		{Name: "Bad", Path: dirA, Types: []string{"unknown"}},
	}
	if err := badCfg.Normalize(); err == nil {
		t.Fatalf("非法文件类型预期校验失败，但返回 nil")
	}

	// 3. 允许 dirs 为空启动（开箱即用，方便启动后在管理后台可视化添加目录）
	emptyDirs := config.Default()
	emptyDirs.Auth.Username = "admin"
	emptyDirs.Auth.Password = "strongpass123"
	emptyDirs.Dirs = nil
	if err := emptyDirs.Normalize(); err != nil {
		t.Fatalf("空 dirs 列表预期允许启动，实际报错: %v", err)
	}

	// 4. 仅配置音频目录时，即使清空 video_exts 也应通过；清空 audio_exts 才报错
	audioOnly := config.Default()
	audioOnly.Auth.Username = "admin"
	audioOnly.Auth.Password = "strongpass123"
	audioOnly.Dirs = []config.DirConfig{
		{Name: "Music", Path: dirA, Types: []string{config.FileTypeAudio}},
	}
	audioOnly.Filter.VideoExts = nil
	if err := audioOnly.Normalize(); err != nil {
		t.Fatalf("仅音频目录时清空 video_exts 不应报错: %v", err)
	}
	audioOnly.Filter.AudioExts = nil
	if err := audioOnly.Normalize(); err == nil {
		t.Fatalf("音频目录缺少 audio_exts 预期报错")
	}
}

func TestFilterMatchExtsAndLoadLegacyYAML(t *testing.T) {
	tmp := t.TempDir()
	mediaDir := filepath.Join(tmp, "movies")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 模拟旧版 YAML（不含 types / audio_exts / lyric_exts / image_exts）
	yamlContent := `
server:
  listen: ":8443"
auth:
  username: "admin"
  password: "strongpassword"
dirs:
  - name: "电影"
    path: ` + "`" + mediaDir + "`" + `
`
	// 使用单引号以兼容所有路径
	yamlContent = strings.ReplaceAll(yamlContent, "`", "'")
	cfgFile := filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(cfgFile, []byte(yamlContent), 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("加载旧版配置失败: %v", err)
	}
	if len(loaded.Dirs[0].Types) != 1 || loaded.Dirs[0].Types[0] != config.FileTypeVideo {
		t.Fatalf("旧版 YAML 加载后 types 预期 [video]，实际 %v", loaded.Dirs[0].Types)
	}
	if len(loaded.Filter.AudioExts) == 0 || len(loaded.Filter.ImageExts) == 0 || len(loaded.Filter.LyricExts) == 0 {
		t.Fatalf("旧版 YAML 加载后应自动补全音频/歌词/图片默认扩展名")
	}

	// 验证 MatchExts 伴随文件归属
	loaded.Filter.ExtraExts = []string{".nfo"}
	vPrimary, vAllowed := loaded.Filter.MatchExts([]string{config.FileTypeVideo})
	if !vPrimary[".mp4"] || vPrimary[".srt"] || vPrimary[".nfo"] || vPrimary[".mp3"] {
		t.Fatalf("视频主扩展名集合异常: %v", vPrimary)
	}
	if !vAllowed[".mp4"] || !vAllowed[".srt"] || !vAllowed[".nfo"] || vAllowed[".mp3"] || vAllowed[".lrc"] {
		t.Fatalf("视频允许扩展名集合异常: %v", vAllowed)
	}

	aPrimary, aAllowed := loaded.Filter.MatchExts([]string{config.FileTypeAudio})
	if !aPrimary[".flac"] || aPrimary[".lrc"] || aPrimary[".mp4"] {
		t.Fatalf("音频主扩展名集合异常: %v", aPrimary)
	}
	if !aAllowed[".flac"] || !aAllowed[".lrc"] || !aAllowed[".cue"] || !aAllowed[".nfo"] || aAllowed[".srt"] {
		t.Fatalf("音频允许扩展名集合异常: %v", aAllowed)
	}
}


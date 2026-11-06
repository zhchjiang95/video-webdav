package config_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"video-server/internal/config"
)

func TestConfigJSONMarshalling(t *testing.T) {
	cfg := config.Default()
	cfg.Dirs = []config.DirConfig{
		{Name: "Movies", Path: "/movies"},
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
		`"filter":`,
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
		"dirs": [{"name": "Movies", "path": "/movies"}],
		"filter": {
			"video_exts": [".mkv", ".mp4"],
			"subtitle_exts": [".srt"],
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
}


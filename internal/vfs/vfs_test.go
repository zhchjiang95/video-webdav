package vfs_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"video-server/internal/config"
	"video-server/internal/vfs"
)

func touchFile(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("创建目录失败 %s: %v", p, err)
	}
	if err := os.WriteFile(p, []byte("test"), 0o644); err != nil {
		t.Fatalf("创建测试文件失败 %s: %v", p, err)
	}
}

func listNames(t *testing.T, fs *vfs.FS, virtualPath string) []string {
	t.Helper()
	f, err := fs.OpenFile(context.Background(), virtualPath, os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("打开虚拟目录 %s 失败: %v", virtualPath, err)
	}
	defer f.Close()

	infos, err := f.Readdir(-1)
	if err != nil {
		t.Fatalf("读取虚拟目录 %s 失败: %v", virtualPath, err)
	}
	names := make([]string, 0, len(infos))
	for _, info := range infos {
		names = append(names, info.Name())
	}
	return names
}

func TestPerDirFileTypeFilteringAndCacheIsolation(t *testing.T) {
	// 将所有类型文件放在同一物理目录下，再通过不同虚拟目录和不同 types 分别挂载，
	// 既验证各类型独立过滤，也验证空目录缓存按虚拟根目录隔离不串扰。
	shared := t.TempDir()
	touchFile(t, filepath.Join(shared, "movie.mkv"))
	touchFile(t, filepath.Join(shared, "movie.srt"))
	touchFile(t, filepath.Join(shared, "song.flac"))
	touchFile(t, filepath.Join(shared, "song.lrc"))
	touchFile(t, filepath.Join(shared, "photo.jpg"))
	touchFile(t, filepath.Join(shared, "meta.nfo"))
	touchFile(t, filepath.Join(shared, "secret.txt"))

	// 子目录：各自只放某一种主文件或伴随文件，测试 hide_empty_dirs 准确性
	touchFile(t, filepath.Join(shared, "only_video", "clip.mp4"))
	touchFile(t, filepath.Join(shared, "only_subtitle", "orphan.srt"))
	touchFile(t, filepath.Join(shared, "only_audio", "track.mp3"))
	touchFile(t, filepath.Join(shared, "only_lyric", "orphan.lrc"))
	touchFile(t, filepath.Join(shared, "only_image", "pic.png"))

	cfg := config.Default()
	cfg.Auth.Username = "admin"
	cfg.Auth.Password = "strongpass123"
	cfg.Filter.ExtraExts = []string{".nfo"}
	cfg.Filter.HideEmptyDirs = true
	cfg.Dirs = []config.DirConfig{
		{Name: "视频库", Path: shared, Types: []string{config.FileTypeVideo}},
		{Name: "音乐库", Path: shared, Types: []string{config.FileTypeAudio}},
		{Name: "相册库", Path: shared, Types: []string{config.FileTypeImage}},
		{Name: "影音混合", Path: shared, Types: []string{config.FileTypeVideo, config.FileTypeAudio}},
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatalf("配置校验失败: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	fs := vfs.New(cfg, logger)

	// 1. 视频库：仅含视频 + 字幕 + extra_exts(.nfo) + 含视频的子目录
	gotVideo := listNames(t, fs, "/视频库")
	wantVideo := []string{"meta.nfo", "movie.mkv", "movie.srt", "only_video"}
	if !reflect.DeepEqual(gotVideo, wantVideo) {
		t.Fatalf("视频库列表不匹配:\n got: %v\nwant: %v", gotVideo, wantVideo)
	}

	// 2. 音乐库：仅含音频 + 歌词 + extra_exts(.nfo) + 含音频的子目录（不能受视频库缓存影响）
	gotAudio := listNames(t, fs, "/音乐库")
	wantAudio := []string{"meta.nfo", "only_audio", "song.flac", "song.lrc"}
	if !reflect.DeepEqual(gotAudio, wantAudio) {
		t.Fatalf("音乐库列表不匹配:\n got: %v\nwant: %v", gotAudio, wantAudio)
	}

	// 3. 相册库：仅含图片 + extra_exts(.nfo) + 含图片的子目录
	gotImage := listNames(t, fs, "/相册库")
	wantImage := []string{"meta.nfo", "only_image", "photo.jpg"}
	if !reflect.DeepEqual(gotImage, wantImage) {
		t.Fatalf("相册库列表不匹配:\n got: %v\nwant: %v", gotImage, wantImage)
	}

	// 4. 影音混合：视频 + 字幕 + 音频 + 歌词 + extra_exts + 含视频或音频的子目录
	gotMixed := listNames(t, fs, "/影音混合")
	wantMixed := []string{"meta.nfo", "movie.mkv", "movie.srt", "only_audio", "only_video", "song.flac", "song.lrc"}
	if !reflect.DeepEqual(gotMixed, wantMixed) {
		t.Fatalf("影音混合列表不匹配:\n got: %v\nwant: %v", gotMixed, wantMixed)
	}

	// 5. 直接 Stat 未选类型的文件必须返回不存在（防止猜测路径越权访问）
	if _, err := fs.Stat(context.Background(), "/视频库/song.flac"); !os.IsNotExist(err) {
		t.Fatalf("视频库中直接 Stat 音频文件应返回 ErrNotExist，实际: %v", err)
	}
	if _, err := fs.Stat(context.Background(), "/音乐库/movie.mkv"); !os.IsNotExist(err) {
		t.Fatalf("音乐库中直接 Stat 视频文件应返回 ErrNotExist，实际: %v", err)
	}
	if _, err := fs.Stat(context.Background(), "/音乐库/song.flac"); err != nil {
		t.Fatalf("音乐库中 Stat 音频文件应成功，实际: %v", err)
	}
}

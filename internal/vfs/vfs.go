// Package vfs 实现一个只读的虚拟文件系统（webdav.FileSystem）：
//   - 根目录为虚拟目录，每个配置的视频目录显示为一个子文件夹；
//   - 只暴露允许的扩展名（视频、字幕等），其余文件对客户端完全不可见；
//   - 严格校验路径，禁止跳出配置目录；
//   - 拒绝一切写操作。
package vfs

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/webdav"

	"video-server/internal/config"
)

// maxScanDepth 判断“目录是否包含视频”时的最大递归深度
const maxScanDepth = 12

// FS 只读、多根目录、按扩展名过滤的文件系统。
type FS struct {
	roots       []*root
	rootByName  map[string]*root
	allowedExts map[string]bool
	videoExts   map[string]bool
	excludes    []string
	hideEmpty   bool
	follow      bool
	cache       *boolCache
	startTime   time.Time
	logger      *slog.Logger
}

type root struct {
	name string // 虚拟目录名
	path string // 本地真实路径
}

// 编译期确认实现了 webdav.FileSystem 接口
var _ webdav.FileSystem = (*FS)(nil)

// New 根据配置创建文件系统。
func New(cfg *config.Config, logger *slog.Logger) *FS {
	f := &FS{
		rootByName:  make(map[string]*root),
		allowedExts: make(map[string]bool),
		videoExts:   make(map[string]bool),
		excludes:    cfg.Filter.ExcludeNames,
		hideEmpty:   cfg.Filter.HideEmptyDirs,
		follow:      cfg.Filter.FollowSymlinks,
		cache:       newBoolCache(cfg.Filter.EmptyDirCacheTTL),
		startTime:   time.Now(),
		logger:      logger,
	}
	for _, d := range cfg.Dirs {
		r := &root{name: d.Name, path: d.Path}
		f.roots = append(f.roots, r)
		f.rootByName[d.Name] = r
	}
	for _, e := range cfg.Filter.VideoExts {
		f.videoExts[e] = true
		f.allowedExts[e] = true
	}
	for _, e := range cfg.Filter.SubtitleExts {
		f.allowedExts[e] = true
	}
	for _, e := range cfg.Filter.ExtraExts {
		f.allowedExts[e] = true
	}
	return f
}

// ---------- 写操作一律拒绝 ----------

func (f *FS) Mkdir(_ context.Context, name string, _ os.FileMode) error {
	return &os.PathError{Op: "mkdir", Path: name, Err: os.ErrPermission}
}

func (f *FS) RemoveAll(_ context.Context, name string) error {
	return &os.PathError{Op: "remove", Path: name, Err: os.ErrPermission}
}

func (f *FS) Rename(_ context.Context, oldName, _ string) error {
	return &os.PathError{Op: "rename", Path: oldName, Err: os.ErrPermission}
}

// ---------- 读操作 ----------

// Stat 返回虚拟路径对应的文件信息，不可见的文件一律返回“不存在”。
func (f *FS) Stat(_ context.Context, name string) (os.FileInfo, error) {
	t, err := f.resolve(name)
	if err != nil {
		return nil, err
	}
	return f.statTarget(t)
}

// OpenFile 以只读方式打开文件或目录。
func (f *FS) OpenFile(_ context.Context, name string, flag int, _ os.FileMode) (webdav.File, error) {
	if flag&(os.O_WRONLY|os.O_RDWR|os.O_APPEND|os.O_CREATE|os.O_TRUNC) != 0 {
		return nil, &os.PathError{Op: "open", Path: name, Err: os.ErrPermission}
	}
	t, err := f.resolve(name)
	if err != nil {
		return nil, err
	}
	if t.root == nil {
		return &rootDir{fs: f}, nil
	}
	info, err := f.statTarget(t)
	if err != nil {
		return nil, err
	}
	h, err := os.Open(t.real)
	if err != nil {
		f.logger.Warn("打开文件失败", "path", t.real, "err", err)
		return nil, notExist("open", t.virtual)
	}
	return &file{File: h, fs: f, real: t.real, info: info}, nil
}

// target 是虚拟路径解析后的结果。
type target struct {
	virtual string // 规范化后的虚拟路径
	root    *root  // 为 nil 表示虚拟根目录 "/"
	real    string // 本地真实路径
}

// resolve 把 WebDAV 虚拟路径映射为本地真实路径，并做安全校验。
func (f *FS) resolve(name string) (*target, error) {
	clean := path.Clean("/" + name)
	t := &target{virtual: clean}
	if clean == "/" {
		return t, nil
	}

	parts := strings.Split(clean[1:], "/")
	r, ok := f.rootByName[parts[0]]
	if !ok {
		return nil, notExist("stat", clean)
	}
	t.root = r

	cur := r.path
	rest := parts[1:]
	for i, p := range rest {
		if !validSegment(p) || f.excluded(p) {
			return nil, notExist("stat", clean)
		}
		cur = filepath.Join(cur, p)

		// 不跟随链接时，中间路径必须都是真实目录，防止通过符号链接/目录联接跳出根目录
		if !f.follow && i < len(rest)-1 {
			fi, err := os.Lstat(cur)
			if err != nil || !fi.IsDir() || fi.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
				return nil, notExist("stat", clean)
			}
		}
	}

	if !within(r.path, cur) {
		return nil, notExist("stat", clean)
	}
	t.real = cur
	return t, nil
}

// statTarget 获取解析后目标的文件信息并做可见性校验。
func (f *FS) statTarget(t *target) (os.FileInfo, error) {
	if t.root == nil {
		return f.virtualRootInfo(), nil
	}
	if t.real == t.root.path {
		fi, err := os.Stat(t.real)
		if err != nil {
			return nil, notExist("stat", t.virtual)
		}
		return renamedInfo{FileInfo: fi, name: t.root.name}, nil
	}
	fi, err := f.lstat(t.real)
	if err != nil || !f.visible(fi.Name(), fi.Mode()) {
		return nil, notExist("stat", t.virtual)
	}
	return fi, nil
}

// lstat 根据是否跟随链接选择 Stat / Lstat。
func (f *FS) lstat(p string) (os.FileInfo, error) {
	if f.follow {
		return os.Stat(p)
	}
	return os.Lstat(p)
}

// visible 判断一个条目是否允许暴露给客户端。
func (f *FS) visible(name string, mode fs.FileMode) bool {
	if f.excluded(name) {
		return false
	}
	if !f.follow && mode&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
		return false
	}
	switch {
	case mode.IsDir():
		return true
	case mode.IsRegular():
		return f.allowedExts[strings.ToLower(filepath.Ext(name))]
	default:
		return false
	}
}

// isVideo 判断文件名是否为视频文件。
func (f *FS) isVideo(name string) bool {
	return f.videoExts[strings.ToLower(filepath.Ext(name))]
}

// excluded 判断名称是否命中排除规则（不区分大小写）。
func (f *FS) excluded(name string) bool {
	lower := strings.ToLower(name)
	for _, p := range f.excludes {
		if ok, _ := path.Match(p, lower); ok {
			return true
		}
	}
	return false
}

// listDir 列出目录下所有可见条目。
func (f *FS) listDir(dir string) ([]os.FileInfo, error) {
	h, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer h.Close()

	infos, err := h.Readdir(-1)
	if err != nil {
		if len(infos) == 0 {
			return nil, err
		}
		// 部分条目读取失败时，仍返回已读到的内容
		f.logger.Warn("读取目录部分失败", "dir", dir, "err", err)
	}

	out := make([]os.FileInfo, 0, len(infos))
	for _, fi := range infos {
		if f.follow && fi.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
			resolved, err := os.Stat(filepath.Join(dir, fi.Name()))
			if err != nil {
				continue
			}
			fi = resolved
		}
		if !f.visible(fi.Name(), fi.Mode()) {
			continue
		}
		if fi.IsDir() && f.hideEmpty && !f.hasVideo(filepath.Join(dir, fi.Name()), 1) {
			continue
		}
		out = append(out, fi)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

// hasVideo 判断目录（含子目录）中是否存在视频文件，结果带缓存。
func (f *FS) hasVideo(dir string, depth int) bool {
	if v, ok := f.cache.get(dir); ok {
		return v
	}
	v := f.scanForVideo(dir, depth)
	f.cache.set(dir, v)
	return v
}

func (f *FS) scanForVideo(dir string, depth int) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	var subdirs []string
	// 先看当前目录的文件，找到视频即可提前返回
	for _, e := range entries {
		name := e.Name()
		mode := e.Type()
		if f.follow && mode&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
			fi, err := os.Stat(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			mode = fi.Mode()
		}
		if !f.visible(name, mode) {
			continue
		}
		if mode.IsDir() {
			subdirs = append(subdirs, filepath.Join(dir, name))
			continue
		}
		if f.isVideo(name) {
			return true
		}
	}
	if depth >= maxScanDepth {
		return false
	}
	for _, sd := range subdirs {
		if f.hasVideo(sd, depth+1) {
			return true
		}
	}
	return false
}

// rootInfos 返回虚拟根目录下的条目（即配置的各视频目录）。
func (f *FS) rootInfos() []os.FileInfo {
	out := make([]os.FileInfo, 0, len(f.roots))
	for _, r := range f.roots {
		fi, err := os.Stat(r.path)
		if err != nil {
			f.logger.Warn("视频目录不可访问", "name", r.name, "path", r.path, "err", err)
			continue
		}
		out = append(out, renamedInfo{FileInfo: fi, name: r.name})
	}
	return out
}

func (f *FS) virtualRootInfo() os.FileInfo {
	return virtualDirInfo{name: "/", modTime: f.startTime}
}

// ---------- 工具函数 ----------

// validSegment 校验单个路径片段，拦截 Windows 下的特殊写法。
func validSegment(p string) bool {
	if p == "" || p == "." || p == ".." {
		return false
	}
	// 反斜杠可能被 Windows 当作分隔符；冒号可用于盘符和 NTFS 备用数据流
	if strings.ContainsAny(p, "\\:\x00") {
		return false
	}
	// Windows 会自动去掉末尾的点和空格，可能被用来绕过排除规则
	if strings.HasSuffix(p, ".") || strings.HasSuffix(p, " ") {
		return false
	}
	return true
}

// within 判断 p 是否位于 base 目录内（含 base 本身）。
func within(base, p string) bool {
	rel, err := filepath.Rel(base, p)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func notExist(op, name string) error {
	return &os.PathError{Op: op, Path: name, Err: os.ErrNotExist}
}

// ---------- webdav.File 实现 ----------

// file 包装真实文件/目录，目录列表经过过滤，禁止写入。
type file struct {
	*os.File
	fs      *FS
	real    string
	info    os.FileInfo
	entries []os.FileInfo
	loaded  bool
	pos     int
}

func (fl *file) Write([]byte) (int, error) {
	return 0, &os.PathError{Op: "write", Path: fl.info.Name(), Err: os.ErrPermission}
}

func (fl *file) Stat() (os.FileInfo, error) { return fl.info, nil }

func (fl *file) Readdir(count int) ([]os.FileInfo, error) {
	if !fl.info.IsDir() {
		return nil, &os.PathError{Op: "readdir", Path: fl.info.Name(), Err: errors.New("不是目录")}
	}
	if !fl.loaded {
		list, err := fl.fs.listDir(fl.real)
		if err != nil {
			return nil, err
		}
		fl.entries, fl.loaded = list, true
	}
	return paginate(fl.entries, &fl.pos, count)
}

// rootDir 是虚拟根目录。
type rootDir struct {
	fs      *FS
	entries []os.FileInfo
	loaded  bool
	pos     int
}

func (d *rootDir) Close() error { return nil }

func (d *rootDir) Read([]byte) (int, error) {
	return 0, &os.PathError{Op: "read", Path: "/", Err: errors.New("是目录")}
}

func (d *rootDir) Seek(int64, int) (int64, error) { return 0, nil }

func (d *rootDir) Write([]byte) (int, error) {
	return 0, &os.PathError{Op: "write", Path: "/", Err: os.ErrPermission}
}

func (d *rootDir) Stat() (os.FileInfo, error) { return d.fs.virtualRootInfo(), nil }

func (d *rootDir) Readdir(count int) ([]os.FileInfo, error) {
	if !d.loaded {
		d.entries, d.loaded = d.fs.rootInfos(), true
	}
	return paginate(d.entries, &d.pos, count)
}

// paginate 按 http.File.Readdir 的语义分页返回条目。
func paginate(all []os.FileInfo, pos *int, count int) ([]os.FileInfo, error) {
	rest := all[*pos:]
	if count <= 0 {
		*pos = len(all)
		return rest, nil
	}
	if len(rest) == 0 {
		return nil, io.EOF
	}
	if count > len(rest) {
		count = len(rest)
	}
	*pos += count
	return rest[:count], nil
}

// ---------- FileInfo 实现 ----------

// renamedInfo 用于把配置目录以自定义名称展示。
type renamedInfo struct {
	os.FileInfo
	name string
}

func (r renamedInfo) Name() string { return r.name }

// virtualDirInfo 是虚拟目录的文件信息。
type virtualDirInfo struct {
	name    string
	modTime time.Time
}

func (v virtualDirInfo) Name() string       { return v.name }
func (v virtualDirInfo) Size() int64        { return 0 }
func (v virtualDirInfo) Mode() fs.FileMode  { return fs.ModeDir | 0o555 }
func (v virtualDirInfo) ModTime() time.Time { return v.modTime }
func (v virtualDirInfo) IsDir() bool        { return true }
func (v virtualDirInfo) Sys() any           { return nil }

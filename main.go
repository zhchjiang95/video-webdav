// video-server：为 VidHub / Infuse 等播放器提供只读 WebDAV 媒体服务，支持 Windows 服务与 Android 运行。
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kardianos/service"
	"gopkg.in/natefinch/lumberjack.v2"

	"video-server/internal/config"
	"video-server/internal/server"
)

// version 编译时通过 -ldflags "-X main.version=xxx" 注入
var version = "dev"

const usage = `video-server %s —— 只读 WebDAV 媒体服务

用法:
  video-server [命令] [-c 配置文件]

命令:
  run         前台运行（默认）
  serve       直接前台运行（不依赖系统服务管理器，适用于 Android/容器）
  check       校验配置文件
  init-config 若配置文件不存在则生成默认 HTTP 配置
  config-info 输出当前配置摘要（JSON）
  dir-add     添加一个媒体目录（-name 名称 -path 路径 -types video,audio,image）
  dir-remove  按名称移除一个媒体目录（-name 名称）
  install     安装为系统服务（Windows 需以管理员身份运行）
  uninstall   卸载系统服务
  start       启动系统服务
  stop        停止系统服务
  restart     重启系统服务
  version     显示版本

参数:
  -c string   配置文件路径（默认为程序所在目录下的 config.yaml）
`

type cliOptions struct {
	cmd      string
	cfgPath  string
	stdinCtl bool
	dirName  string
	dirPath  string
	dirTypes string
}

func main() {
	opts := parseArgs()

	// 不依赖系统服务管理器的独立命令优先处理（保证 Android/容器环境零依赖可用）
	switch opts.cmd {
	case "serve":
		if err := runDirect(opts.cfgPath, opts.stdinCtl); err != nil {
			fatal("运行失败: %v", err)
		}
		return
	case "init-config":
		created, err := config.EnsureDefaultConfig(opts.cfgPath)
		if err != nil {
			writeCLIError(err)
		}
		writeCLIJSON(map[string]any{"ok": true, "created": created, "path": opts.cfgPath})
		return
	case "config-info":
		handleConfigInfo(opts.cfgPath)
		return
	case "dir-add":
		handleDirAdd(opts.cfgPath, opts.dirName, opts.dirPath, opts.dirTypes)
		return
	case "dir-remove":
		handleDirRemove(opts.cfgPath, opts.dirName)
		return
	case "check":
		cfg, err := config.Load(opts.cfgPath)
		if err != nil {
			fatal("配置文件有误 (%s):\n%v", opts.cfgPath, err)
		}
		fmt.Printf("配置文件校验通过: %s\n", opts.cfgPath)
		fmt.Printf("监听地址: %s  HTTPS: %v\n", cfg.Server.Listen, cfg.Server.TLS.Enabled)
		if len(cfg.Dirs) == 0 {
			fmt.Println("  (当前尚未配置媒体目录，启动后可在 Web 管理后台 /admin/ 中直接添加)")
		} else {
			for _, d := range cfg.Dirs {
				fmt.Printf("  /%s  ->  %s  (类型: %s)\n", d.Name, d.Path, strings.Join(d.Types, ","))
			}
		}
		printAdminURLs(cfg)
		return
	case "version":
		fmt.Println(version)
		return
	case "help", "-h", "--help":
		fmt.Printf(usage, version)
		return
	}

	prg := &program{cfgPath: opts.cfgPath}
	svc, err := service.New(prg, &service.Config{
		Name:        "VideoServer",
		DisplayName: "Video Server (WebDAV)",
		Description: "为 VidHub / Infuse 等播放器提供只读 WebDAV 媒体服务",
		Arguments:   []string{"-c", opts.cfgPath},
	})
	if err != nil {
		// 在 Android / Docker 等无系统服务管理器的环境中，run 命令自动降级为直接前台运行
		if opts.cmd == "" || opts.cmd == "run" {
			if runErr := runDirect(opts.cfgPath, opts.stdinCtl); runErr != nil {
				fatal("运行失败: %v", runErr)
			}
			return
		}
		fatal("初始化服务失败: %v", err)
	}

	switch opts.cmd {
	case "", "run":
		if err := svc.Run(); err != nil {
			fatal("运行失败: %v", err)
		}
	case "install":
		// 安装前先校验配置，避免装上了却启动不了
		if _, err := config.Load(opts.cfgPath); err != nil {
			fatal("配置文件有误 (%s):\n%v", opts.cfgPath, err)
		}
		control(svc, opts.cmd)
	case "start", "restart":
		control(svc, opts.cmd)
		if cfg, err := config.Load(opts.cfgPath); err == nil {
			printAdminURLs(cfg)
		}
	case "uninstall", "stop":
		control(svc, opts.cmd)
	default:
		fmt.Fprintf(os.Stderr, "未知命令: %s\n\n", opts.cmd)
		fmt.Printf(usage, version)
		os.Exit(2)
	}
}

// runDirect 直接在前台运行服务，支持信号优雅退出及可选的 stdin 控制管道（供 Android 外壳热重载/停止）。
func runDirect(cfgPath string, stdinCtl bool) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	logger, logCloser := setupLogger(cfg)
	if logCloser != nil {
		defer logCloser.Close()
	}
	logger.Info("video-server 启动中", "version", version, "config", cfgPath)

	server.Version = version
	srv, err := server.New(cfg, cfgPath, logger)
	if err != nil {
		return err
	}
	if err := srv.Start(); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if stdinCtl {
		go func() {
			scanner := bufio.NewScanner(os.Stdin)
			for scanner.Scan() {
				switch strings.TrimSpace(scanner.Text()) {
				case "reload":
					if rerr := srv.ReloadFromFile(); rerr != nil {
						logger.Warn("热重载配置失败", "err", rerr)
					}
				case "stop":
					logger.Info("收到停止指令，准备关闭服务")
					stop()
					return
				}
			}
			// 父进程关闭 stdin 管道（如 Android 服务退出）时，自动退出避免孤儿进程占端口
			logger.Info("控制管道已关闭，自动退出服务")
			stop()
		}()
	}

	<-ctx.Done()

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		logger.Warn("关闭服务时出现错误", "err", err)
	}
	logger.Info("服务已停止")
	return nil
}

// handleConfigInfo 输出当前配置文件的摘要 JSON（不包含密码）。
func handleConfigInfo(cfgPath string) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		writeCLIError(err)
	}
	dirs := cfg.Dirs
	if dirs == nil {
		dirs = []config.DirConfig{}
	}
	writeCLIJSON(map[string]any{
		"ok":       true,
		"version":  version,
		"listen":   cfg.Server.Listen,
		"tls":      cfg.Server.TLS.Enabled,
		"username": cfg.Auth.Username,
		"dirs":     dirs,
	})
}

// handleDirAdd 向配置文件添加一个媒体目录并保存。
func handleDirAdd(cfgPath, name, dirPath, typesRaw string) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		writeCLIError(err)
	}

	name = strings.TrimSpace(name)
	dirPath = strings.TrimSpace(dirPath)
	if name == "" || dirPath == "" {
		writeCLIError(fmt.Errorf("name 和 path 不能为空"))
	}

	var types []string
	for _, part := range strings.Split(typesRaw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			types = append(types, part)
		}
	}
	normTypes, err := config.NormalizeFileTypes(types)
	if err != nil {
		writeCLIError(err)
	}

	for _, existing := range cfg.Dirs {
		if strings.EqualFold(existing.Name, name) {
			writeCLIError(fmt.Errorf("已存在同名目录 %q，请更换显示名称", name))
		}
	}

	cfg.Dirs = append(cfg.Dirs, config.DirConfig{
		Name:  name,
		Path:  dirPath,
		Types: normTypes,
	})
	if err := cfg.Save(cfgPath); err != nil {
		writeCLIError(err)
	}
	writeCLIJSON(map[string]any{"ok": true, "dirs": cfg.Dirs})
}

// handleDirRemove 按名称从配置文件中移除一个媒体目录并保存。
func handleDirRemove(cfgPath, name string) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		writeCLIError(err)
	}

	name = strings.TrimSpace(name)
	if name == "" {
		writeCLIError(fmt.Errorf("name 不能为空"))
	}

	out := make([]config.DirConfig, 0, len(cfg.Dirs))
	removed := false
	for _, d := range cfg.Dirs {
		if strings.EqualFold(d.Name, name) {
			removed = true
			continue
		}
		out = append(out, d)
	}
	if !removed {
		writeCLIError(fmt.Errorf("未找到名为 %q 的目录", name))
	}

	cfg.Dirs = out
	if err := cfg.Save(cfgPath); err != nil {
		writeCLIError(err)
	}
	writeCLIJSON(map[string]any{"ok": true, "dirs": cfg.Dirs})
}

func writeCLIJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeCLIError(err error) {
	writeCLIJSON(map[string]any{"ok": false, "error": err.Error()})
	os.Exit(1)
}

// parseArgs 解析命令和参数，命令放在参数前后都可以。
func parseArgs() cliOptions {
	args := os.Args[1:]
	var cmd string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("video-server", flag.ExitOnError)
	fs.Usage = func() { fmt.Printf(usage, version) }
	c := fs.String("c", defaultConfigPath(), "配置文件路径")
	stdinCtl := fs.Bool("stdin-ctl", false, "启用标准输入控制管道（支持 reload / stop）")
	dirName := fs.String("name", "", "媒体目录显示名称")
	dirPath := fs.String("path", "", "媒体目录本地绝对路径")
	dirTypes := fs.String("types", "video", "媒体目录文件类型，逗号分隔（video,audio,image）")
	_ = fs.Parse(args)
	if cmd == "" && fs.NArg() > 0 {
		cmd = fs.Arg(0)
	}
	abs, err := filepath.Abs(*c)
	if err != nil {
		fatal("解析配置文件路径失败: %v", err)
	}
	return cliOptions{
		cmd:      cmd,
		cfgPath:  abs,
		stdinCtl: *stdinCtl,
		dirName:  *dirName,
		dirPath:  *dirPath,
		dirTypes: *dirTypes,
	}
}

// defaultConfigPath 优先使用程序所在目录的 config.yaml（服务模式下工作目录是 System32），
// 找不到时退回当前目录（方便开发时 go run）。
func defaultConfigPath() string {
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), "config.yaml")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "config.yaml"
}

func control(svc service.Service, action string) {
	if err := service.Control(svc, action); err != nil {
		fatal("执行 %s 失败: %v", action, err)
	}
	fmt.Printf("执行 %s 成功\n", action)
}

// printAdminURLs 输出管理后台的本地与局域网访问地址。
func printAdminURLs(cfg *config.Config) {
	scheme := "http"
	if cfg.Server.TLS.Enabled {
		scheme = "https"
	}

	port := cfg.Server.Listen
	if idx := strings.LastIndex(port, ":"); idx != -1 {
		port = port[idx:]
	} else {
		port = ":" + port
	}

	fmt.Println("\n==================================================")
	fmt.Println("🎬 video-webdav 管理后台地址:")
	fmt.Printf("   本地访问:   %s://localhost%s/admin/\n", scheme, port)
	for _, ip := range getLocalIPs() {
		fmt.Printf("   局域网访问: %s://%s%s/admin/\n", scheme, ip, port)
	}
	fmt.Println("==================================================")
}

// getLocalIPs 获取本机所有的有效 IPv4 局域网地址。
func getLocalIPs() []string {
	var ips []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ips
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ip4 := ipnet.IP.To4(); ip4 != nil {
				ips = append(ips, ip4.String())
			}
		}
	}
	return ips
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}

// program 实现 service.Interface。
type program struct {
	cfgPath   string
	srv       *server.Server
	logger    *slog.Logger
	logCloser io.Closer
}

// Start 不能阻塞：加载配置、启动监听后立即返回。
func (p *program) Start(s service.Service) error {
	cfg, err := config.Load(p.cfgPath)
	if err != nil {
		p.reportStartError(s, err)
		return err
	}

	p.logger, p.logCloser = setupLogger(cfg)
	p.logger.Info("video-server 启动中", "version", version, "config", p.cfgPath)

	server.Version = version
	srv, err := server.New(cfg, p.cfgPath, p.logger)
	if err == nil {
		err = srv.Start()
	}
	if err != nil {
		p.logger.Error("启动失败", "err", err)
		p.reportStartError(s, err)
		return err
	}
	p.srv = srv
	return nil
}

// Stop 优雅关闭，最多等待 5 秒。
func (p *program) Stop(_ service.Service) error {
	if p.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.srv.Shutdown(ctx)
		p.logger.Info("服务已停止")
	}
	if p.logCloser != nil {
		_ = p.logCloser.Close()
	}
	return nil
}

// reportStartError 服务模式下没有控制台，把启动错误写入系统日志（Windows 事件查看器）。
func (p *program) reportStartError(s service.Service, err error) {
	if service.Interactive() {
		return
	}
	if l, lerr := s.Logger(nil); lerr == nil {
		_ = l.Error(fmt.Sprintf("video-server 启动失败: %v", err))
	}
}

// setupLogger 创建日志器：前台运行时同时输出到控制台，服务模式只写文件。
func setupLogger(cfg *config.Config) (*slog.Logger, io.Closer) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.Log.Level)); err != nil {
		level = slog.LevelInfo
	}

	var writers []io.Writer
	var closer io.Closer
	if cfg.Log.File != "" {
		lj := &lumberjack.Logger{
			Filename:   cfg.Log.File,
			MaxSize:    20, // 单个文件最大 20MB
			MaxBackups: 5,
			MaxAge:     30, // 保留 30 天
			LocalTime:  true,
		}
		writers = append(writers, lj)
		closer = lj
	}
	if service.Interactive() || len(writers) == 0 {
		writers = append(writers, os.Stdout)
	}

	h := slog.NewTextHandler(io.MultiWriter(writers...), &slog.HandlerOptions{Level: level})
	return slog.New(h), closer
}

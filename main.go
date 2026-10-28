// video-server：为 VidHub 等播放器提供只读 WebDAV 视频服务，可注册为 Windows 服务。
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kardianos/service"
	"gopkg.in/natefinch/lumberjack.v2"

	"video-server/internal/config"
	"video-server/internal/server"
)

// version 编译时通过 -ldflags "-X main.version=xxx" 注入
var version = "dev"

const usage = `video-server %s —— 只读 WebDAV 视频服务

用法:
  video-server [命令] [-c 配置文件]

命令:
  run         前台运行（默认）
  check       校验配置文件
  install     安装为系统服务（Windows 需以管理员身份运行）
  uninstall   卸载系统服务
  start       启动系统服务
  stop        停止系统服务
  restart     重启系统服务
  version     显示版本

参数:
  -c string   配置文件路径（默认为程序所在目录下的 config.yaml）
`

func main() {
	cmd, cfgPath := parseArgs()

	prg := &program{cfgPath: cfgPath}
	svc, err := service.New(prg, &service.Config{
		Name:        "VideoServer",
		DisplayName: "Video Server (WebDAV)",
		Description: "为 VidHub 等播放器提供只读 WebDAV 视频服务",
		Arguments:   []string{"-c", cfgPath},
	})
	if err != nil {
		fatal("初始化服务失败: %v", err)
	}

	switch cmd {
	case "", "run":
		if err := svc.Run(); err != nil {
			fatal("运行失败: %v", err)
		}
	case "check":
		cfg, err := config.Load(cfgPath)
		if err != nil {
			fatal("配置文件有误 (%s):\n%v", cfgPath, err)
		}
		fmt.Printf("配置文件校验通过: %s\n", cfgPath)
		fmt.Printf("监听地址: %s  HTTPS: %v\n", cfg.Server.Listen, cfg.Server.TLS.Enabled)
		for _, d := range cfg.Dirs {
			fmt.Printf("  /%s  ->  %s\n", d.Name, d.Path)
		}
	case "install":
		// 安装前先校验配置，避免装上了却启动不了
		if _, err := config.Load(cfgPath); err != nil {
			fatal("配置文件有误 (%s):\n%v", cfgPath, err)
		}
		control(svc, cmd)
	case "uninstall", "start", "stop", "restart":
		control(svc, cmd)
	case "version":
		fmt.Println(version)
	case "help", "-h", "--help":
		fmt.Printf(usage, version)
	default:
		fmt.Fprintf(os.Stderr, "未知命令: %s\n\n", cmd)
		fmt.Printf(usage, version)
		os.Exit(2)
	}
}

// parseArgs 解析命令和参数，命令放在 -c 前后都可以。
func parseArgs() (cmd, cfgPath string) {
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("video-server", flag.ExitOnError)
	fs.Usage = func() { fmt.Printf(usage, version) }
	c := fs.String("c", defaultConfigPath(), "配置文件路径")
	_ = fs.Parse(args)
	if cmd == "" && fs.NArg() > 0 {
		cmd = fs.Arg(0)
	}
	abs, err := filepath.Abs(*c)
	if err != nil {
		fatal("解析配置文件路径失败: %v", err)
	}
	return cmd, abs
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

	srv, err := server.New(cfg, p.logger)
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

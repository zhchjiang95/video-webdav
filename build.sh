#!/usr/bin/env bash
# 交叉编译 Windows 版本，并在 dist/ 下自动生成开箱即用的默认配置与说明文档
set -euo pipefail

# 兼容标准 pkg 安装路径
export PATH="/usr/local/go/bin:$PATH"

cd "$(dirname "$0")"

VERSION="${VERSION:-$(date +%Y%m%d%H%M)}"
ARCH="${GOARCH:-amd64}"
OUT="dist"

mkdir -p "$OUT"

echo "构建 Web 管理后台前端 (pnpm build) ..."
(cd web && pnpm build)

echo "编译 windows/${ARCH} 版本 ${VERSION} ..."
CGO_ENABLED=0 GOOS=windows GOARCH="$ARCH" \
  go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
  -o "$OUT/video-server.exe" .

# 生成开箱即用的默认 HTTP 配置（dirs 初始为空，启动后可直接打开管理后台图形化添加目录）
cat > "$OUT/config.yaml" <<'EOF'
# video-server 默认配置文件（开箱即用 HTTP 模式）
# 直接运行 video-server.exe 后，打开浏览器访问：http://localhost:8443/admin/
# 【重要】首次登录后，请务必在「服务配置」中修改默认密码！
# 详细配置项说明请查看同目录下的「配置说明.txt」或「config.example.yaml」。

server:
  listen: ":8443"
  tls:
    enabled: false
    cert_file: 'certs\video.example.com_bundle.crt'
    key_file: 'certs\video.example.com.key'

auth:
  username: "admin"
  password: "admin123456"

security:
  max_failures: 5
  failure_window: 10m
  ban_duration: 30m

# 初始留空以确保任意电脑都可直接启动；
# 启动后请在管理后台「媒体目录」中图形化添加，也可按下方注释格式手工填写：
# dirs:
#   - name: "电影"
#     path: 'D:\Movies'
#     types: ["video"]
#   - name: "音乐"
#     path: 'D:\Music'
#     types: ["audio"]
#   - name: "相册"
#     path: 'D:\Photos'
#     types: ["image"]
dirs: []

filter:
  video_exts: [".mkv", ".mp4", ".m4v", ".avi", ".mov", ".wmv", ".flv", ".f4v", ".webm",
               ".ts", ".m2ts", ".mts", ".mpg", ".mpeg", ".vob", ".rmvb", ".rm", ".3gp", ".iso"]
  subtitle_exts: [".srt", ".ass", ".ssa", ".sup", ".vtt", ".sub", ".idx"]
  audio_exts: [".mp3", ".flac", ".wav", ".aac", ".m4a", ".ogg", ".opus", ".wma",
               ".ape", ".alac", ".aiff", ".aif", ".dsf", ".dff", ".wv", ".mka"]
  lyric_exts: [".lrc", ".cue"]
  image_exts: [".jpg", ".jpeg", ".png", ".webp", ".gif", ".bmp",
               ".heic", ".heif", ".avif", ".tif", ".tiff", ".svg"]
  extra_exts: []
  exclude_names: [".*", "$*", "System Volume Information", "@eaDir", "#recycle", "#snapshot"]
  hide_empty_dirs: true
  empty_dir_cache_ttl: 2m
  follow_symlinks: false

log:
  file: 'logs\video-server.log'
  level: "info"
  access_log: true
EOF

# 复制完整示例配置与通用 TXT 配置说明（转为 Windows CRLF 换行，记事本直接打开不乱行）
cp config.example.yaml "$OUT/config.example.yaml"
awk '{sub(/\r$/, ""); printf "%s\r\n", $0}' docs/配置说明.txt > "$OUT/配置说明.txt"
awk '{sub(/\r$/, ""); printf "%s\r\n", $0}' "$OUT/config.yaml" > "$OUT/config.yaml.tmp" && mv "$OUT/config.yaml.tmp" "$OUT/config.yaml"

echo "完成：产物已输出至 $OUT/"
echo "  - $OUT/video-server.exe"
echo "  - $OUT/config.yaml        (默认可用的 HTTP 配置，默认密码 admin123456)"
echo "  - $OUT/config.example.yaml"
echo "  - $OUT/配置说明.txt"

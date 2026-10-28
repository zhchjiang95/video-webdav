#!/usr/bin/env bash
# 交叉编译 Windows 版本，产物输出到 dist/
set -euo pipefail

# 兼容标准 pkg 安装路径
export PATH="/usr/local/go/bin:$PATH"

cd "$(dirname "$0")"

VERSION="${VERSION:-$(date +%Y%m%d%H%M)}"
ARCH="${GOARCH:-amd64}"
OUT="dist"

mkdir -p "$OUT"

echo "编译 windows/${ARCH} 版本 ${VERSION} ..."
CGO_ENABLED=0 GOOS=windows GOARCH="$ARCH" \
  go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
  -o "$OUT/video-server.exe" .

cp config.example.yaml "$OUT/config.example.yaml"

echo "完成：$OUT/video-server.exe"

#!/usr/bin/env sh
# 在 Windows（Git Bash/WSL）或 Mac 上执行，交叉编译为 linux/amd64，使用 -s -w 减小体积

set -e
OUT="stockMaxWin-linux-amd64"
echo "Building for linux/amd64 -> $OUT"
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o "$OUT" .
echo "Done: $OUT"

#!/usr/bin/env sh
# NAS 上用：只运行已编译好的二进制，不依赖 Go。用法: ./start.sh [--schedule]
# 调度模式：STOCKMAXWIN_SCHEDULE=1 常驻，9:15~15:00 每半小时执行。
set -eu

ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT"
BINARY="stockMaxWin"
OUT="$ROOT/$BINARY"

if [ ! -f "$OUT" ]; then
	echo "错误: 未找到 $OUT，请在开发机上执行 make build-linux 后上传到 NAS" >&2
	exit 1
fi
if [ ! -x "$OUT" ]; then
	chmod +x "$OUT"
fi

case "${1:-}" in
  --schedule) export STOCKMAXWIN_SCHEDULE=1 ;;
esac
if [ -n "${STOCKMAXWIN_SCHEDULE:-}" ]; then
  echo "[start] 调度模式：9:15~15:00 每半小时 周一至周五"
fi
echo "[start] 运行 $BINARY"
exec "$OUT" "$@"

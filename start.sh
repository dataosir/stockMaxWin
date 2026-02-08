#!/usr/bin/env sh
# NAS 上用：只运行已编译好的二进制，不依赖 Go。
# 默认定时模式（9:15~15:00 每半小时 周一至周五）；传 --once 则只跑一次退出。
# 用法: ./start.sh  或  ./start.sh --once
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

# 脚本启动默认定时；仅传 --once 时跑一次
case "${1:-}" in
  --once) export STOCKMAXWIN_SCHEDULE=0; shift ;;
  *)      export STOCKMAXWIN_SCHEDULE=1 ;;
esac
if [ "$STOCKMAXWIN_SCHEDULE" = "1" ]; then
  echo "[start] 定时模式：9:15~15:00 每半小时 周一至周五"
fi
echo "[start] 运行 $BINARY"
exec "$OUT" "$@"

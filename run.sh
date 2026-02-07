#!/usr/bin/env sh
# 一键启动：先编译再运行，保证执行到最新代码。用法: sh run.sh 或 ./run.sh
set -eu

ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT"
BINARY="stockMaxWin"
OUT="$ROOT/$BINARY"

if ! command -v go >/dev/null 2>&1; then
	echo "错误: 未找到 go 命令，请先安装 Go 并加入 PATH" >&2
	exit 1
fi

echo "[run] 同步依赖 (go mod tidy)..."
go mod tidy
echo "[run] 编译中..."
if ! go build -trimpath -ldflags "-s -w" -o "$OUT" .; then
	echo "错误: 编译失败" >&2
	exit 1
fi
# 调度模式：常驻并按 9:15~15:00 每半小时执行。用法: sh run.sh --schedule 或 STOCKMAXWIN_SCHEDULE=1 sh run.sh
case "${1:-}" in
  --schedule) export STOCKMAXWIN_SCHEDULE=1; shift ;;
esac
if [ -n "${STOCKMAXWIN_SCHEDULE:-}" ]; then
  echo "[run] 调度模式：9:15~15:00 每半小时 周一至周五"
fi
echo "[run] 运行 $BINARY"
exec "$OUT" "$@"

# stockMaxWin Makefile
# 本地编译、交叉编译（linux/amd64）、清理

BINARY   := stockMaxWin
LDFLAGS  := -s -w
GOFLAGS  := -trimpath

.PHONY: build build-linux run clean help

# 本地编译（当前系统）
build:
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY) .

# 先编译再运行，保证执行最新代码
run: build
	./$(BINARY)

# 交叉编译：目标 linux/amd64（在 Windows/Mac 上执行）
build-linux:
	GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY)-linux-amd64 .

# 清理生成的二进制
clean:
	rm -f $(BINARY) $(BINARY)-linux-amd64 $(BINARY).exe

help:
	@echo "targets:"
	@echo "  build        - 编译当前系统可执行文件"
	@echo "  run          - 编译并运行（保证执行最新代码）"
	@echo "  build-linux  - 交叉编译为 linux/amd64（在 Windows/Mac 上运行此目标）"
	@echo "  clean        - 删除编译产物"
	@echo "  help         - 显示此帮助"

# stockMaxWin

轻量级 A 股选股自动化工具，面向资源受限的 Linux NAS（如 i5-2430M、6GB RAM）环境。

## 环境要求

- Go 1.21+
- 仅使用标准库；JSON 用 encoding/json 流式解析（json.Decoder），不整 body 读入内存

## 快速开始

```bash
go mod tidy
go build -o stockMaxWin .
./stockMaxWin
```

**安全（开放仓库）**：`config.json` 已加入 `.gitignore`，不会随仓库提交。请勿将含 SMTP 授权码等敏感信息的配置文件提交到公开仓库。若曾误提交过，请执行：`git rm --cached config.json` 后再次提交。

**交叉编译（在 Windows/Mac 上编译出 Linux 可执行文件，供 NAS 使用）：**

```bash
# 方式一：Makefile（需在 Git Bash / WSL / Mac 终端下执行）
make build-linux
# 生成 stockMaxWin-linux-amd64

# 方式二：脚本
chmod +x build-linux.sh && ./build-linux.sh
```

编译已加 `-ldflags "-s -w"` 以减小体积。Cron 定时任务示例见 `cron.example`（每周一至周五 14:00 运行）。

可选：通过环境变量调整并发数（默认 10，防止封 IP/内存溢出）：

```bash
STOCKMAXWIN_CONCURRENCY=6 ./stockMaxWin
```

## 项目结构

```
stockMaxWin/
├── go.mod
├── go.sum
├── main.go                # 入口、选股流程、邮件触发
├── config.json.example    # 邮件配置示例（复制为 config.json 并填写）
├── internal/
│   ├── api/
│   │   └── eastmoney.go   # 东方财富 API：全市场列表、日 K 线
│   ├── config/
│   │   └── smtp.go        # SMTP 配置（环境变量 / config.json）
│   ├── mail/
│   │   └── send.go        # 使用 net/smtp 发送 HTML 表格邮件
│   ├── model/
│   │   └── stock.go       # Stock / StockBrief / KLine
│   └── worker/
│       └── worker.go      # Worker Pool（生产者-消费者）、选股过滤
└── README.md
```

## 核心接口

| 函数 / 行为 | 说明 |
|-------------|------|
| `GetAllStocks(ctx)` | 通过东方财富公开 API 获取当前所有 A 股列表（仅代码、名称），分页请求 |
| `GetKLines(code)` | 获取指定股票最近 30 个交易日的日 K 线 |
| Worker Pool | 从列表逐只下发任务，限制并发数（默认 10，可配置），每只抓取后立即算 MA20/涨跌幅，仅保留符合条件的 `Stock` 输出，不一次性加载全部到内存 |

## 数据模型

- **Stock**：Code, Name, Price, MA20, ChangePct（用于选股结果）
- **StockBrief**：Code, Name（列表/任务，省内存）
- **KLine**：Date, Open, Close, Volume（日 K 线单条）

## 选股逻辑

- 默认过滤条件：**当前价格 > MA20**（严格大于 20 日均线）
- 可在 `worker.NewPool` 时传入自定义 `worker.Filter` 修改条件

## 邮件发送

- 使用标准库 **net/smtp**，无第三方邮件库。
- 选股结束后，若有符合条件的股票且已配置 SMTP，则发送一封 HTML 邮件，正文为表格：**代码、名称、现价、MA20**。
- **若今日无符合条件的股票，仅记录日志，不发送邮件。**

配置方式二选一（环境变量优先于配置文件）：

| 环境变量 | 说明 |
|----------|------|
| `SMTP_SERVER` | SMTP 服务器，如 smtp.qq.com |
| `SMTP_PORT` | 端口，465（TLS）或 587（StartTLS） |
| `SMTP_USER` | 登录账号 |
| `SMTP_PASSWORD` / `SMTP_AUTH_CODE` | 授权码或密码 |
| `SMTP_FROM` | 发件人（不填则用 SMTP_USER） |
| `SMTP_TO` | 收件人，多个用逗号分隔 |
| `CONFIG_PATH` | 配置文件路径，默认 `./config.json` |

配置文件示例：复制 `config.json.example` 为 `config.json`，按 JSON 填写 `smtp_server`、`smtp_port`、`smtp_user`、`smtp_password`、`smtp_from`、`smtp_to`。

## 开发说明

- 优先使用标准库，无第三方依赖；API 响应使用 json.Decoder 从 resp.Body 流式解析以降低内存峰值。
- 并发数通过 `worker.Config.Concurrency` 或环境变量 `STOCKMAXWIN_CONCURRENCY` 配置。
- **防 IP 被封**：请求间间隔 200ms + 0~150ms 随机抖动（`STOCKMAXWIN_API_DELAY_MS`、`STOCKMAXWIN_API_JITTER_MS`）；同时进行中的请求数上限 4（`STOCKMAXWIN_API_MAX_CONCURRENT`）；请求头带 User-Agent / Referer(quote.eastmoney.com) / Accept / Accept-Language；遇 429 时等待 5s 再重试。

// Package main 是 A 股选股程序的入口：拉取主板行情、按条件筛选、可选邮件推送。
// 支持单次运行或调度模式（STOCKMAXWIN_SCHEDULE=1 时每半小时 9:15~15:00 执行）。
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"time"

	"stockMaxWin/internal/api"
	"stockMaxWin/internal/config"
	"stockMaxWin/internal/filter"
	"stockMaxWin/internal/mail"
	"stockMaxWin/internal/model"
	"stockMaxWin/internal/trace"
	"stockMaxWin/internal/worker"
)

// 环境变量名（便于维护与文档）
const (
	envConcurrency = "STOCKMAXWIN_CONCURRENCY"
	envSchedule    = "STOCKMAXWIN_SCHEDULE"
)

// 运行与超时
const (
	runTimeout       = 10 * time.Minute
	getKLinesTimeout = 15 * time.Second
)

// 并发与通道
const (
	defaultConcurrency = 10
	jobChannelBuffer  = 50
)

// 选股结果与提醒
const (
 	topNByChangePct         = 10
	emptyRunsBeforeReminder = 3
)

// 调度时间（本地时区，周一至周五）
const (
	scheduleMarketOpen   = 9
	scheduleMarketClose  = 15
	scheduleFirstMinute  = 15
	scheduleSlotInterval = 30
)

// 日志时间格式
const timeFormatNextRun = "2006-01-02 15:04"

// 初选预分配容量系数（candidates 约 len(quotes)/candidateCapDiv）
const candidateCapDiv = 4

func concurrency() int {
	if s := os.Getenv(envConcurrency); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	return defaultConcurrency
}

func scheduleEnabled() bool {
	s := os.Getenv(envSchedule)
	return s == "true" || s == "1"
}

var apiClient = api.NewClient()

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	if scheduleEnabled() {
		runScheduler()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	_ = runOnce(ctx)
}

// runScheduler 常驻进程：每半小时 9:15~15:00（周一至周五）执行一次，保证按指定时间周期一直执行。
// 连续 emptyRunsBeforeReminder 次无入选时发送提醒邮件（请好好工作 + 随机炒股格言）。
func runScheduler() {
	traceID := trace.NewTraceID()
	ctx := trace.WithTraceID(context.Background(), traceID)
	trace.Log(ctx, "main: 调度模式启动，每半小时 9:15~15:00 周一至周五")
	var emptyRunCount int
	for {
		next := nextRunTime()
		now := time.Now()
		if next.After(now) {
			d := next.Sub(now)
			trace.Log(ctx, "main: 下次执行 %s (约 %s 后)", next.Format(timeFormatNextRun), d.Round(time.Second))
			time.Sleep(d)
		}
		runCtx, cancel := context.WithTimeout(context.Background(), runTimeout)
		runCtx = trace.WithTraceID(runCtx, trace.NewTraceID())
		selected := runOnce(runCtx)
		cancel()
		if len(selected) == 0 {
			emptyRunCount++
			if emptyRunCount >= emptyRunsBeforeReminder {
				trace.Log(ctx, "main: 连续 %d 次无入选，发送提醒邮件", emptyRunCount)
				mailCfg := buildMailConfig(config.LoadSMTP())
				if err := mail.SendNoSelectionReminder(context.Background(), mailCfg); err != nil {
					trace.Log(ctx, "main: 发送提醒邮件失败 err=%v", err)
				} else {
					trace.Log(ctx, "main: 已发提醒邮件，请好好工作")
				}
				emptyRunCount = 0
			}
		} else {
			emptyRunCount = 0
		}
	}
}

// nextRunTime 返回下次应执行时刻（本地时区，周一至周五 9:15/9:45/.../15:00）
func nextRunTime() time.Time {
	loc := time.Local
	now := time.Now().In(loc)
	slots := buildScheduleSlots()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	minutesSinceMidnight := now.Hour()*60 + now.Minute()
	isWeekday := now.Weekday() != time.Sunday && now.Weekday() != time.Saturday

	if isWeekday {
		for _, slotMin := range slots {
			if minutesSinceMidnight < slotMin {
				return dayStart.Add(time.Duration(slotMin) * time.Minute)
			}
		}
	}
	return nextWeekdayAt(now, loc, scheduleMarketOpen, scheduleFirstMinute)
}

func buildScheduleSlots() []int {
	var slots []int
	for h := scheduleMarketOpen; h < scheduleMarketClose; h++ {
		slots = append(slots, h*60+scheduleFirstMinute, h*60+scheduleFirstMinute+scheduleSlotInterval)
	}
	slots = append(slots, scheduleMarketClose*60+0)
	return slots
}

func nextWeekdayAt(from time.Time, loc *time.Location, hour, min int) time.Time {
	next := from
	for {
		next = next.AddDate(0, 0, 1)
		if next.Weekday() != time.Sunday && next.Weekday() != time.Saturday {
			break
		}
	}
	return time.Date(next.Year(), next.Month(), next.Day(), hour, min, 0, 0, loc)
}

func runOnce(ctx context.Context) []*model.Stock {
	ctx = trace.WithTraceID(ctx, trace.NewTraceID())
	trace.Log(ctx, "main: start")
	quotes, err := apiClient.GetMainBoardQuotes(ctx)
	if err != nil {
		trace.Log(ctx, "main: GetMainBoardQuotes err=%v", err)
		log.Printf("GetMainBoardQuotes: %v", err)
		return nil
	}
	if quotes == nil {
		quotes = []model.StockQuote{}
	}
	candidates := make([]model.StockQuote, 0, len(quotes)/candidateCapDiv)
	for i := range quotes {
		if filter.QuotePreFilter(&quotes[i]) {
			candidates = append(candidates, quotes[i])
		}
	}
	trace.Log(ctx, "main: 初选 主板 %d 只 -> 基本面+成交量 %d 只，仅对后者请求 K 线", len(quotes), len(candidates))

	nConc := concurrency()
	jobs := make(chan model.StockQuote, jobChannelBuffer)
	results := make(chan *model.Stock, jobChannelBuffer)
	cfg := worker.DefaultConfig()
	cfg.Concurrency = nConc
	cfg.Filter = func(s *model.Stock) bool { return filter.TrendMomentumStrategy()(s) }
	pool := worker.NewPool(cfg, apiClient, jobs, results)

	var selected []*model.Stock
	done := make(chan struct{})
	go func() {
		for s := range results {
			if s == nil {
				continue
			}
			selected = append(selected, s)
			fmt.Fprintf(os.Stdout, "%s %s 主营=%s 现价=%.2f 涨跌幅=%.2f%%\n",
				s.Code, s.Name, s.MainBusiness, s.Price, s.ChangePct)
		}
		close(done)
	}()

	go pool.Run(ctx)

	for i := range candidates {
		select {
		case <-ctx.Done():
			trace.Log(ctx, "main: ctx done, produced %d jobs", i)
			goto done
		case jobs <- candidates[i]:
		}
	}
done:
	close(jobs)
	<-done

	sort.Slice(selected, func(i, j int) bool {
		return selected[i].ChangePct > selected[j].ChangePct
	})
	if len(selected) > topNByChangePct {
		selected = selected[:topNByChangePct]
	}
	trace.Log(ctx, "main: 选股完成，按涨幅取前 %d 只, 发邮件", len(selected))
	mailCfg := buildMailConfig(config.LoadSMTP())
	mail.MustSendReport(ctx, mailCfg, selected)
	trace.Log(ctx, "main: end, 共 %d 只", len(selected))
	return selected
}

func buildMailConfig(smtpCfg *config.SMTP) *mail.SMTPConfig {
	if smtpCfg == nil {
		smtpCfg = &config.SMTP{}
	}
	return &mail.SMTPConfig{
		Server:   smtpCfg.Server,
		Port:     smtpCfg.Port,
		User:     smtpCfg.User,
		Password: smtpCfg.Password,
		From:     smtpCfg.From,
		To:       smtpCfg.To,
	}
}

func GetAllStocks(ctx context.Context) ([]model.StockBrief, error) {
	return apiClient.GetAllStocks(ctx)
}

func GetKLines(code string) ([]model.KLine, error) {
	ctx, cancel := context.WithTimeout(context.Background(), getKLinesTimeout)
	defer cancel()
	return apiClient.GetKLines(ctx, code)
}

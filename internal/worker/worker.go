// Package worker 提供选股任务池：消费行情列表、拉 K 线算均线、按条件过滤后输出。
package worker

import (
	"context"
	"sync"

	"stockMaxWin/internal/api"
	"stockMaxWin/internal/model"
	"stockMaxWin/internal/trace"
)

const (
	defaultConcurrency    = 10
	minKlinesForMA20      = 20
	klineCountForStrategy = 80 // 一次请求 80 天，同一 slice 滑动算 MA20/MA60/MACD，不重复请求
	ma60TrendLookback     = 5
	macdFast              = 12
	macdSlow              = 26
	macdSignal            = 9
)

func MA5(klines []model.KLine) float64  { return maN(klines, 5) }
func MA10(klines []model.KLine) float64 { return maN(klines, 10) }
func MA20(klines []model.KLine) float64 { return maN(klines, 20) }
func MA60(klines []model.KLine) float64 { return maN(klines, 60) }

func maN(klines []model.KLine, n int) float64 {
	if len(klines) < n {
		return 0
	}
	last := klines[len(klines)-n:]
	var sum float64
	for i := range last {
		sum += last[i].Close
	}
	return sum / float64(n)
}

// maNAt 计算以第 (len-offset-1) 日为末的 n 日均价，offset 0 表示最后一根 K。
func maNAt(klines []model.KLine, n, offset int) float64 {
	if len(klines) < n+offset {
		return 0
	}
	start := len(klines) - n - offset
	var sum float64
	for i := start; i < start+n; i++ {
		sum += klines[i].Close
	}
	return sum / float64(n)
}

// Filter 对合并后的 Stock 做是否入选判断。
type Filter func(*model.Stock) bool

func DefaultFilter(s *model.Stock) bool {
	return s != nil && s.Price > s.MA20
}

// Config 控制并发数与筛选逻辑。
type Config struct {
	Concurrency int
	Filter      Filter
}

func DefaultConfig() Config {
	return Config{Concurrency: defaultConcurrency, Filter: DefaultFilter}
}

// macdResult 存放 MACD 当日/昨日红柱及是否刚金叉。
type macdResult struct {
	histogram    float64
	histogramPrev float64
	goldenCross  bool
}

func computeMACD(klines []model.KLine) macdResult {
	n := len(klines)
	if n < macdSlow+macdSignal {
		return macdResult{}
	}
	closes := make([]float64, n)
	for i := range klines {
		closes[i] = klines[i].Close
	}
	ema12 := ema(closes, macdFast)
	ema26 := ema(closes, macdSlow)
	dif := make([]float64, n)
	for i := macdSlow - 1; i < n; i++ {
		dif[i] = ema12[i] - ema26[i]
	}
	dea := ema(dif[macdSlow-1:], macdSignal)
	// dea 对应到 closes 的索引：dea[j] 对应 dif[macdSlow-1+j]
	histogram := make([]float64, n)
	for i := macdSlow - 1 + macdSignal - 1; i < n; i++ {
		j := i - (macdSlow - 1)
		histogram[i] = 2 * (dif[i] - dea[j])
	}
	last := n - 1
	prev := n - 2
	h0 := float64(0)
	h1 := float64(0)
	if last >= macdSlow-1+macdSignal-1 {
		h0 = histogram[last]
	}
	if prev >= macdSlow-1+macdSignal-1 {
		h1 = histogram[prev]
	}
	goldenCross := false
	if prev >= macdSlow-1 && last >= macdSlow-1 {
		difPrev := dif[prev]
		difLast := dif[last]
		deaPrevIdx := prev - (macdSlow - 1)
		deaLastIdx := last - (macdSlow - 1)
		if deaPrevIdx >= 0 && deaLastIdx < len(dea) {
			deaPrev := dea[deaPrevIdx]
			deaLast := dea[deaLastIdx]
			if difLast > deaLast && difPrev <= deaPrev {
				goldenCross = true
			}
		}
	}
	return macdResult{histogram: h0, histogramPrev: h1, goldenCross: goldenCross}
}

func ema(data []float64, period int) []float64 {
	if len(data) < period {
		return nil
	}
	out := make([]float64, len(data))
	mult := 2.0 / float64(period+1)
	var sum float64
	for i := 0; i < period; i++ {
		sum += data[i]
	}
	out[period-1] = sum / float64(period)
	for i := period; i < len(data); i++ {
		out[i] = (data[i]-out[i-1])*mult + out[i-1]
	}
	return out
}

// Pool 从 jobs 取行情，拉 K 线合并为 Stock，经 Filter 通过后写入 results。
type Pool struct {
	cfg    Config
	api    *api.Client
	jobs   <-chan model.StockQuote
	out    chan<- *model.Stock
	filter Filter
}

func NewPool(cfg Config, apiClient *api.Client, jobs <-chan model.StockQuote, results chan<- *model.Stock) *Pool {
	if apiClient == nil {
		panic("worker: api client must not be nil")
	}
	if jobs == nil || results == nil {
		panic("worker: jobs and results channels must not be nil")
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = defaultConcurrency
	}
	if cfg.Filter == nil {
		cfg.Filter = DefaultFilter
	}
	return &Pool{
		cfg:    cfg,
		api:    apiClient,
		jobs:   jobs,
		out:    results,
		filter: cfg.Filter,
	}
}

func (p *Pool) Run(ctx context.Context) {
	trace.Log(ctx, "worker: Pool.Run start concurrency=%d", p.cfg.Concurrency)
	var wg sync.WaitGroup
	for i := 0; i < p.cfg.Concurrency; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			p.runWorker(ctx, id)
		}(i)
	}
	wg.Wait()
	close(p.out)
	trace.Log(ctx, "worker: Pool.Run done")
}

func (p *Pool) runWorker(ctx context.Context, workerID int) {
	for {
		select {
		case <-ctx.Done():
			return
		case q, ok := <-p.jobs:
			if !ok {
				return
			}
			stock := p.fetchAndMerge(ctx, &q)
			if stock == nil {
				continue
			}
			if !p.filter(stock) {
				continue
			}
			select {
			case <-ctx.Done():
				return
			case p.out <- stock:
			}
		}
	}
}

func (p *Pool) fetchAndMerge(ctx context.Context, q *model.StockQuote) *model.Stock {
	klines, err := p.api.GetHisKlines(ctx, q.Code, klineCountForStrategy)
	if err != nil {
		trace.Log(ctx, "worker: GetHisKlines code=%s err=%v", q.Code, err)
		return nil
	}
	if len(klines) < minKlinesForMA20 {
		trace.Log(ctx, "worker: klines<%d code=%s", minKlinesForMA20, q.Code)
		return nil
	}
	// 同一 slice 滑动计算，不重复请求：MA5/10/20/60、MA60 趋势、MACD 均从 klines 推导
	ma60Now := maNAt(klines, 60, 0)
	ma60Prev := maNAt(klines, 60, ma60TrendLookback)
	macd := computeMACD(klines)
	return &model.Stock{
		Code:              q.Code,
		Name:              q.Name,
		MainBusiness:      q.MainBusiness,
		Price:             q.Price,
		MA5:               MA5(klines),
		MA10:              MA10(klines),
		MA20:              MA20(klines),
		MA60:              ma60Now,
		ChangePct:         q.ChangePct,
		Amount:            q.Amount,
		VolumeRatio:       q.VolumeRatio,
		TurnoverRate:      q.TurnoverRate,
		MarketCap:         q.MarketCap,
		PE:                q.PE,
		NetInflow:         q.NetInflow,
		MainForceInflow:   q.MainForceInflow,
		MainForceOutflow:  q.MainForceOutflow,
		MA60Up:            ma60Prev > 0 && ma60Now > ma60Prev,
		MacdHistogram:     macd.histogram,
		MacdHistogramPrev: macd.histogramPrev,
		MacdGoldenCross:   macd.goldenCross,
	}
}

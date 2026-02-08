// Package api 封装东方财富行情与 K 线接口，含请求节流、重试与 trace 日志。
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
	"stockMaxWin/internal/model"
	"stockMaxWin/internal/trace"
)

// 环境变量名（API 节流与并发，可选覆盖）
const (
	envAPIDelayMS       = "STOCKMAXWIN_API_DELAY_MS"
	envAPIJitterMS      = "STOCKMAXWIN_API_JITTER_MS"
	envAPIMaxConcurrent = "STOCKMAXWIN_API_MAX_CONCURRENT"
)

// 东方财富接口地址
const (
	EastMoneyListURL   = "https://82.push2.eastmoney.com/api/qt/clist/get"
	EastMoneyKLineURL  = "https://push2his.eastmoney.com/api/qt/stock/kline/get"
	EastMoneyIndexURL  = "https://push2.eastmoney.com/api/qt/ulist.np/get"
	indexSecIDs        = "1.000001,0.399001,0.399006" // 上证指数、深证成指、创业板指
	indexFields        = "f12,f14,f2,f3"              // 代码、名称、现价、涨跌幅
)

// 列表接口请求字段：f2 现价 f3 涨跌幅(%) f6 成交量 f8 换手 f10 量比 f12 代码 f14 名称 f23 成交额 f20 总市值 f9 市盈率
const listFieldsMainBoard = "f2,f3,f6,f8,f10,f12,f14,f23,f20,f9"

// 指数接口 ulist 的 f3 为“百分比×100”，如 -0.25% 返回 -25，需除以 100 后使用
const indexChangePctDivisor = 100

// 全市场列表字段：f12 代码 f14 名称
const listFieldsBrief = "f12,f14"

// 分页
const listPageSize = 500

// 请求超时与重试
const (
	defaultHTTPTimeout = 5 * time.Second
	maxRetries         = 3
	retryDelay         = 500 * time.Millisecond
	retryDelay429      = 5 * time.Second
	httpStatusTooMany  = 429
)

// 防封：请求间隔、抖动、并发上限
const (
	maxRespLogLen        = 1200
	defaultRequestGap    = 200 * time.Millisecond
	defaultRequestJitter = 150
	defaultMaxConcurrent = 4
	maxConcurrentCap     = 20
)

// 请求头（模拟浏览器）
const (
	userAgent      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	referer        = "https://quote.eastmoney.com/"
	acceptLanguage = "zh-CN,zh;q=0.9,en;q=0.8"
)

var (
	requestGap       = defaultRequestGap
	requestJitter    = defaultRequestJitter
	maxConcurrent    = defaultMaxConcurrent
	concurrentSem    chan struct{}
	lastReqTime      time.Time
	lastReqMu        sync.Mutex
	requestGapMu     sync.Mutex
)

func init() {
	if s := os.Getenv(envAPIDelayMS); s != "" {
		if ms, err := strconv.Atoi(s); err == nil && ms > 0 {
			requestGap = time.Duration(ms) * time.Millisecond
		}
	}
	if s := os.Getenv(envAPIJitterMS); s != "" {
		if ms, err := strconv.Atoi(s); err == nil && ms >= 0 {
			requestJitter = ms
		}
	}
	n := defaultMaxConcurrent
	if s := os.Getenv(envAPIMaxConcurrent); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			n = v
			if n > maxConcurrentCap {
				n = maxConcurrentCap
			}
			maxConcurrent = n
		}
	}
	concurrentSem = make(chan struct{}, maxConcurrent)
}

type Client struct {
	HTTPClient *http.Client
}

func NewClient() *Client {
	return &Client{HTTPClient: &http.Client{Timeout: defaultHTTPTimeout}}
}

func paceRequest(ctx context.Context) {
	requestGapMu.Lock()
	gap := requestGap
	jitter := requestJitter
	requestGapMu.Unlock()
	if gap <= 0 && jitter <= 0 {
		return
	}
	lastReqMu.Lock()
	elapsed := time.Since(lastReqTime)
	lastReqMu.Unlock()
	d := gap - elapsed
	if jitter > 0 {
		d += time.Duration(rand.Intn(jitter+1)) * time.Millisecond
	}
	if d > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(d):
		}
	}
	lastReqMu.Lock()
	lastReqTime = time.Now()
	lastReqMu.Unlock()
}

func (c *Client) doWithRetry(ctx context.Context, method, url string) (*http.Response, error) {
	if c == nil {
		return nil, fmt.Errorf("api client is nil")
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}
	var lastErr error
	var lastStatus int
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			backoff := retryDelay
			if lastStatus == httpStatusTooMany {
				backoff = retryDelay429
				trace.Log(ctx, "api: 429 限流，等待 %s 后重试", backoff)
			} else {
				trace.Log(ctx, "api: retry %d/%d %s", attempt, maxRetries, url)
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}
		paceRequest(ctx)
		select {
		case concurrentSem <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		req, err := http.NewRequestWithContext(ctx, method, url, nil)
		if err != nil {
			<-concurrentSem
			lastErr = err
			continue
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Referer", referer)
		req.Header.Set("Accept", "application/json, text/plain, */*")
		req.Header.Set("Accept-Language", acceptLanguage)
		trace.Log(ctx, "api: req %s %s", method, url)
		resp, err := client.Do(req)
		if err != nil {
			<-concurrentSem
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastStatus = resp.StatusCode
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			<-concurrentSem
			trace.Log(ctx, "api: resp status=%d len=%d body=%s", resp.StatusCode, len(body), truncateForLog(body))
			lastErr = fmt.Errorf("http %d", resp.StatusCode)
			continue
		}
		lastStatus = 0
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			_ = resp.Body.Close()
			<-concurrentSem
			lastErr = err
			continue
		}
		_ = resp.Body.Close()
		trace.Log(ctx, "api: resp status=%d len=%d body=%s", resp.StatusCode, len(body), truncateForLog(body))
		resp.Body = &releaseOnClose{Reader: bytes.NewReader(body), release: func() { <-concurrentSem }}
		return resp, nil
	}
	trace.Log(ctx, "api: doWithRetry fail url=%s err=%v", url, lastErr)
	return nil, lastErr
}

type releaseOnClose struct {
	io.Reader
	release func()
}

func (r *releaseOnClose) Close() error {
	if r.release != nil {
		r.release()
		r.release = nil
	}
	return nil
}

func truncateForLog(b []byte) string {
	s := string(b)
	if len(b) > maxRespLogLen {
		s = s[:maxRespLogLen] + "..."
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " ")
}

func (c *Client) GetAllStocks(ctx context.Context) ([]model.StockBrief, error) {
	var all []model.StockBrief
	page := 1
	for {
		url := fmt.Sprintf("%s?pn=%d&pz=%d&fs=m:0+t:6,m:0+t:80,m:1+t:2,m:1+t:23&fields=%s",
			EastMoneyListURL, page, listPageSize, listFieldsBrief)
		resp, err := c.doWithRetry(ctx, http.MethodGet, url)
		if err != nil {
			return nil, err
		}
		total, count, err := decodeStockListStream(resp.Body, &all)
		_ = resp.Body.Close()
		if err != nil && err != io.EOF {
			return nil, err
		}
		if count == 0 {
			break
		}
		if total <= len(all) || count < listPageSize {
			break
		}
		page++
	}
	return all, nil
}

func (c *Client) GetMainBoardQuotes(ctx context.Context) ([]model.StockQuote, error) {
	var list []model.StockQuote
	page := 1
	trace.Log(ctx, "api: GetMainBoardQuotes start")
	for {
		url := fmt.Sprintf("%s?pn=%d&pz=%d&fs=m:1+t:2,m:0+t:2&fields=%s",
			EastMoneyListURL, page, listPageSize, listFieldsMainBoard)
		if page == 1 {
			trace.Log(ctx, "api: GetMainBoardQuotes url=%s", url)
		}
		resp, err := c.doWithRetry(ctx, http.MethodGet, url)
		if err != nil {
			return nil, err
		}
		total, count, err := decodeQuoteListStream(ctx, resp.Body, &list)
		_ = resp.Body.Close()
		if err != nil && err != io.EOF {
			return nil, err
		}
		if count == 0 {
			break
		}
		if total <= len(list) || count < listPageSize {
			break
		}
		page++
	}
	trace.Log(ctx, "api: GetMainBoardQuotes done len=%d", len(list))
	if len(list) == 0 {
		trace.Log(ctx, "api: 主板结果为空，可浏览器打开上述 url 或检查 data.diff 是否被跳过")
	}
	return list, nil
}

// decodeQuoteListStream 解析列表接口 JSON：根对象下 data.total、data.diff（数组或对象 "0","1",...）
func decodeQuoteListStream(ctx context.Context, r io.Reader, list *[]model.StockQuote) (total int, count int, err error) {
	dec := json.NewDecoder(r)
	if t, err := dec.Token(); err != nil {
		return 0, 0, err
	} else if d, ok := t.(json.Delim); !ok || d != '{' {
		return 0, 0, fmt.Errorf("expected {")
	}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return total, count, err
		}
		s, ok := key.(string)
		if !ok || s != "data" {
			if err := skipValue(dec); err != nil {
				return total, count, err
			}
			continue
		}
		if t, err := dec.Token(); err != nil {
			return total, count, err
		} else if d, ok := t.(json.Delim); !ok || d != '{' {
			return total, count, fmt.Errorf("expected data {")
		}
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return total, count, err
			}
			ks, ok := k.(string)
			if !ok {
				return total, count, fmt.Errorf("expected key")
			}
			if ks == "total" {
				var n json.Number
				if err := dec.Decode(&n); err != nil {
					return total, count, err
				}
				v, _ := n.Int64()
				total = int(v)
				continue
			}
			if ks == "diff" {
				t, err := dec.Token()
				if err != nil {
					return total, count, err
				}
				d, ok := t.(json.Delim)
				if !ok {
					trace.Log(ctx, "api: data.diff 非数组/对象已跳过 total=%d", total)
					count = 0
					continue
				}
				start := len(*list)
				if d == '[' {
					for dec.More() {
						if err := decodeQuoteItem(dec, list); err != nil {
							return total, len(*list) - start, err
						}
					}
					if _, err := dec.Token(); err != nil {
						return total, len(*list) - start, err
					}
				} else if d == '{' {
					for dec.More() {
						if _, err := dec.Token(); err != nil {
							return total, len(*list) - start, err
						}
						if err := decodeQuoteItem(dec, list); err != nil {
							return total, len(*list) - start, err
						}
					}
					if _, err := dec.Token(); err != nil {
						return total, len(*list) - start, err
					}
				} else {
					trace.Log(ctx, "api: data.diff 非数组/对象已跳过 total=%d", total)
					count = 0
					continue
				}
				count = len(*list) - start
				continue
			}
			if err := skipValue(dec); err != nil {
				return total, count, err
			}
		}
		if _, err := dec.Token(); err != nil {
			return total, count, err
		}
		break
	}
	return total, count, nil
}

// quoteItemFields 对应东方财富 data.diff 单条：f2 现价 f3 涨跌幅 f6 成交量 f8 换手率 f10 量比 f12 代码 f14 名称 f23 成交额 f20 总市值 f9 市盈率
func decodeQuoteItem(dec *json.Decoder, list *[]model.StockQuote) error {
	var item struct {
		F2   json.Number `json:"f2"`
		F3   json.Number `json:"f3"`
		F6   json.Number `json:"f6"`
		F8   json.Number `json:"f8"`
		F10  json.Number `json:"f10"`
		F12  string      `json:"f12"`
		F14  string      `json:"f14"`
		F23  json.Number `json:"f23"`
		F20  json.Number `json:"f20"`
		F9   json.Number `json:"f9"`
		F62  json.Number `json:"f62"`
		F184 json.Number `json:"f184"`
		F66  json.Number `json:"f66"`
	}
	if err := dec.Decode(&item); err != nil {
		return err
	}
	if item.F12 == "" {
		return nil
	}
	price, _ := item.F2.Float64()
	changePct, _ := item.F3.Float64()
	vol, _ := item.F6.Int64()
	turnoverRate, _ := item.F8.Float64()
	volumeRatio, _ := item.F10.Float64()
	amount, _ := item.F23.Float64()
	if amount <= 0 && vol > 0 && price > 0 {
		amount = float64(vol) * 100 * price
	}
	marketCap, _ := item.F20.Float64()
	pe, _ := item.F9.Float64()
	if pe < 0 {
		pe = 0
	}
	netInflow, _ := item.F62.Float64()
	mainIn, _ := item.F184.Float64()
	mainOut, _ := item.F66.Float64()
	*list = append(*list, model.StockQuote{
		Code:             item.F12,
		Name:             item.F14,
		Price:            price,
		ChangePct:        changePct,
		Amount:           amount,
		VolumeRatio:      volumeRatio,
		TurnoverRate:     turnoverRate,
		MarketCap:        marketCap,
		PE:               pe,
		NetInflow:        netInflow,
		MainForceInflow:  mainIn,
		MainForceOutflow: mainOut,
	})
	return nil
}

func decodeStockListStream(r io.Reader, list *[]model.StockBrief) (total int, count int, err error) {
	dec := json.NewDecoder(r)
	if t, err := dec.Token(); err != nil {
		return 0, 0, err
	} else if d, ok := t.(json.Delim); !ok || d != '{' {
		return 0, 0, fmt.Errorf("expected {")
	}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return total, count, err
		}
		s, ok := key.(string)
		if !ok || s != "data" {
			if err := skipValue(dec); err != nil {
				return total, count, err
			}
			continue
		}
		if t, err := dec.Token(); err != nil {
			return total, count, err
		} else if d, ok := t.(json.Delim); !ok || d != '{' {
			return total, count, fmt.Errorf("expected data {")
		}
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return total, count, err
			}
			ks, ok := k.(string)
			if !ok {
				return total, count, fmt.Errorf("expected key")
			}
			if ks == "total" {
				var n json.Number
				if err := dec.Decode(&n); err != nil {
					return total, count, err
				}
				v, _ := n.Int64()
				total = int(v)
				continue
			}
			if ks == "diff" {
				if t, err := dec.Token(); err != nil {
					return total, count, err
				} else if d, ok := t.(json.Delim); !ok || d != '[' {
					return total, count, fmt.Errorf("expected diff [")
				}
				start := len(*list)
				for dec.More() {
					var item struct {
						F12 string `json:"f12"`
						F14 string `json:"f14"`
					}
					if err := dec.Decode(&item); err != nil {
						return total, len(*list) - start, err
					}
					if item.F12 != "" {
						*list = append(*list, model.StockBrief{Code: item.F12, Name: item.F14})
					}
				}
				if _, err := dec.Token(); err != nil {
					return total, len(*list) - start, err
				}
				count = len(*list) - start
				continue
			}
			if err := skipValue(dec); err != nil {
				return total, count, err
			}
		}
		if _, err := dec.Token(); err != nil {
			return total, count, err
		}
		break
	}
	return total, count, nil
}

// GetHisKlines 拉取 A 股前复权历史 K 线，count 为条数；使用东方财富 API，fqt=1 前复权，5 秒超时。
func (c *Client) GetHisKlines(ctx context.Context, code string, count int) ([]model.KLine, error) {
	if code == "" || count <= 0 {
		return nil, fmt.Errorf("invalid code or count")
	}
	secid := FormatCode(code)
	if count > 1000 {
		count = 1000
	}
	url := fmt.Sprintf("%s?secid=%s&fields1=f1,f2,f3,f4,f5,f6&fields2=f51,f52,f53,f54,f55,f56&klt=101&fqt=1&lmt=%d",
		EastMoneyKLineURL, secid, count)
	resp, err := c.doWithRetry(ctx, http.MethodGet, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return parseKlinesGJSON(body, code)
}

func parseKlinesGJSON(body []byte, code string) ([]model.KLine, error) {
	klines := gjson.GetBytes(body, "data.klines")
	if !klines.Exists() || !klines.IsArray() {
		return nil, fmt.Errorf("api: no data.klines for %s", code)
	}
	arr := klines.Array()
	out := make([]model.KLine, 0, len(arr))
	for _, v := range arr {
		s := strings.TrimSpace(v.String())
		if s == "" {
			continue
		}
		parts := strings.Split(s, ",")
		if len(parts) < 5 {
			continue
		}
		closeVal, _ := strconv.ParseFloat(parts[2], 64)
		openVal, _ := strconv.ParseFloat(parts[1], 64)
		var vol int64
		if len(parts) >= 6 {
			vol, _ = strconv.ParseInt(parts[5], 10, 64)
		}
		out = append(out, model.KLine{
			Date:   parts[0],
			Open:   openVal,
			Close:  closeVal,
			Volume: vol,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("api: no klines for %s", code)
	}
	return out, nil
}

func (c *Client) GetKLines(ctx context.Context, code string) ([]model.KLine, error) {
	return c.GetHisKlines(ctx, code, 30)
}

// GetIndexQuotes 获取今日大盘指数：上证、深证成指、创业板指（用于启动问候邮件）。
func (c *Client) GetIndexQuotes(ctx context.Context) ([]model.IndexQuote, error) {
	url := fmt.Sprintf("%s?secids=%s&fields=%s", EastMoneyIndexURL, indexSecIDs, indexFields)
	resp, err := c.doWithRetry(ctx, http.MethodGet, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read index body: %w", err)
	}
	return parseIndexQuotesGJSON(body)
}

func parseIndexQuotesGJSON(body []byte) ([]model.IndexQuote, error) {
	diff := gjson.GetBytes(body, "data.diff")
	if !diff.Exists() || !diff.IsArray() {
		return nil, fmt.Errorf("api: no data.diff for index")
	}
	arr := diff.Array()
	out := make([]model.IndexQuote, 0, len(arr))
	for _, v := range arr {
		code := strings.TrimSpace(v.Get("f12").String())
		name := strings.TrimSpace(v.Get("f14").String())
		if code == "" && name == "" {
			continue
		}
		price := v.Get("f2").Float()
		rawF3 := v.Get("f3").Float()
		// 东方财富指数接口 f3 多为百分比×100（-0.25% 返回 -25），绝对值>20 时按需除以 100
		changePct := rawF3
		if rawF3 > 20 || rawF3 < -20 {
			changePct = rawF3 / indexChangePctDivisor
		}
		out = append(out, model.IndexQuote{
			Code:      code,
			Name:      name,
			Price:     price,
			ChangePct: changePct,
		})
	}
	return out, nil
}

// FormatCode 转为东方财富 secid：上海 0.600519，深圳 1.000001
func FormatCode(code string) string {
	code = strings.TrimSpace(code)
	if code == "" {
		return "0.000000"
	}
	if code[0] == '6' || code[0] == '5' || code[0] == '9' {
		return "0." + code
	}
	return "1." + code
}

func secID(code string) string {
	return FormatCode(code)
}

func skipValue(dec *json.Decoder) error {
	t, err := dec.Token()
	if err != nil {
		return err
	}
	switch d := t.(type) {
	case json.Delim:
		if d == '{' || d == '[' {
			n := 1
			for n > 0 {
				tt, err := dec.Token()
				if err != nil {
					return err
				}
				if dd, ok := tt.(json.Delim); ok {
					if dd == '{' || dd == '[' {
						n++
					} else {
						n--
					}
				}
			}
		}
	}
	return nil
}

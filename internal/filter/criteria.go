// Package filter 定义选股条件（Criterion）与组合方式（And/Or），DefaultStrategy / TrendMomentumStrategy 为策略入口。
package filter

import (
	"strings"

	"stockMaxWin/internal/model"
)

// 名称关键词（剔除用）
const (
	nameKeywordST   = "ST"
	nameKeywordDelist = "退"
)

// 证券代码前缀：上海主板 6/5、深圳主板 00
const (
	codePrefixShanghai = '6'
	codePrefixShanghaiB = '5'
	codePrefixShenzhen = '0'
	codeSecondShenzhenMain = '0'
)

// Criterion 单条条件：入参为合并后的 Stock，返回是否通过。
type Criterion func(*model.Stock) bool

func And(cs ...Criterion) Criterion {
	return func(s *model.Stock) bool {
		if s == nil {
			return false
		}
		for _, c := range cs {
			if c == nil {
				continue
			}
			if !c(s) {
				return false
			}
		}
		return true
	}
}

func Or(cs ...Criterion) Criterion {
	return func(s *model.Stock) bool {
		if s == nil {
			return false
		}
		for _, c := range cs {
			if c == nil {
				continue
			}
			if c(s) {
				return true
			}
		}
		return false
	}
}

// 默认策略阈值（成交额/量比/换手/涨幅/资金）
const (
	amountMin10Yi   = 1e9
	volumeRatioMin  = 1.5
	turnoverRateMin = 3
	turnoverRateMax = 12
	changePctMin    = 3.5
	changePctMax    = 7
	netInflowMin1Yi = 1e8
)

// MainBoard 仅主板：上海 6/5 开头，深圳 00 开头。
func MainBoard(s *model.Stock) bool {
	code := strings.TrimSpace(s.Code)
	if len(code) < 2 {
		return false
	}
	switch code[0] {
	case codePrefixShanghai, codePrefixShanghaiB:
		return true
	case codePrefixShenzhen:
		return len(code) >= 2 && code[1] == codeSecondShenzhenMain
	default:
		return false
	}
}

func AmountMin(min float64) Criterion {
	return func(s *model.Stock) bool { return s.Amount >= min }
}

func VolumeRatioMin(min float64) Criterion {
	return func(s *model.Stock) bool { return s.VolumeRatio >= min }
}

func TurnoverRateRange(min, max float64) Criterion {
	return func(s *model.Stock) bool { return s.TurnoverRate >= min && s.TurnoverRate <= max }
}

func ChangePctRange(min, max float64) Criterion {
	return func(s *model.Stock) bool { return s.ChangePct >= min && s.ChangePct <= max }
}

func PriceAboveMA5(s *model.Stock) bool   { return s.Price > s.MA5 }
func MA5AboveMA10(s *model.Stock) bool    { return s.MA5 > s.MA10 }
func PriceAboveMA20(s *model.Stock) bool  { return s.Price > s.MA20 }

func ExcludeST(s *model.Stock) bool {
	return !strings.Contains(strings.ToUpper(s.Name), nameKeywordST)
}

func NetInflowMin(min float64) Criterion {
	return func(s *model.Stock) bool {
		if s.NetInflow == 0 && s.MainForceInflow == 0 && s.MainForceOutflow == 0 {
			return true
		}
		return s.NetInflow >= min
	}
}

func MainForceInflowAboveOutflow(s *model.Stock) bool {
	if s.MainForceInflow == 0 && s.MainForceOutflow == 0 {
		return true
	}
	return s.MainForceInflow > s.MainForceOutflow
}

// 趋势动能策略阈值：市值/PE/换手/量比
const (
	marketCapMin50Yi    = 50 * 1e8
	peMin               = 0
	peMax               = 60
	turnoverRateMin3_10  = 3
	turnoverRateMax3_10  = 10
	volumeRatioMin1_2   = 1.2
)

// QuotePreFilter 仅用列表接口数据做初选：剔除 ST/退市、市值>50亿、PE 0-60、换手 3%-10%、量比>1.2。
// 通过后再请求 K 线做技术面过滤，避免对全量股票请求 K 线，大幅缩短耗时。
func QuotePreFilter(q *model.StockQuote) bool {
	if q == nil {
		return false
	}
	if strings.Contains(strings.ToUpper(q.Name), nameKeywordST) {
		return false
	}
	if strings.Contains(q.Name, nameKeywordDelist) {
		return false
	}
	if q.MarketCap < marketCapMin50Yi {
		return false
	}
	if q.PE <= 0 || q.PE < peMin || q.PE > peMax {
		return false
	}
	if q.TurnoverRate < turnoverRateMin3_10 || q.TurnoverRate > turnoverRateMax3_10 {
		return false
	}
	if q.VolumeRatio < volumeRatioMin1_2 {
		return false
	}
	return true
}

func ExcludeDelisted(s *model.Stock) bool {
	return !strings.Contains(s.Name, nameKeywordDelist)
}

func MarketCapMin(min float64) Criterion {
	return func(s *model.Stock) bool { return s.MarketCap >= min }
}

func PERange(min, max float64) Criterion {
	return func(s *model.Stock) bool {
		if s.PE <= 0 {
			return false
		}
		return s.PE >= min && s.PE <= max
	}
}

func MA60Up(s *model.Stock) bool {
	return s.MA60Up
}

// MacdHistogramGrow 红柱较昨日增长且今日为红柱
func MacdHistogramGrow(s *model.Stock) bool {
	return s.MacdHistogram > 0 && s.MacdHistogram > s.MacdHistogramPrev
}

func MacdGoldenCross(s *model.Stock) bool {
	return s.MacdGoldenCross
}

// MacdMomentum 红柱较昨日增长 或 刚完成低位金叉
func MacdMomentum(s *model.Stock) bool {
	return MacdHistogramGrow(s) || MacdGoldenCross(s)
}

// TrendMomentumStrategy 复合策略：基础过滤 + 趋势 + 动能 + 成交量；结果由调用方按涨幅排序取前 N。
func TrendMomentumStrategy() Criterion {
	return And(
		ExcludeST,
		ExcludeDelisted,
		MarketCapMin(marketCapMin50Yi),
		PERange(peMin, peMax),
		PriceAboveMA20,
		MA60Up,
		MacdMomentum,
		TurnoverRateRange(turnoverRateMin3_10, turnoverRateMax3_10),
		VolumeRatioMin(volumeRatioMin1_2),
	)
}

// DefaultStrategy 当前选股策略：主板、成交额≥10亿、量比≥1.5、换手 3%~12%、涨幅 3.5%~7%、均线多头、剔除 ST、资金条件。
func DefaultStrategy() Criterion {
	return And(
		MainBoard,
		AmountMin(amountMin10Yi),
		VolumeRatioMin(volumeRatioMin),
		TurnoverRateRange(turnoverRateMin, turnoverRateMax),
		ChangePctRange(changePctMin, changePctMax),
		PriceAboveMA5,
		MA5AboveMA10,
		PriceAboveMA20,
		ExcludeST,
		NetInflowMin(netInflowMin1Yi),
		MainForceInflowAboveOutflow,
	)
}

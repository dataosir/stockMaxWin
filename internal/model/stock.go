// Package model 定义行情、K 线、选股结果等数据结构。
package model

// Stock 选股结果：行情 + K 线均线 + 市值/PE + MACD 等，供过滤与邮件展示。
type Stock struct {
	Code             string
	Name             string
	MainBusiness     string
	Price            float64
	MA5              float64
	MA10             float64
	MA20             float64
	MA60             float64
	ChangePct        float64
	Amount           float64
	VolumeRatio      float64
	TurnoverRate     float64
	MarketCap        float64 // 总市值(元)
	PE               float64 // 市盈率，无效或负为 0
	NetInflow        float64
	MainForceInflow  float64
	MainForceOutflow float64
	MA60Up           bool    // MA60 相对 5 日前向上
	MacdHistogram    float64 // 当日 MACD 红柱
	MacdHistogramPrev float64 // 昨日 MACD 红柱
	MacdGoldenCross  bool    // 近两日发生低位金叉
}

// StockQuote 列表接口单条：代码、名称、现价、涨跌幅、成交额、量比、换手、市值、PE 等。
type StockQuote struct {
	Code             string
	Name             string
	MainBusiness     string
	Price            float64
	ChangePct        float64
	Amount           float64
	VolumeRatio      float64
	TurnoverRate     float64
	MarketCap        float64
	PE               float64
	NetInflow        float64
	MainForceInflow  float64
	MainForceOutflow float64
}

// StockBrief 仅代码与名称，用于全市场列表等。
type StockBrief struct {
	Code string
	Name string
}

// KLine 单日 K：日期、开收、成交量。
type KLine struct {
	Date   string
	Close  float64
	Open   float64
	Volume int64
}

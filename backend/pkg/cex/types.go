package cex

import "time"

// BinanceConfig 币安配置
type BinanceConfig struct {
	APIEndpoint string // https://api.binance.com
	WSEndpoint  string // wss://stream.binance.com:9443
	APIKey      string // API Key（只读权限）
	APISecret   string // API Secret（只读权限）
	RateLimit   int    // 每分钟请求限制（默认1200）
}

// Ticker 24小时行情数据（币安返回格式）
type Ticker struct {
	Symbol             string `json:"symbol"`              // 交易对符号
	PriceChange        string `json:"priceChange"`         // 24h价格变化
	PriceChangePercent string `json:"priceChangePercent"`  // 24h价格变化百分比
	WeightedAvgPrice   string `json:"weightedAvgPrice"`    // 加权平均价
	PrevClosePrice     string `json:"prevClosePrice"`      // 前收盘价
	LastPrice          string `json:"lastPrice"`           // 最新价
	LastQty            string `json:"lastQty"`             // 最新成交量
	BidPrice           string `json:"bidPrice"`            // 最优买价
	BidQty             string `json:"bidQty"`              // 买一量
	AskPrice           string `json:"askPrice"`            // 最优卖价
	AskQty             string `json:"askQty"`              // 卖一量
	OpenPrice          string `json:"openPrice"`           // 开盘价
	HighPrice          string `json:"highPrice"`           // 最高价
	LowPrice           string `json:"lowPrice"`            // 最低价
	Volume             string `json:"volume"`              // 基础资产成交量
	QuoteVolume        string `json:"quoteVolume"`         // 报价资产成交额
	OpenTime           int64  `json:"openTime"`            // 统计开始时间
	CloseTime          int64  `json:"closeTime"`           // 统计结束时间
	FirstID            int64  `json:"firstId"`             // 首笔成交ID
	LastID             int64  `json:"lastId"`              // 末笔成交ID
	Count              int64  `json:"count"`               // 成交笔数
}

// OrderBook 订单簿数据（币安返回格式）
type OrderBookResponse struct {
	LastUpdateID int64       `json:"lastUpdateId"` // 最后更新ID
	Bids         [][2]string `json:"bids"`         // 买单：[[price, quantity], ...]
	Asks         [][2]string `json:"asks"`         // 卖单：[[price, quantity], ...]
}

// TickerUpdate WebSocket 实时行情更新
type TickerUpdate struct {
	EventType          string `json:"e"` // 事件类型（"24hrTicker"）
	EventTime          int64  `json:"E"` // 事件时间
	Symbol             string `json:"s"` // 交易对
	PriceChange        string `json:"p"` // 24h价格变化
	PriceChangePercent string `json:"P"` // 24h价格变化百分比
	WeightedAvgPrice   string `json:"w"` // 加权平均价
	FirstTradePrice    string `json:"x"` // 首笔成交价
	LastPrice          string `json:"c"` // 最新价
	LastQty            string `json:"Q"` // 最新成交量
	BidPrice           string `json:"b"` // 买一价
	BidQty             string `json:"B"` // 买一量
	AskPrice           string `json:"a"` // 卖一价
	AskQty             string `json:"A"` // 卖一量
	OpenPrice          string `json:"o"` // 开盘价
	HighPrice          string `json:"h"` // 最高价
	LowPrice           string `json:"l"` // 最低价
	Volume             string `json:"v"` // 基础资产成交量
	QuoteVolume        string `json:"q"` // 报价资产成交额
	OpenTime           int64  `json:"O"` // 统计开始时间
	CloseTime          int64  `json:"C"` // 统计结束时间
	FirstTradeID       int64  `json:"F"` // 首笔成交ID
	LastTradeID        int64  `json:"L"` // 末笔成交ID
	TradeCount         int64  `json:"n"` // 成交笔数
}

// PriceData 简化的价格数据
type PriceData struct {
	Symbol    string    `json:"symbol"`
	Price     string    `json:"price"`
	Timestamp time.Time `json:"timestamp"`
}

// ExchangeInfo 交易所信息（用于验证交易对）
type ExchangeInfo struct {
	Timezone   string `json:"timezone"`
	ServerTime int64  `json:"serverTime"`
	Symbols    []SymbolInfo `json:"symbols"`
}

// SymbolInfo 交易对信息
type SymbolInfo struct {
	Symbol              string   `json:"symbol"`
	Status              string   `json:"status"` // TRADING, BREAK, etc.
	BaseAsset           string   `json:"baseAsset"`
	QuoteAsset          string   `json:"quoteAsset"`
	BaseAssetPrecision  int      `json:"baseAssetPrecision"`
	QuoteAssetPrecision int      `json:"quotePrecision"`
	OrderTypes          []string `json:"orderTypes"`
	IsSpotTradingAllowed bool    `json:"isSpotTradingAllowed"`
}


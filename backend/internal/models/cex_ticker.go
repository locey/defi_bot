package models

import (
	"time"

	"github.com/shopspring/decimal"
)

// CexTicker CEX 24小时行情数据表（优化版 - 使用 decimal）
type CexTicker struct {
	ID     uint `gorm:"primaryKey" json:"id"`
	PairID uint `gorm:"index:idx_cex_ticker_pair_time;not null" json:"pair_id"` // 交易对ID

	// === 价格数据（使用 decimal.Decimal）===
	LastPrice        decimal.Decimal  `gorm:"type:numeric(36,18);not null" json:"last_price"`  // 最新成交价
	BidPrice         *decimal.Decimal `gorm:"type:numeric(36,18)" json:"bid_price,omitempty"`            // 买一价
	AskPrice         *decimal.Decimal `gorm:"type:numeric(36,18)" json:"ask_price,omitempty"`            // 卖一价
	HighPrice        *decimal.Decimal `gorm:"type:numeric(36,18)" json:"high_price,omitempty"`           // 24h最高价
	LowPrice         *decimal.Decimal `gorm:"type:numeric(36,18)" json:"low_price,omitempty"`            // 24h最低价
	OpenPrice        *decimal.Decimal `gorm:"type:numeric(36,18)" json:"open_price,omitempty"`           // 开盘价
	ClosePrice       *decimal.Decimal `gorm:"type:numeric(36,18)" json:"close_price,omitempty"`          // 收盘价（当前价）
	WeightedAvgPrice *decimal.Decimal `gorm:"type:numeric(36,18)" json:"weighted_avg_price,omitempty"`   // 加权平均价

	// === 成交量数据 ===
	Volume24h      *decimal.Decimal `gorm:"type:numeric(36,18)" json:"volume_24h,omitempty"`       // 24h成交量（基础资产）
	QuoteVolume24h *decimal.Decimal `gorm:"type:numeric(36,18)" json:"quote_volume_24h,omitempty"` // 24h成交额（报价资产）
	TradeCount     int64            `json:"trade_count"`                                           // 24h成交笔数

	// === 价格变化 ===
	PriceChange        *decimal.Decimal `gorm:"type:numeric(36,18)" json:"price_change,omitempty"` // 24h价格变化（绝对值）
	PriceChangePercent float64          `json:"price_change_percent"`                              // 24h价格变化百分比

	// === 时间信息 ===
	OpenTime  int64 `json:"open_time"`  // 统计开始时间（Unix毫秒）
	CloseTime int64 `json:"close_time"` // 统计结束时间（Unix毫秒）

	// === 元数据 ===
	ExchangeName string    `gorm:"index;size:50" json:"exchange_name"`                      // 交易所名称（如"Binance"）
	Timestamp    time.Time `gorm:"index:idx_cex_ticker_pair_time;not null" json:"timestamp"` // 数据时间戳
	CreatedAt    time.Time `json:"created_at"`

	// 关联
	Pair TradingPair `gorm:"foreignKey:PairID" json:"pair,omitempty"`
}

// TableName 指定表名
func (CexTicker) TableName() string {
	return "cex_tickers"
}

// GetPriceChangeFloat 获取价格变化（float64）
func (ct *CexTicker) GetPriceChangeFloat() float64 {
	return ct.PriceChangePercent
}

// IsPositive 判断是否上涨
func (ct *CexTicker) IsPositive() bool {
	return ct.PriceChangePercent > 0
}

// GetMidPrice 获取中间价（买卖价平均）
func (ct *CexTicker) GetMidPrice() decimal.Decimal {
	if ct.BidPrice == nil || ct.AskPrice == nil || ct.BidPrice.IsZero() || ct.AskPrice.IsZero() {
		return ct.LastPrice
	}
	return ct.BidPrice.Add(*ct.AskPrice).Div(decimal.NewFromInt(2))
}

// GetSpread 获取买卖价差
func (ct *CexTicker) GetSpread() decimal.Decimal {
	if ct.BidPrice == nil || ct.AskPrice == nil || ct.BidPrice.IsZero() || ct.AskPrice.IsZero() {
		return decimal.Zero
	}
	return ct.AskPrice.Sub(*ct.BidPrice)
}

// GetSpreadPercent 获取买卖价差百分比
func (ct *CexTicker) GetSpreadPercent() decimal.Decimal {
	spread := ct.GetSpread()
	if ct.BidPrice == nil || ct.BidPrice.IsZero() {
		return decimal.Zero
	}
	return spread.Div(*ct.BidPrice).Mul(decimal.NewFromInt(100))
}


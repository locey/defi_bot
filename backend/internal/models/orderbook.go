package models

import (
	"time"
)

// OrderBook CEX 订单簿表
type OrderBook struct {
	ID     uint `gorm:"primaryKey" json:"id"`
	PairID uint `gorm:"index:idx_orderbook_pair_time;not null" json:"pair_id"` // 交易对ID

	// === 买单数据（Bids - 买方挂单）===
	Bids string `gorm:"type:jsonb;not null" json:"bids"` // [[price, amount], [price, amount], ...]
	// 按价格从高到低排序

	// === 卖单数据（Asks - 卖方挂单）===
	Asks string `gorm:"type:jsonb;not null" json:"asks"` // [[price, amount], [price, amount], ...]
	// 按价格从低到高排序

	// === 深度统计 ===
	BidDepth  string `gorm:"type:varchar(78)" json:"bid_depth"`  // 买单总深度（总量）
	AskDepth  string `gorm:"type:varchar(78)" json:"ask_depth"`  // 卖单总深度（总量）
	BidPrice1 string `gorm:"type:varchar(78)" json:"bid_price1"` // 最优买价（买一价）
	AskPrice1 string `gorm:"type:varchar(78)" json:"ask_price1"` // 最优卖价（卖一价）
	MidPrice  string `gorm:"type:varchar(78)" json:"mid_price"`  // 中间价（(bid1+ask1)/2）
	Spread    float64 `json:"spread"`                             // 价差百分比（(ask1-bid1)/bid1 × 100）

	// === 深度质量指标 ===
	Bid5Depth  string `gorm:"type:varchar(78)" json:"bid_5_depth"`  // 前5档买单深度
	Ask5Depth  string `gorm:"type:varchar(78)" json:"ask_5_depth"`  // 前5档卖单深度
	Bid10Depth string `gorm:"type:varchar(78)" json:"bid_10_depth"` // 前10档买单深度
	Ask10Depth string `gorm:"type:varchar(78)" json:"ask_10_depth"` // 前10档卖单深度

	// === 元数据 ===
	SequenceID int64     `json:"sequence_id"`                                     // CEX订单簿序列号（用于更新检测）
	Timestamp  time.Time `gorm:"index:idx_orderbook_pair_time;not null" json:"timestamp"` // 快照时间
	CreatedAt  time.Time `json:"created_at"`

	// 关联
	Pair TradingPair `gorm:"foreignKey:PairID" json:"pair,omitempty"`
}

// TableName 指定表名
func (OrderBook) TableName() string {
	return "orderbooks"
}

// GetSpreadBps 获取价差（基点）
func (ob *OrderBook) GetSpreadBps() int {
	return int(ob.Spread * 10000)
}

// IsLiquidEnough 判断流动性是否充足
func (ob *OrderBook) IsLiquidEnough(minDepth float64) bool {
	// 简单判断：需要实现字符串到float64的转换
	// TODO: 实现完整的流动性判断逻辑
	return ob.Spread < 0.01 // 价差小于1%视为流动性充足
}


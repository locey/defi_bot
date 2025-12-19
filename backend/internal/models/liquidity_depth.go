package models

import (
	"time"
)

// LiquidityDepth 流动性深度表
// 用于存储不同交易金额下的预期输出，是计算滑点和评估套利可行性的关键数据
type LiquidityDepth struct {
	ID     uint `gorm:"primaryKey" json:"id"`
	PairID uint `gorm:"index:idx_pair_direction_time;not null" json:"pair_id"` // 交易对 ID

	// 测试金额和预期输出（用于滑点计算）
	AmountIn    string  `gorm:"type:varchar(78);not null" json:"amount_in"`  // 输入金额（如 1 ETH, 10 ETH, 100 ETH）
	AmountOut   string  `gorm:"type:varchar(78);not null" json:"amount_out"` // 预期输出（通过模拟计算得出）
	PriceImpact float64 `gorm:"not null" json:"price_impact"`                // 价格影响（滑点）百分比
	SlippageBps uint32  `gorm:"not null" json:"slippage_bps"`                // 滑点（基点，1 bps = 0.01%）

	// 交易方向
	Direction string `gorm:"index:idx_pair_direction_time;size:20;not null" json:"direction"` // "token0_to_token1" 或 "token1_to_token0"

	// 执行价格（实际成交价格）
	ExecutionPrice string `gorm:"type:varchar(78)" json:"execution_price"` // 实际成交价格

	// 区块和时间信息
	BlockNumber uint64    `gorm:"index;not null" json:"block_number"`                      // 区块号
	Timestamp   time.Time `gorm:"index:idx_pair_direction_time;not null" json:"timestamp"` // 时间戳
	CreatedAt   time.Time `json:"created_at"`

	// 关联
	Pair TradingPair `gorm:"foreignKey:PairID" json:"pair,omitempty"`
}

// TableName 指定表名
func (LiquidityDepth) TableName() string {
	return "liquidity_depths"
}

// GetSlippagePercent 获取滑点百分比
func (l *LiquidityDepth) GetSlippagePercent() float64 {
	return float64(l.SlippageBps) / 100.0
}

// IsHighSlippage 判断是否为高滑点（>1%）
func (l *LiquidityDepth) IsHighSlippage() bool {
	return l.SlippageBps > 100 // 100 bps = 1%
}

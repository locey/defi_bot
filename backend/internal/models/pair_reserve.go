package models

import (
	"time"
)

// PairReserve 流动性池储备表（实时数据）
type PairReserve struct {
	ID     uint `gorm:"primaryKey" json:"id"`
	PairID uint `gorm:"index:idx_pair_time;not null" json:"pair_id"` // 交易对 ID

	// === V2 储备量数据 ===
	Reserve0 string `gorm:"type:varchar(78);not null" json:"reserve0"` // 代币0储备量（使用字符串存储大整数）
	Reserve1 string `gorm:"type:varchar(78);not null" json:"reserve1"` // 代币1储备量

	// === V3 流动性数据 ===
	SqrtPriceX96 string `gorm:"type:varchar(78)" json:"sqrt_price_x96"` // V3 当前价格的平方根（96位定点数）
	Tick         int32  `gorm:"default:0" json:"tick"`                  // V3 当前tick
	Liquidity    string `gorm:"type:varchar(78)" json:"liquidity"`      // V3 当前活跃流动性

	// === 元数据 ===
	BlockNumber uint64    `gorm:"index;not null" json:"block_number"`            // 区块号
	Timestamp   time.Time `gorm:"index:idx_pair_time;not null" json:"timestamp"` // 时间戳
	CreatedAt   time.Time `json:"created_at"`

	// 关联
	Pair TradingPair `gorm:"foreignKey:PairID" json:"pair,omitempty"`
}

// TableName 指定表名
func (PairReserve) TableName() string {
	return "pair_reserves"
}

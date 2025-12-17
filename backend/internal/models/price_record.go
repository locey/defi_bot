package models

import (
	"time"
)

// PriceRecord 价格记录表
type PriceRecord struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	PairID       uint      `gorm:"index:idx_pair_time;not null" json:"pair_id"`    // 交易对 ID
	Price        string    `gorm:"type:varchar(78);not null" json:"price"`         // 价格（token1/token0）
	InversePrice string    `gorm:"type:varchar(78);not null" json:"inverse_price"` // 反向价格（token0/token1）
	Reserve0     string    `gorm:"type:varchar(78);not null" json:"reserve0"`      // 代币0储备量
	Reserve1     string    `gorm:"type:varchar(78);not null" json:"reserve1"`      // 代币1储备量
	BlockNumber  uint64    `gorm:"index;not null" json:"block_number"`             // 区块号
	Timestamp    time.Time `gorm:"index:idx_pair_time;not null" json:"timestamp"`  // 时间戳
	CreatedAt    time.Time `json:"created_at"`

	// 关联
	Pair TradingPair `gorm:"foreignKey:PairID" json:"pair,omitempty"`
}

// TableName 指定表名
func (PriceRecord) TableName() string {
	return "price_records"
}

package models

import (
	"time"
)

// PairReserve 流动性池储备表（实时数据）
type PairReserve struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	PairID      uint      `gorm:"index:idx_pair_time;not null" json:"pair_id"`   // 交易对 ID
	Reserve0    string    `gorm:"type:varchar(78);not null" json:"reserve0"`     // 代币0储备量（使用字符串存储大整数）
	Reserve1    string    `gorm:"type:varchar(78);not null" json:"reserve1"`     // 代币1储备量
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

package models

import (
	"time"
)

// TradingPair 交易对表
type TradingPair struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	DexID       uint      `gorm:"index:idx_dex_tokens;not null" json:"dex_id"`      // DEX ID
	Token0ID    uint      `gorm:"index:idx_dex_tokens;not null" json:"token0_id"`   // 代币0 ID
	Token1ID    uint      `gorm:"index:idx_dex_tokens;not null" json:"token1_id"`   // 代币1 ID
	PairAddress string    `gorm:"uniqueIndex;not null;size:42" json:"pair_address"` // 交易对合约地址
	IsActive    bool      `gorm:"default:true" json:"is_active"`                    // 是否启用
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	// 关联
	Dex      Dex           `gorm:"foreignKey:DexID" json:"dex,omitempty"`
	Token0   Token         `gorm:"foreignKey:Token0ID" json:"token0,omitempty"`
	Token1   Token         `gorm:"foreignKey:Token1ID" json:"token1,omitempty"`
	Reserves []PairReserve `gorm:"foreignKey:PairID" json:"-"`
}

// TableName 指定表名
func (TradingPair) TableName() string {
	return "trading_pairs"
}

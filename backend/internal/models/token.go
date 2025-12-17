package models

import (
	"time"
)

// Token 代币信息表
type Token struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Address   string    `gorm:"uniqueIndex;not null;size:42" json:"address"` // 代币合约地址
	Symbol    string    `gorm:"index;not null;size:20" json:"symbol"`        // 代币符号，如 ETH, USDT
	Name      string    `gorm:"size:100" json:"name"`                        // 代币名称
	Decimals  int       `gorm:"not null" json:"decimals"`                    // 精度
	ChainID   int64     `gorm:"index;not null" json:"chain_id"`              // 链 ID
	IsActive  bool      `gorm:"default:true" json:"is_active"`               // 是否启用
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// 关联
	TradingPairs []TradingPair `gorm:"foreignKey:Token0ID;references:ID" json:"-"`
}

// TableName 指定表名
func (Token) TableName() string {
	return "tokens"
}

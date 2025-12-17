package models

import (
	"time"
)

// Dex DEX 信息表
type Dex struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	Name           string    `gorm:"uniqueIndex;not null;size:50" json:"name"`         // DEX 名称，如 Uniswap V2
	Protocol       string    `gorm:"index;size:20;default:uniswap_v2" json:"protocol"` // 协议类型：uniswap_v2, uniswap_v3, sushiswap 等
	RouterAddress  string    `gorm:"not null;size:42" json:"router_address"`           // 路由合约地址
	FactoryAddress string    `gorm:"not null;size:42" json:"factory_address"`          // 工厂合约地址
	Fee            int       `gorm:"not null" json:"fee"`                              // 手续费（基点，如 30 表示 0.3%）
	FeeTier        uint32    `gorm:"default:0" json:"fee_tier"`                        // V3 费率层级（如 500, 3000, 10000），V2 为 0
	ChainID        int64     `gorm:"index;not null" json:"chain_id"`                   // 链 ID
	IsActive       bool      `gorm:"default:true" json:"is_active"`                    // 是否启用
	Version        string    `gorm:"size:20" json:"version"`                           // 版本，如 v2, v3
	Description    string    `gorm:"type:text" json:"description"`                     // 描述
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`

	// 关联
	TradingPairs []TradingPair `gorm:"foreignKey:DexID;references:ID" json:"-"`
}

// TableName 指定表名
func (Dex) TableName() string {
	return "dexes"
}

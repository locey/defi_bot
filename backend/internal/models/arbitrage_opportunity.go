package models

import (
	"time"
)

// ArbitrageOpportunity 套利机会表
type ArbitrageOpportunity struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	TokenInID      uint      `gorm:"index;not null" json:"token_in_id"`                      // 输入代币 ID
	TokenOutID     uint      `gorm:"not null" json:"token_out_id"`                           // 输出代币 ID（中间代币）
	AmountIn       string    `gorm:"type:varchar(78);not null" json:"amount_in"`             // 输入金额
	ExpectedProfit string    `gorm:"type:varchar(78);not null" json:"expected_profit"`       // 预期利润
	ProfitRate     float64   `gorm:"not null" json:"profit_rate"`                            // 利润率（百分比）
	SwapPath       string    `gorm:"type:text;not null" json:"swap_path"`                    // 交易路径（JSON 数组）
	DexPath        string    `gorm:"type:text;not null" json:"dex_path"`                     // DEX 路径（JSON 数组）
	GasEstimate    uint64    `gorm:"not null" json:"gas_estimate"`                           // Gas 估算
	Status         string    `gorm:"index;not null;size:20;default:'pending'" json:"status"` // 状态：pending, executed, expired, failed
	Priority       int       `gorm:"index;default:0" json:"priority"`                        // 优先级（利润率高的优先）
	ExpiresAt      time.Time `gorm:"index;not null" json:"expires_at"`                       // 过期时间
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`

	// 关联
	TokenIn  Token `gorm:"foreignKey:TokenInID" json:"token_in,omitempty"`
	TokenOut Token `gorm:"foreignKey:TokenOutID" json:"token_out,omitempty"`
}

// TableName 指定表名
func (ArbitrageOpportunity) TableName() string {
	return "arbitrage_opportunities"
}

// IsExpired 检查是否已过期
func (a *ArbitrageOpportunity) IsExpired() bool {
	return time.Now().After(a.ExpiresAt)
}

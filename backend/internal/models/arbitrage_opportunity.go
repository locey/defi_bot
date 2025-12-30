package models

import (
	"time"
)

// ArbitrageOpportunity 套利机会表
type ArbitrageOpportunity struct {
	ID         uint `gorm:"primaryKey" json:"id"`
	TokenInID  uint `gorm:"index;not null" json:"token_in_id"` // 输入代币 ID
	TokenOutID uint `gorm:"not null" json:"token_out_id"`      // 输出代币 ID（中间代币）

	// === 套利类型标识 ===
	ArbitrageType string `gorm:"index;size:20;not null;default:'cross_dex'" json:"arbitrage_type"`
	// 类型：cross_dex（跨DEX）, fee_tier（V3费率套利）, triangular（三角套利）, flash_loan（闪电贷套利）

	// === 金额和利润 ===
	AmountIn       string  `gorm:"type:varchar(78);not null" json:"amount_in"`       // 输入金额
	ExpectedProfit string  `gorm:"type:varchar(78);not null" json:"expected_profit"` // 预期利润
	MinProfit      string  `gorm:"type:varchar(78);not null" json:"min_profit"`      // 最小利润（合约需要）
	ProfitRate     float64 `gorm:"not null" json:"profit_rate"`                      // 利润率（百分比）
	MinProfitUSD   float64 `gorm:"default:0" json:"min_profit_usd"`                  // 最小美元利润

	// === 路径信息 ===
	SwapPath   string `gorm:"type:jsonb;not null" json:"swap_path"`   // 交易路径（JSON 数组，代币地址）
	DexPath    string `gorm:"type:jsonb;not null" json:"dex_path"`    // DEX 路径（JSON 数组，DEX名称）
	DexRouters string `gorm:"type:jsonb;not null" json:"dex_routers"` // DEX 路由器地址数组（合约调用需要）

	// === V3 专用字段（费率套利）===
	PoolAddresses string `gorm:"type:jsonb" json:"pool_addresses"` // V3 池地址数组 ["0xabc...", "0xdef..."]
	FeeTiers      string `gorm:"type:jsonb" json:"fee_tiers"`      // V3 费率数组 [500, 10000]

	// === 滑点和深度 ===
	ExpectedSlippage   float64 `gorm:"default:0" json:"expected_slippage"`          // 预期滑点（百分比）
	MaxSlippage        float64 `gorm:"default:1.0" json:"max_slippage"`             // 最大可接受滑点（百分比）
	AvailableLiquidity string  `gorm:"type:varchar(78)" json:"available_liquidity"` // 可用流动性

	// === 执行条件 ===
	MaxGasPrice string `gorm:"type:varchar(78)" json:"max_gas_price"` // 最大可接受Gas价格（wei）
	GasEstimate uint64 `gorm:"not null" json:"gas_estimate"`          // Gas 估算

	// === 状态管理 ===
	Status    string    `gorm:"index;not null;size:20;default:'pending'" json:"status"` // 状态：pending, executing, executed, expired, failed
	Priority  int       `gorm:"index;default:0" json:"priority"`                        // 优先级（利润率高的优先）
	ExpiresAt time.Time `gorm:"index;not null" json:"expires_at"`                       // 过期时间

	// === 时间戳 ===
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

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

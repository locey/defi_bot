package models

import (
	"time"
)

// ArbitrageExecution 套利执行记录表
type ArbitrageExecution struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	OpportunityID   uint      `gorm:"index" json:"opportunity_id"`                    // 套利机会 ID（可为空，手动执行时）
	VaultAddress    string    `gorm:"index;size:42" json:"vault_address"`             // 金库地址
	TokenInID       uint      `gorm:"index;not null" json:"token_in_id"`              // 输入代币 ID
	TokenOutID      uint      `gorm:"not null" json:"token_out_id"`                   // 输出代币 ID
	AmountIn        string    `gorm:"type:varchar(78);not null" json:"amount_in"`     // 输入金额
	AmountOut       string    `gorm:"type:varchar(78);not null" json:"amount_out"`    // 输出金额
	ActualProfit    string    `gorm:"type:varchar(78);not null" json:"actual_profit"` // 实际利润
	ProfitRate      float64   `gorm:"not null" json:"profit_rate"`                    // 利润率（百分比）
	SwapPath        string    `gorm:"type:text;not null" json:"swap_path"`            // 交易路径（JSON 数组）
	DexPath         string    `gorm:"type:text;not null" json:"dex_path"`             // DEX 路径（JSON 数组）
	GasUsed         uint64    `gorm:"not null" json:"gas_used"`                       // 实际 Gas 消耗
	GasPrice        string    `gorm:"type:varchar(78);not null" json:"gas_price"`     // Gas 价格（wei）
	TxHash          string    `gorm:"uniqueIndex;not null;size:66" json:"tx_hash"`    // 交易哈希
	BlockNumber     uint64    `gorm:"index;not null" json:"block_number"`             // 区块号
	Status          string    `gorm:"index;not null;size:20" json:"status"`           // 状态：pending, success, failed
	ErrorMessage    string    `gorm:"type:text" json:"error_message"`                 // 错误信息
	ExecutionTimeMs int64     `gorm:"not null" json:"execution_time_ms"`              // 执行时间（毫秒）
	Timestamp       time.Time `gorm:"index;not null" json:"timestamp"`                // 时间戳
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`

	// 关联
	Opportunity *ArbitrageOpportunity `gorm:"foreignKey:OpportunityID" json:"opportunity,omitempty"`
	TokenIn     Token                 `gorm:"foreignKey:TokenInID" json:"token_in,omitempty"`
	TokenOut    Token                 `gorm:"foreignKey:TokenOutID" json:"token_out,omitempty"`
}

// TableName 指定表名
func (ArbitrageExecution) TableName() string {
	return "arbitrage_executions"
}

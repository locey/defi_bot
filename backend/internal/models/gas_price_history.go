package models

import (
	"time"
)

// GasPriceHistory Gas价格历史表
// 用于跟踪Gas价格变化，帮助优化套利执行时机
type GasPriceHistory struct {
	ID uint `gorm:"primaryKey" json:"id"`

	// === Gas 价格信息 ===
	GasPrice string `gorm:"type:varchar(78);not null" json:"gas_price"` // 基础 Gas 价格（wei）
	Priority string `gorm:"type:varchar(78)" json:"priority"`           // EIP-1559 priority fee (wei)
	MaxFee   string `gorm:"type:varchar(78)" json:"max_fee"`            // EIP-1559 max fee per gas (wei)
	BaseFee  string `gorm:"type:varchar(78)" json:"base_fee"`           // EIP-1559 base fee (wei)

	// === 不同速度的 Gas 价格 ===
	FastPrice     string `gorm:"type:varchar(78)" json:"fast_price"`     // 快速确认价格（<30秒）
	StandardPrice string `gorm:"type:varchar(78)" json:"standard_price"` // 标准价格（<5分钟）
	SlowPrice     string `gorm:"type:varchar(78)" json:"slow_price"`     // 慢速价格（<30分钟）

	// === 网络状态 ===
	PendingTxCount int    `gorm:"default:0" json:"pending_tx_count"`            // 待处理交易数量
	NetworkLoad    string `gorm:"size:20;default:'normal'" json:"network_load"` // 网络负载：low, normal, high, congested

	// === 元数据 ===
	BlockNumber uint64    `gorm:"index;not null" json:"block_number"` // 区块号
	Timestamp   time.Time `gorm:"index;not null" json:"timestamp"`    // 时间戳
	CreatedAt   time.Time `json:"created_at"`
}

// TableName 指定表名
func (GasPriceHistory) TableName() string {
	return "gas_price_history"
}

// IsNetworkCongested 判断网络是否拥堵
func (g *GasPriceHistory) IsNetworkCongested() bool {
	return g.NetworkLoad == "high" || g.NetworkLoad == "congested"
}

// GetRecommendedGasPrice 根据优先级获取推荐 Gas 价格
func (g *GasPriceHistory) GetRecommendedGasPrice(priority string) string {
	switch priority {
	case "fast":
		return g.FastPrice
	case "slow":
		return g.SlowPrice
	default:
		return g.StandardPrice
	}
}

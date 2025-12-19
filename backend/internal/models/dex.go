package models

import (
	"time"
)

// Dex DEX 信息表
type Dex struct {
	ID   uint   `gorm:"primaryKey" json:"id"`
	Name string `gorm:"uniqueIndex;not null;size:50" json:"name"` // DEX 名称，如 Uniswap V2

	// === DEX 分类 ===
	DexType  string `gorm:"index;size:20;not null;default:'amm'" json:"dex_type"` // DEX类型：amm, aggregator, orderbook, hybrid
	Protocol string `gorm:"index;size:20;default:uniswap_v2" json:"protocol"`     // 协议类型：uniswap_v2, uniswap_v3, sushiswap, curve, 1inch 等
	Version  string `gorm:"size:20" json:"version"`                               // 版本，如 v2, v3

	// === 合约地址 ===
	RouterAddress  string `gorm:"not null;size:42" json:"router_address"`  // 路由合约地址
	FactoryAddress string `gorm:"not null;size:42" json:"factory_address"` // 工厂合约地址（聚合器可为空）
	QuoterAddress  string `gorm:"size:42" json:"quoter_address"`           // Quoter合约地址（V3专用）

	// === 费用配置 ===
	Fee        int    `gorm:"not null" json:"fee"`              // 手续费（基点，如 30 表示 0.3%）
	FeeTier    uint32 `gorm:"default:0" json:"fee_tier"`        // V3 费率层级（如 500, 3000, 10000），V2 为 0
	DynamicFee bool   `gorm:"default:false" json:"dynamic_fee"` // 是否为动态费率（如 1inch）

	// === 功能支持 ===
	SupportFlashLoan bool `gorm:"default:false" json:"support_flash_loan"` // 是否支持闪电贷
	SupportMultiHop  bool `gorm:"default:true" json:"support_multi_hop"`   // 是否支持多跳路由
	SupportV3Ticks   bool `gorm:"default:false" json:"support_v3_ticks"`   // 是否支持V3 tick数据

	// === 元数据 ===
	ChainID     int64  `gorm:"index;not null" json:"chain_id"` // 链 ID
	IsActive    bool   `gorm:"default:true" json:"is_active"`  // 是否启用
	Priority    int    `gorm:"default:100" json:"priority"`    // 优先级（数值越小越优先）
	Description string `gorm:"type:text" json:"description"`   // 描述
	WebsiteURL  string `gorm:"size:200" json:"website_url"`    // 官网地址

	// === 时间戳 ===
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// 关联
	TradingPairs []TradingPair `gorm:"foreignKey:DexID;references:ID" json:"-"`
}

// GetDexCategory 获取 DEX 类别（用于业务逻辑判断）
func (d *Dex) GetDexCategory() string {
	return d.DexType
}

// IsAMM 判断是否为 AMM 类型
func (d *Dex) IsAMM() bool {
	return d.DexType == "amm"
}

// IsAggregator 判断是否为聚合器
func (d *Dex) IsAggregator() bool {
	return d.DexType == "aggregator"
}

// SupportsQuoter 判断是否支持 Quoter（V3 和部分聚合器）
func (d *Dex) SupportsQuoter() bool {
	return d.QuoterAddress != "" && d.QuoterAddress != "0x0000000000000000000000000000000000000000"
}

// TableName 指定表名
func (Dex) TableName() string {
	return "dexes"
}

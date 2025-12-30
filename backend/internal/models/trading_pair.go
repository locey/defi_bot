package models

import (
	"time"
)

// TradingPair 交易对表（支持 DEX 和 CEX）
type TradingPair struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	ExchangeID  uint   `gorm:"index:idx_exchange_tokens;not null" json:"exchange_id"` // 交易所ID（DEX或CEX）
	Token0ID    uint   `gorm:"index:idx_exchange_tokens;not null" json:"token0_id"`   // 代币0 ID
	Token1ID    uint   `gorm:"index:idx_exchange_tokens;not null" json:"token1_id"`   // 代币1 ID
	
	// === DEX 字段 ===
	PairAddress string `gorm:"size:42" json:"pair_address"` // DEX交易对合约地址（CEX为空）

	// === V3 特有字段（DEX）===
	TickSpacing int32  `gorm:"default:0" json:"tick_spacing"`            // V3 tick间距（60, 200等）
	PoolVersion string `gorm:"size:10;default:'v2'" json:"pool_version"` // 池版本（"v2", "v3"）
	
	// === CEX 字段 ===
	Symbol     string `gorm:"index;size:20" json:"symbol"`      // CEX交易对符号（如"ETHUSDT"）
	BaseAsset  string `gorm:"size:10" json:"base_asset"`        // 基础资产（如"ETH"）
	QuoteAsset string `gorm:"size:10" json:"quote_asset"`       // 报价资产（如"USDT"）

	// === 流动性状态 ===
	MinLiquidity       string    `gorm:"type:varchar(78)" json:"min_liquidity"`     // 最小流动性阈值
	CurrentLiquidity   string    `gorm:"type:varchar(78)" json:"current_liquidity"` // 当前流动性
	IsLiquidEnough     bool      `gorm:"default:true" json:"is_liquid_enough"`      // 流动性是否充足
	LastLiquidityCheck time.Time `json:"last_liquidity_check"`                      // 上次流动性检查时间

	// === 状态标识 ===
	IsActive  bool      `gorm:"default:true" json:"is_active"` // 是否启用
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// 关联
	Exchange Exchange      `gorm:"foreignKey:ExchangeID" json:"exchange,omitempty"`
	Token0   Token         `gorm:"foreignKey:Token0ID" json:"token0,omitempty"`
	Token1   Token         `gorm:"foreignKey:Token1ID" json:"token1,omitempty"`
	Reserves []PairReserve `gorm:"foreignKey:PairID" json:"-"`
	
	// 向后兼容
	Dex Exchange `gorm:"foreignKey:ExchangeID" json:"dex,omitempty"`
}

// TableName 指定表名
func (TradingPair) TableName() string {
	return "trading_pairs"
}

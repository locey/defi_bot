package models

import (
	"time"
)

// TradingPair 交易对表
type TradingPair struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	DexID       uint   `gorm:"index:idx_dex_tokens;not null" json:"dex_id"`      // DEX ID
	Token0ID    uint   `gorm:"index:idx_dex_tokens;not null" json:"token0_id"`   // 代币0 ID
	Token1ID    uint   `gorm:"index:idx_dex_tokens;not null" json:"token1_id"`   // 代币1 ID
	PairAddress string `gorm:"uniqueIndex;not null;size:42" json:"pair_address"` // 交易对合约地址

	// === V3 特有字段 ===
	TickSpacing int32  `gorm:"default:0" json:"tick_spacing"`            // V3 tick间距（60, 200等）
	PoolVersion string `gorm:"size:10;default:'v2'" json:"pool_version"` // 池版本（"v2", "v3"）

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
	Dex      Dex           `gorm:"foreignKey:DexID" json:"dex,omitempty"`
	Token0   Token         `gorm:"foreignKey:Token0ID" json:"token0,omitempty"`
	Token1   Token         `gorm:"foreignKey:Token1ID" json:"token1,omitempty"`
	Reserves []PairReserve `gorm:"foreignKey:PairID" json:"-"`
}

// TableName 指定表名
func (TradingPair) TableName() string {
	return "trading_pairs"
}

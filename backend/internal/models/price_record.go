package models

import (
	"time"

	"github.com/shopspring/decimal"
)

// PriceRecord 价格记录表（优化版 - 业界标准）
type PriceRecord struct {
	ID     uint `gorm:"primaryKey" json:"id"`
	PairID uint `gorm:"index:idx_pair_time;not null" json:"pair_id"` // 交易对 ID

	// === 原始值（用于合约调用和精确计算）===
	Reserve0 string `gorm:"type:varchar(78);not null;column:reserve0" json:"reserve0"` // 代币0储备量（Wei）
	Reserve1 string `gorm:"type:varchar(78);not null;column:reserve1" json:"reserve1"` // 代币1储备量（Wei）

	// === 标准化价格（用于显示和分析）===
	// 注意：使用 numeric(78,18) 支持超大价格（如 WETH/USDC 由于 decimals 差异导致的极端值）
	Price        decimal.Decimal  `gorm:"type:numeric(78,18);not null" json:"price"`                   // 标准化价格（token1/token0）
	InversePrice *decimal.Decimal `gorm:"type:numeric(78,18)" json:"inverse_price,omitempty"`          // 反向价格（token0/token1）
	PriceUSD     *decimal.Decimal `gorm:"type:numeric(78,18)" json:"price_usd,omitempty"`              // USD 计价（可选）

	// === V3 核心数据 ===
	SqrtPriceX96     string `gorm:"type:varchar(78)" json:"sqrt_price_x96"`      // V3 当前价格的平方根（96位定点数）
	Tick             int32  `gorm:"default:0" json:"tick"`                       // V3 当前tick
	Liquidity        string `gorm:"type:varchar(78)" json:"liquidity"`           // V3 当前活跃流动性
	FeeGrowthGlobal0 string `gorm:"type:varchar(78)" json:"fee_growth_global_0"` // V3 手续费增长0
	FeeGrowthGlobal1 string `gorm:"type:varchar(78)" json:"fee_growth_global_1"` // V3 手续费增长1

	// === 深度数据（JSON 格式存储多个测试点）===
	DepthToken0To1 *string `gorm:"type:jsonb" json:"depth_token0_to1"` // token0->token1 深度数组（可为NULL）
	DepthToken1To0 *string `gorm:"type:jsonb" json:"depth_token1_to0"` // token1->token0 深度数组（可为NULL）

	// === 成交量数据 ===
	Volume24h *decimal.Decimal `gorm:"type:numeric(36,18)" json:"volume_24h,omitempty"` // 24小时成交量（标准化）

	// === 元数据 ===
	BlockNumber uint64    `gorm:"index;not null" json:"block_number"`            // 区块号
	Timestamp   time.Time `gorm:"index:idx_pair_time;not null" json:"timestamp"` // 时间戳
	CreatedAt   time.Time `json:"created_at"`

	// 关联
	Pair TradingPair `gorm:"foreignKey:PairID" json:"pair,omitempty"`
}

// TableName 指定表名
func (PriceRecord) TableName() string {
	return "price_records"
}

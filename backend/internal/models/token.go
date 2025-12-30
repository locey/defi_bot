package models

import (
	"time"
)

// Token 代币信息表
type Token struct {
	ID       uint   `gorm:"primaryKey" json:"id"`
	Address  string `gorm:"uniqueIndex;not null;size:42" json:"address"` // 代币合约地址
	Symbol   string `gorm:"index;not null;size:20" json:"symbol"`        // 代币符号，如 ETH, USDT
	Name     string `gorm:"size:100" json:"name"`                        // 代币名称
	Decimals int    `gorm:"not null" json:"decimals"`                    // 精度
	ChainID  int64  `gorm:"index;not null" json:"chain_id"`              // 链 ID

	// === 价格信息 ===
	PriceUSD       float64   `gorm:"default:0" json:"price_usd"`        // 当前美元价格
	PriceUpdatedAt time.Time `json:"price_updated_at"`                  // 价格更新时间
	Price24hChange float64   `gorm:"default:0" json:"price_24h_change"` // 24小时价格变化（百分比）

	// === 市场数据 ===
	TotalSupply       string  `gorm:"type:varchar(78)" json:"total_supply"`       // 总供应量
	CirculatingSupply string  `gorm:"type:varchar(78)" json:"circulating_supply"` // 流通供应量
	MarketCapUSD      float64 `gorm:"default:0" json:"market_cap_usd"`            // 市值（美元）
	Volume24hUSD      float64 `gorm:"default:0" json:"volume_24h_usd"`            // 24小时成交量（美元）

	// === 代币属性 ===
	IsStablecoin bool `gorm:"default:false" json:"is_stablecoin"` // 是否为稳定币
	IsWrapped    bool `gorm:"default:false" json:"is_wrapped"`    // 是否为包装代币（如WETH）

	// === 外部数据源 ID ===
	CoingeckoID     string `gorm:"size:50" json:"coingecko_id"`     // CoinGecko ID（用于获取价格）
	CoinmarketcapID string `gorm:"size:50" json:"coinmarketcap_id"` // CoinMarketCap ID

	// === 状态标识 ===
	IsActive  bool      `gorm:"default:true" json:"is_active"` // 是否启用
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// 关联
	TradingPairs []TradingPair `gorm:"foreignKey:Token0ID;references:ID" json:"-"`
}

// TableName 指定表名
func (Token) TableName() string {
	return "tokens"
}

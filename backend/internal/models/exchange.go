package models

import (
	"time"
)

// Exchange 交易所信息表（统一 DEX 和 CEX）
type Exchange struct {
	ID   uint   `gorm:"primaryKey" json:"id"`
	Name string `gorm:"uniqueIndex;not null;size:50" json:"name"` // 交易所名称

	// === 交易所分类 ===
	ExchangeType string `gorm:"index;size:20;not null;default:'dex'" json:"exchange_type"`
	// 类型：dex（去中心化）, cex（中心化）

	DexType string `gorm:"index;size:20" json:"dex_type"`
	// DEX子类型：amm, aggregator, orderbook, hybrid（仅DEX使用）

	Protocol string `gorm:"index;size:20;default:uniswap_v2" json:"protocol"`
	// 协议类型：uniswap_v2, uniswap_v3, sushiswap, curve, binance_spot, okx_spot 等

	Version string `gorm:"size:20" json:"version"` // 版本，如 v2, v3

	// === DEX 专用字段 ===
	RouterAddress  string `gorm:"size:42" json:"router_address"`  // DEX路由合约地址
	FactoryAddress string `gorm:"size:42" json:"factory_address"` // DEX工厂合约地址
	QuoterAddress  string `gorm:"size:42" json:"quoter_address"`  // V3 Quoter合约地址
	ChainID        int64  `gorm:"index" json:"chain_id"`          // 链ID（仅DEX）

	// === CEX 专用字段 ===
	APIEndpoint  string `gorm:"size:200" json:"api_endpoint"`       // CEX API地址
	WSEndpoint   string `gorm:"size:200" json:"ws_endpoint"`        // WebSocket地址
	APIKey       string `gorm:"size:100" json:"api_key"`            // API Key（加密存储）
	APISecret    string `gorm:"size:100" json:"api_secret"`         // API Secret（加密存储）
	RequiresAuth bool   `gorm:"default:false" json:"requires_auth"` // 是否需要认证（读取公开数据不需要）

	// === 费用配置 ===
	// DEX 使用 Fee (基点), CEX 使用 TakerFee/MakerFee (小数)
	TakerFee   float64 `gorm:"default:0.001" json:"taker_fee"`   // Taker费率（CEX：0.001=0.1%）
	MakerFee   float64 `gorm:"default:0.001" json:"maker_fee"`   // Maker费率（CEX：0.001=0.1%）
	Fee        int     `gorm:"default:30" json:"fee"`            // 统一费率基点（DEX：30=0.3%）
	FeeTier    uint32  `gorm:"default:0" json:"fee_tier"`        // V3费率层级（500, 3000, 10000）
	DynamicFee bool    `gorm:"default:false" json:"dynamic_fee"` // 是否为动态费率

	// === 功能支持 ===
	SupportFlashLoan bool `gorm:"default:false" json:"support_flash_loan"` // 支持闪电贷
	SupportMultiHop  bool `gorm:"default:true" json:"support_multi_hop"`   // 支持多跳路由
	SupportV3Ticks   bool `gorm:"default:false" json:"support_v3_ticks"`   // 支持V3 tick数据
	SupportOrderbook bool `gorm:"default:false" json:"support_orderbook"`  // 支持订单簿（CEX）
	SupportWebSocket bool `gorm:"default:false" json:"support_websocket"`  // 支持WebSocket实时流

	// === 限制配置 ===
	RateLimit      int    `gorm:"default:1200" json:"rate_limit"`           // 每分钟请求限制（CEX）
	MinOrderAmount string `gorm:"type:varchar(78)" json:"min_order_amount"` // 最小下单量

	// === 元数据 ===
	IsActive    bool   `gorm:"default:true" json:"is_active"` // 是否启用
	Priority    int    `gorm:"default:100" json:"priority"`   // 优先级（数值越小越优先）
	Description string `gorm:"type:text" json:"description"`  // 描述
	WebsiteURL  string `gorm:"size:200" json:"website_url"`   // 官网地址

	// === 时间戳 ===
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// 关联
	TradingPairs []TradingPair `gorm:"foreignKey:ExchangeID;references:ID" json:"-"`
}

// IsDEX 判断是否为 DEX
func (e *Exchange) IsDEX() bool {
	return e.ExchangeType == "dex"
}

// IsCEX 判断是否为 CEX
func (e *Exchange) IsCEX() bool {
	return e.ExchangeType == "cex"
}

// IsAMM 判断是否为 AMM 类型（仅DEX）
func (e *Exchange) IsAMM() bool {
	return e.IsDEX() && e.DexType == "amm"
}

// IsAggregator 判断是否为聚合器（仅DEX）
func (e *Exchange) IsAggregator() bool {
	return e.IsDEX() && e.DexType == "aggregator"
}

// SupportsQuoter 判断是否支持 Quoter（V3 和部分聚合器）
func (e *Exchange) SupportsQuoter() bool {
	return e.QuoterAddress != "" && e.QuoterAddress != "0x0000000000000000000000000000000000000000"
}

// GetFeeRate 获取费率（统一接口）
func (e *Exchange) GetFeeRate() float64 {
	if e.IsCEX() {
		return e.TakerFee // CEX 使用 Taker 费率
	}
	// DEX 使用基点转换为小数
	return float64(e.Fee) / 10000.0
}

// TableName 指定表名
func (Exchange) TableName() string {
	return "exchanges"
}

// 向后兼容：保留 Dex 类型别名
type Dex = Exchange

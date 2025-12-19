package dex

import (
	"math/big"
	"time"
)

// Protocol DEX协议接口
// 为不同的DEX协议（V2, V3等）提供统一的抽象
type Protocol interface {
	// GetPairAddress 获取交易对地址
	// V2: factory, token0, token1
	// V3: factory, token0, token1, fee (需要从params中获取)
	GetPairAddress(factory, token0, token1 string, params ...interface{}) (string, error)

	// GetPrice 获取价格信息
	GetPrice(pairAddress string) (*PriceInfo, error)

	// GetLiquidity 获取流动性信息
	GetLiquidity(pairAddress string) (*LiquidityInfo, error)

	// GetProtocolName 获取协议名称
	GetProtocolName() string
}

// PriceInfo 价格信息
type PriceInfo struct {
	Price        *big.Float // token1/token0 的价格
	InversePrice *big.Float // token0/token1 的价格
	Reserve0     *big.Int   // token0 储备量
	Reserve1     *big.Int   // token1 储备量
	Liquidity    *big.Int   // 流动性（V2: sqrt(reserve0*reserve1), V3: 实际流动性）

	// === V3 专用字段 ===
	SqrtPriceX96     *big.Int // V3 的 sqrtPriceX96
	Tick             int32    // V3 的 tick
	FeeGrowthGlobal0 *big.Int // V3 手续费增长0
	FeeGrowthGlobal1 *big.Int // V3 手续费增长1

	Timestamp time.Time // 时间戳
}

// LiquidityInfo 流动性信息
type LiquidityInfo struct {
	Liquidity    *big.Int // 流动性数量
	Reserve0     *big.Int // token0 储备量（V2）
	Reserve1     *big.Int // token1 储备量（V2）
	Tick         int32    // 当前 tick（V3）
	SqrtPriceX96 *big.Int // 当前价格的平方根（V3）
	FeeGrowth0   *big.Int // 手续费增长（V3）
	FeeGrowth1   *big.Int // 手续费增长（V3）
}

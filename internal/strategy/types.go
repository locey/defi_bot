// internal/strategy/types.go
package strategy

import (
    "math/big"
    "time"

    "github.com/ethereum/go-ethereum/common"
)

// ArbitrageOpportunity 套利机会
type ArbitrageOpportunity struct {
    ID              string           `json:"id"`
    SwapPath        []common.Address `json:"swap_path"`        // 代币路径
    Dexes           []common.Address `json:"dexes"`            // DEX路径
    DexNames        []string         `json:"dex_names"`        // DEX名称
    AmountIn        *big.Int         `json:"amount_in"`        // 最优投入金额
    ExpectedOut     *big.Int         `json:"expected_out"`     // 预期输出
    ExpectProfit    *big.Int         `json:"expect_profit"`    // 预期利润
    MinProfit       *big.Int         `json:"min_profit"`       // 最小利润(2*gas)
    ProfitRate      float64          `json:"profit_rate"`      // 利润率
    GasEstimate     uint64           `json:"gas_estimate"`     // Gas估算
    GasPrice        *big.Int         `json:"gas_price"`        // Gas价格
    GasCost         *big.Int         `json:"gas_cost"`         // Gas成本
    Timestamp       time.Time        `json:"timestamp"`        // 发现时间
    ValidUntil      time.Time        `json:"valid_until"`      // 有效期
    Confidence      float64          `json:"confidence"`       // 置信度(0-1)
    PathLength      int              `json:"path_length"`      // 路径长度
}

// PoolInfo 池子信息
type PoolInfo struct {
    Address     common.Address `json:"address"`
    Token0      common.Address `json:"token0"`
    Token1      common.Address `json:"token1"`
    Reserve0    *big.Int       `json:"reserve0"`
    Reserve1    *big.Int       `json:"reserve1"`
    Fee         uint64         `json:"fee"`          // basis points
    DexName     string         `json:"dex_name"`
    DexAddress  common.Address `json:"dex_address"`  // Router地址
    LastUpdate  time.Time      `json:"last_update"`
}

// PathNode 路径节点
type PathNode struct {
    Token    common.Address
    Pool     *PoolInfo
    Dex      common.Address
    DexName  string
}

// SwapStep 交换步骤
type SwapStep struct {
    TokenIn   common.Address
    TokenOut  common.Address
    Pool      *PoolInfo
    Dex       common.Address
    AmountIn  *big.Int
    AmountOut *big.Int
}

// StrategyConfig 策略配置
type StrategyConfig struct {
    MinProfitRate       float64          // 最小利润率 (如0.005 = 0.5%)
    MaxPathLength       int              // 最大路径长度 (3-5)
    MinPathLength       int              // 最小路径长度 (3)
    MaxSlippage         float64          // 最大滑点 (如0.01 = 1%)
    GasMultiplier       float64          // Gas倍数 (minProfit = gas * multiplier)
    ValidityDuration    time.Duration    // 机会有效期
    BaseTokens          []common.Address // 基础代币列表
    SupportedDexes      []DexConfig      // 支持的DEX
    MaxConcurrentPaths  int              // 最大并发路径计算
}

// DexConfig DEX配置
type DexConfig struct {
    Name          string
    RouterAddress common.Address
    FactoryAddress common.Address
    Type          string  // "uniswap_v2", "uniswap_v3", "curve"
    Fee           uint64  // 默认费率
}

// PriceData 价格数据
type PriceData struct {
    Token0       common.Address
    Token1       common.Address
    Price        *big.Float     // Token0/Token1 价格
    SqrtPriceX96 *big.Int       // V3价格格式
    Liquidity    *big.Int
    Timestamp    time.Time
}
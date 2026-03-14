// internal/liquidation/types.go
// Aave V3 清算机器人 — 类型定义
package liquidation

import (
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// UserPosition Aave V3 用户仓位
type UserPosition struct {
	User                        common.Address `json:"user"`
	TotalCollateralBase         *big.Int       `json:"total_collateral_base"`          // 总抵押品（以 base currency 计，8位精度 USD）
	TotalDebtBase               *big.Int       `json:"total_debt_base"`                // 总债务
	AvailableBorrowsBase        *big.Int       `json:"available_borrows_base"`
	CurrentLiquidationThreshold *big.Int       `json:"current_liquidation_threshold"`  // 清算阈值（bps）
	Ltv                         *big.Int       `json:"ltv"`
	HealthFactor                *big.Int       `json:"health_factor"`                  // 健康因子（1e18 精度）
	LastChecked                 time.Time      `json:"last_checked"`
}

// IsLiquidatable 是否可被清算 (healthFactor < 1e18)
func (p *UserPosition) IsLiquidatable() bool {
	if p.HealthFactor == nil || p.TotalDebtBase == nil {
		return false
	}
	oneE18 := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	return p.HealthFactor.Cmp(oneE18) < 0 && p.TotalDebtBase.Sign() > 0
}

// HealthFactorFloat 健康因子浮点值
func (p *UserPosition) HealthFactorFloat() float64 {
	if p.HealthFactor == nil {
		return 0
	}
	hf := new(big.Float).SetInt(p.HealthFactor)
	oneE18 := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
	result, _ := new(big.Float).Quo(hf, oneE18).Float64()
	return result
}

// ReserveInfo Aave V3 储备资产信息
type ReserveInfo struct {
	Asset                  common.Address `json:"asset"`
	ATokenAddress          common.Address `json:"a_token_address"`
	VariableDebtToken      common.Address `json:"variable_debt_token"`
	LiquidationBonus       uint16         `json:"liquidation_bonus"`       // 清算奖励（bps，如 10500 = 5%）
	LiquidationThreshold   uint16         `json:"liquidation_threshold"`   // 清算阈值（bps）
	Decimals               uint8          `json:"decimals"`
	Symbol                 string         `json:"symbol"`
	IsActive               bool           `json:"is_active"`
}

// UserReserveData 用户在某资产上的具体头寸
type UserReserveData struct {
	User                 common.Address `json:"user"`
	Asset                common.Address `json:"asset"`
	ATokenBalance        *big.Int       `json:"a_token_balance"`         // 抵押余额
	VariableDebtBalance  *big.Int       `json:"variable_debt_balance"`   // 可变利率债务
	StableDebtBalance    *big.Int       `json:"stable_debt_balance"`     // 稳定利率债务
}

// LiquidationOpportunity 清算机会
type LiquidationOpportunity struct {
	ID               string         `json:"id"`
	User             common.Address `json:"user"`              // 被清算用户
	CollateralAsset  common.Address `json:"collateral_asset"`  // 抵押品资产
	DebtAsset        common.Address `json:"debt_asset"`        // 债务资产
	DebtToCover      *big.Int       `json:"debt_to_cover"`     // 可覆盖的债务数量
	CollateralSymbol string         `json:"collateral_symbol"`
	DebtSymbol       string         `json:"debt_symbol"`

	// 清算奖励
	LiquidationBonus uint16   `json:"liquidation_bonus"`  // bps，如 10500 = 5% bonus
	ExpectedProfit   *big.Int `json:"expected_profit"`    // 预期利润（以 debtAsset 计）

	// DEX swap 参数（collateral → debt）
	SwapRouter  common.Address `json:"swap_router"`
	SwapFeeTier uint32         `json:"swap_fee_tier"` // 0=V2, 500/3000/10000=V3

	// 元数据
	HealthFactor float64   `json:"health_factor"`
	Timestamp    time.Time `json:"timestamp"`
	ValidUntil   time.Time `json:"valid_until"`
	GasEstimate  uint64    `json:"gas_estimate"`
	GasCost      *big.Int  `json:"gas_cost"`
}

// LiquidationResult 清算执行结果
type LiquidationResult struct {
	OpportunityID      string         `json:"opportunity_id"`
	TxHash             common.Hash    `json:"tx_hash"`
	Success            bool           `json:"success"`
	CollateralReceived *big.Int       `json:"collateral_received"`
	DebtCovered        *big.Int       `json:"debt_covered"`
	Profit             *big.Int       `json:"profit"`
	GasUsed            uint64         `json:"gas_used"`
	GasCost            *big.Int       `json:"gas_cost"`
	Error              string         `json:"error,omitempty"`
	Timestamp          time.Time      `json:"timestamp"`
}

// AaveV3Addresses Aave V3 合约地址集合
type AaveV3Addresses struct {
	Pool                common.Address // Aave V3 Pool (= LendingPool)
	PoolDataProvider    common.Address // Aave V3 PoolDataProvider
	Oracle              common.Address // Aave V3 Oracle
}

// FlashLoanLiquidatorABI 清算合约 ABI（最小化）
// 支持两个入口：executeLiquidation（向后兼容）和 executeLiquidationWithMinProfit（MEV 保护）
const FlashLoanLiquidatorABI = `[
    {
        "inputs": [
            {"internalType":"address","name":"collateralAsset","type":"address"},
            {"internalType":"address","name":"debtAsset","type":"address"},
            {"internalType":"address","name":"user","type":"address"},
            {"internalType":"uint256","name":"debtToCover","type":"uint256"},
            {"internalType":"address","name":"swapRouter","type":"address"},
            {"internalType":"uint24","name":"swapFeeTier","type":"uint24"}
        ],
        "name": "executeLiquidation",
        "outputs": [],
        "stateMutability": "nonpayable",
        "type": "function"
    },
    {
        "inputs": [
            {"internalType":"address","name":"collateralAsset","type":"address"},
            {"internalType":"address","name":"debtAsset","type":"address"},
            {"internalType":"address","name":"user","type":"address"},
            {"internalType":"uint256","name":"debtToCover","type":"uint256"},
            {"internalType":"address","name":"swapRouter","type":"address"},
            {"internalType":"uint24","name":"swapFeeTier","type":"uint24"},
            {"internalType":"uint256","name":"minProfitAmount","type":"uint256"}
        ],
        "name": "executeLiquidationWithMinProfit",
        "outputs": [],
        "stateMutability": "nonpayable",
        "type": "function"
    }
]`

// Aave V3 Pool ABI（getUserAccountData）
const AaveV3PoolABI = `[
    {
        "inputs": [{"internalType":"address","name":"user","type":"address"}],
        "name": "getUserAccountData",
        "outputs": [
            {"internalType":"uint256","name":"totalCollateralBase","type":"uint256"},
            {"internalType":"uint256","name":"totalDebtBase","type":"uint256"},
            {"internalType":"uint256","name":"availableBorrowsBase","type":"uint256"},
            {"internalType":"uint256","name":"currentLiquidationThreshold","type":"uint256"},
            {"internalType":"uint256","name":"ltv","type":"uint256"},
            {"internalType":"uint256","name":"healthFactor","type":"uint256"}
        ],
        "stateMutability": "view",
        "type": "function"
    }
]`

// Aave V3 PoolDataProvider ABI（getUserReserveData + getReserveConfigurationData）
const AaveV3DataProviderABI = `[
    {
        "inputs": [
            {"internalType":"address","name":"asset","type":"address"},
            {"internalType":"address","name":"user","type":"address"}
        ],
        "name": "getUserReserveData",
        "outputs": [
            {"internalType":"uint256","name":"currentATokenBalance","type":"uint256"},
            {"internalType":"uint256","name":"currentStableDebt","type":"uint256"},
            {"internalType":"uint256","name":"currentVariableDebt","type":"uint256"},
            {"internalType":"uint256","name":"principalStableDebt","type":"uint256"},
            {"internalType":"uint256","name":"scaledVariableDebt","type":"uint256"},
            {"internalType":"uint256","name":"stableBorrowRate","type":"uint256"},
            {"internalType":"uint256","name":"liquidityRate","type":"uint256"},
            {"internalType":"uint40","name":"stableRateLastUpdated","type":"uint40"},
            {"internalType":"bool","name":"usageAsCollateralEnabled","type":"bool"}
        ],
        "stateMutability": "view",
        "type": "function"
    },
    {
        "inputs": [{"internalType":"address","name":"asset","type":"address"}],
        "name": "getReserveConfigurationData",
        "outputs": [
            {"internalType":"uint256","name":"decimals","type":"uint256"},
            {"internalType":"uint256","name":"ltv","type":"uint256"},
            {"internalType":"uint256","name":"liquidationThreshold","type":"uint256"},
            {"internalType":"uint256","name":"liquidationBonus","type":"uint256"},
            {"internalType":"uint256","name":"reserveFactor","type":"uint256"},
            {"internalType":"bool","name":"usageAsCollateralEnabled","type":"bool"},
            {"internalType":"bool","name":"borrowingEnabled","type":"bool"},
            {"internalType":"bool","name":"stableBorrowRateEnabled","type":"bool"},
            {"internalType":"bool","name":"isActive","type":"bool"},
            {"internalType":"bool","name":"isFrozen","type":"bool"}
        ],
        "stateMutability": "view",
        "type": "function"
    },
    {
        "inputs": [],
        "name": "getAllReservesTokens",
        "outputs": [
            {
                "components": [
                    {"internalType":"string","name":"symbol","type":"string"},
                    {"internalType":"address","name":"tokenAddress","type":"address"}
                ],
                "internalType":"struct IPoolDataProvider.TokenData[]",
                "name":"",
                "type":"tuple[]"
            }
        ],
        "stateMutability": "view",
        "type": "function"
    },
    {
        "inputs": [{"internalType":"address","name":"asset","type":"address"}],
        "name": "getReserveTokensAddresses",
        "outputs": [
            {"internalType":"address","name":"aTokenAddress","type":"address"},
            {"internalType":"address","name":"stableDebtTokenAddress","type":"address"},
            {"internalType":"address","name":"variableDebtTokenAddress","type":"address"}
        ],
        "stateMutability": "view",
        "type": "function"
    }
]`

// BalancerLiquidatorABI Balancer 免费闪电贷清算合约 ABI
// 函数签名与 FlashLoanLiquidator 的 executeLiquidationWithMinProfit 相同，
// 但合约内部使用 Balancer Vault 闪电贷（0% 费率）而非 Aave（0.05%）
const BalancerLiquidatorABI = `[
    {
        "inputs": [
            {"internalType":"address","name":"collateralAsset","type":"address"},
            {"internalType":"address","name":"debtAsset","type":"address"},
            {"internalType":"address","name":"user","type":"address"},
            {"internalType":"uint256","name":"debtToCover","type":"uint256"},
            {"internalType":"address","name":"swapRouter","type":"address"},
            {"internalType":"uint24","name":"swapFeeTier","type":"uint24"},
            {"internalType":"uint256","name":"minProfitAmount","type":"uint256"}
        ],
        "name": "executeLiquidation",
        "outputs": [],
        "stateMutability": "nonpayable",
        "type": "function"
    }
]`

// ERC20 balanceOf ABI
const ERC20BalanceOfABI = `[
    {
        "inputs": [{"internalType":"address","name":"account","type":"address"}],
        "name": "balanceOf",
        "outputs": [{"internalType":"uint256","name":"","type":"uint256"}],
        "stateMutability": "view",
        "type": "function"
    }
]`

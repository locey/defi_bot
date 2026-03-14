// internal/liquidation/detector.go
// 清算机会检测器 — 发现可清算仓位并计算利润
package liquidation

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/defi-bot/backend/pkg/log"
	"github.com/ethereum/go-ethereum/common"
)

// DetectorConfig 检测器配置
type DetectorConfig struct {
	// 健康因子低于此值的仓位进入监控列表（如 1.05）
	WatchThreshold float64

	// 最小债务金额（USD，低于此值不值得清算）
	MinDebtUSD float64

	// 默认 swap router（collateral → debt）
	DefaultSwapRouter common.Address
	DefaultSwapFee    uint32 // 0=V2, 500/3000/10000=V3

	// Gas 相关
	EstimatedGasUsed uint64   // 预估 Gas 用量
	MaxGasPrice      *big.Int // 最大 Gas 价格（wei）
}

// DefaultDetectorConfig 默认配置
func DefaultDetectorConfig() *DetectorConfig {
	return &DetectorConfig{
		WatchThreshold:   1.05,
		MinDebtUSD:       50,      // 最小 $50 债务
		EstimatedGasUsed: 500_000, // 清算 + swap 约 500K gas
	}
}

// LiquidationDetector 清算机会检测器
type LiquidationDetector struct {
	collector *AaveCollector
	config    *DetectorConfig
}

// NewLiquidationDetector 创建检测器
func NewLiquidationDetector(collector *AaveCollector, config *DetectorConfig) *LiquidationDetector {
	if config == nil {
		config = DefaultDetectorConfig()
	}
	return &LiquidationDetector{
		collector: collector,
		config:    config,
	}
}

// Detect 检测清算机会
// 流程：1. 获取可清算仓位 → 2. 获取用户各资产头寸 → 3. 计算最佳清算参数 → 4. 估算利润
func (d *LiquidationDetector) Detect(ctx context.Context) ([]*LiquidationOpportunity, error) {
	// Step 1: 获取 healthFactor < 1 的仓位
	positions := d.collector.GetLiquidatablePositions()
	if len(positions) == 0 {
		return nil, nil
	}

	log.Strategy().Info().Int("liquidatable", len(positions)).Msg("清算：发现可清算仓位")

	reserves := d.collector.GetReserves()
	if len(reserves) == 0 {
		return nil, fmt.Errorf("no reserves loaded")
	}

	// 构建 asset → reserve 映射
	reserveMap := make(map[common.Address]*ReserveInfo)
	var assetAddrs []common.Address
	for i := range reserves {
		reserveMap[reserves[i].Asset] = &reserves[i]
		assetAddrs = append(assetAddrs, reserves[i].Asset)
	}

	var opportunities []*LiquidationOpportunity

	for _, pos := range positions {
		// 过滤小额债务
		debtUSD := baseToUSD(pos.TotalDebtBase)
		if debtUSD < d.config.MinDebtUSD {
			continue
		}

		// Step 2: 获取用户在各资产上的具体头寸
		userReserves, err := d.collector.FetchUserReserveData(ctx, pos.User, assetAddrs)
		if err != nil {
			log.Strategy().Warn().Err(err).
				Str("user", pos.User.Hex()).
				Msg("清算：获取用户储备数据失败")
			continue
		}

		// Step 3: 找到最佳清算对（最大抵押品 + 最大债务）
		opp := d.findBestLiquidationPair(pos, userReserves, reserveMap)
		if opp != nil {
			opportunities = append(opportunities, opp)
		}
	}

	log.Strategy().Info().
		Int("opportunities", len(opportunities)).
		Int("positions_checked", len(positions)).
		Msg("清算：机会检测完成")

	return opportunities, nil
}

// findBestLiquidationPair 找到最优的清算对
func (d *LiquidationDetector) findBestLiquidationPair(
	pos *UserPosition,
	userReserves []UserReserveData,
	reserveMap map[common.Address]*ReserveInfo,
) *LiquidationOpportunity {
	// 分类：抵押品 vs 债务
	var collaterals []UserReserveData
	var debts []UserReserveData

	for _, ur := range userReserves {
		if ur.ATokenBalance.Sign() > 0 {
			collaterals = append(collaterals, ur)
		}
		totalDebt := new(big.Int).Add(ur.VariableDebtBalance, ur.StableDebtBalance)
		if totalDebt.Sign() > 0 {
			debts = append(debts, ur)
		}
	}

	if len(collaterals) == 0 || len(debts) == 0 {
		return nil
	}

	// 选择最大的债务资产
	var bestDebt *UserReserveData
	var bestDebtAmount *big.Int
	for i := range debts {
		totalDebt := new(big.Int).Add(debts[i].VariableDebtBalance, debts[i].StableDebtBalance)
		if bestDebtAmount == nil || totalDebt.Cmp(bestDebtAmount) > 0 {
			bestDebt = &debts[i]
			bestDebtAmount = totalDebt
		}
	}

	// 选择清算奖励最高的抵押品
	var bestCollateral *UserReserveData
	var bestBonus uint16
	for i := range collaterals {
		ri := reserveMap[collaterals[i].Asset]
		if ri == nil {
			continue
		}
		if ri.LiquidationBonus > bestBonus {
			bestCollateral = &collaterals[i]
			bestBonus = ri.LiquidationBonus
		}
	}

	if bestDebt == nil || bestCollateral == nil {
		return nil
	}

	debtReserve := reserveMap[bestDebt.Asset]
	collateralReserve := reserveMap[bestCollateral.Asset]
	if debtReserve == nil || collateralReserve == nil {
		return nil
	}

	// Aave V3 清算规则：最多清算 50% 的债务（close factor）
	// 当 healthFactor < 0.95 时可以清算 100%
	closeFactor := big.NewInt(5000) // 50% (bps)
	oneE18 := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	hf095 := new(big.Int).Mul(big.NewInt(95), new(big.Int).Div(oneE18, big.NewInt(100)))
	if pos.HealthFactor.Cmp(hf095) < 0 {
		closeFactor = big.NewInt(10000) // 100%
	}

	// debtToCover = totalDebt * closeFactor / 10000
	debtToCover := new(big.Int).Mul(bestDebtAmount, closeFactor)
	debtToCover.Div(debtToCover, big.NewInt(10000))

	// 估算利润：清算奖励 - flash loan fee - swap slippage - gas
	// bonus = collateral * (liquidationBonus - 10000) / 10000
	bonusBps := int64(collateralReserve.LiquidationBonus) - 10000 // e.g., 10500 - 10000 = 500 (5%)
	if bonusBps <= 0 {
		bonusBps = 500 // 默认 5%
	}
	grossProfit := new(big.Int).Mul(debtToCover, big.NewInt(bonusBps))
	grossProfit.Div(grossProfit, big.NewInt(10000))

	// 减去 flash loan fee (0.05% = 5 bps)
	flashFee := new(big.Int).Mul(debtToCover, big.NewInt(5))
	flashFee.Div(flashFee, big.NewInt(10000))

	// 减去 swap slippage 估算 (0.5% = 50 bps for collateral→debt swap)
	// 仅在 collateral != debt 时扣除（同资产无需 swap）
	swapSlippage := big.NewInt(0)
	if collateralReserve.Asset != debtReserve.Asset {
		swapSlippage = new(big.Int).Mul(debtToCover, big.NewInt(50))
		swapSlippage.Div(swapSlippage, big.NewInt(10000))
	}

	// 减去 gas 成本估算: 500K gas * 0.1 Gwei = 0.00005 ETH ≈ $0.10
	// Aave base currency 是 8 位精度 USD，$0.10 = 10_000_000 (1e7)
	gasCostUSD := big.NewInt(10_000_000) // $0.10 in 8-decimal base

	estimatedProfit := new(big.Int).Sub(grossProfit, flashFee)
	estimatedProfit.Sub(estimatedProfit, swapSlippage)
	estimatedProfit.Sub(estimatedProfit, gasCostUSD)

	if estimatedProfit.Sign() <= 0 {
		log.Strategy().Debug().
			Str("user", pos.User.Hex()).
			Str("debt", debtReserve.Symbol).
			Str("collateral", collateralReserve.Symbol).
			Int64("bonus_bps", bonusBps).
			Str("gross_profit", grossProfit.String()).
			Str("flash_fee", flashFee.String()).
			Str("swap_slippage", swapSlippage.String()).
			Msg("清算：利润不足，跳过")
		return nil
	}

	now := time.Now()
	oppID := fmt.Sprintf("liq-%s-%s-%d", pos.User.Hex()[:10], bestDebt.Asset.Hex()[:10], now.UnixMilli())

	return &LiquidationOpportunity{
		ID:               oppID,
		User:             pos.User,
		CollateralAsset:  bestCollateral.Asset,
		DebtAsset:        bestDebt.Asset,
		DebtToCover:      debtToCover,
		CollateralSymbol: collateralReserve.Symbol,
		DebtSymbol:       debtReserve.Symbol,
		LiquidationBonus: collateralReserve.LiquidationBonus,
		ExpectedProfit:   estimatedProfit,
		SwapRouter:       d.config.DefaultSwapRouter,
		SwapFeeTier:      d.config.DefaultSwapFee,
		HealthFactor:     pos.HealthFactorFloat(),
		Timestamp:        now,
		ValidUntil:       now.Add(30 * time.Second), // 清算机会有效期短
		GasEstimate:      d.config.EstimatedGasUsed,
	}
}

// baseToUSD 将 Aave base currency (8位精度) 转换为 USD
func baseToUSD(base *big.Int) float64 {
	if base == nil {
		return 0
	}
	f := new(big.Float).SetInt(base)
	divisor := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(8), nil))
	result, _ := new(big.Float).Quo(f, divisor).Float64()
	return result
}

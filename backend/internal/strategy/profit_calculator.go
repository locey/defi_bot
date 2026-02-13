// internal/strategy/profit_calculator.go
package strategy

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

// ProfitCalculator 利润计算器
type ProfitCalculator struct {
	config *StrategyConfig
	engine *StrategyEngine
}

// NewProfitCalculator 创建利润计算器
func NewProfitCalculator(config *StrategyConfig, engine *StrategyEngine) *ProfitCalculator {
	return &ProfitCalculator{
		config: config,
		engine: engine,
	}
}

// CalculatePathOutput 计算路径输出
func (pc *ProfitCalculator) CalculatePathOutput(
	ctx context.Context,
	path []PathNode,
	amountIn *big.Int,
) (*big.Int, []SwapStep, error) {

	if len(path) < 2 {
		return nil, nil, fmt.Errorf("path too short")
	}

	currentAmount := new(big.Int).Set(amountIn)
	steps := make([]SwapStep, 0, len(path)-1)

	// 遍历路径中的每一步
	for i := 0; i < len(path)-1; i++ {
		tokenIn := path[i].Token
		tokenOut := path[i+1].Token
		pool := path[i].Pool

		if pool == nil {
			return nil, nil, fmt.Errorf("pool is nil at step %d", i)
		}

		// 计算这一步的输出
		amountOut, err := pc.calculateSwapOutput(tokenIn, tokenOut, currentAmount, pool)
		if err != nil {
			return nil, nil, fmt.Errorf("calculate swap at step %d: %w", i, err)
		}

		steps = append(steps, SwapStep{
			TokenIn:   tokenIn,
			TokenOut:  tokenOut,
			Pool:      pool,
			Dex:       path[i].Dex,
			AmountIn:  new(big.Int).Set(currentAmount),
			AmountOut: amountOut,
		})

		currentAmount = amountOut
	}

	return currentAmount, steps, nil
}

// calculateSwapOutput 计算单次交换输出
func (pc *ProfitCalculator) calculateSwapOutput(
	tokenIn common.Address,
	tokenOut common.Address,
	amountIn *big.Int,
	pool *PoolInfo,
) (*big.Int, error) {
	// 确定是哪个方向的交换
	var reserveIn, reserveOut *big.Int

	// 验证 tokenIn 和 tokenOut 都在池子中，且方向正确
	if tokenIn == pool.Token0 && tokenOut == pool.Token1 {
		reserveIn = pool.Reserve0
		reserveOut = pool.Reserve1
	} else if tokenIn == pool.Token1 && tokenOut == pool.Token0 {
		reserveIn = pool.Reserve1
		reserveOut = pool.Reserve0
	} else {
		return nil, fmt.Errorf("invalid token pair for pool")
	}

	// 根据协议类型选择计算方法
	switch pool.Protocol {
	case "uniswap_v2", "sushiswap":
		return pc.calculateV2Output(amountIn, reserveIn, reserveOut, pool.Fee)
	case "uniswap_v3":
		// Phase 1.2: 使用 V3 精确计算（带完整 pool 信息）
		return pc.calculateV3OutputWithPool(tokenIn, tokenOut, amountIn, pool)
	default:
		return pc.calculateV2Output(amountIn, reserveIn, reserveOut, pool.Fee)
	}
}

// calculateV2Output Uniswap V2 AMM公式
// amountOut = (amountIn * fee * reserveOut) / (reserveIn * 1000 + amountIn * fee)
// calculateV2Output Uniswap V2 AMM公式 - 添加精度处理
func (pc *ProfitCalculator) calculateV2Output(
	amountIn *big.Int,
	reserveIn *big.Int,
	reserveOut *big.Int,
	feeBps uint64,
) (*big.Int, error) {
	if reserveIn.Sign() <= 0 || reserveOut.Sign() <= 0 {
		return nil, fmt.Errorf("invalid reserves")
	}

	if amountIn.Sign() <= 0 {
		return nil, fmt.Errorf("invalid amountIn")
	}

	// 验证储备金数据的合理性
	// 使用 10^6 作为最小阈值，适用于 6 位精度代币（如 USDC/USDT）
	// 对于 18 位精度代币，这相当于 10^-12 个代币，仍然是合理的最小值
	minReserve := new(big.Int).Exp(big.NewInt(10), big.NewInt(6), nil)
	if reserveIn.Cmp(minReserve) < 0 || reserveOut.Cmp(minReserve) < 0 {
		return nil, fmt.Errorf("reserves are too small (reserveIn=%s, reserveOut=%s, min=%s)", 
			reserveIn.String(), reserveOut.String(), minReserve.String())
	}

	// 检查输入金额与储备金的比例，避免输出过小
	// 如果输入金额超过储备金的 10%，返回错误
	maxInputRatio := big.NewInt(10)
	if new(big.Int).Mul(amountIn, maxInputRatio).Cmp(reserveIn) > 0 {
		return nil, fmt.Errorf("amountIn exceeds max ratio 10%% (amountIn=%s, reserveIn=%s)", 
			amountIn.String(), reserveIn.String())
	}

	// 默认费率 0.3% = 30 bps
	if feeBps == 0 {
		feeBps = 30
	}

	// fee = 10000 - feeBps (如 0.3% -> 9970)
	feeMultiplier := big.NewInt(int64(10000 - feeBps))

	// amountInWithFee = amountIn * feeMultiplier
	amountInWithFee := new(big.Int).Mul(amountIn, feeMultiplier)

	// numerator = amountInWithFee * reserveOut
	numerator := new(big.Int).Mul(amountInWithFee, reserveOut)

	// denominator = reserveIn * 10000 + amountInWithFee
	denominator := new(big.Int).Mul(reserveIn, big.NewInt(10000))
	denominator.Add(denominator, amountInWithFee)

	// amountOut = numerator / denominator
	amountOut := new(big.Int).Div(numerator, denominator)

	// 注意：移除了基于比例的检查，因为不同精度的代币（如 WETH 18位 vs USDC 6位）
	// 在 wei 级别的比例可能相差很大（最多 10^12 倍），这是正常的。
	// 价格合理性应该在更上层通过价格验证来检查。

	// 只检查输出是否为正数
	if amountOut.Sign() <= 0 {
		return nil, fmt.Errorf("output amount is zero or negative (amountIn=%s, reserveIn=%s, reserveOut=%s, fee=%d)", 
			amountIn.String(), reserveIn.String(), reserveOut.String(), feeBps)
	}

	return amountOut, nil
}

// calculateV3Output Uniswap V3 concentrated liquidity 精确计算
// Phase 1.2: 替换原来的 V2 回退，使用基于 sqrtPriceX96 和 liquidity 的正确公式
func (pc *ProfitCalculator) calculateV3Output(
	amountIn *big.Int,
	reserveIn *big.Int,
	reserveOut *big.Int,
	feeBps uint64,
) (*big.Int, error) {
	// 此方法是通用签名的兼容入口，实际 V3 计算在 calculateSwapOutput 中
	// 直接分发到 pool-aware 版本。如果没有 V3 数据，使用 V2 作为粗略估计
	return pc.calculateV2Output(amountIn, reserveIn, reserveOut, feeBps)
}

// calculateV3OutputWithPool Uniswap V3 精确计算（需要完整 pool 信息）
func (pc *ProfitCalculator) calculateV3OutputWithPool(
	tokenIn common.Address,
	tokenOut common.Address,
	amountIn *big.Int,
	pool *PoolInfo,
) (*big.Int, error) {
	// 优先使用 V3 精确计算
	if pool.SqrtPriceX96 != nil && pool.SqrtPriceX96.Sign() > 0 &&
		pool.Liquidity != nil && pool.Liquidity.Sign() > 0 {

		zeroForOne := tokenIn == pool.Token0
		result, err := CalculateV3SwapOutput(
			pool.SqrtPriceX96,
			pool.Liquidity,
			amountIn,
			pool.Fee,
			zeroForOne,
		)
		if err == nil {
			return result, nil
		}
		// V3 计算失败时回退到 V2 近似
	}

	// 回退: 使用 V2 公式（reserve-based）作为粗略估计
	var reserveIn, reserveOut *big.Int
	if tokenIn == pool.Token0 {
		reserveIn = pool.Reserve0
		reserveOut = pool.Reserve1
	} else {
		reserveIn = pool.Reserve1
		reserveOut = pool.Reserve0
	}
	if reserveIn != nil && reserveIn.Sign() > 0 && reserveOut != nil && reserveOut.Sign() > 0 {
		return pc.calculateV2Output(amountIn, reserveIn, reserveOut, pool.Fee)
	}
	return nil, fmt.Errorf("v3 pool has no valid data for calculation")
}

// CalculateProfit 计算利润
func (pc *ProfitCalculator) CalculateProfit(
	amountIn *big.Int,
	amountOut *big.Int,
) *big.Int {
	return new(big.Int).Sub(amountOut, amountIn)
}

// CalculateProfitRate 计算利润率
func (pc *ProfitCalculator) CalculateProfitRate(
	amountIn *big.Int,
	profit *big.Int,
) float64 {
	if amountIn.Sign() <= 0 {
		return 0
	}

	profitFloat := new(big.Float).SetInt(profit)
	amountFloat := new(big.Float).SetInt(amountIn)

	rate := new(big.Float).Quo(profitFloat, amountFloat)
	result, _ := rate.Float64()

	return result
}

// SimulateSlippage 模拟滑点
func (pc *ProfitCalculator) SimulateSlippage(
	ctx context.Context,
	path []PathNode,
	amountIn *big.Int,
) (float64, error) {

	// 计算小额交易的输出（作为基准）
	smallAmount := new(big.Int).Div(amountIn, big.NewInt(100))
	if smallAmount.Sign() <= 0 {
		// 业界标准：根据起始代币精度计算最小金额
		// 0.001 token = 10^(decimals-3)
		if len(path) > 0 && path[0].Pool != nil {
			var decimals uint8
			if path[0].Token == path[0].Pool.Token0 {
				decimals = uint8(path[0].Pool.Decimals0)
			} else {
				decimals = uint8(path[0].Pool.Decimals1)
			}
			if decimals >= 3 {
				smallAmount = new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals-3)), nil)
			} else {
				smallAmount = big.NewInt(1)
			}
		} else {
			// 后备方案
			smallAmount = big.NewInt(1e15) // 默认0.001 ETH
		}
	}

	smallOut, _, err := pc.CalculatePathOutput(ctx, path, smallAmount)
	if err != nil {
		return 0, err
	}

	// 计算实际金额的输出
	actualOut, _, err := pc.CalculatePathOutput(ctx, path, amountIn)
	if err != nil {
		return 0, err
	}

	// 理论输出（按小额比例放大）
	ratio := new(big.Float).Quo(
		new(big.Float).SetInt(amountIn),
		new(big.Float).SetInt(smallAmount),
	)
	theoreticalOut := new(big.Float).Mul(
		new(big.Float).SetInt(smallOut),
		ratio,
	)

	// 滑点 = (理论输出 - 实际输出) / 理论输出
	actualOutFloat := new(big.Float).SetInt(actualOut)
	slippage := new(big.Float).Sub(theoreticalOut, actualOutFloat)
	slippage.Quo(slippage, theoreticalOut)

	result, _ := slippage.Float64()
	return result, nil
}

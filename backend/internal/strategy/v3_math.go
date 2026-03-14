// internal/strategy/v3_math.go
// Phase 1.2: Uniswap V3 concentrated liquidity 精确计算
// 基于 Uniswap V3 白皮书和 SqrtPriceMath 库的 Go 实现
// 所有计算使用 big.Int 整数运算，避免浮点精度丢失
package strategy

import (
	"fmt"
	"math/big"
)

var (
	// Q96 = 2^96，用于 sqrtPriceX96 的定点数运算
	Q96  = new(big.Int).Lsh(big.NewInt(1), 96)
	Q192 = new(big.Int).Mul(Q96, Q96)

	// MIN_SQRT_RATIO 和 MAX_SQRT_RATIO（与 Uniswap V3 合约一致）
	MinSqrtRatio = big.NewInt(4295128739)
	MaxSqrtRatio, _ = new(big.Int).SetString("1461446703485210103287273052203988822378723970342", 10)

	// MIN_TICK 和 MAX_TICK
	MinTick int32 = -887272
	MaxTick int32 = 887272
)

// V3SwapResult V3 swap 计算结果
type V3SwapResult struct {
	AmountOut       *big.Int // 输出金额
	SqrtPriceAfter  *big.Int // swap 后的 sqrtPriceX96
	TickAfter       int32    // swap 后的 tick
	LiquidityAfter  *big.Int // swap 后的流动性（单 tick 范围内不变）
}

// CalculateV3SwapOutput 计算 Uniswap V3 单池 swap 输出
// 在当前 tick 范围内进行精确计算
// 参数:
//   - sqrtPriceX96: 当前池子的 sqrtPriceX96
//   - liquidity: 当前 tick 范围的活跃流动性
//   - amountIn: 输入金额
//   - feeBps: 手续费（basis points，如 3000 = 0.3%）
//   - zeroForOne: 是否从 token0 换到 token1
func CalculateV3SwapOutput(
	sqrtPriceX96 *big.Int,
	liquidity *big.Int,
	amountIn *big.Int,
	feeBps uint64,
	zeroForOne bool,
) (*big.Int, error) {
	if sqrtPriceX96 == nil || sqrtPriceX96.Sign() <= 0 {
		return nil, fmt.Errorf("v3_math: invalid sqrtPriceX96")
	}
	if liquidity == nil || liquidity.Sign() <= 0 {
		return nil, fmt.Errorf("v3_math: invalid liquidity")
	}
	if amountIn == nil || amountIn.Sign() <= 0 {
		return nil, fmt.Errorf("v3_math: invalid amountIn")
	}

	// 1. 扣除手续费
	// feeBps 是基点（basis points）：30 = 0.3%，5 = 0.05%，100 = 1%
	// 注意：这里用的是 bps（来自 exchange.fee 列），不是 V3 合约的 fee tier（ppm）
	// feeAmount = amountIn * feeBps / 10000
	feeAmount := new(big.Int).Mul(amountIn, big.NewInt(int64(feeBps)))
	feeAmount.Div(feeAmount, big.NewInt(10000))
	amountInAfterFee := new(big.Int).Sub(amountIn, feeAmount)

	if amountInAfterFee.Sign() <= 0 {
		return nil, fmt.Errorf("v3_math: amountIn too small after fee")
	}

	// 2. 计算 swap 输出（当前 tick 范围内）
	var amountOut *big.Int
	var err error

	if zeroForOne {
		// token0 -> token1：价格下降
		amountOut, err = getAmount1ForAmount0(sqrtPriceX96, liquidity, amountInAfterFee)
	} else {
		// token1 -> token0：价格上升
		amountOut, err = getAmount0ForAmount1(sqrtPriceX96, liquidity, amountInAfterFee)
	}

	if err != nil {
		return nil, fmt.Errorf("v3_math: calculate output: %w", err)
	}

	if amountOut.Sign() <= 0 {
		return nil, fmt.Errorf("v3_math: output is zero or negative")
	}

	// Conservative dampening: single-tick model overestimates by 2-100x for
	// swaps that cross tick boundaries. Apply 50% haircut — QuoterV2 on-chain
	// validation provides the ground truth for paths that survive this filter.
	amountOut.Mul(amountOut, big.NewInt(50))
	amountOut.Div(amountOut, big.NewInt(100))

	return amountOut, nil
}

// getAmount1ForAmount0 计算给定 amount0（token0 输入）能换多少 amount1（token1 输出）
// 使用 Uniswap V3 的 SqrtPriceMath:
// Δy = L * (√P_before - √P_after)
// 其中 √P_after = (L * √P_before) / (L + Δx * √P_before)
func getAmount1ForAmount0(
	sqrtPriceX96 *big.Int,
	liquidity *big.Int,
	amount0 *big.Int,
) (*big.Int, error) {
	// sqrtPriceAfter = (L * sqrtPrice) / (L + amount0 * sqrtPrice / Q96)
	// 为避免溢出，先算 amount0 * sqrtPrice / Q96
	numerator := new(big.Int).Mul(amount0, sqrtPriceX96)
	numerator.Div(numerator, Q96)

	denominator := new(big.Int).Add(liquidity, numerator)
	if denominator.Sign() <= 0 {
		return nil, fmt.Errorf("denominator is zero or negative")
	}

	// sqrtPriceAfterX96 = L * sqrtPriceX96 / denominator
	sqrtPriceAfterX96 := new(big.Int).Mul(liquidity, sqrtPriceX96)
	sqrtPriceAfterX96.Div(sqrtPriceAfterX96, denominator)

	// 价格变化验证（zeroForOne 时价格应下降）
	if sqrtPriceAfterX96.Cmp(sqrtPriceX96) > 0 {
		return nil, fmt.Errorf("price increased in zeroForOne swap")
	}
	if sqrtPriceAfterX96.Cmp(MinSqrtRatio) < 0 {
		sqrtPriceAfterX96.Set(MinSqrtRatio)
	}

	// amount1 = L * (sqrtPrice - sqrtPriceAfter) / Q96
	priceDiff := new(big.Int).Sub(sqrtPriceX96, sqrtPriceAfterX96)
	amount1 := new(big.Int).Mul(liquidity, priceDiff)
	amount1.Div(amount1, Q96)

	return amount1, nil
}

// getAmount0ForAmount1 计算给定 amount1（token1 输入）能换多少 amount0（token0 输出）
// Δx = L * (1/√P_after - 1/√P_before)
// 其中 √P_after = √P_before + Δy / L * Q96 (但这里需要Q96 scale)
// 更精确: sqrtPriceAfterX96 = sqrtPriceX96 + amount1 * Q96 / L
func getAmount0ForAmount1(
	sqrtPriceX96 *big.Int,
	liquidity *big.Int,
	amount1 *big.Int,
) (*big.Int, error) {
	// sqrtPriceAfterX96 = sqrtPriceX96 + (amount1 * Q96) / liquidity
	delta := new(big.Int).Mul(amount1, Q96)
	delta.Div(delta, liquidity)

	sqrtPriceAfterX96 := new(big.Int).Add(sqrtPriceX96, delta)

	// 价格变化验证（oneForZero 时价格应上升）
	if sqrtPriceAfterX96.Cmp(sqrtPriceX96) < 0 {
		return nil, fmt.Errorf("price decreased in oneForZero swap")
	}
	if sqrtPriceAfterX96.Cmp(MaxSqrtRatio) > 0 {
		sqrtPriceAfterX96.Set(MaxSqrtRatio)
	}

	// amount0 = L * Q96 * (sqrtPriceAfter - sqrtPrice) / (sqrtPrice * sqrtPriceAfter)
	// 等价于: amount0 = L * Q96 / sqrtPriceX96 - L * Q96 / sqrtPriceAfterX96
	term1 := new(big.Int).Mul(liquidity, Q96)
	term1.Div(term1, sqrtPriceX96)

	term2 := new(big.Int).Mul(liquidity, Q96)
	term2.Div(term2, sqrtPriceAfterX96)

	amount0 := new(big.Int).Sub(term1, term2)

	if amount0.Sign() < 0 {
		return nil, fmt.Errorf("negative amount0 output")
	}

	return amount0, nil
}

// CalculateV3OutputForPool 根据 PoolInfo 计算 V3 输出（供 ProfitCalculator 调用）
func CalculateV3OutputForPool(
	pool *PoolInfo,
	tokenIn, tokenOut interface{ Hex() string },
	amountIn *big.Int,
) (*big.Int, error) {
	if pool.SqrtPriceX96 == nil || pool.Liquidity == nil {
		// 如果没有 V3 数据，回退到 V2 公式（作为粗略估计）
		return nil, fmt.Errorf("v3_math: pool missing V3 fields (SqrtPriceX96 or Liquidity)")
	}

	// 确定 swap 方向
	zeroForOne := tokenIn.Hex() == pool.Token0.Hex()

	return CalculateV3SwapOutput(
		pool.SqrtPriceX96,
		pool.Liquidity,
		amountIn,
		pool.Fee,
		zeroForOne,
	)
}

// SqrtPriceX96ToPrice 将 sqrtPriceX96 转换为可读价格
// price = (sqrtPriceX96 / 2^96)^2
// 返回 token1/token0 的价格（需要根据 decimals 调整）
func SqrtPriceX96ToPrice(sqrtPriceX96 *big.Int, decimals0, decimals1 uint) float64 {
	if sqrtPriceX96 == nil || sqrtPriceX96.Sign() <= 0 {
		return 0
	}

	// price = (sqrtPriceX96)^2 / Q192
	sqrtPrice := new(big.Float).SetInt(sqrtPriceX96)
	q96Float := new(big.Float).SetInt(Q96)

	// sqrtPrice / Q96
	ratio := new(big.Float).Quo(sqrtPrice, q96Float)
	// (ratio)^2
	price := new(big.Float).Mul(ratio, ratio)

	// 调整精度差异: price * 10^(decimals0 - decimals1)
	decimalDiff := int(decimals0) - int(decimals1)
	if decimalDiff != 0 {
		adjustment := new(big.Float).SetInt(
			new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(abs(decimalDiff))), nil),
		)
		if decimalDiff > 0 {
			price.Mul(price, adjustment)
		} else {
			price.Quo(price, adjustment)
		}
	}

	result, _ := price.Float64()
	return result
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

package strategy

import (
	"math"
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================
// CalculateV3SwapOutput tests
// ============================================================

// WETH/USDC pool on Uniswap V3 (realistic values)
// sqrtPriceX96 ≈ sqrt(2500) * 2^96 for a ~2500 USDC/WETH pool (6 vs 18 decimals)
// In practice, sqrtPriceX96 for USDC/WETH ~ 1.25e27 range
func TestCalculateV3SwapOutput_ZeroForOne_Normal(t *testing.T) {
	// sqrtPriceX96 = sqrt(price) * 2^96
	// For a pool with token0=WETH(18), token1=USDC(6):
	// raw price (token1_raw / token0_raw) ≈ 2500e6 / 1e18 = 2.5e-9
	// sqrtPriceX96 = sqrt(2.5e-9) * 2^96 ≈ 3.96e39
	sqrtPriceX96, _ := new(big.Int).SetString("3960000000000000000000000000000000000000", 10)
	liquidity := new(big.Int).SetUint64(1e18)             // 1 WETH equivalent
	amountIn := new(big.Int).Mul(big.NewInt(1), big.NewInt(1e15)) // 0.001 WETH

	out, err := CalculateV3SwapOutput(sqrtPriceX96, liquidity, amountIn, 30, true)
	require.NoError(t, err)
	assert.True(t, out.Sign() > 0, "output should be positive")
}

func TestCalculateV3SwapOutput_OneForZero_Normal(t *testing.T) {
	// Use sqrtPriceX96 = Q96 (price=1.0) for a same-decimals pool to avoid precision loss
	sqrtPriceX96 := new(big.Int).Set(Q96)
	liquidity, _ := new(big.Int).SetString("1000000000000000000000", 10) // 1000e18 large liquidity
	amountIn := big.NewInt(1e18)                                         // 1 token

	out, err := CalculateV3SwapOutput(sqrtPriceX96, liquidity, amountIn, 30, false)
	require.NoError(t, err)
	assert.True(t, out.Sign() > 0, "output should be positive")
}

func TestCalculateV3SwapOutput_FeeTier5bps(t *testing.T) {
	sqrtPriceX96, _ := new(big.Int).SetString("3960000000000000000000000000000000000000", 10)
	liquidity := new(big.Int).SetUint64(1e18)
	amountIn := big.NewInt(1e15)

	out5, err := CalculateV3SwapOutput(sqrtPriceX96, liquidity, amountIn, 5, true)
	require.NoError(t, err)

	out30, err := CalculateV3SwapOutput(sqrtPriceX96, liquidity, amountIn, 30, true)
	require.NoError(t, err)

	// Lower fee should yield more output
	assert.True(t, out5.Cmp(out30) > 0, "5bps fee should give more output than 30bps")
}

func TestCalculateV3SwapOutput_FeeTier100bps(t *testing.T) {
	sqrtPriceX96, _ := new(big.Int).SetString("3960000000000000000000000000000000000000", 10)
	liquidity := new(big.Int).SetUint64(1e18)
	amountIn := big.NewInt(1e15)

	out30, err := CalculateV3SwapOutput(sqrtPriceX96, liquidity, amountIn, 30, true)
	require.NoError(t, err)

	out100, err := CalculateV3SwapOutput(sqrtPriceX96, liquidity, amountIn, 100, true)
	require.NoError(t, err)

	assert.True(t, out30.Cmp(out100) > 0, "30bps fee should give more output than 100bps")
}

func TestCalculateV3SwapOutput_LargeAmountIn(t *testing.T) {
	sqrtPriceX96, _ := new(big.Int).SetString("3960000000000000000000000000000000000000", 10)
	liquidity, _ := new(big.Int).SetString("100000000000000000000", 10) // 100 WETH liquidity
	amountIn := new(big.Int).Mul(big.NewInt(10), big.NewInt(1e18))     // 10 WETH

	out, err := CalculateV3SwapOutput(sqrtPriceX96, liquidity, amountIn, 30, true)
	require.NoError(t, err)
	assert.True(t, out.Sign() > 0)
}

func TestCalculateV3SwapOutput_SmallAmountIn(t *testing.T) {
	sqrtPriceX96, _ := new(big.Int).SetString("3960000000000000000000000000000000000000", 10)
	liquidity := new(big.Int).SetUint64(1e18)
	amountIn := big.NewInt(1000) // very small

	out, err := CalculateV3SwapOutput(sqrtPriceX96, liquidity, amountIn, 30, true)
	require.NoError(t, err)
	assert.True(t, out.Sign() >= 0)
}

func TestCalculateV3SwapOutput_NilSqrtPrice(t *testing.T) {
	_, err := CalculateV3SwapOutput(nil, big.NewInt(1e18), big.NewInt(1e15), 30, true)
	assert.Error(t, err)
}

func TestCalculateV3SwapOutput_ZeroSqrtPrice(t *testing.T) {
	_, err := CalculateV3SwapOutput(big.NewInt(0), big.NewInt(1e18), big.NewInt(1e15), 30, true)
	assert.Error(t, err)
}

func TestCalculateV3SwapOutput_NilLiquidity(t *testing.T) {
	sqrtPriceX96, _ := new(big.Int).SetString("3960000000000000000000000000000000000000", 10)
	_, err := CalculateV3SwapOutput(sqrtPriceX96, nil, big.NewInt(1e15), 30, true)
	assert.Error(t, err)
}

func TestCalculateV3SwapOutput_ZeroAmountIn(t *testing.T) {
	sqrtPriceX96, _ := new(big.Int).SetString("3960000000000000000000000000000000000000", 10)
	_, err := CalculateV3SwapOutput(sqrtPriceX96, big.NewInt(1e18), big.NewInt(0), 30, true)
	assert.Error(t, err)
}

func TestCalculateV3SwapOutput_NilAmountIn(t *testing.T) {
	sqrtPriceX96, _ := new(big.Int).SetString("3960000000000000000000000000000000000000", 10)
	_, err := CalculateV3SwapOutput(sqrtPriceX96, big.NewInt(1e18), nil, 30, true)
	assert.Error(t, err)
}

// ============================================================
// getAmount1ForAmount0 / getAmount0ForAmount1 tests
// ============================================================

func TestGetAmount1ForAmount0_PriceDecreases(t *testing.T) {
	sqrtPriceX96, _ := new(big.Int).SetString("3960000000000000000000000000000000000000", 10)
	liquidity := new(big.Int).SetUint64(1e18)
	amount0 := big.NewInt(1e15)

	amount1, err := getAmount1ForAmount0(sqrtPriceX96, liquidity, amount0)
	require.NoError(t, err)
	assert.True(t, amount1.Sign() > 0, "amount1 output should be positive")
}

func TestGetAmount0ForAmount1_PriceIncreases(t *testing.T) {
	sqrtPriceX96, _ := new(big.Int).SetString("3960000000000000000000000000000000000000", 10)
	liquidity := new(big.Int).SetUint64(1e18)
	amount1 := big.NewInt(1_000_000) // 1 USDC

	amount0, err := getAmount0ForAmount1(sqrtPriceX96, liquidity, amount1)
	require.NoError(t, err)
	assert.True(t, amount0.Sign() >= 0, "amount0 output should be non-negative")
}

func TestGetAmount1ForAmount0_MinSqrtRatioCapping(t *testing.T) {
	// Very small sqrtPriceX96, close to MinSqrtRatio
	sqrtPriceX96 := new(big.Int).Add(MinSqrtRatio, big.NewInt(1000000))
	liquidity := big.NewInt(1e18)
	amount0 := big.NewInt(1e18) // Large amount to push price down to MinSqrtRatio

	// Should not error, should cap at MinSqrtRatio
	amount1, err := getAmount1ForAmount0(sqrtPriceX96, liquidity, amount0)
	require.NoError(t, err)
	assert.True(t, amount1.Sign() >= 0)
}

func TestGetAmount0ForAmount1_MaxSqrtRatioCapping(t *testing.T) {
	// Large sqrtPriceX96, close to MaxSqrtRatio
	sqrtPriceX96 := new(big.Int).Sub(MaxSqrtRatio, big.NewInt(1000000))
	liquidity := big.NewInt(1e18)
	amount1, _ := new(big.Int).SetString("1000000000000000000000000000", 10) // very large

	amount0, err := getAmount0ForAmount1(sqrtPriceX96, liquidity, amount1)
	require.NoError(t, err)
	assert.True(t, amount0.Sign() >= 0)
}

// ============================================================
// SqrtPriceX96ToPrice tests
// ============================================================

func TestSqrtPriceX96ToPrice_KnownValue(t *testing.T) {
	// For a 1:1 pool (same decimals), sqrtPriceX96 = 2^96 means price = 1.0
	price := SqrtPriceX96ToPrice(Q96, 18, 18)
	assert.InDelta(t, 1.0, price, 0.001, "sqrtPriceX96=Q96 should give price≈1.0 for same decimals")
}

func TestSqrtPriceX96ToPrice_DifferentDecimals(t *testing.T) {
	// sqrtPriceX96 = Q96 → raw price = 1.0
	// With decimals0=18, decimals1=6 → adjusted price = 1.0 * 10^(18-6) = 1e12
	price := SqrtPriceX96ToPrice(Q96, 18, 6)
	assert.InDelta(t, 1e12, price, 1e9, "should adjust for decimal difference")
}

func TestSqrtPriceX96ToPrice_NilInput(t *testing.T) {
	price := SqrtPriceX96ToPrice(nil, 18, 18)
	assert.Equal(t, float64(0), price, "nil input should return 0")
}

func TestSqrtPriceX96ToPrice_ZeroInput(t *testing.T) {
	price := SqrtPriceX96ToPrice(big.NewInt(0), 18, 18)
	assert.Equal(t, float64(0), price, "zero input should return 0")
}

func TestSqrtPriceX96ToPrice_Positive(t *testing.T) {
	// Any valid sqrtPriceX96 should give a positive price
	sqrtPriceX96, _ := new(big.Int).SetString("3960000000000000000000000000000000000000", 10)
	price := SqrtPriceX96ToPrice(sqrtPriceX96, 18, 6)
	assert.True(t, price > 0, "price should be positive")
	assert.False(t, math.IsInf(price, 0), "price should not be infinite")
	assert.False(t, math.IsNaN(price), "price should not be NaN")
}

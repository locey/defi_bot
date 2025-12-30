// Package utils 提供价格计算和转换工具
package utils

import (
	"fmt"
	"math/big"

	"github.com/shopspring/decimal"
)

// PriceCalculator 价格计算器（业界标准实现）
// 参考: Flashbots, 1inch, Uniswap
type PriceCalculator struct{}

// NewPriceCalculator 创建价格计算器
func NewPriceCalculator() *PriceCalculator {
	return &PriceCalculator{}
}

// CalculateNormalizedPrice 计算标准化价格
// 将 Wei 单位的储备量转换为人类可读的价格
//
// 参数:
//   - reserve0: 代币0的储备量（Wei 单位）
//   - reserve1: 代币1的储备量（Wei 单位）
//   - decimals0: 代币0的精度
//   - decimals1: 代币1的精度
//
// 返回: 标准化价格（token1/token0）
//
// 示例:
//   reserve0 = 100 * 10^18 (100 WETH)
//   reserve1 = 293000 * 10^6 (293000 USDT)
//   decimals0 = 18, decimals1 = 6
//   结果 = 2930.00 USDT/ETH
func (pc *PriceCalculator) CalculateNormalizedPrice(
	reserve0, reserve1 *big.Int,
	decimals0, decimals1 int,
) decimal.Decimal {

	// 1. 转换为 decimal（自动处理精度）
	// NewFromBigInt 的第二个参数是指数，负数表示除以 10^n
	r0 := decimal.NewFromBigInt(reserve0, int32(-decimals0)) // 100 * 10^18 → 100.0
	r1 := decimal.NewFromBigInt(reserve1, int32(-decimals1)) // 293000 * 10^6 → 293000.0

	// 2. 计算价格 = reserve1 / reserve0
	if r0.IsZero() {
		return decimal.Zero
	}

	price := r1.Div(r0) // 293000 / 100 = 2930.00

	return price
}

// CalculateInversePrice 计算反向价格
func (pc *PriceCalculator) CalculateInversePrice(price decimal.Decimal) decimal.Decimal {
	if price.IsZero() {
		return decimal.Zero
	}
	return decimal.NewFromInt(1).Div(price)
}

// CalculatePriceFromRawStrings 从原始字符串计算价格
// 便捷方法，用于处理从数据库读取的字符串
func (pc *PriceCalculator) CalculatePriceFromRawStrings(
	reserve0Str, reserve1Str string,
	decimals0, decimals1 int,
) (decimal.Decimal, error) {

	reserve0 := new(big.Int)
	reserve1 := new(big.Int)

	if _, ok := reserve0.SetString(reserve0Str, 10); !ok {
		return decimal.Zero, fmt.Errorf("invalid reserve0: %s", reserve0Str)
	}
	if _, ok := reserve1.SetString(reserve1Str, 10); !ok {
		return decimal.Zero, fmt.Errorf("invalid reserve1: %s", reserve1Str)
	}

	return pc.CalculateNormalizedPrice(reserve0, reserve1, decimals0, decimals1), nil
}

// FormatPrice 格式化价格用于显示
//
// precision: 小数位数
// 示例: FormatPrice(2930.123456, 2) → "2930.12"
func (pc *PriceCalculator) FormatPrice(price decimal.Decimal, precision int32) string {
	return price.StringFixed(precision)
}

// ConvertToWei 将标准化金额转换为 Wei（用于合约调用）
//
// 示例: ConvertToWei(1.5, 18) → "1500000000000000000"
func (pc *PriceCalculator) ConvertToWei(
	amount decimal.Decimal,
	decimals int,
) *big.Int {
	// amount * 10^decimals
	wei := amount.Shift(int32(decimals))
	return wei.BigInt()
}

// ConvertFromWei 将 Wei 转换为标准化金额
//
// 示例: ConvertFromWei("1500000000000000000", 18) → 1.5
func (pc *PriceCalculator) ConvertFromWei(
	wei *big.Int,
	decimals int,
) decimal.Decimal {
	return decimal.NewFromBigInt(wei, int32(-decimals))
}

// ConvertFromWeiString 从 Wei 字符串转换
func (pc *PriceCalculator) ConvertFromWeiString(
	weiStr string,
	decimals int,
) (decimal.Decimal, error) {
	wei := new(big.Int)
	if _, ok := wei.SetString(weiStr, 10); !ok {
		return decimal.Zero, fmt.Errorf("invalid wei string: %s", weiStr)
	}
	return pc.ConvertFromWei(wei, decimals), nil
}

// ComparePrices 比较两个价格
// 返回: -1 (p1 < p2), 0 (p1 = p2), 1 (p1 > p2)
func (pc *PriceCalculator) ComparePrices(p1, p2 decimal.Decimal) int {
	return p1.Cmp(p2)
}

// CalculatePriceChange 计算价格变化率
// 返回: 变化率（0.05 表示 5%）
func (pc *PriceCalculator) CalculatePriceChange(oldPrice, newPrice decimal.Decimal) decimal.Decimal {
	if oldPrice.IsZero() {
		return decimal.Zero
	}
	change := newPrice.Sub(oldPrice).Div(oldPrice)
	return change
}

// CalculatePriceImpact 计算价格影响
// 用于评估大额交易的滑点
func (pc *PriceCalculator) CalculatePriceImpact(
	spotPrice, executionPrice decimal.Decimal,
) decimal.Decimal {
	if spotPrice.IsZero() {
		return decimal.Zero
	}
	impact := executionPrice.Sub(spotPrice).Div(spotPrice).Abs()
	return impact
}

// RoundPrice 四舍五入价格到指定精度
func (pc *PriceCalculator) RoundPrice(price decimal.Decimal, places int32) decimal.Decimal {
	return price.Round(places)
}

// ParsePrice 解析价格字符串
func (pc *PriceCalculator) ParsePrice(priceStr string) (decimal.Decimal, error) {
	price, err := decimal.NewFromString(priceStr)
	if err != nil {
		return decimal.Zero, fmt.Errorf("invalid price string: %s - %w", priceStr, err)
	}
	return price, nil
}


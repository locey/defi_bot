// Package validation 提供数据验证功能
package validation

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

// PriceValidator 价格验证器
// 实现三层验证机制（Flashbots 标准）
type PriceValidator struct {
	history map[string]*PriceHistory
	mu      sync.RWMutex
}

// PriceHistory 价格历史记录
type PriceHistory struct {
	LastPrice     decimal.Decimal
	LastTimestamp time.Time
	Prices24h     []PricePoint // 24小时价格历史
}

// PricePoint 价格点
type PricePoint struct {
	Price     decimal.Decimal
	Timestamp time.Time
}

// NewPriceValidator 创建价格验证器
func NewPriceValidator() *PriceValidator {
	return &PriceValidator{
		history: make(map[string]*PriceHistory),
	}
}

// Validate 完整的三层验证
func (pv *PriceValidator) Validate(
	pair string,
	price decimal.Decimal,
	pairType string,
) error {

	// Level 1: 基础验证
	if err := pv.ValidateBasic(price); err != nil {
		return fmt.Errorf("basic validation failed: %w", err)
	}

	// Level 2: 合理性验证
	if err := pv.ValidateReasonable(pair, price, pairType); err != nil {
		return fmt.Errorf("reasonable validation failed: %w", err)
	}

	// Level 3: 变化率验证
	if err := pv.ValidateChangeRate(pair, price); err != nil {
		return fmt.Errorf("change rate validation failed: %w", err)
	}

	return nil
}

// ValidateBasic Level 1: 基础验证
func (pv *PriceValidator) ValidateBasic(price decimal.Decimal) error {
	if price.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("price must be positive, got %s", price.String())
	}
	return nil
}

// ValidateReasonable Level 2: 合理性验证
func (pv *PriceValidator) ValidateReasonable(
	pair string,
	price decimal.Decimal,
	pairType string,
) error {

	// 稳定币对验证
	if pairType == "stablecoin" {
		min := decimal.NewFromFloat(0.95)
		max := decimal.NewFromFloat(1.05)

		if price.LessThan(min) || price.GreaterThan(max) {
			return fmt.Errorf(
				"stablecoin price out of range: %s (expected 0.95-1.05)",
				price.StringFixed(4),
			)
		}
	}

	// 极端值检查
	maxPrice := decimal.NewFromInt(10000000) // 1000万
	if price.GreaterThan(maxPrice) {
		return fmt.Errorf("price too high: %s (max: 10,000,000)", price.String())
	}

	return nil
}

// ValidateChangeRate Level 3: 变化率验证
func (pv *PriceValidator) ValidateChangeRate(
	pair string,
	price decimal.Decimal,
) error {

	pv.mu.RLock()
	history := pv.history[pair]
	pv.mu.RUnlock()

	// 第一次记录，直接通过
	if history == nil || history.LastPrice.IsZero() {
		pv.updateHistory(pair, price)
		return nil
	}

	// 计算变化率
	changeRate := price.Sub(history.LastPrice).Div(history.LastPrice).Abs()

	// 警告阈值: 10%
	if changeRate.GreaterThan(decimal.NewFromFloat(0.1)) {
		log.Printf("⚠️  价格大幅变化 %s: %.2f%% (从 %s 到 %s)",
			pair,
			changeRate.Mul(decimal.NewFromInt(100)).InexactFloat64(),
			history.LastPrice.StringFixed(2),
			price.StringFixed(2),
		)
	}

	// 错误阈值: 50%
	if changeRate.GreaterThan(decimal.NewFromFloat(0.5)) {
		return fmt.Errorf(
			"price change too large: %.2f%% (from %s to %s)",
			changeRate.Mul(decimal.NewFromInt(100)).InexactFloat64(),
			history.LastPrice.StringFixed(2),
			price.StringFixed(2),
		)
	}

	// 更新历史
	pv.updateHistory(pair, price)

	return nil
}

// updateHistory 更新价格历史
func (pv *PriceValidator) updateHistory(pair string, price decimal.Decimal) {
	pv.mu.Lock()
	defer pv.mu.Unlock()

	if pv.history[pair] == nil {
		pv.history[pair] = &PriceHistory{
			Prices24h: make([]PricePoint, 0, 1440), // 24h * 60min
		}
	}

	history := pv.history[pair]
	history.LastPrice = price
	history.LastTimestamp = time.Now()

	// 添加到24小时历史
	history.Prices24h = append(history.Prices24h, PricePoint{
		Price:     price,
		Timestamp: time.Now(),
	})

	// 清理超过24小时的数据
	cutoff := time.Now().Add(-24 * time.Hour)
	validPrices := make([]PricePoint, 0, len(history.Prices24h))
	for _, point := range history.Prices24h {
		if point.Timestamp.After(cutoff) {
			validPrices = append(validPrices, point)
		}
	}
	history.Prices24h = validPrices
}

// GetPriceStatistics 获取价格统计信息
func (pv *PriceValidator) GetPriceStatistics(pair string) *PriceStatistics {
	pv.mu.RLock()
	defer pv.mu.RUnlock()

	history := pv.history[pair]
	if history == nil || len(history.Prices24h) == 0 {
		return nil
	}

	var min, max, sum decimal.Decimal
	min = history.Prices24h[0].Price
	max = history.Prices24h[0].Price

	for _, point := range history.Prices24h {
		if point.Price.LessThan(min) {
			min = point.Price
		}
		if point.Price.GreaterThan(max) {
			max = point.Price
		}
		sum = sum.Add(point.Price)
	}

	count := decimal.NewFromInt(int64(len(history.Prices24h)))
	avg := sum.Div(count)

	return &PriceStatistics{
		Current: history.LastPrice,
		Min24h:  min,
		Max24h:  max,
		Avg24h:  avg,
		Count:   len(history.Prices24h),
	}
}

// PriceStatistics 价格统计信息
type PriceStatistics struct {
	Current decimal.Decimal
	Min24h  decimal.Decimal
	Max24h  decimal.Decimal
	Avg24h  decimal.Decimal
	Count   int
}

// DeterminePairType 判断交易对类型
func DeterminePairType(token0, token1 string) string {
	stablecoins := map[string]bool{
		"USDT": true,
		"USDC": true,
		"DAI":  true,
		"BUSD": true,
		"TUSD": true,
		"USDD": true,
		"FRAX": true,
		"LUSD": true,
	}

	if stablecoins[token0] && stablecoins[token1] {
		return "stablecoin"
	}
	return "normal"
}

// ValidateTimestamp 验证时间戳合理性
func ValidateTimestamp(ts time.Time) error {
	now := time.Now()

	// 不能是未来时间（允许1分钟误差）
	if ts.After(now.Add(1 * time.Minute)) {
		return fmt.Errorf("timestamp is in the future: %v", ts)
	}

	// 不能太旧（超过1小时认为是异常）
	if ts.Before(now.Add(-1 * time.Hour)) {
		return fmt.Errorf("timestamp too old: %v (more than 1 hour old)", ts)
	}

	return nil
}




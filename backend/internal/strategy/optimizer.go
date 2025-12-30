// internal/strategy/optimizer.go
package strategy

import (
    "context"
    "fmt"
    "math/big"
)

// AmountOptimizer 金额优化器
type AmountOptimizer struct {
    config *StrategyConfig
    engine *StrategyEngine
}

// NewAmountOptimizer 创建金额优化器
func NewAmountOptimizer(config *StrategyConfig, engine *StrategyEngine) *AmountOptimizer {
    return &AmountOptimizer{
        config: config,
        engine: engine,
    }
}

// FindOptimalAmount 使用二分搜索找到最优投入金额
func (ao *AmountOptimizer) FindOptimalAmount(
    ctx context.Context,
    path []PathNode,
) (*big.Int, *big.Int, error) {
    
    profitCalc := ao.engine.profitCalc
    
    // 获取池子的最小流动性，确定搜索范围
    minLiquidity := ao.getMinLiquidity(path)
    
    // 搜索范围：0.001 ETH 到 最小流动性的10%
    minAmount := big.NewInt(1e15)  // 0.001 ETH
    maxAmount := new(big.Int).Div(minLiquidity, big.NewInt(10))
    
    if maxAmount.Cmp(minAmount) <= 0 {
        return nil, nil, fmt.Errorf("insufficient liquidity")
    }
    
    // 二分搜索找最优金额
    optimalAmount, maxProfit, err := ao.binarySearchOptimal(
        ctx, path, minAmount, maxAmount, profitCalc,
    )
    if err != nil {
        return nil, nil, err
    }
    
    // 计算最优金额对应的输出
    expectedOut, _, err := profitCalc.CalculatePathOutput(ctx, path, optimalAmount)
    if err != nil {
        return nil, nil, err
    }
    
    // 验证利润为正
    if maxProfit.Sign() <= 0 {
        return nil, nil, fmt.Errorf("no profitable amount found")
    }
    
    return optimalAmount, expectedOut, nil
}

// binarySearchOptimal 二分搜索最优金额
func (ao *AmountOptimizer) binarySearchOptimal(
    ctx context.Context,
    path []PathNode,
    low *big.Int,
    high *big.Int,
    profitCalc *ProfitCalculator,
) (*big.Int, *big.Int, error) {
    
    // 精度：0.01 ETH
    precision := big.NewInt(1e16)
    
    var optimalAmount *big.Int
    maxProfit := big.NewInt(0)
    
    for new(big.Int).Sub(high, low).Cmp(precision) > 0 {
        // 计算中点
        mid := new(big.Int).Add(low, high)
        mid.Div(mid, big.NewInt(2))
        
        // 计算中点利润
        profit, err := ao.calculateProfitAt(ctx, path, mid, profitCalc)
        if err != nil {
            // 如果计算失败，缩小范围
            high = mid
            continue
        }
        
        // 计算左右两侧的利润梯度
        delta := new(big.Int).Div(new(big.Int).Sub(high, low), big.NewInt(10))
        if delta.Sign() <= 0 {
            delta = precision
        }
        
        leftPoint := new(big.Int).Sub(mid, delta)
        rightPoint := new(big.Int).Add(mid, delta)
        
        leftProfit, _ := ao.calculateProfitAt(ctx, path, leftPoint, profitCalc)
        rightProfit, _ := ao.calculateProfitAt(ctx, path, rightPoint, profitCalc)
        
        // 更新最优值
        if profit.Cmp(maxProfit) > 0 {
            maxProfit = profit
            optimalAmount = mid
        }
        
        // 根据梯度调整搜索范围
        if rightProfit != nil && leftProfit != nil {
            if rightProfit.Cmp(leftProfit) > 0 {
                // 右边更好，向右搜索
                low = mid
            } else {
                // 左边更好，向左搜索
                high = mid
            }
        } else if profit.Sign() > 0 {
            // 如果当前有利润，尝试增加金额
            low = mid
        } else {
            // 如果当前无利润，减少金额
            high = mid
        }
    }
    
    if optimalAmount == nil {
        optimalAmount = low
        maxProfit, _ = ao.calculateProfitAt(ctx, path, low, profitCalc)
    }
    
    return optimalAmount, maxProfit, nil
}

// calculateProfitAt 计算指定金额的利润
func (ao *AmountOptimizer) calculateProfitAt(
    ctx context.Context,
    path []PathNode,
    amount *big.Int,
    profitCalc *ProfitCalculator,
) (*big.Int, error) {
    
    amountOut, _, err := profitCalc.CalculatePathOutput(ctx, path, amount)
    if err != nil {
        return nil, err
    }
    
    profit := profitCalc.CalculateProfit(amount, amountOut)
    return profit, nil
}

// getMinLiquidity 获取路径中最小的流动性
func (ao *AmountOptimizer) getMinLiquidity(path []PathNode) *big.Int {
    minLiquidity := new(big.Int).SetUint64(^uint64(0)) // 最大值
    
    for i := 0; i < len(path)-1; i++ {
        pool := path[i].Pool
        if pool == nil {
            continue
        }
        
        // 使用较小的储备作为流动性指标
        liquidity := pool.Reserve0
        if pool.Reserve1.Cmp(liquidity) < 0 {
            liquidity = pool.Reserve1
        }
        
        if liquidity.Cmp(minLiquidity) < 0 {
            minLiquidity = liquidity
        }
    }
    
    return minLiquidity
}

// OptimizeWithConstraints 带约束的优化
func (ao *AmountOptimizer) OptimizeWithConstraints(
    ctx context.Context,
    path []PathNode,
    maxAmount *big.Int,      // 最大可用金额
    minProfitRate float64,   // 最小利润率
) (*big.Int, *big.Int, error) {
    
    profitCalc := ao.engine.profitCalc
    
    // 先找理论最优
    optimalAmount, expectedOut, err := ao.FindOptimalAmount(ctx, path)
    if err != nil {
        return nil, nil, err
    }
    
    // 检查是否超过最大可用金额
    if optimalAmount.Cmp(maxAmount) > 0 {
        // 使用最大可用金额
        optimalAmount = maxAmount
        expectedOut, _, err = profitCalc.CalculatePathOutput(ctx, path, optimalAmount)
        if err != nil {
            return nil, nil, err
        }
    }
    
    // 检查利润率
    profit := profitCalc.CalculateProfit(optimalAmount, expectedOut)
    profitRate := profitCalc.CalculateProfitRate(optimalAmount, profit)
    
    if profitRate < minProfitRate {
        return nil, nil, fmt.Errorf("profit rate %.4f below minimum %.4f", 
            profitRate, minProfitRate)
    }
    
    return optimalAmount, expectedOut, nil
}
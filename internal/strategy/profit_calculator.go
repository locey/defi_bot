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
    
    if tokenIn == pool.Token0 {
        reserveIn = pool.Reserve0
        reserveOut = pool.Reserve1
    } else if tokenIn == pool.Token1 {
        reserveIn = pool.Reserve1
        reserveOut = pool.Reserve0
    } else {
        return nil, fmt.Errorf("tokenIn not in pool")
    }
    
    // 根据DEX类型计算输出
    switch pool.DexName {
    case "uniswap_v2", "sushiswap":
        return pc.calculateV2Output(amountIn, reserveIn, reserveOut, pool.Fee)
    case "uniswap_v3":
        return pc.calculateV3Output(amountIn, reserveIn, reserveOut, pool.Fee)
    default:
        return pc.calculateV2Output(amountIn, reserveIn, reserveOut, pool.Fee)
    }
}

// calculateV2Output Uniswap V2 AMM公式
// amountOut = (amountIn * fee * reserveOut) / (reserveIn * 1000 + amountIn * fee)
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
    
    return amountOut, nil
}

// calculateV3Output Uniswap V3 计算（简化版）
func (pc *ProfitCalculator) calculateV3Output(
    amountIn *big.Int,
    reserveIn *big.Int,
    reserveOut *big.Int,
    feeBps uint64,
) (*big.Int, error) {
    // V3的计算更复杂，这里使用简化版本
    // 实际应该考虑tick范围和集中流动性
    return pc.calculateV2Output(amountIn, reserveIn, reserveOut, feeBps)
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
        smallAmount = big.NewInt(1e15) // 最小0.001 ETH
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
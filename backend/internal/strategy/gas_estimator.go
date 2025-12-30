// internal/strategy/gas_estimator.go
package strategy

import (
    "context"
    "fmt"
    "math/big"

    "github.com/ethereum/go-ethereum"
    "github.com/ethereum/go-ethereum/common"
    "your-project/pkg/web3"
)

// GasEstimator Gas估算器
type GasEstimator struct {
    web3Client *web3.Client
    
    // Gas缓存
    baseGasPerSwap  uint64  // 每次swap的基础Gas
    baseGasOverhead uint64  // 固定开销
}

// NewGasEstimator 创建Gas估算器
func NewGasEstimator(web3Client *web3.Client) *GasEstimator {
    return &GasEstimator{
        web3Client:      web3Client,
        baseGasPerSwap:  150000, // 每次swap约150k gas
        baseGasOverhead: 50000,  // 固定开销约50k
    }
}

// EstimateGas 估算交易Gas
func (ge *GasEstimator) EstimateGas(
    ctx context.Context,
    path []PathNode,
    amountIn *big.Int,
) (uint64, *big.Int, error) {
    
    // 1. 估算Gas用量
    gasEstimate := ge.estimateGasUsage(path)
    
    // 2. 获取当前Gas价格
    gasPrice, err := ge.getGasPrice(ctx)
    if err != nil {
        return 0, nil, fmt.Errorf("get gas price: %w", err)
    }
    
    return gasEstimate, gasPrice, nil
}

// estimateGasUsage 估算Gas用量
func (ge *GasEstimator) estimateGasUsage(path []PathNode) uint64 {
    // 基础方法：固定开销 + 每步swap的Gas
    numSwaps := len(path) - 1
    
    // 不同DEX的Gas消耗不同
    var totalGas uint64 = ge.baseGasOverhead
    
    for i := 0; i < numSwaps; i++ {
        swapGas := ge.getSwapGas(path[i].DexName)
        totalGas += swapGas
    }
    
    // 添加20%安全边际
    totalGas = totalGas * 120 / 100
    
    return totalGas
}

// getSwapGas 获取不同DEX的swap Gas消耗
func (ge *GasEstimator) getSwapGas(dexName string) uint64 {
    switch dexName {
    case "uniswap_v2":
        return 120000
    case "sushiswap":
        return 120000
    case "uniswap_v3":
        return 180000  // V3稍贵
    case "curve":
        return 250000  // Curve更贵
    default:
        return 150000
    }
}

// getGasPrice 获取当前Gas价格
func (ge *GasEstimator) getGasPrice(ctx context.Context) (*big.Int, error) {
    gasPrice, err := ge.web3Client.SuggestGasPrice(ctx)
    if err != nil {
        return nil, err
    }
    
    // 添加10%溢价确保交易被打包
    premium := new(big.Int).Div(gasPrice, big.NewInt(10))
    gasPrice.Add(gasPrice, premium)
    
    return gasPrice, nil
}

// EstimateGasWithSimulation 通过模拟获取更精确的Gas估算
func (ge *GasEstimator) EstimateGasWithSimulation(
    ctx context.Context,
    contractAddress common.Address,
    callData []byte,
    from common.Address,
) (uint64, *big.Int, error) {
    
    // 构造调用消息
    msg := ethereum.CallMsg{
        From: from,
        To:   &contractAddress,
        Data: callData,
    }
    
    // 估算Gas
    gasEstimate, err := ge.web3Client.EstimateGas(ctx, msg)
    if err != nil {
        // 如果估算失败，使用默认值
        gasEstimate = 500000
    }
    
    // 添加安全边际
    gasEstimate = gasEstimate * 130 / 100
    
    // 获取Gas价格
    gasPrice, err := ge.getGasPrice(ctx)
    if err != nil {
        return 0, nil, err
    }
    
    return gasEstimate, gasPrice, nil
}

// CalculateGasCost 计算Gas成本（以代币计）
func (ge *GasEstimator) CalculateGasCost(
    gasEstimate uint64,
    gasPrice *big.Int,
) *big.Int {
    return new(big.Int).Mul(
        new(big.Int).SetUint64(gasEstimate),
        gasPrice,
    )
}

// CalculateMinProfit 计算最小利润（2 * Gas成本）
func (ge *GasEstimator) CalculateMinProfit(
    gasEstimate uint64,
    gasPrice *big.Int,
) *big.Int {
    gasCost := ge.CalculateGasCost(gasEstimate, gasPrice)
    return new(big.Int).Mul(gasCost, big.NewInt(2))
}

// IsGasReasonable 检查Gas价格是否合理
func (ge *GasEstimator) IsGasReasonable(
    ctx context.Context,
    maxGasPrice *big.Int,
) (bool, *big.Int, error) {
    
    currentPrice, err := ge.getGasPrice(ctx)
    if err != nil {
        return false, nil, err
    }
    
    return currentPrice.Cmp(maxGasPrice) <= 0, currentPrice, nil
}
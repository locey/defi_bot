// internal/strategy/gas_estimator.go
package strategy

import (
	"context"
	"fmt"
	"math/big"

	"github.com/defi-bot/backend/pkg/web3"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
)

// GasEstimator Gas估算器
type GasEstimator struct {
	web3Client *web3.Client

	// Gas缓存
	baseGasPerSwap  uint64 // 每次swap的基础Gas
	baseGasOverhead uint64 // 固定开销
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
// 实测数据：3-hop 路径总 gas ~1.5M，单步 V3 约 400-500K（含合约路由开销）
func (ge *GasEstimator) getSwapGas(dexName string) uint64 {
	switch dexName {
	case "uniswap_v2":
		return 200000 // V2 含 DoubleRouter 路由开销
	case "sushiswap":
		return 200000
	case "uniswap_v3":
		return 450000 // V3 含 tick 遍历 + 路由开销（实测 ~467K）
	case "curve":
		return 350000 // Curve 更贵
	default:
		return 350000 // 未知 DEX 保守估计
	}
}

// getGasPrice 获取当前Gas价格
func (ge *GasEstimator) getGasPrice(ctx context.Context) (*big.Int, error) {
	gasPrice, err := ge.web3Client.GetClient().SuggestGasPrice(ctx)
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
	gasEstimate, err := ge.web3Client.GetClient().EstimateGas(ctx, msg)
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

// EstimateArbitrumTotalGasCost 估算 Arbitrum 上的总 Gas 成本（L2 执行费 + L1 数据费）
// Arbitrum 是 L2，每笔交易除 L2 Gas 外，还需支付将 calldata 发布到 L1 的费用
// L1 数据费 = calldata 字节数 * L1 Gas Price * 16（每字节约 16 L1 gas）
// 返回值：以 wei 为单位的总 Gas 成本估算
func (ge *GasEstimator) EstimateArbitrumTotalGasCost(
	ctx context.Context,
	path []PathNode,
	calldataSize int, // calldata 字节数（套利合约调用约 700-1000 字节）
) (*big.Int, error) {
	// 1. L2 执行 Gas 成本
	gasEstimate := ge.estimateGasUsage(path)
	l2GasPrice, err := ge.getGasPrice(ctx)
	if err != nil {
		// fallback: 0.1 gwei
		l2GasPrice = big.NewInt(100_000_000)
	}
	l2Cost := new(big.Int).Mul(new(big.Int).SetUint64(gasEstimate), l2GasPrice)

	// 2. L1 数据费估算
	// 通过查询 ArbGasInfo 预编译合约获取 L1 BaseFee
	// 合约地址：0x000000000000000000000000000000000000006C
	// 方法：getL1BaseFeeEstimate() returns (uint256)
	l1GasPrice, l1Err := ge.getL1GasPrice(ctx)
	if l1Err != nil {
		// fallback: 假设 L1 BaseFee = 20 gwei（以太坊正常水平）
		l1GasPrice = new(big.Int).Mul(big.NewInt(20), big.NewInt(1_000_000_000))
	}

	if calldataSize <= 0 {
		calldataSize = 800 // 默认套利 calldata 约 800 字节
	}
	// L1 数据费 = calldataBytes * 16 * l1GasPrice（16 gas/byte 是 non-zero 字节的标准）
	l1DataGas := int64(calldataSize) * 16
	l1Cost := new(big.Int).Mul(big.NewInt(l1DataGas), l1GasPrice)

	// 3. 总成本
	totalCost := new(big.Int).Add(l2Cost, l1Cost)
	return totalCost, nil
}

// EstimateTotalCostWei 估算 Arbitrum 总 Gas 成本（L2 + L1），直接返回 wei 值
// 用于利润判断管道：profit > EstimateTotalCostWei 才值得执行
// pathLen: swap 跳数（2-hop = 2 个 swap）
func (ge *GasEstimator) EstimateTotalCostWei(ctx context.Context, pathLen int) *big.Int {
	// L2 Gas 估算（实测 V3 单步 ~450K，含 DoubleRouter 路由开销）
	gasPerSwap := uint64(450_000) // V3 swap 实测 ~467K
	gasOverhead := uint64(50_000)
	l2Gas := gasOverhead + uint64(pathLen)*gasPerSwap
	l2Gas = l2Gas * 120 / 100 // 20% 安全边际（已用实测值，不需要太高）

	l2GasPrice, err := ge.getGasPrice(ctx)
	if err != nil {
		l2GasPrice = big.NewInt(100_000_000) // 0.1 gwei fallback
	}
	l2Cost := new(big.Int).Mul(new(big.Int).SetUint64(l2Gas), l2GasPrice)

	// L1 数据费
	calldataSize := 800 + pathLen*64 // 基础 800 字节 + 每 hop 约 64 字节地址
	l1GasPrice, l1Err := ge.getL1GasPrice(ctx)
	if l1Err != nil {
		l1GasPrice = new(big.Int).Mul(big.NewInt(20), big.NewInt(1_000_000_000)) // 20 gwei fallback
	}
	l1DataGas := int64(calldataSize) * 16
	l1Cost := new(big.Int).Mul(big.NewInt(l1DataGas), l1GasPrice)

	return new(big.Int).Add(l2Cost, l1Cost)
}

// getL1GasPrice 获取 Arbitrum L1 BaseFee 估算（用于 L1 数据费计算）
func (ge *GasEstimator) getL1GasPrice(ctx context.Context) (*big.Int, error) {
	// ArbGasInfo 预编译合约地址（Arbitrum 所有链通用）
	arbGasInfoAddr := common.HexToAddress("0x000000000000000000000000000000000000006C")

	// getL1BaseFeeEstimate() 方法的 selector: keccak256("getL1BaseFeeEstimate()")[:4]
	// = 0xf5d6ded7
	callData := []byte{0xf5, 0xd6, 0xde, 0xd7}

	result, err := ge.web3Client.GetClient().CallContract(ctx, ethereum.CallMsg{
		To:   &arbGasInfoAddr,
		Data: callData,
	}, nil)
	if err != nil || len(result) < 32 {
		return nil, fmt.Errorf("getL1BaseFeeEstimate failed: %w", err)
	}

	l1BaseFee := new(big.Int).SetBytes(result[:32])
	return l1BaseFee, nil
}

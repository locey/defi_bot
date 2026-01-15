// Package executor 提供交易执行相关功能
package executor

import (
	"context"
	"fmt"
	"math/big"

	"github.com/defi-bot/backend/pkg/log"
	"github.com/ethereum/go-ethereum/ethclient"
)

// GasPricer Gas 价格管理器
// 提供 Gas 价格获取和动态调整功能
type GasPricer struct {
	client      *ethclient.Client
	maxGasPrice *big.Int // 最大 Gas 价格（Wei）

	// 缓存的 Gas 价格
	lastGasPrice *big.Int
	lastBaseFee  *big.Int
	lastTipCap   *big.Int
}

// NewGasPricer 创建 Gas 价格管理器
func NewGasPricer(client *ethclient.Client, maxGasPriceGwei int64) *GasPricer {
	maxGasPrice := new(big.Int).Mul(
		big.NewInt(maxGasPriceGwei),
		big.NewInt(1e9), // Gwei to Wei
	)

	return &GasPricer{
		client:      client,
		maxGasPrice: maxGasPrice,
	}
}

// GetGasPrice 获取 Gas 价格（Legacy 模式）
// retryCount: 重试次数，每次重试会提升 Gas 价格
func (g *GasPricer) GetGasPrice(ctx context.Context, retryCount int) (*big.Int, error) {
	// 获取建议的 Gas 价格
	gasPrice, err := g.client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取 Gas 价格失败: %w", err)
	}

	// 根据重试次数提升 Gas 价格
	// 每次重试提升 10%
	if retryCount > 0 {
		boost := 100 + (retryCount * 10) // 110%, 120%, 130%...
		gasPrice = new(big.Int).Mul(gasPrice, big.NewInt(int64(boost)))
		gasPrice = new(big.Int).Div(gasPrice, big.NewInt(100))
	}

	// 添加 10% 基础溢价
	premium := new(big.Int).Div(gasPrice, big.NewInt(10))
	gasPrice = new(big.Int).Add(gasPrice, premium)

	// 检查是否超过最大值
	if gasPrice.Cmp(g.maxGasPrice) > 0 {
		log.Warn("Gas 价格 %s Wei 超过最大值 %s Wei，使用最大值",
			gasPrice.String(), g.maxGasPrice.String())
		gasPrice = new(big.Int).Set(g.maxGasPrice)
	}

	g.lastGasPrice = gasPrice
	return gasPrice, nil
}

// GetEIP1559GasPrice 获取 EIP-1559 Gas 参数
// 返回: gasTipCap (priority fee), gasFeeCap (max fee)
func (g *GasPricer) GetEIP1559GasPrice(ctx context.Context, retryCount int) (*big.Int, *big.Int, error) {
	// 获取最新区块头
	header, err := g.client.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("获取区块头失败: %w", err)
	}

	// 检查是否支持 EIP-1559
	if header.BaseFee == nil {
		return nil, nil, fmt.Errorf("该链不支持 EIP-1559")
	}

	baseFee := header.BaseFee

	// 获取建议的 priority fee
	tipCap, err := g.client.SuggestGasTipCap(ctx)
	if err != nil {
		// 使用默认值
		tipCap = big.NewInt(1e9) // 1 Gwei
	}

	// 根据重试次数提升 tip
	if retryCount > 0 {
		boost := 100 + (retryCount * 15) // 115%, 130%, 145%...
		tipCap = new(big.Int).Mul(tipCap, big.NewInt(int64(boost)))
		tipCap = new(big.Int).Div(tipCap, big.NewInt(100))
	}

	// 计算 maxFeePerGas = baseFee * 2 + tipCap
	// 这是业界标准公式，确保即使 baseFee 翻倍也能被打包
	feeCap := new(big.Int).Mul(baseFee, big.NewInt(2))
	feeCap = new(big.Int).Add(feeCap, tipCap)

	// 检查是否超过最大值
	if feeCap.Cmp(g.maxGasPrice) > 0 {
		log.Warn("Max fee %s Wei 超过最大值 %s Wei，使用最大值",
			feeCap.String(), g.maxGasPrice.String())
		feeCap = new(big.Int).Set(g.maxGasPrice)

		// 重新计算 tipCap 以确保 feeCap >= baseFee + tipCap
		if feeCap.Cmp(baseFee) > 0 {
			tipCap = new(big.Int).Sub(feeCap, baseFee)
		} else {
			tipCap = big.NewInt(0)
		}
	}

	g.lastBaseFee = baseFee
	g.lastTipCap = tipCap

	return tipCap, feeCap, nil
}

// EstimateGasCost 估算 Gas 成本
func (g *GasPricer) EstimateGasCost(ctx context.Context, gasLimit uint64) (*big.Int, error) {
	gasPrice, err := g.GetGasPrice(ctx, 0)
	if err != nil {
		return nil, err
	}

	return new(big.Int).Mul(gasPrice, big.NewInt(int64(gasLimit))), nil
}

// GetLastGasPrice 获取上次使用的 Gas 价格
func (g *GasPricer) GetLastGasPrice() *big.Int {
	if g.lastGasPrice == nil {
		return big.NewInt(0)
	}
	return new(big.Int).Set(g.lastGasPrice)
}

// GetLastBaseFee 获取上次的 BaseFee
func (g *GasPricer) GetLastBaseFee() *big.Int {
	if g.lastBaseFee == nil {
		return big.NewInt(0)
	}
	return new(big.Int).Set(g.lastBaseFee)
}

// SetMaxGasPrice 设置最大 Gas 价格
func (g *GasPricer) SetMaxGasPrice(maxGasPriceGwei int64) {
	g.maxGasPrice = new(big.Int).Mul(
		big.NewInt(maxGasPriceGwei),
		big.NewInt(1e9),
	)
}

// IsGasReasonable 检查当前 Gas 价格是否合理
func (g *GasPricer) IsGasReasonable(ctx context.Context, threshold *big.Int) (bool, *big.Int, error) {
	gasPrice, err := g.client.SuggestGasPrice(ctx)
	if err != nil {
		return false, nil, err
	}

	return gasPrice.Cmp(threshold) <= 0, gasPrice, nil
}

// WeiToGwei 将 Wei 转换为 Gwei
func WeiToGwei(wei *big.Int) float64 {
	if wei == nil {
		return 0
	}
	gwei := new(big.Float).SetInt(wei)
	gwei.Quo(gwei, big.NewFloat(1e9))
	result, _ := gwei.Float64()
	return result
}

// GweiToWei 将 Gwei 转换为 Wei
func GweiToWei(gwei float64) *big.Int {
	wei := new(big.Float).SetFloat64(gwei)
	wei.Mul(wei, big.NewFloat(1e9))
	result, _ := wei.Int(nil)
	return result
}

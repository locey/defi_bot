package dex

import (
	"fmt"
	"math/big"
	"time"

	"github.com/defi-bot/backend/pkg/web3"
)

// UniswapV2Protocol Uniswap V2 协议适配器
// 也兼容 SushiSwap, PancakeSwap V2 等所有 V2 分叉
type UniswapV2Protocol struct {
	web3Client *web3.Client
}

// NewUniswapV2Protocol 创建 Uniswap V2 协议适配器
func NewUniswapV2Protocol(web3Client *web3.Client) *UniswapV2Protocol {
	return &UniswapV2Protocol{
		web3Client: web3Client,
	}
}

// GetProtocolName 获取协议名称
func (p *UniswapV2Protocol) GetProtocolName() string {
	return "uniswap_v2"
}

// GetPairAddress 获取交易对地址
func (p *UniswapV2Protocol) GetPairAddress(factory, token0, token1 string, params ...interface{}) (string, error) {
	pairAddress, err := p.web3Client.GetPairFromFactory(factory, token0, token1)
	if err != nil {
		return "", fmt.Errorf("获取V2交易对地址失败: %w", err)
	}
	return pairAddress, nil
}

// GetPrice 获取价格信息
func (p *UniswapV2Protocol) GetPrice(pairAddress string) (*PriceInfo, error) {
	// 获取储备量
	reserves, err := p.web3Client.GetPairReserves(pairAddress)
	if err != nil {
		return nil, fmt.Errorf("获取储备量失败: %w", err)
	}

	// 检查流动性
	if reserves.Reserve0.Sign() == 0 || reserves.Reserve1.Sign() == 0 {
		return nil, fmt.Errorf("无流动性")
	}

	// 计算价格
	r0 := new(big.Float).SetInt(reserves.Reserve0)
	r1 := new(big.Float).SetInt(reserves.Reserve1)

	price := new(big.Float).Quo(r1, r0)        // token1/token0
	inversePrice := new(big.Float).Quo(r0, r1) // token0/token1

	// 计算流动性 (k = reserve0 * reserve1)
	liquidity := new(big.Int).Mul(reserves.Reserve0, reserves.Reserve1)
	sqrtLiquidity := new(big.Int).Sqrt(liquidity)

	return &PriceInfo{
		Price:        price,
		InversePrice: inversePrice,
		Reserve0:     reserves.Reserve0,
		Reserve1:     reserves.Reserve1,
		Liquidity:    sqrtLiquidity,
		Timestamp:    time.Now(),
	}, nil
}

// GetLiquidity 获取流动性信息
func (p *UniswapV2Protocol) GetLiquidity(pairAddress string) (*LiquidityInfo, error) {
	reserves, err := p.web3Client.GetPairReserves(pairAddress)
	if err != nil {
		return nil, err
	}

	liquidity := new(big.Int).Mul(reserves.Reserve0, reserves.Reserve1)
	sqrtLiquidity := new(big.Int).Sqrt(liquidity)

	return &LiquidityInfo{
		Liquidity: sqrtLiquidity,
		Reserve0:  reserves.Reserve0,
		Reserve1:  reserves.Reserve1,
	}, nil
}

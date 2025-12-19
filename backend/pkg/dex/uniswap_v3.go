package dex

import (
	"fmt"
	"math/big"
	"time"

	"github.com/defi-bot/backend/pkg/web3"
)

// UniswapV3Protocol Uniswap V3 协议适配器
type UniswapV3Protocol struct {
	web3Client *web3.Client
}

// NewUniswapV3Protocol 创建 Uniswap V3 协议适配器
func NewUniswapV3Protocol(web3Client *web3.Client) *UniswapV3Protocol {
	return &UniswapV3Protocol{
		web3Client: web3Client,
	}
}

// GetProtocolName 获取协议名称
func (p *UniswapV3Protocol) GetProtocolName() string {
	return "uniswap_v3"
}

// GetPairAddress 获取 Pool 地址（V3使用Pool而不是Pair）
// params[0] 应该是 fee (uint32): 500, 3000, 10000
func (p *UniswapV3Protocol) GetPairAddress(factory, token0, token1 string, params ...interface{}) (string, error) {
	if len(params) == 0 {
		return "", fmt.Errorf("V3需要指定fee参数")
	}

	fee, ok := params[0].(uint32)
	if !ok {
		// 尝试从int转换
		if feeInt, ok := params[0].(int); ok {
			fee = uint32(feeInt)
		} else {
			return "", fmt.Errorf("fee参数类型错误，应该是uint32")
		}
	}

	poolAddress, err := p.web3Client.GetV3Pool(factory, token0, token1, fee)
	if err != nil {
		return "", fmt.Errorf("获取V3 Pool地址失败: %w", err)
	}

	return poolAddress, nil
}

// GetPrice 获取 V3 Pool 的价格信息
func (p *UniswapV3Protocol) GetPrice(pairAddress string) (*PriceInfo, error) {
	// 获取 slot0（包含当前价格）
	slot0, err := p.web3Client.GetV3PoolSlot0(pairAddress)
	if err != nil {
		return nil, fmt.Errorf("获取slot0失败: %w", err)
	}

	// 获取流动性
	liquidity, err := p.web3Client.GetV3PoolLiquidity(pairAddress)
	if err != nil {
		return nil, fmt.Errorf("获取流动性失败: %w", err)
	}

	// 检查价格和流动性
	if slot0.SqrtPriceX96 == nil || slot0.SqrtPriceX96.Sign() == 0 {
		return nil, fmt.Errorf("无效的价格")
	}

	if liquidity.Sign() == 0 {
		return nil, fmt.Errorf("无流动性")
	}

	// 转换 sqrtPriceX96 为实际价格
	price := p.sqrtPriceX96ToPrice(slot0.SqrtPriceX96)
	inversePrice := new(big.Float).Quo(big.NewFloat(1.0), price)

	// V3 不直接提供储备量，计算虚拟储备量
	reserve0, reserve1 := p.CalculateVirtualReserves(liquidity, slot0.SqrtPriceX96)

	return &PriceInfo{
		Price:        price,
		InversePrice: inversePrice,
		Reserve0:     reserve0,
		Reserve1:     reserve1,
		Liquidity:    liquidity,
		
		// === ✅ V3 专用数据 ===
		SqrtPriceX96:     slot0.SqrtPriceX96,
		Tick:             slot0.Tick,
		FeeGrowthGlobal0: big.NewInt(0), // TODO: 从合约获取
		FeeGrowthGlobal1: big.NewInt(0), // TODO: 从合约获取
		
		Timestamp:    time.Now(),
	}, nil
}

// GetLiquidity 获取详细的流动性信息
func (p *UniswapV3Protocol) GetLiquidity(pairAddress string) (*LiquidityInfo, error) {
	slot0, err := p.web3Client.GetV3PoolSlot0(pairAddress)
	if err != nil {
		return nil, err
	}

	liquidity, err := p.web3Client.GetV3PoolLiquidity(pairAddress)
	if err != nil {
		return nil, err
	}

	return &LiquidityInfo{
		Liquidity:    liquidity,
		Tick:         slot0.Tick,
		SqrtPriceX96: slot0.SqrtPriceX96,
	}, nil
}

// sqrtPriceX96ToPrice 将 V3 的 sqrtPriceX96 转换为标准价格
// 公式: price = (sqrtPriceX96 / 2^96)^2
func (p *UniswapV3Protocol) sqrtPriceX96ToPrice(sqrtPriceX96 *big.Int) *big.Float {
	// Q96 = 2^96
	q96 := new(big.Int).Exp(big.NewInt(2), big.NewInt(96), nil)

	// sqrtPrice = sqrtPriceX96 / 2^96
	sqrtPrice := new(big.Float).Quo(
		new(big.Float).SetInt(sqrtPriceX96),
		new(big.Float).SetInt(q96),
	)

	// price = sqrtPrice^2
	price := new(big.Float).Mul(sqrtPrice, sqrtPrice)

	return price
}

// CalculateVirtualReserves 根据流动性和价格计算虚拟储备量
// 这是一个估算，用于与V2保持接口一致
func (p *UniswapV3Protocol) CalculateVirtualReserves(liquidity *big.Int, sqrtPriceX96 *big.Int) (*big.Int, *big.Int) {
	// 简化计算：
	// reserve0 ≈ liquidity / sqrtPrice
	// reserve1 ≈ liquidity * sqrtPrice

	// 这是近似值，实际V3的流动性分布更复杂
	// 完整实现需要考虑tick范围

	q96 := new(big.Int).Exp(big.NewInt(2), big.NewInt(96), nil)
	sqrtPrice := new(big.Float).Quo(
		new(big.Float).SetInt(sqrtPriceX96),
		new(big.Float).SetInt(q96),
	)

	liquidityFloat := new(big.Float).SetInt(liquidity)

	// reserve0 = liquidity / sqrtPrice
	reserve0Float := new(big.Float).Quo(liquidityFloat, sqrtPrice)
	reserve0, _ := reserve0Float.Int(nil)

	// reserve1 = liquidity * sqrtPrice
	reserve1Float := new(big.Float).Mul(liquidityFloat, sqrtPrice)
	reserve1, _ := reserve1Float.Int(nil)

	return reserve0, reserve1
}

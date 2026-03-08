// Package cexdex 提供 CEX-DEX 套利功能
package cexdex

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/defi-bot/backend/pkg/log"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

// DEXQuoterConfig DEX 报价器配置
type DEXQuoterConfig struct {
	// Uniswap V3 Quoter 合约地址
	QuoterV2Address common.Address
	// 常用交易对配置
	Pairs []QuoterPair
	// 查询超时
	Timeout time.Duration
}

// QuoterPair 交易对配置
type QuoterPair struct {
	Symbol     string         // CEX 符号 (如 "ETHUSDT")
	TokenIn    common.Address // 输入代币地址
	TokenOut   common.Address // 输出代币地址
	FeeTier    *big.Int       // 手续费层级 (500, 3000, 10000)
	DecimalsIn int            // 输入代币精度
	DecimalsOut int           // 输出代币精度
}

// DEXQuoter DEX 实时报价器
type DEXQuoter struct {
	config    *DEXQuoterConfig
	client    *ethclient.Client
	quoterABI abi.ABI
	prices    map[string]float64 // symbol -> price
	pricesMu  sync.RWMutex
	running   bool
	runningMu sync.RWMutex
	cancelFn  context.CancelFunc
}

// Uniswap V3 QuoterV2 ABI (只需要 quoteExactInputSingle)
const quoterV2ABI = `[{
	"inputs": [{
		"components": [
			{"name": "tokenIn", "type": "address"},
			{"name": "tokenOut", "type": "address"},
			{"name": "amountIn", "type": "uint256"},
			{"name": "fee", "type": "uint24"},
			{"name": "sqrtPriceLimitX96", "type": "uint160"}
		],
		"name": "params",
		"type": "tuple"
	}],
	"name": "quoteExactInputSingle",
	"outputs": [
		{"name": "amountOut", "type": "uint256"},
		{"name": "sqrtPriceX96After", "type": "uint160"},
		{"name": "initializedTicksCrossed", "type": "uint32"},
		{"name": "gasEstimate", "type": "uint256"}
	],
	"stateMutability": "nonpayable",
	"type": "function"
}]`

// NewDEXQuoter 创建 DEX 报价器
func NewDEXQuoter(client *ethclient.Client, config *DEXQuoterConfig) (*DEXQuoter, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}

	parsedABI, err := abi.JSON(strings.NewReader(quoterV2ABI))
	if err != nil {
		return nil, fmt.Errorf("解析 QuoterV2 ABI 失败: %w", err)
	}

	return &DEXQuoter{
		config:    config,
		client:    client,
		quoterABI: parsedABI,
		prices:    make(map[string]float64),
	}, nil
}

// Start 启动实时报价
func (q *DEXQuoter) Start(ctx context.Context) error {
	q.runningMu.Lock()
	if q.running {
		q.runningMu.Unlock()
		return nil
	}
	q.running = true
	q.runningMu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	q.cancelFn = cancel

	// 立即查询一次
	q.updateAllPrices(ctx)

	// 启动定时查询循环
	go q.queryLoop(ctx)

	log.Info("DEX 报价器已启动，监控 %d 个交易对", len(q.config.Pairs))
	return nil
}

// Stop 停止报价
func (q *DEXQuoter) Stop() {
	q.runningMu.Lock()
	defer q.runningMu.Unlock()

	if !q.running {
		return
	}
	q.running = false
	if q.cancelFn != nil {
		q.cancelFn()
	}
	log.Info("DEX 报价器已停止")
}

// queryLoop 查询循环
func (q *DEXQuoter) queryLoop(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second) // 每 3 秒更新一次
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			q.updateAllPrices(ctx)
		}
	}
}

// updateAllPrices 更新所有价格
func (q *DEXQuoter) updateAllPrices(ctx context.Context) {
	var wg sync.WaitGroup
	for _, pair := range q.config.Pairs {
		wg.Add(1)
		go func(p QuoterPair) {
			defer wg.Done()
			price, err := q.getPrice(ctx, p)
			if err != nil {
				log.Main().Debug().Err(err).Str("symbol", p.Symbol).Msg("获取 DEX 价格失败")
				return
			}
			q.pricesMu.Lock()
			q.prices[p.Symbol] = price
			q.pricesMu.Unlock()
		}(pair)
	}
	wg.Wait()
}

// getPrice 获取单个交易对价格
func (q *DEXQuoter) getPrice(ctx context.Context, pair QuoterPair) (float64, error) {
	// 使用 1 个单位的输入代币查询输出
	amountIn := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(pair.DecimalsIn)), nil)

	// 构造调用数据
	callData, err := q.quoterABI.Pack("quoteExactInputSingle", struct {
		TokenIn           common.Address
		TokenOut          common.Address
		AmountIn          *big.Int
		Fee               *big.Int
		SqrtPriceLimitX96 *big.Int
	}{
		TokenIn:           pair.TokenIn,
		TokenOut:          pair.TokenOut,
		AmountIn:          amountIn,
		Fee:               pair.FeeTier,
		SqrtPriceLimitX96: big.NewInt(0),
	})
	if err != nil {
		return 0, fmt.Errorf("打包调用数据失败: %w", err)
	}

	// 调用合约
	callCtx, cancel := context.WithTimeout(ctx, q.config.Timeout)
	defer cancel()

	result, err := q.client.CallContract(callCtx, ethereum.CallMsg{
		To:   &q.config.QuoterV2Address,
		Data: callData,
	}, nil)
	if err != nil {
		return 0, fmt.Errorf("调用 Quoter 失败: %w", err)
	}

	// 解析结果
	outputs, err := q.quoterABI.Unpack("quoteExactInputSingle", result)
	if err != nil {
		return 0, fmt.Errorf("解析结果失败: %w", err)
	}

	amountOut := outputs[0].(*big.Int)

	// 计算价格
	// price = amountOut / 10^decimalsOut
	divisor := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(pair.DecimalsOut)), nil))
	price := new(big.Float).SetInt(amountOut)
	price.Quo(price, divisor)

	result64, _ := price.Float64()
	return result64, nil
}

// GetPrice 获取指定符号的价格
func (q *DEXQuoter) GetPrice(symbol string) (float64, error) {
	q.pricesMu.RLock()
	defer q.pricesMu.RUnlock()

	price, ok := q.prices[symbol]
	if !ok {
		return 0, fmt.Errorf("未找到 %s 的价格", symbol)
	}
	return price, nil
}

// GetAllPrices 获取所有价格
func (q *DEXQuoter) GetAllPrices() map[string]float64 {
	q.pricesMu.RLock()
	defer q.pricesMu.RUnlock()

	result := make(map[string]float64)
	for k, v := range q.prices {
		result[k] = v
	}
	return result
}

// GetPoolAddress 获取池子地址（用于检测器接口）
func (q *DEXQuoter) GetPoolAddress(symbol string) common.Address {
	// 对于 Quoter 模式，我们不需要池子地址
	return common.Address{}
}

// GetRouterAddress 获取路由地址
func (q *DEXQuoter) GetRouterAddress(symbol string) common.Address {
	// 返回 Quoter 地址
	return q.config.QuoterV2Address
}

// GetPoolFeeTier 获取池子的 fee tier（DEXQuoter 默认返回 V3 0.05%）
func (q *DEXQuoter) GetPoolFeeTier(symbol string) uint32 {
	return 500 // 默认 V3 0.05%
}

// IsRunning 是否正在运行
func (q *DEXQuoter) IsRunning() bool {
	q.runningMu.RLock()
	defer q.runningMu.RUnlock()
	return q.running
}

// ============ 预定义的链配置 ============

// ArbitrumQuoterConfig 返回 Arbitrum 的 Quoter 配置
func ArbitrumQuoterConfig() *DEXQuoterConfig {
	return &DEXQuoterConfig{
		QuoterV2Address: common.HexToAddress("0x61fFE014bA17989E743c5F6cB21bF9697530B21e"),
		Timeout:         10 * time.Second,
		Pairs: []QuoterPair{
			{
				Symbol:      "ETHUSDT",
				TokenIn:     common.HexToAddress("0x82aF49447D8a07e3bd95BD0d56f35241523fBab1"), // WETH
				TokenOut:    common.HexToAddress("0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9"), // USDT
				FeeTier:     big.NewInt(500),
				DecimalsIn:  18,
				DecimalsOut: 6,
			},
			{
				Symbol:      "BTCUSDT",
				TokenIn:     common.HexToAddress("0x2f2a2543B76A4166549F7aaB2e75Bef0aefC5B0f"), // WBTC
				TokenOut:    common.HexToAddress("0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9"), // USDT
				FeeTier:     big.NewInt(500),
				DecimalsIn:  8,
				DecimalsOut: 6,
			},
			{
				Symbol:      "ARBUSDT",
				TokenIn:     common.HexToAddress("0x912CE59144191C1204E64559FE8253a0e49E6548"), // ARB
				TokenOut:    common.HexToAddress("0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9"), // USDT
				FeeTier:     big.NewInt(3000),
				DecimalsIn:  18,
				DecimalsOut: 6,
			},
			{
				Symbol:      "LINKUSDT",
				TokenIn:     common.HexToAddress("0xf97f4df75117a78c1A5a0DBb814Af92458539FB4"), // LINK
				TokenOut:    common.HexToAddress("0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9"), // USDT
				FeeTier:     big.NewInt(3000),
				DecimalsIn:  18,
				DecimalsOut: 6,
			},
			{
				Symbol:      "UNIUSDT",
				TokenIn:     common.HexToAddress("0xFa7F8980b0f1E64A2062791cc3b0871572f1F7f0"), // UNI
				TokenOut:    common.HexToAddress("0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9"), // USDT
				FeeTier:     big.NewInt(3000),
				DecimalsIn:  18,
				DecimalsOut: 6,
			},
			{
				Symbol:      "GMXUSDT",
				TokenIn:     common.HexToAddress("0xfc5A1A6EB076a2C7aD06eD22C90d7E710E35ad0a"), // GMX
				TokenOut:    common.HexToAddress("0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9"), // USDT
				FeeTier:     big.NewInt(3000),
				DecimalsIn:  18,
				DecimalsOut: 6,
			},
		},
	}
}

// EthereumQuoterConfig 返回以太坊主网的 Quoter 配置
func EthereumQuoterConfig() *DEXQuoterConfig {
	return &DEXQuoterConfig{
		QuoterV2Address: common.HexToAddress("0x61fFE014bA17989E743c5F6cB21bF9697530B21e"),
		Timeout:         15 * time.Second,
		Pairs: []QuoterPair{
			{
				Symbol:      "ETHUSDT",
				TokenIn:     common.HexToAddress("0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2"), // WETH
				TokenOut:    common.HexToAddress("0xdAC17F958D2ee523a2206206994597C13D831ec7"), // USDT
				FeeTier:     big.NewInt(500),
				DecimalsIn:  18,
				DecimalsOut: 6,
			},
			{
				Symbol:      "BTCUSDT",
				TokenIn:     common.HexToAddress("0x2260FAC5E5542a773Aa44fBCfeDf7C193bc2C599"), // WBTC
				TokenOut:    common.HexToAddress("0xdAC17F958D2ee523a2206206994597C13D831ec7"), // USDT
				FeeTier:     big.NewInt(500),
				DecimalsIn:  8,
				DecimalsOut: 6,
			},
		},
	}
}

// OptimismQuoterConfig 返回 Optimism 的 Quoter 配置
func OptimismQuoterConfig() *DEXQuoterConfig {
	return &DEXQuoterConfig{
		QuoterV2Address: common.HexToAddress("0x61fFE014bA17989E743c5F6cB21bF9697530B21e"),
		Timeout:         10 * time.Second,
		Pairs: []QuoterPair{
			{
				Symbol:      "ETHUSDT",
				TokenIn:     common.HexToAddress("0x4200000000000000000000000000000000000006"), // WETH
				TokenOut:    common.HexToAddress("0x94b008aA00579c1307B0EF2c499aD98a8ce58e58"), // USDT
				FeeTier:     big.NewInt(500),
				DecimalsIn:  18,
				DecimalsOut: 6,
			},
		},
	}
}

// GetQuoterConfigByChainID 根据链 ID 获取配置
func GetQuoterConfigByChainID(chainID int64) *DEXQuoterConfig {
	switch chainID {
	case 1:
		return EthereumQuoterConfig()
	case 42161:
		return ArbitrumQuoterConfig()
	case 10:
		return OptimismQuoterConfig()
	default:
		return nil
	}
}

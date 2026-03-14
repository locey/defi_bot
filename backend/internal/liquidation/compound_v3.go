// internal/liquidation/compound_v3.go
// Compound V3 (Comet) 清算监控 — 扫描可清算仓位并执行 absorb + buyCollateral
//
// Compound V3 清算模型与 Aave 不同：
//   - 两步式：absorb()（协议没收仓位）+ buyCollateral()（折价买抵押品）
//   - 全量清算：不能部分清算，absorb 会没收全部抵押品
//   - 折扣定价：discount = storeFrontPriceFactor * (1 - liquidationFactor)，通常 4-7%
//   - 无需偿债：absorb 由协议从储备金偿债，liquidator 只需在 buyCollateral 时提供 base asset
//
// Arbitrum 上的 Comet 市场：
//   USDC:   0x9c4ec768c28520B50860ea7a15bd7213a9fF58bf
//   USDC.e: 0xA5EDBDD9646f8dFF606d7448e414884C7d905dCA
//   USDT:   0xd98Be00b5D27fc98112BdE293e487f8D4cA57d07
//   WETH:   0x6f7D514bbD4aFf3BcD1140B7344b32f063dEe486
package liquidation

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/defi-bot/backend/pkg/log"
	"github.com/defi-bot/backend/pkg/web3"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// CompoundV3Market Compound V3 市场配置
type CompoundV3Market struct {
	Name       string         `json:"name"`        // e.g. "USDC", "USDT", "WETH"
	Comet      common.Address `json:"comet"`        // Comet proxy 地址
	BaseToken  common.Address `json:"base_token"`   // base asset 地址
	BaseSymbol string         `json:"base_symbol"`
	Decimals   uint8          `json:"decimals"`
}

// CompoundV3Position 用户在某个 Comet 市场的仓位
type CompoundV3Position struct {
	User          common.Address
	Market        *CompoundV3Market
	BorrowBalance *big.Int // base asset 借款
	IsLiquidatable bool
	LastChecked   time.Time
}

// CompoundV3Opportunity Compound V3 清算机会
type CompoundV3Opportunity struct {
	ID              string
	Market          *CompoundV3Market
	User            common.Address
	CollateralAsset common.Address
	CollateralSymbol string
	BorrowBalance   *big.Int       // base 借款
	BaseAmount      *big.Int       // 用于 buyCollateral 的 base 数量
	ExpectedProfit  *big.Int       // 预期利润（base asset 计）
	SwapRouter      common.Address
	SwapFeeTier     uint32
	Timestamp       time.Time
	ValidUntil      time.Time
}

// CompoundV3Monitor Compound V3 清算监控器
type CompoundV3Monitor struct {
	web3Client *web3.Client
	markets    []*CompoundV3Market
	cometABI   abi.ABI

	// 已知借款人（从 Supply/Withdraw 事件扫描）
	borrowers   map[common.Address]map[common.Address]bool // market → set of users
	borrowersMu sync.RWMutex

	// 配置
	scanInterval time.Duration
	dryRun       bool

	stopCh chan struct{}
}

// DefaultArbitrumMarkets 返回 Arbitrum 上的 Compound V3 市场
func DefaultArbitrumMarkets() []*CompoundV3Market {
	return []*CompoundV3Market{
		{
			Name:       "USDC",
			Comet:      common.HexToAddress("0x9c4ec768c28520B50860ea7a15bd7213a9fF58bf"),
			BaseToken:  common.HexToAddress("0xaf88d065e77c8cC2239327C5EDb3A432268e5831"),
			BaseSymbol: "USDC",
			Decimals:   6,
		},
		{
			Name:       "USDC.e",
			Comet:      common.HexToAddress("0xA5EDBDD9646f8dFF606d7448e414884C7d905dCA"),
			BaseToken:  common.HexToAddress("0xFF970A61A04b1cA14834A43f5dE4533eBDDB5CC8"),
			BaseSymbol: "USDCe",
			Decimals:   6,
		},
		{
			Name:       "USDT",
			Comet:      common.HexToAddress("0xd98Be00b5D27fc98112BdE293e487f8D4cA57d07"),
			BaseToken:  common.HexToAddress("0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9"),
			BaseSymbol: "USDT",
			Decimals:   6,
		},
		{
			Name:       "WETH",
			Comet:      common.HexToAddress("0x6f7D514bbD4aFf3BcD1140B7344b32f063dEe486"),
			BaseToken:  common.HexToAddress("0x82aF49447D8a07e3bd95BD0d56f35241523fBab1"),
			BaseSymbol: "WETH",
			Decimals:   18,
		},
	}
}

// CompoundV3CometABI 最小化 ABI
const CompoundV3CometABI = `[
    {
        "inputs": [{"internalType":"address","name":"account","type":"address"}],
        "name": "isLiquidatable",
        "outputs": [{"internalType":"bool","name":"","type":"bool"}],
        "stateMutability": "view",
        "type": "function"
    },
    {
        "inputs": [{"internalType":"address","name":"account","type":"address"}],
        "name": "borrowBalanceOf",
        "outputs": [{"internalType":"uint256","name":"","type":"uint256"}],
        "stateMutability": "view",
        "type": "function"
    },
    {
        "inputs": [
            {"internalType":"address","name":"account","type":"address"},
            {"internalType":"address","name":"asset","type":"address"}
        ],
        "name": "collateralBalanceOf",
        "outputs": [{"internalType":"uint128","name":"","type":"uint128"}],
        "stateMutability": "view",
        "type": "function"
    },
    {
        "inputs": [
            {"internalType":"address","name":"asset","type":"address"},
            {"internalType":"uint256","name":"baseAmount","type":"uint256"}
        ],
        "name": "quoteCollateral",
        "outputs": [{"internalType":"uint256","name":"","type":"uint256"}],
        "stateMutability": "view",
        "type": "function"
    },
    {
        "inputs": [],
        "name": "numAssets",
        "outputs": [{"internalType":"uint8","name":"","type":"uint8"}],
        "stateMutability": "view",
        "type": "function"
    },
    {
        "inputs": [{"internalType":"uint8","name":"i","type":"uint8"}],
        "name": "getAssetInfo",
        "outputs": [
            {
                "components": [
                    {"internalType":"uint8","name":"offset","type":"uint8"},
                    {"internalType":"address","name":"asset","type":"address"},
                    {"internalType":"address","name":"priceFeed","type":"address"},
                    {"internalType":"uint64","name":"scale","type":"uint64"},
                    {"internalType":"uint64","name":"borrowCollateralFactor","type":"uint64"},
                    {"internalType":"uint64","name":"liquidateCollateralFactor","type":"uint64"},
                    {"internalType":"uint64","name":"liquidationFactor","type":"uint64"},
                    {"internalType":"uint128","name":"supplyCap","type":"uint128"}
                ],
                "internalType":"struct CometCore.AssetInfo",
                "name":"",
                "type":"tuple"
            }
        ],
        "stateMutability": "view",
        "type": "function"
    },
    {
        "inputs": [],
        "name": "baseToken",
        "outputs": [{"internalType":"address","name":"","type":"address"}],
        "stateMutability": "view",
        "type": "function"
    },
    {
        "inputs": [],
        "name": "getReserves",
        "outputs": [{"internalType":"int256","name":"","type":"int256"}],
        "stateMutability": "view",
        "type": "function"
    },
    {
        "inputs": [],
        "name": "targetReserves",
        "outputs": [{"internalType":"uint256","name":"","type":"uint256"}],
        "stateMutability": "view",
        "type": "function"
    }
]`

// NewCompoundV3Monitor 创建 Compound V3 清算监控器
func NewCompoundV3Monitor(
	web3Client *web3.Client,
	markets []*CompoundV3Market,
	scanInterval time.Duration,
) (*CompoundV3Monitor, error) {
	parsedABI, err := abi.JSON(strings.NewReader(CompoundV3CometABI))
	if err != nil {
		return nil, fmt.Errorf("parse comet ABI: %w", err)
	}

	borrowers := make(map[common.Address]map[common.Address]bool)
	for _, m := range markets {
		borrowers[m.Comet] = make(map[common.Address]bool)
	}

	return &CompoundV3Monitor{
		web3Client:   web3Client,
		markets:      markets,
		cometABI:     parsedABI,
		borrowers:    borrowers,
		scanInterval: scanInterval,
		stopCh:       make(chan struct{}),
	}, nil
}

// Start 启动监控
func (m *CompoundV3Monitor) Start(ctx context.Context) error {
	log.Strategy().Info().
		Int("markets", len(m.markets)).
		Msg("Compound V3：启动清算监控")

	// 初始扫描：发现借款人
	m.discoverBorrowers(ctx)

	go m.runLoop(ctx)
	return nil
}

// Stop 停止
func (m *CompoundV3Monitor) Stop() {
	close(m.stopCh)
}

// runLoop 主循环
func (m *CompoundV3Monitor) runLoop(ctx context.Context) {
	ticker := time.NewTicker(m.scanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.scanCycle(ctx)
		}
	}
}

// discoverBorrowers 通过链上查询发现有借款的用户
// Compound V3 没有像 Aave 那样的 Borrow 事件，需要通过 Supply 事件反推
// 简化方案：直接查询已知地址的 borrowBalanceOf
func (m *CompoundV3Monitor) discoverBorrowers(ctx context.Context) {
	client := m.web3Client.GetClient()

	for _, market := range m.markets {
		// 扫描 WithdrawCollateral 事件来发现借款人
		// topic0 = keccak256("WithdrawCollateral(address,address,address,uint256)")
		// 但更实际的方法是扫描 Supply 事件的 from 参数
		// 这里用一个更高效的方式：扫描最近区块的所有交互地址

		// 查询最近 100000 个区块的 Transfer 事件到 Comet 合约
		latestBlock, err := client.BlockNumber(ctx)
		if err != nil {
			log.Strategy().Warn().Err(err).
				Str("market", market.Name).
				Msg("Compound V3：获取最新区块失败")
			continue
		}

		fromBlock := latestBlock - 100000
		if fromBlock > latestBlock {
			fromBlock = 0
		}

		// 查询 Withdraw 事件来发现借款人（Supply 只说明存款，不代表借款）
		// Compound V3 Withdraw 事件: Withdraw(address indexed src, address indexed to, uint256 amount)
		// 只有真正借款的用户才会有 Withdraw（borrow 在 V3 中体现为 withdraw base asset）
		// topic0 = keccak256("Withdraw(address,address,uint256)")
		withdrawTopic := common.HexToHash("0x9b1bfa7fa9ee420a16e124f794c35ac9f90472acc99140eb2f6447c714cad8eb")

		// 分批查询避免 RPC 限制（QuickNode 限制 10K blocks）
		batchSize := uint64(9000)
		m.borrowersMu.Lock()
		for start := fromBlock; start <= latestBlock; start += batchSize {
			end := start + batchSize - 1
			if end > latestBlock {
				end = latestBlock
			}

			query := ethereum.FilterQuery{
				FromBlock: new(big.Int).SetUint64(start),
				ToBlock:   new(big.Int).SetUint64(end),
				Addresses: []common.Address{market.Comet},
				Topics:    [][]common.Hash{{withdrawTopic}},
			}

			batchLogs, err := client.FilterLogs(ctx, query)
			if err != nil {
				log.Strategy().Debug().Err(err).
					Str("market", market.Name).
					Uint64("from", start).
					Uint64("to", end).
					Msg("Compound V3：扫描 Withdraw 事件批次失败")
				continue
			}

			for _, lg := range batchLogs {
				if len(lg.Topics) >= 3 {
					user := common.BytesToAddress(lg.Topics[2].Bytes())
					m.borrowers[market.Comet][user] = true
				}
			}
		}
		count := len(m.borrowers[market.Comet])
		m.borrowersMu.Unlock()

		log.Strategy().Info().
			Str("market", market.Name).
			Int("borrowers", count).
			Uint64("from_block", fromBlock).
			Msg("Compound V3：借款人扫描完成")
	}
}

// scanCycle 扫描一轮所有市场
func (m *CompoundV3Monitor) scanCycle(ctx context.Context) {
	for _, market := range m.markets {
		m.scanMarket(ctx, market)
	}
}

// scanMarket 扫描单个市场的可清算仓位
func (m *CompoundV3Monitor) scanMarket(ctx context.Context, market *CompoundV3Market) {
	m.borrowersMu.RLock()
	users := make([]common.Address, 0, len(m.borrowers[market.Comet]))
	for user := range m.borrowers[market.Comet] {
		users = append(users, user)
	}
	m.borrowersMu.RUnlock()

	if len(users) == 0 {
		return
	}

	// 批量查询 isLiquidatable（使用 Multicall）
	liquidatable := m.batchCheckLiquidatable(ctx, market, users)

	if len(liquidatable) > 0 {
		log.Strategy().Info().
			Str("market", market.Name).
			Int("liquidatable", len(liquidatable)).
			Int("total_users", len(users)).
			Msg("Compound V3：发现可清算仓位！")

		for _, user := range liquidatable {
			m.evaluateOpportunity(ctx, market, user)
		}
	}
}

// batchCheckLiquidatable 批量检查用户是否可清算
func (m *CompoundV3Monitor) batchCheckLiquidatable(
	ctx context.Context,
	market *CompoundV3Market,
	users []common.Address,
) []common.Address {
	client := m.web3Client.GetClient()
	var liquidatable []common.Address

	for _, user := range users {
		callData, err := m.cometABI.Pack("isLiquidatable", user)
		if err != nil {
			continue
		}

		result, err := client.CallContract(ctx, ethereum.CallMsg{
			To:   &market.Comet,
			Data: callData,
		}, nil)
		if err != nil {
			continue
		}

		output, err := m.cometABI.Unpack("isLiquidatable", result)
		if err != nil || len(output) == 0 {
			continue
		}

		if isLiq, ok := output[0].(bool); ok && isLiq {
			liquidatable = append(liquidatable, user)
		}
	}

	return liquidatable
}

// evaluateOpportunity 评估清算机会的利润
func (m *CompoundV3Monitor) evaluateOpportunity(ctx context.Context, market *CompoundV3Market, user common.Address) {
	client := m.web3Client.GetClient()

	// 查询借款余额
	callData, _ := m.cometABI.Pack("borrowBalanceOf", user)
	result, err := client.CallContract(ctx, ethereum.CallMsg{
		To:   &market.Comet,
		Data: callData,
	}, nil)
	if err != nil {
		return
	}

	output, err := m.cometABI.Unpack("borrowBalanceOf", result)
	if err != nil || len(output) == 0 {
		return
	}

	borrowBalance, ok := output[0].(*big.Int)
	if !ok || borrowBalance.Sign() <= 0 {
		return
	}

	// 计算人类可读数值（仅用于日志显示）
	// 注意：不能用 Int64()，大额借款会 overflow panic
	bf := new(big.Float).SetInt(borrowBalance)
	divisor := new(big.Float).SetFloat64(math.Pow10(int(market.Decimals)))
	borrowHuman, _ := new(big.Float).Quo(bf, divisor).Float64()

	// 查询各抵押品余额
	numAssetsData, _ := m.cometABI.Pack("numAssets")
	numResult, err := client.CallContract(ctx, ethereum.CallMsg{
		To:   &market.Comet,
		Data: numAssetsData,
	}, nil)
	if err != nil {
		return
	}

	numOutput, _ := m.cometABI.Unpack("numAssets", numResult)
	if len(numOutput) == 0 {
		return
	}
	numAssets, _ := numOutput[0].(uint8)
	if numAssets > 50 {
		numAssets = 50 // 安全上限，Compound V3 通常 < 10 个抵押品
	}

	for i := uint8(0); i < numAssets; i++ {
		assetData, _ := m.cometABI.Pack("getAssetInfo", i)
		assetResult, err := client.CallContract(ctx, ethereum.CallMsg{
			To:   &market.Comet,
			Data: assetData,
		}, nil)
		if err != nil {
			continue
		}

		// 解码 AssetInfo tuple
		assetOutput, err := m.cometABI.Unpack("getAssetInfo", assetResult)
		if err != nil || len(assetOutput) == 0 {
			continue
		}

		// go-ethereum ABI 解码 tuple 返回匿名 struct（无 JSON tag）
		// 字段名必须与 ABI 中首字母大写的名称完全一致
		infoStruct, ok := assetOutput[0].(struct {
			Offset                    uint8
			Asset                     common.Address
			PriceFeed                 common.Address
			Scale                     uint64
			BorrowCollateralFactor    uint64
			LiquidateCollateralFactor uint64
			LiquidationFactor         uint64
			SupplyCap                 *big.Int
		})
		if !ok {
			log.Strategy().Debug().
				Uint8("index", i).
				Str("market", market.Name).
				Msg("Compound V3：getAssetInfo 类型断言失败")
			continue
		}

		// 查询用户该抵押品余额
		colData, _ := m.cometABI.Pack("collateralBalanceOf", user, infoStruct.Asset)
		colResult, err := client.CallContract(ctx, ethereum.CallMsg{
			To:   &market.Comet,
			Data: colData,
		}, nil)
		if err != nil {
			continue
		}

		colOutput, _ := m.cometABI.Unpack("collateralBalanceOf", colResult)
		if len(colOutput) == 0 {
			continue
		}

		colBalance, ok := colOutput[0].(*big.Int)
		if !ok || colBalance.Sign() <= 0 {
			continue
		}

		log.Strategy().Info().
			Str("market", market.Name).
			Str("user", user.Hex()[:10]+"...").
			Str("collateral", infoStruct.Asset.Hex()[:10]+"...").
			Str("collateral_balance", colBalance.String()).
			Float64("borrow_amount", borrowHuman).
			Str("base_symbol", market.BaseSymbol).
			Uint64("liquidation_factor", infoStruct.LiquidationFactor).
			Msg("Compound V3：可清算仓位详情")
	}
}

// GetStats 获取监控统计
func (m *CompoundV3Monitor) GetStats() map[string]interface{} {
	m.borrowersMu.RLock()
	defer m.borrowersMu.RUnlock()

	stats := map[string]interface{}{
		"markets": len(m.markets),
	}
	for _, market := range m.markets {
		stats[market.Name+"_users"] = len(m.borrowers[market.Comet])
	}
	return stats
}

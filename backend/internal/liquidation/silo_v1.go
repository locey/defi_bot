// internal/liquidation/silo_v1.go
// Silo Finance V1 清算监控 — 隔离借贷市场（Arbitrum）
//
// Silo 与 Aave/Compound 的区别：
//   - 隔离市场：每个 Silo 是独立的两资产市场（token + bridge asset）
//   - 风险隔离：某个 token 被攻击不会影响其他 Silo
//   - 内置闪电清算：flashLiquidate() 先发抵押品再还债，无需外部闪电贷
//   - 批量清算：一笔 tx 可清算多个用户
//   - 权限开放：任何人都可创建 Silo（permissionless）
//
// Arbitrum 合约地址：
//   SiloRepository: 0x8658047e48CC09161f4152c79155Dac1d710Ff0a
//   SiloLens:       0xBDb843c7a7e48Dc543424474d7Aa63b61B5D9536
//   SiloRouter:     0x9992f660137979C1ca7f8b119Cd16361594E3681
package liquidation

import (
	"context"
	"fmt"
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

// Silo V1 合约地址（Arbitrum One）
var (
	SiloRepositoryAddress = common.HexToAddress("0x8658047e48CC09161f4152c79155Dac1d710Ff0a")
	SiloLensAddress       = common.HexToAddress("0xBDb843c7a7e48Dc543424474d7Aa63b61B5D9536")
)

// SiloMarket 单个 Silo 市场信息
type SiloMarket struct {
	SiloAddress common.Address // Silo 合约地址
	Asset       common.Address // 市场的非 bridge 资产
	AssetSymbol string
}

// SiloV1Monitor Silo Finance V1 清算监控
type SiloV1Monitor struct {
	web3Client *web3.Client
	lensABI    abi.ABI
	repoABI    abi.ABI
	siloABI    abi.ABI

	// 已知 Silo 市场
	markets   []*SiloMarket
	marketsMu sync.RWMutex

	// 每个 Silo 的已知借款人
	borrowers   map[common.Address]map[common.Address]bool // silo → set of users
	borrowersMu sync.RWMutex

	// 配置
	scanInterval time.Duration
	stopCh       chan struct{}
}

// SiloLensABI SiloLens 合约 ABI（监控用）
const SiloLensABI = `[
    {
        "inputs": [
            {"internalType":"address","name":"_silo","type":"address"},
            {"internalType":"address","name":"_user","type":"address"}
        ],
        "name": "getUserLTV",
        "outputs": [{"internalType":"uint256","name":"userLTV","type":"uint256"}],
        "stateMutability": "view",
        "type": "function"
    },
    {
        "inputs": [
            {"internalType":"address","name":"_silo","type":"address"},
            {"internalType":"address","name":"_user","type":"address"}
        ],
        "name": "getUserLiquidationThreshold",
        "outputs": [{"internalType":"uint256","name":"liquidationThreshold","type":"uint256"}],
        "stateMutability": "view",
        "type": "function"
    },
    {
        "inputs": [
            {"internalType":"address","name":"_silo","type":"address"},
            {"internalType":"address","name":"_user","type":"address"}
        ],
        "name": "inDebt",
        "outputs": [{"internalType":"bool","name":"","type":"bool"}],
        "stateMutability": "view",
        "type": "function"
    },
    {
        "inputs": [
            {"internalType":"address","name":"_silo","type":"address"},
            {"internalType":"address","name":"_asset","type":"address"}
        ],
        "name": "totalBorrowAmount",
        "outputs": [{"internalType":"uint256","name":"","type":"uint256"}],
        "stateMutability": "view",
        "type": "function"
    },
    {
        "inputs": [
            {"internalType":"address","name":"_silo","type":"address"},
            {"internalType":"address","name":"_asset","type":"address"}
        ],
        "name": "totalDeposits",
        "outputs": [{"internalType":"uint256","name":"","type":"uint256"}],
        "stateMutability": "view",
        "type": "function"
    }
]`

// SiloABI 单个 Silo 合约 ABI（清算用）
const SiloABI = `[
    {
        "inputs": [{"internalType":"address","name":"_user","type":"address"}],
        "name": "isSolvent",
        "outputs": [{"internalType":"bool","name":"","type":"bool"}],
        "stateMutability": "view",
        "type": "function"
    },
    {
        "inputs": [],
        "name": "assetStorage",
        "outputs": [
            {"internalType":"address","name":"","type":"address"},
            {"internalType":"address","name":"","type":"address"}
        ],
        "stateMutability": "view",
        "type": "function"
    }
]`

// SiloRepositoryABI SiloRepository 合约 ABI
const SiloRepositoryABI = `[
    {
        "inputs": [{"internalType":"address","name":"_asset","type":"address"}],
        "name": "getSilo",
        "outputs": [{"internalType":"address","name":"","type":"address"}],
        "stateMutability": "view",
        "type": "function"
    }
]`

// NewSiloV1Monitor 创建 Silo V1 清算监控
func NewSiloV1Monitor(
	web3Client *web3.Client,
	scanInterval time.Duration,
) (*SiloV1Monitor, error) {
	lensABI, err := abi.JSON(strings.NewReader(SiloLensABI))
	if err != nil {
		return nil, fmt.Errorf("parse SiloLens ABI: %w", err)
	}

	repoABI, err := abi.JSON(strings.NewReader(SiloRepositoryABI))
	if err != nil {
		return nil, fmt.Errorf("parse SiloRepository ABI: %w", err)
	}

	siloABI, err := abi.JSON(strings.NewReader(SiloABI))
	if err != nil {
		return nil, fmt.Errorf("parse Silo ABI: %w", err)
	}

	return &SiloV1Monitor{
		web3Client:   web3Client,
		lensABI:      lensABI,
		repoABI:      repoABI,
		siloABI:      siloABI,
		borrowers:    make(map[common.Address]map[common.Address]bool),
		scanInterval: scanInterval,
		stopCh:       make(chan struct{}),
	}, nil
}

// Start 启动监控
func (m *SiloV1Monitor) Start(ctx context.Context) error {
	log.Strategy().Info().Msg("Silo V1：启动清算监控")

	// 发现 Silo 市场（通过 Borrow 事件）
	m.discoverSilos(ctx)

	go m.runLoop(ctx)
	return nil
}

// Stop 停止
func (m *SiloV1Monitor) Stop() {
	close(m.stopCh)
}

// discoverSilos 扫描 Borrow 事件发现活跃 Silo 和借款人
func (m *SiloV1Monitor) discoverSilos(ctx context.Context) {
	client := m.web3Client.GetClient()

	latestBlock, err := client.BlockNumber(ctx)
	if err != nil {
		log.Strategy().Warn().Err(err).Msg("Silo V1：获取最新区块失败")
		return
	}

	// 扫描最近 200000 区块 (~2.5 天) 的 Borrow 事件
	// Silo V1 Borrow(address indexed asset, address indexed user, uint256 amount)
	// topic0 = keccak256("Borrow(address,address,uint256)")
	// 验证: ethers.id("Borrow(address,address,uint256)") === 0x312a5e5e...
	// 注意: 如果 Silo V1 实际事件签名不同，需要从 Arbiscan 确认后更新
	borrowTopic := common.HexToHash("0x312a5e5e1079f5dda4e95dbbd0b908b291fd5b992ef22073643ab691572c5b52")

	fromBlock := latestBlock - 200000
	if fromBlock > latestBlock {
		fromBlock = 0
	}

	// 不能直接过滤所有 Silo 地址（太多），扫描全部然后过滤
	// 分批扫描避免 RPC 限制
	batchSize := uint64(9000)
	totalBorrowers := 0
	siloSet := make(map[common.Address]bool)

	for start := fromBlock; start < latestBlock; start += batchSize {
		end := start + batchSize - 1
		if end > latestBlock {
			end = latestBlock
		}

		query := ethereum.FilterQuery{
			FromBlock: new(big.Int).SetUint64(start),
			ToBlock:   new(big.Int).SetUint64(end),
			Topics:    [][]common.Hash{{borrowTopic}},
		}

		logs, err := client.FilterLogs(ctx, query)
		if err != nil {
			log.Strategy().Debug().Err(err).
				Uint64("from", start).
				Uint64("to", end).
				Msg("Silo V1：扫描区块失败")
			continue
		}

		m.borrowersMu.Lock()
		for _, lg := range logs {
			siloAddr := lg.Address
			if len(lg.Topics) >= 3 {
				user := common.BytesToAddress(lg.Topics[2].Bytes())
				if m.borrowers[siloAddr] == nil {
					m.borrowers[siloAddr] = make(map[common.Address]bool)
				}
				m.borrowers[siloAddr][user] = true
				siloSet[siloAddr] = true
				totalBorrowers++
			}
		}
		m.borrowersMu.Unlock()
	}

	// 将发现的 Silo 记录为市场（验证合约是否响应 isSolvent 调用）
	m.marketsMu.Lock()
	validated := 0
	for silo := range siloSet {
		// 简单验证：尝试调用 isSolvent(zeroAddress)，能调用说明是 Silo 合约
		checkData, err := m.siloABI.Pack("isSolvent", common.Address{})
		if err != nil {
			continue // ABI Pack 失败，跳过
		}
		siloAddr := silo
		_, callErr := client.CallContract(ctx, ethereum.CallMsg{
			To:   &siloAddr,
			Data: checkData,
		}, nil)
		if callErr != nil {
			continue // 不是有效的 Silo 合约，跳过
		}
		m.markets = append(m.markets, &SiloMarket{
			SiloAddress: silo,
		})
		validated++
	}
	m.marketsMu.Unlock()

	log.Strategy().Info().
		Int("candidates", len(siloSet)).
		Int("validated", validated).
		Int("borrowers", totalBorrowers).
		Uint64("from_block", fromBlock).
		Msg("Silo V1：市场发现完成")
}

// runLoop 主循环
func (m *SiloV1Monitor) runLoop(ctx context.Context) {
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

// scanCycle 扫描所有 Silo 市场
func (m *SiloV1Monitor) scanCycle(ctx context.Context) {
	m.marketsMu.RLock()
	markets := make([]*SiloMarket, len(m.markets))
	copy(markets, m.markets)
	m.marketsMu.RUnlock()

	totalInsolvent := 0

	for _, market := range markets {
		insolvent := m.scanSilo(ctx, market)
		totalInsolvent += insolvent
	}

	if totalInsolvent > 0 {
		log.Strategy().Info().
			Int("insolvent", totalInsolvent).
			Int("silos_scanned", len(markets)).
			Msg("Silo V1：发现不可偿付仓位")
	}
}

// scanSilo 扫描单个 Silo 的可清算仓位
func (m *SiloV1Monitor) scanSilo(ctx context.Context, market *SiloMarket) int {
	m.borrowersMu.RLock()
	users := make([]common.Address, 0)
	for user := range m.borrowers[market.SiloAddress] {
		users = append(users, user)
	}
	m.borrowersMu.RUnlock()

	if len(users) == 0 {
		return 0
	}

	client := m.web3Client.GetClient()
	insolvent := 0

	for _, user := range users {
		// 调用 Silo.isSolvent(user) — false 表示可清算
		callData, err := m.siloABI.Pack("isSolvent", user)
		if err != nil {
			continue
		}

		siloAddr := market.SiloAddress
		result, err := client.CallContract(ctx, ethereum.CallMsg{
			To:   &siloAddr,
			Data: callData,
		}, nil)
		if err != nil {
			continue
		}

		output, err := m.siloABI.Unpack("isSolvent", result)
		if err != nil || len(output) == 0 {
			continue
		}

		solvent, ok := output[0].(bool)
		if !ok {
			continue
		}

		if !solvent {
			insolvent++

			// 获取 LTV 详情
			ltv, threshold := m.getUserLTVInfo(ctx, market.SiloAddress, user)

			log.Strategy().Info().
				Str("silo", market.SiloAddress.Hex()[:10]+"...").
				Str("user", user.Hex()[:10]+"...").
				Str("ltv", ltv.String()).
				Str("threshold", threshold.String()).
				Msg("Silo V1：可清算仓位！")
		}
	}

	return insolvent
}

// getUserLTVInfo 获取用户 LTV 信息
func (m *SiloV1Monitor) getUserLTVInfo(ctx context.Context, silo, user common.Address) (*big.Int, *big.Int) {
	client := m.web3Client.GetClient()
	lensAddr := SiloLensAddress

	// getUserLTV
	ltvData, err := m.lensABI.Pack("getUserLTV", silo, user)
	if err != nil {
		return big.NewInt(0), big.NewInt(0)
	}

	ltvResult, err := client.CallContract(ctx, ethereum.CallMsg{
		To:   &lensAddr,
		Data: ltvData,
	}, nil)
	if err != nil {
		return big.NewInt(0), big.NewInt(0)
	}

	ltvOutput, _ := m.lensABI.Unpack("getUserLTV", ltvResult)
	ltv := big.NewInt(0)
	if len(ltvOutput) > 0 {
		if v, ok := ltvOutput[0].(*big.Int); ok {
			ltv = v
		}
	}

	// getUserLiquidationThreshold
	threshData, _ := m.lensABI.Pack("getUserLiquidationThreshold", silo, user)
	threshResult, err := client.CallContract(ctx, ethereum.CallMsg{
		To:   &lensAddr,
		Data: threshData,
	}, nil)
	if err != nil {
		return ltv, big.NewInt(0)
	}

	threshOutput, _ := m.lensABI.Unpack("getUserLiquidationThreshold", threshResult)
	threshold := big.NewInt(0)
	if len(threshOutput) > 0 {
		if v, ok := threshOutput[0].(*big.Int); ok {
			threshold = v
		}
	}

	return ltv, threshold
}

// GetStats 获取监控统计
func (m *SiloV1Monitor) GetStats() map[string]interface{} {
	m.marketsMu.RLock()
	m.borrowersMu.RLock()
	defer m.marketsMu.RUnlock()
	defer m.borrowersMu.RUnlock()

	totalBorrowers := 0
	for _, users := range m.borrowers {
		totalBorrowers += len(users)
	}

	return map[string]interface{}{
		"silos":          len(m.markets),
		"total_borrowers": totalBorrowers,
	}
}

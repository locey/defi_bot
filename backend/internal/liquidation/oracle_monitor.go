// internal/liquidation/oracle_monitor.go
// Chainlink 预言机监控 — 通过 WebSocket 订阅 AnswerUpdated 事件
// 价格变动时立即重新检查高风险仓位，替代 3 秒轮询
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
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Chainlink 事件签名
var (
	// AnswerUpdated(int256 indexed current, uint256 indexed roundId, uint256 updatedAt)
	AnswerUpdatedTopic = crypto.Keccak256Hash([]byte("AnswerUpdated(int256,uint256,uint256)"))
)

// Aave V3 Oracle ABI（只需 getSourceOfAsset）
const AaveOracleABI = `[
	{
		"inputs": [{"internalType":"address","name":"asset","type":"address"}],
		"name": "getSourceOfAsset",
		"outputs": [{"internalType":"address","name":"","type":"address"}],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"inputs": [],
		"name": "getAssetsPrices",
		"outputs": [{"internalType":"uint256[]","name":"","type":"uint256[]"}],
		"stateMutability": "view",
		"type": "function"
	}
]`

// OracleMonitor 预言机监控器
type OracleMonitor struct {
	web3Client *web3.Client
	wsURL      string

	// Aave Oracle 地址（用于查询 Chainlink feed 地址）
	aaveOracle    common.Address
	aaveOracleABI abi.ABI

	// feed 地址 → 资产地址（反向映射）
	feedToAssets map[common.Address][]common.Address
	feedsMu      sync.RWMutex

	// 所有监控的 feed 地址
	feeds []common.Address

	// 价格变动回调
	onPriceChange func(asset common.Address, newPrice *big.Int)

	// WebSocket 客户端
	wsClient *ethclient.Client
	sub      ethereum.Subscription

	// 统计
	totalUpdates int64
	updatesMu    sync.Mutex

	ctx    context.Context
	cancel context.CancelFunc
}

// NewOracleMonitor 创建预言机监控器
func NewOracleMonitor(
	web3Client *web3.Client,
	wsURL string,
	aaveOracle common.Address,
	onPriceChange func(asset common.Address, newPrice *big.Int),
) (*OracleMonitor, error) {
	oracleABI, err := abi.JSON(strings.NewReader(AaveOracleABI))
	if err != nil {
		return nil, fmt.Errorf("parse oracle ABI: %w", err)
	}

	return &OracleMonitor{
		web3Client:    web3Client,
		wsURL:         wsURL,
		aaveOracle:    aaveOracle,
		aaveOracleABI: oracleABI,
		feedToAssets:   make(map[common.Address][]common.Address),
		onPriceChange: onPriceChange,
	}, nil
}

// Start 启动预言机监控
// 1. 查询所有储备资产的 Chainlink feed 地址
// 2. 通过 WebSocket 订阅 AnswerUpdated 事件
func (m *OracleMonitor) Start(ctx context.Context, reserves []ReserveInfo) error {
	m.ctx, m.cancel = context.WithCancel(ctx)

	// Step 1: 查询每个储备资产对应的 Chainlink 价格源
	if err := m.discoverFeeds(ctx, reserves); err != nil {
		return fmt.Errorf("discover chainlink feeds: %w", err)
	}

	if len(m.feeds) == 0 {
		log.Strategy().Warn().Msg("清算预言机：未发现 Chainlink 价格源，跳过订阅")
		return nil
	}

	// Step 2: WebSocket 订阅
	if err := m.subscribeFeeds(); err != nil {
		return fmt.Errorf("subscribe feeds: %w", err)
	}

	log.Strategy().Info().
		Int("feeds", len(m.feeds)).
		Str("oracle", m.aaveOracle.Hex()[:10]).
		Msg("清算预言机监控已启动")

	return nil
}

// discoverFeeds 查询 Aave Oracle 获取各资产的 Chainlink feed 地址
func (m *OracleMonitor) discoverFeeds(ctx context.Context, reserves []ReserveInfo) error {
	multicallAddr := common.HexToAddress(web3.Multicall3Address)
	mcABI, _ := abi.JSON(strings.NewReader(`[{"inputs":[{"components":[{"name":"target","type":"address"},{"name":"allowFailure","type":"bool"},{"name":"callData","type":"bytes"}],"name":"calls","type":"tuple[]"}],"name":"aggregate3","outputs":[{"components":[{"name":"success","type":"bool"},{"name":"returnData","type":"bytes"}],"name":"returnData","type":"tuple[]"}],"stateMutability":"payable","type":"function"}]`))

	type Call3 struct {
		Target       common.Address
		AllowFailure bool
		CallData     []byte
	}

	calls := make([]Call3, 0, len(reserves))
	for _, r := range reserves {
		callData, err := m.aaveOracleABI.Pack("getSourceOfAsset", r.Asset)
		if err != nil {
			continue
		}
		calls = append(calls, Call3{Target: m.aaveOracle, AllowFailure: true, CallData: callData})
	}

	if len(calls) == 0 {
		return nil
	}

	mcCallData, err := mcABI.Pack("aggregate3", calls)
	if err != nil {
		return fmt.Errorf("pack multicall: %w", err)
	}

	result, err := m.web3Client.GetClient().CallContract(ctx, ethereum.CallMsg{
		To:   &multicallAddr,
		Data: mcCallData,
	}, nil)
	if err != nil {
		return fmt.Errorf("multicall getSourceOfAsset: %w", err)
	}

	mcOutputs, err := mcABI.Unpack("aggregate3", result)
	if err != nil {
		return fmt.Errorf("unpack multicall: %w", err)
	}

	results, ok := mcOutputs[0].([]struct {
		Success    bool   `json:"success"`
		ReturnData []byte `json:"returnData"`
	})
	if !ok {
		return fmt.Errorf("unexpected multicall result type")
	}

	m.feedsMu.Lock()
	defer m.feedsMu.Unlock()

	feedSet := make(map[common.Address]bool)
	for i, r := range results {
		if !r.Success || len(r.ReturnData) < 32 {
			continue
		}
		out, err := m.aaveOracleABI.Unpack("getSourceOfAsset", r.ReturnData)
		if err != nil || len(out) == 0 {
			continue
		}
		feedAddr, ok := out[0].(common.Address)
		if !ok || feedAddr == (common.Address{}) {
			continue
		}

		asset := reserves[i].Asset
		m.feedToAssets[feedAddr] = append(m.feedToAssets[feedAddr], asset)
		if !feedSet[feedAddr] {
			feedSet[feedAddr] = true
			m.feeds = append(m.feeds, feedAddr)
		}

		log.Strategy().Debug().
			Str("asset", reserves[i].Symbol).
			Str("feed", feedAddr.Hex()[:10]).
			Msg("清算预言机：发现 Chainlink feed")
	}

	return nil
}

// subscribeFeeds 通过 WebSocket 订阅 AnswerUpdated 事件
func (m *OracleMonitor) subscribeFeeds() error {
	if m.wsURL == "" {
		return fmt.Errorf("WebSocket URL not configured")
	}

	client, err := ethclient.DialContext(m.ctx, m.wsURL)
	if err != nil {
		return fmt.Errorf("connect ws: %w", err)
	}
	m.wsClient = client

	// 订阅所有 feed 的 AnswerUpdated 事件
	query := ethereum.FilterQuery{
		Addresses: m.feeds,
		Topics:    [][]common.Hash{{AnswerUpdatedTopic}},
	}

	logCh := make(chan types.Log, 100)
	sub, err := client.SubscribeFilterLogs(m.ctx, query, logCh)
	if err != nil {
		client.Close()
		return fmt.Errorf("subscribe AnswerUpdated: %w", err)
	}
	m.sub = sub

	go m.listenLoop(logCh, sub)
	return nil
}

// listenLoop 监听预言机事件
func (m *OracleMonitor) listenLoop(logCh chan types.Log, sub ethereum.Subscription) {
	for {
		select {
		case <-m.ctx.Done():
			return
		case err := <-sub.Err():
			if err != nil {
				log.Strategy().Warn().Err(err).Msg("清算预言机：订阅错误，尝试重连")
				go m.reconnect()
			}
			return
		case vLog := <-logCh:
			m.handleAnswerUpdated(vLog)
		}
	}
}

// handleAnswerUpdated 处理 Chainlink 价格更新
func (m *OracleMonitor) handleAnswerUpdated(vLog types.Log) {
	// AnswerUpdated(int256 indexed current, uint256 indexed roundId, uint256 updatedAt)
	// current 在 topics[1]
	if len(vLog.Topics) < 2 {
		return
	}

	newPrice := new(big.Int).SetBytes(vLog.Topics[1].Bytes())
	// int256: 如果最高位是 1，是负数（不应该发生但防御性处理）
	if vLog.Topics[1][0]&0x80 != 0 {
		newPrice.Sub(newPrice, new(big.Int).Lsh(big.NewInt(1), 256))
	}

	feedAddr := vLog.Address

	m.updatesMu.Lock()
	m.totalUpdates++
	m.updatesMu.Unlock()

	// 查找这个 feed 对应哪些资产
	m.feedsMu.RLock()
	assets := m.feedToAssets[feedAddr]
	m.feedsMu.RUnlock()

	for _, asset := range assets {
		if m.onPriceChange != nil {
			m.onPriceChange(asset, newPrice)
		}
	}
}

// reconnect 重连
func (m *OracleMonitor) reconnect() {
	if m.sub != nil {
		m.sub.Unsubscribe()
	}
	if m.wsClient != nil {
		m.wsClient.Close()
	}

	for attempt := 0; attempt < 5; attempt++ {
		select {
		case <-m.ctx.Done():
			return
		case <-time.After(time.Duration(attempt+1) * 2 * time.Second):
		}

		if err := m.subscribeFeeds(); err != nil {
			log.Strategy().Warn().Err(err).Int("attempt", attempt+1).Msg("清算预言机：重连失败")
			continue
		}

		log.Strategy().Info().Msg("清算预言机：重连成功")
		return
	}

	log.Strategy().Error().Msg("清算预言机：重连失败，已达最大重试")
}

// Stop 停止监控
func (m *OracleMonitor) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	if m.sub != nil {
		m.sub.Unsubscribe()
	}
	if m.wsClient != nil {
		m.wsClient.Close()
	}
	log.Strategy().Info().Msg("清算预言机监控已停止")
}

// Stats 统计
func (m *OracleMonitor) Stats() map[string]interface{} {
	m.updatesMu.Lock()
	updates := m.totalUpdates
	m.updatesMu.Unlock()

	return map[string]interface{}{
		"feeds":         len(m.feeds),
		"total_updates": updates,
	}
}

// internal/collector/pool_watcher.go
// Phase 2.2: 新池子监控器 — 订阅 Uniswap V2/V3 Factory 的 PairCreated/PoolCreated 事件
// 新池子出现后立即查询储备量，与已知池比较价格，触发套利检测
package collector

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
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// PoolWatcher 新池子监控器
type PoolWatcher struct {
	client    *ethclient.Client
	wsClient  *ethclient.Client // WebSocket 客户端（用于订阅事件）
	factories []FactoryConfig

	// 新池子通知 channel
	newPoolCh chan *NewPoolEvent

	// 状态
	running bool
	mu      sync.RWMutex
	ctx     context.Context
	cancel  context.CancelFunc

	// 统计
	poolsDetected int64
}

// FactoryConfig 工厂合约配置
type FactoryConfig struct {
	Address  common.Address
	Protocol string // "uniswap_v2" / "uniswap_v3" / "sushiswap"
	Name     string
}

// NewPoolEvent 新池子事件
type NewPoolEvent struct {
	PoolAddress common.Address `json:"pool_address"`
	Token0      common.Address `json:"token0"`
	Token1      common.Address `json:"token1"`
	Factory     common.Address `json:"factory"`
	Protocol    string         `json:"protocol"`
	Fee         uint64         `json:"fee"`         // V3 专用
	BlockNumber uint64         `json:"block_number"`
	TxHash      common.Hash    `json:"tx_hash"`
	Timestamp   time.Time      `json:"timestamp"`
}

// Uniswap V2 PairCreated 事件签名
// event PairCreated(address indexed token0, address indexed token1, address pair, uint)
var pairCreatedSig = crypto.Keccak256Hash([]byte("PairCreated(address,address,address,uint256)"))

// Uniswap V3 PoolCreated 事件签名
// event PoolCreated(address indexed token0, address indexed token1, uint24 indexed fee, int24 tickSpacing, address pool)
var poolCreatedSig = crypto.Keccak256Hash([]byte("PoolCreated(address,address,uint24,int24,address)"))

// NewPoolWatcher 创建新池子监控器
func NewPoolWatcher(wsClient *ethclient.Client, factories []FactoryConfig) *PoolWatcher {
	ctx, cancel := context.WithCancel(context.Background())
	return &PoolWatcher{
		wsClient:  wsClient,
		factories: factories,
		newPoolCh: make(chan *NewPoolEvent, 100),
		ctx:       ctx,
		cancel:    cancel,
	}
}

// NewPoolEvents 返回新池子事件 channel（供外部消费）
func (w *PoolWatcher) NewPoolEvents() <-chan *NewPoolEvent {
	return w.newPoolCh
}

// Start 启动监控
func (w *PoolWatcher) Start(ctx context.Context) error {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return nil
	}
	w.running = true
	w.mu.Unlock()

	if w.wsClient == nil {
		return fmt.Errorf("pool_watcher: WebSocket client required for event subscription")
	}

	// 构建事件过滤器
	var addresses []common.Address
	for _, f := range w.factories {
		addresses = append(addresses, f.Address)
	}

	if len(addresses) == 0 {
		return fmt.Errorf("pool_watcher: no factory addresses configured")
	}

	// 订阅 PairCreated + PoolCreated 事件
	query := ethereum.FilterQuery{
		Addresses: addresses,
		Topics: [][]common.Hash{
			{pairCreatedSig, poolCreatedSig}, // 匹配任意一种事件
		},
	}

	logCh := make(chan types.Log, 100)
	sub, err := w.wsClient.SubscribeFilterLogs(ctx, query, logCh)
	if err != nil {
		return fmt.Errorf("pool_watcher: subscribe failed: %w", err)
	}

	go w.processEvents(logCh, sub)

	log.Collector().Info().
		Int("factories", len(addresses)).
		Msg("PoolWatcher started - monitoring for new pools")

	return nil
}

// processEvents 处理事件
func (w *PoolWatcher) processEvents(logCh chan types.Log, sub ethereum.Subscription) {
	defer sub.Unsubscribe()

	for {
		select {
		case <-w.ctx.Done():
			return
		case err := <-sub.Err():
			if err != nil {
				log.Collector().Error().Err(err).Msg("PoolWatcher subscription error")
			}
			return
		case vLog := <-logCh:
			event := w.parseLogEvent(vLog)
			if event != nil {
				w.poolsDetected++
				select {
				case w.newPoolCh <- event:
					log.Collector().Info().
						Str("pool", event.PoolAddress.Hex()).
						Str("token0", event.Token0.Hex()).
						Str("token1", event.Token1.Hex()).
						Str("protocol", event.Protocol).
						Msg("New pool detected!")
				default:
					log.Collector().Warn().Msg("PoolWatcher: event channel full, dropping event")
				}
			}
		}
	}
}

// parseLogEvent 解析日志事件
func (w *PoolWatcher) parseLogEvent(vLog types.Log) *NewPoolEvent {
	if len(vLog.Topics) == 0 {
		return nil
	}

	event := &NewPoolEvent{
		Factory:     vLog.Address,
		BlockNumber: vLog.BlockNumber,
		TxHash:      vLog.TxHash,
		Timestamp:   time.Now(),
	}

	// 确定协议
	for _, f := range w.factories {
		if f.Address == vLog.Address {
			event.Protocol = f.Protocol
			break
		}
	}

	switch vLog.Topics[0] {
	case pairCreatedSig:
		return w.parsePairCreated(vLog, event)
	case poolCreatedSig:
		return w.parsePoolCreated(vLog, event)
	default:
		return nil
	}
}

// parsePairCreated 解析 V2 PairCreated 事件
func (w *PoolWatcher) parsePairCreated(vLog types.Log, event *NewPoolEvent) *NewPoolEvent {
	// PairCreated(address indexed token0, address indexed token1, address pair, uint)
	if len(vLog.Topics) < 3 {
		return nil
	}

	event.Token0 = common.HexToAddress(vLog.Topics[1].Hex())
	event.Token1 = common.HexToAddress(vLog.Topics[2].Hex())

	// pair address 在 data 中
	if len(vLog.Data) >= 32 {
		event.PoolAddress = common.BytesToAddress(vLog.Data[12:32])
	}

	return event
}

// parsePoolCreated 解析 V3 PoolCreated 事件
func (w *PoolWatcher) parsePoolCreated(vLog types.Log, event *NewPoolEvent) *NewPoolEvent {
	// PoolCreated(address indexed token0, address indexed token1, uint24 indexed fee, int24 tickSpacing, address pool)
	if len(vLog.Topics) < 4 {
		return nil
	}

	event.Token0 = common.HexToAddress(vLog.Topics[1].Hex())
	event.Token1 = common.HexToAddress(vLog.Topics[2].Hex())

	// fee 在 topic[3]
	fee := new(big.Int).SetBytes(vLog.Topics[3].Bytes())
	event.Fee = fee.Uint64()

	// pool address 在 data 中（跳过 tickSpacing 的 32 字节）
	if len(vLog.Data) >= 64 {
		event.PoolAddress = common.BytesToAddress(vLog.Data[44:64])
	}

	return event
}

// Stop 停止监控
func (w *PoolWatcher) Stop() {
	w.cancel()
	w.mu.Lock()
	w.running = false
	w.mu.Unlock()
	log.Collector().Info().Int64("pools_detected", w.poolsDetected).Msg("PoolWatcher stopped")
}

// GetStats 获取统计
func (w *PoolWatcher) GetStats() int64 {
	return w.poolsDetected
}

// ParseFactoriesFromConfig 从配置中解析工厂地址
func ParseFactoriesFromConfig(dexConfigs []struct {
	Name     string
	Protocol string
	Factory  string
}) []FactoryConfig {
	var factories []FactoryConfig
	seen := make(map[common.Address]bool)

	for _, dc := range dexConfigs {
		if dc.Factory == "" {
			continue
		}
		addr := common.HexToAddress(dc.Factory)
		if seen[addr] {
			continue
		}
		seen[addr] = true

		factories = append(factories, FactoryConfig{
			Address:  addr,
			Protocol: dc.Protocol,
			Name:     dc.Name,
		})
	}

	return factories
}

// 确保导入的 abi 和 strings 不报未使用错误
var _ = abi.JSON
var _ = strings.NewReader

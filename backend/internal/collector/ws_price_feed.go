// Package collector 提供数据采集功能
package collector

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/defi-bot/backend/pkg/log"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Sync 事件签名 (Uniswap V2 风格)
// event Sync(uint112 reserve0, uint112 reserve1)
var syncEventTopic = crypto.Keccak256Hash([]byte("Sync(uint112,uint112)"))

// WSPriceFeedConfig WebSocket 价格订阅配置
type WSPriceFeedConfig struct {
	ReconnectDelay time.Duration // 重连延迟
	BufferSize     int           // 缓冲区大小
	MaxPools       int           // 最大订阅池子数
}

// DefaultWSPriceFeedConfig 默认配置
func DefaultWSPriceFeedConfig() *WSPriceFeedConfig {
	return &WSPriceFeedConfig{
		ReconnectDelay: 5 * time.Second,
		BufferSize:     1000,
		MaxPools:       100,
	}
}

// PriceUpdate 价格更新事件
type PriceUpdate struct {
	Pool      common.Address
	Token0    common.Address
	Token1    common.Address
	Reserve0  *big.Int
	Reserve1  *big.Int
	BlockNum  uint64
	Timestamp time.Time
}

// WSPriceFeed WebSocket 实时价格订阅
type WSPriceFeed struct {
	wsURL      string
	client     *ethclient.Client
	config     *WSPriceFeedConfig
	priceCh    chan *PriceUpdate
	pools      map[common.Address]*PoolInfo // 订阅的池子信息
	poolsMu    sync.RWMutex
	sub        ethereum.Subscription
	running    bool
	runningMu  sync.RWMutex
	cancelFunc context.CancelFunc
}

// PoolInfo 池子信息（用于 WebSocket 订阅）
type PoolInfo struct {
	Address  common.Address
	Token0   common.Address
	Token1   common.Address
	Protocol string // uniswap_v2, uniswap_v3, sushiswap
}

// NewWSPriceFeed 创建 WebSocket 价格订阅器
func NewWSPriceFeed(wsURL string, config *WSPriceFeedConfig) *WSPriceFeed {
	if config == nil {
		config = DefaultWSPriceFeedConfig()
	}

	return &WSPriceFeed{
		wsURL:   wsURL,
		config:  config,
		priceCh: make(chan *PriceUpdate, config.BufferSize),
		pools:   make(map[common.Address]*PoolInfo),
	}
}

// Start 启动 WebSocket 订阅
func (f *WSPriceFeed) Start(ctx context.Context) error {
	f.runningMu.Lock()
	if f.running {
		f.runningMu.Unlock()
		return nil
	}
	f.running = true
	f.runningMu.Unlock()

	// 创建取消上下文
	ctx, cancel := context.WithCancel(ctx)
	f.cancelFunc = cancel

	// 连接 WebSocket
	if err := f.connect(ctx); err != nil {
		return err
	}

	// 启动订阅
	go f.subscribeLoop(ctx)

	log.Info("WebSocket 价格订阅已启动: %s", f.wsURL)
	return nil
}

// Stop 停止 WebSocket 订阅
func (f *WSPriceFeed) Stop() {
	f.runningMu.Lock()
	defer f.runningMu.Unlock()

	if !f.running {
		return
	}

	f.running = false
	if f.cancelFunc != nil {
		f.cancelFunc()
	}
	if f.sub != nil {
		f.sub.Unsubscribe()
	}
	if f.client != nil {
		f.client.Close()
	}

	log.Info("WebSocket 价格订阅已停止")
}

// connect 连接 WebSocket
func (f *WSPriceFeed) connect(ctx context.Context) error {
	client, err := ethclient.DialContext(ctx, f.wsURL)
	if err != nil {
		return fmt.Errorf("连接 WebSocket 失败: %w", err)
	}
	f.client = client
	return nil
}

// subscribeLoop 订阅循环
func (f *WSPriceFeed) subscribeLoop(ctx context.Context) {
	for {
		f.runningMu.RLock()
		running := f.running
		f.runningMu.RUnlock()

		if !running {
			return
		}

		// 订阅事件
		if err := f.subscribe(ctx); err != nil {
			log.Warn("订阅失败: %v, %v 后重试", err, f.config.ReconnectDelay)
			time.Sleep(f.config.ReconnectDelay)

			// 重新连接
			if f.client != nil {
				f.client.Close()
			}
			if err := f.connect(ctx); err != nil {
				log.Warn("重新连接失败: %v", err)
				continue
			}
		}
	}
}

// subscribe 订阅 Sync 事件
func (f *WSPriceFeed) subscribe(ctx context.Context) error {
	f.poolsMu.RLock()
	poolAddresses := make([]common.Address, 0, len(f.pools))
	for addr := range f.pools {
		poolAddresses = append(poolAddresses, addr)
	}
	f.poolsMu.RUnlock()

	if len(poolAddresses) == 0 {
		// 没有订阅的池子，等待
		time.Sleep(time.Second)
		return nil
	}

	// 构建过滤器
	query := ethereum.FilterQuery{
		Addresses: poolAddresses,
		Topics:    [][]common.Hash{{syncEventTopic}},
	}

	// 创建日志通道
	logs := make(chan types.Log)

	// 订阅
	sub, err := f.client.SubscribeFilterLogs(ctx, query, logs)
	if err != nil {
		return fmt.Errorf("订阅事件失败: %w", err)
	}
	f.sub = sub

	log.Info("已订阅 %d 个池子的 Sync 事件", len(poolAddresses))

	// 处理事件
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case err := <-sub.Err():
			return fmt.Errorf("订阅错误: %w", err)

		case vLog := <-logs:
			f.handleSyncEvent(vLog)
		}
	}
}

// handleSyncEvent 处理 Sync 事件
func (f *WSPriceFeed) handleSyncEvent(vLog types.Log) {
	// 解析 Sync 事件数据
	// Sync(uint112 reserve0, uint112 reserve1)
	if len(vLog.Data) < 64 {
		return
	}

	reserve0 := new(big.Int).SetBytes(vLog.Data[:32])
	reserve1 := new(big.Int).SetBytes(vLog.Data[32:64])

	// 获取池子信息
	f.poolsMu.RLock()
	pool, ok := f.pools[vLog.Address]
	f.poolsMu.RUnlock()

	if !ok {
		return
	}

	// 发送价格更新
	update := &PriceUpdate{
		Pool:      vLog.Address,
		Token0:    pool.Token0,
		Token1:    pool.Token1,
		Reserve0:  reserve0,
		Reserve1:  reserve1,
		BlockNum:  vLog.BlockNumber,
		Timestamp: time.Now(),
	}

	select {
	case f.priceCh <- update:
	default:
		// 通道满，丢弃
		log.Warn("价格更新通道已满，丢弃: %s", vLog.Address.Hex())
	}
}

// AddPool 添加要订阅的池子
func (f *WSPriceFeed) AddPool(pool *PoolInfo) error {
	f.poolsMu.Lock()
	defer f.poolsMu.Unlock()

	if len(f.pools) >= f.config.MaxPools {
		return fmt.Errorf("已达到最大订阅数 %d", f.config.MaxPools)
	}

	f.pools[pool.Address] = pool
	log.Info("添加池子订阅: %s (%s)", pool.Address.Hex(), pool.Protocol)
	return nil
}

// AddPools 批量添加池子
func (f *WSPriceFeed) AddPools(pools []*PoolInfo) error {
	f.poolsMu.Lock()
	defer f.poolsMu.Unlock()

	for _, pool := range pools {
		if len(f.pools) >= f.config.MaxPools {
			log.Warn("已达到最大订阅数 %d, 跳过剩余池子", f.config.MaxPools)
			break
		}
		f.pools[pool.Address] = pool
	}

	log.Info("批量添加 %d 个池子订阅", len(pools))
	return nil
}

// RemovePool 移除池子订阅
func (f *WSPriceFeed) RemovePool(address common.Address) {
	f.poolsMu.Lock()
	defer f.poolsMu.Unlock()
	delete(f.pools, address)
}

// GetPriceChan 获取价格更新通道
func (f *WSPriceFeed) GetPriceChan() <-chan *PriceUpdate {
	return f.priceCh
}

// GetSubscribedPoolCount 获取已订阅的池子数量
func (f *WSPriceFeed) GetSubscribedPoolCount() int {
	f.poolsMu.RLock()
	defer f.poolsMu.RUnlock()
	return len(f.pools)
}

// IsRunning 是否正在运行
func (f *WSPriceFeed) IsRunning() bool {
	f.runningMu.RLock()
	defer f.runningMu.RUnlock()
	return f.running
}

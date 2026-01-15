// Package discovery 提供代币自动发现功能
package discovery

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

// PairCreated 事件签名
// event PairCreated(address indexed token0, address indexed token1, address pair, uint)
var pairCreatedTopic = crypto.Keccak256Hash([]byte("PairCreated(address,address,address,uint256)"))

// PoolCreated 事件签名 (Uniswap V3)
// event PoolCreated(address indexed token0, address indexed token1, uint24 indexed fee, int24 tickSpacing, address pool)
var poolCreatedTopic = crypto.Keccak256Hash([]byte("PoolCreated(address,address,uint24,int24,address)"))

// NewPairEvent 新交易对事件
type NewPairEvent struct {
	Token0     common.Address
	Token1     common.Address
	PairAddr   common.Address
	Factory    common.Address
	Fee        uint32 // V3 专用
	BlockNum   uint64
	TxHash     common.Hash
	Timestamp  time.Time
}

// EventListenerConfig 事件监听器配置
type EventListenerConfig struct {
	ReconnectDelay time.Duration
	BufferSize     int
	Factories      []common.Address // 要监听的工厂合约地址
}

// DefaultEventListenerConfig 默认配置
func DefaultEventListenerConfig() *EventListenerConfig {
	return &EventListenerConfig{
		ReconnectDelay: 5 * time.Second,
		BufferSize:     100,
		Factories:      []common.Address{},
	}
}

// EventListener 事件监听器
// 监听 DEX 工厂合约的 PairCreated/PoolCreated 事件
type EventListener struct {
	wsURL      string
	client     *ethclient.Client
	config     *EventListenerConfig
	eventCh    chan *NewPairEvent
	running    bool
	runningMu  sync.RWMutex
	cancelFunc context.CancelFunc
}

// NewEventListener 创建事件监听器
func NewEventListener(wsURL string, config *EventListenerConfig) *EventListener {
	if config == nil {
		config = DefaultEventListenerConfig()
	}

	return &EventListener{
		wsURL:   wsURL,
		config:  config,
		eventCh: make(chan *NewPairEvent, config.BufferSize),
	}
}

// Start 启动事件监听
func (l *EventListener) Start(ctx context.Context) error {
	l.runningMu.Lock()
	if l.running {
		l.runningMu.Unlock()
		return nil
	}
	l.running = true
	l.runningMu.Unlock()

	// 创建取消上下文
	ctx, cancel := context.WithCancel(ctx)
	l.cancelFunc = cancel

	// 连接 WebSocket
	if err := l.connect(ctx); err != nil {
		return err
	}

	// 启动订阅
	go l.subscribeLoop(ctx)

	log.Info("事件监听器已启动，监听 %d 个工厂合约", len(l.config.Factories))
	return nil
}

// Stop 停止事件监听
func (l *EventListener) Stop() {
	l.runningMu.Lock()
	defer l.runningMu.Unlock()

	if !l.running {
		return
	}

	l.running = false
	if l.cancelFunc != nil {
		l.cancelFunc()
	}
	if l.client != nil {
		l.client.Close()
	}

	log.Info("事件监听器已停止")
}

// connect 连接 WebSocket
func (l *EventListener) connect(ctx context.Context) error {
	client, err := ethclient.DialContext(ctx, l.wsURL)
	if err != nil {
		return fmt.Errorf("连接 WebSocket 失败: %w", err)
	}
	l.client = client
	return nil
}

// subscribeLoop 订阅循环
func (l *EventListener) subscribeLoop(ctx context.Context) {
	for {
		l.runningMu.RLock()
		running := l.running
		l.runningMu.RUnlock()

		if !running {
			return
		}

		if err := l.subscribe(ctx); err != nil {
			log.Warn("事件订阅失败: %v, %v 后重试", err, l.config.ReconnectDelay)
			time.Sleep(l.config.ReconnectDelay)

			// 重新连接
			if l.client != nil {
				l.client.Close()
			}
			if err := l.connect(ctx); err != nil {
				log.Warn("重新连接失败: %v", err)
				continue
			}
		}
	}
}

// subscribe 订阅 PairCreated/PoolCreated 事件
func (l *EventListener) subscribe(ctx context.Context) error {
	if len(l.config.Factories) == 0 {
		// 没有要监听的工厂，等待
		time.Sleep(time.Second)
		return nil
	}

	// 构建过滤器：监听 PairCreated 和 PoolCreated 事件
	query := ethereum.FilterQuery{
		Addresses: l.config.Factories,
		Topics: [][]common.Hash{
			{pairCreatedTopic, poolCreatedTopic}, // 监听两种事件
		},
	}

	// 创建日志通道
	logs := make(chan types.Log)

	// 订阅
	sub, err := l.client.SubscribeFilterLogs(ctx, query, logs)
	if err != nil {
		return fmt.Errorf("订阅事件失败: %w", err)
	}

	log.Info("已订阅工厂合约事件")

	// 处理事件
	for {
		select {
		case <-ctx.Done():
			sub.Unsubscribe()
			return ctx.Err()

		case err := <-sub.Err():
			return fmt.Errorf("订阅错误: %w", err)

		case vLog := <-logs:
			l.handleLog(vLog)
		}
	}
}

// handleLog 处理日志事件
func (l *EventListener) handleLog(vLog types.Log) {
	if len(vLog.Topics) < 3 {
		return
	}

	eventSig := vLog.Topics[0]
	var event *NewPairEvent

	switch eventSig {
	case pairCreatedTopic:
		event = l.parsePairCreatedEvent(vLog)
	case poolCreatedTopic:
		event = l.parsePoolCreatedEvent(vLog)
	default:
		return
	}

	if event == nil {
		return
	}

	// 发送事件
	select {
	case l.eventCh <- event:
		log.Info("发现新交易对: %s - Token0: %s, Token1: %s",
			event.PairAddr.Hex()[:10],
			event.Token0.Hex()[:10],
			event.Token1.Hex()[:10])
	default:
		log.Warn("事件通道已满，丢弃事件")
	}
}

// parsePairCreatedEvent 解析 PairCreated 事件 (V2)
func (l *EventListener) parsePairCreatedEvent(vLog types.Log) *NewPairEvent {
	// PairCreated(address indexed token0, address indexed token1, address pair, uint)
	// Topics: [0]=eventSig, [1]=token0, [2]=token1
	// Data: pair address, pairIndex

	if len(vLog.Topics) < 3 || len(vLog.Data) < 32 {
		return nil
	}

	token0 := common.HexToAddress(vLog.Topics[1].Hex())
	token1 := common.HexToAddress(vLog.Topics[2].Hex())
	pairAddr := common.BytesToAddress(vLog.Data[:32])

	return &NewPairEvent{
		Token0:    token0,
		Token1:    token1,
		PairAddr:  pairAddr,
		Factory:   vLog.Address,
		BlockNum:  vLog.BlockNumber,
		TxHash:    vLog.TxHash,
		Timestamp: time.Now(),
	}
}

// parsePoolCreatedEvent 解析 PoolCreated 事件 (V3)
func (l *EventListener) parsePoolCreatedEvent(vLog types.Log) *NewPairEvent {
	// PoolCreated(address indexed token0, address indexed token1, uint24 indexed fee, int24 tickSpacing, address pool)
	// Topics: [0]=eventSig, [1]=token0, [2]=token1, [3]=fee
	// Data: tickSpacing, pool address

	if len(vLog.Topics) < 4 || len(vLog.Data) < 64 {
		return nil
	}

	token0 := common.HexToAddress(vLog.Topics[1].Hex())
	token1 := common.HexToAddress(vLog.Topics[2].Hex())
	fee := new(big.Int).SetBytes(vLog.Topics[3].Bytes()).Uint64()
	poolAddr := common.BytesToAddress(vLog.Data[32:64])

	return &NewPairEvent{
		Token0:    token0,
		Token1:    token1,
		PairAddr:  poolAddr,
		Factory:   vLog.Address,
		Fee:       uint32(fee),
		BlockNum:  vLog.BlockNumber,
		TxHash:    vLog.TxHash,
		Timestamp: time.Now(),
	}
}

// AddFactory 添加要监听的工厂合约
func (l *EventListener) AddFactory(factory common.Address) {
	l.config.Factories = append(l.config.Factories, factory)
}

// AddFactories 批量添加工厂合约
func (l *EventListener) AddFactories(factories []common.Address) {
	l.config.Factories = append(l.config.Factories, factories...)
}

// GetEventChan 获取事件通道
func (l *EventListener) GetEventChan() <-chan *NewPairEvent {
	return l.eventCh
}

// IsRunning 是否正在运行
func (l *EventListener) IsRunning() bool {
	l.runningMu.RLock()
	defer l.runningMu.RUnlock()
	return l.running
}

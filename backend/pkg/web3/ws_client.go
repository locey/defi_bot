package web3

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

// Uniswap V2/V3 事件签名
var (
	// Uniswap V2 Sync 事件: Sync(uint112 reserve0, uint112 reserve1)
	SyncEventSignature = crypto.Keccak256Hash([]byte("Sync(uint112,uint112)"))
	// Uniswap V2 Swap 事件
	SwapV2EventSignature = crypto.Keccak256Hash([]byte("Swap(address,uint256,uint256,uint256,uint256,address)"))
	// Uniswap V3 Swap 事件
	SwapV3EventSignature = crypto.Keccak256Hash([]byte("Swap(address,address,int256,int256,uint160,uint128,int24)"))
)

// SyncEvent Uniswap V2 Sync 事件数据
type SyncEvent struct {
	PairAddress common.Address
	Reserve0    *big.Int
	Reserve1    *big.Int
	BlockNumber uint64
	TxHash      common.Hash
	Timestamp   time.Time
}

// SwapEvent 大额 Swap 事件数据（用于鲸鱼交易检测）
type SwapEvent struct {
	PoolAddress common.Address
	AmountIn    *big.Int // 输入金额（绝对值）
	AmountOut   *big.Int // 输出金额（绝对值）
	IsV3        bool
	BlockNumber uint64
	TxHash      common.Hash
	Timestamp   time.Time
}

// PriceUpdateCallback 价格更新回调函数
type PriceUpdateCallback func(event *SyncEvent)

// SwapCallback 大额 Swap 回调函数
type SwapCallback func(event *SwapEvent)

// NewBlockCallback 新区块回调函数
type NewBlockCallback func(header *types.Header)

// WSClient WebSocket 客户端（用于实时订阅）
type WSClient struct {
	wsURL       string
	client      *ethclient.Client
	chainID     *big.Int
	isConnected bool

	// 订阅管理
	subscriptions map[string]ethereum.Subscription
	subMutex      sync.RWMutex

	// 回调函数
	priceCallbacks []PriceUpdateCallback
	swapCallbacks  []SwapCallback
	blockCallbacks []NewBlockCallback
	callbackMutex  sync.RWMutex

	// 监控的池地址
	watchedPools map[common.Address]bool
	poolMutex    sync.RWMutex

	// 上下文控制
	ctx    context.Context
	cancel context.CancelFunc
}

// NewWSClient 创建新的 WebSocket 客户端
func NewWSClient(wsURL string, chainID int64) (*WSClient, error) {
	if wsURL == "" {
		return nil, fmt.Errorf("WebSocket URL 不能为空")
	}

	ctx, cancel := context.WithCancel(context.Background())

	ws := &WSClient{
		wsURL:          wsURL,
		chainID:        big.NewInt(chainID),
		subscriptions:  make(map[string]ethereum.Subscription),
		priceCallbacks: make([]PriceUpdateCallback, 0),
		swapCallbacks:  make([]SwapCallback, 0),
		blockCallbacks: make([]NewBlockCallback, 0),
		watchedPools:   make(map[common.Address]bool),
		ctx:            ctx,
		cancel:         cancel,
	}

	return ws, nil
}

// Connect 连接到 WebSocket 节点
func (ws *WSClient) Connect() error {
	ctx, cancel := context.WithTimeout(ws.ctx, 30*time.Second)
	defer cancel()

	client, err := ethclient.DialContext(ctx, ws.wsURL)
	if err != nil {
		return fmt.Errorf("连接 WebSocket 失败: %w", err)
	}

	// 验证连接
	_, err = client.ChainID(ctx)
	if err != nil {
		client.Close()
		return fmt.Errorf("WebSocket 连接验证失败: %w", err)
	}

	ws.client = client
	ws.isConnected = true
	log.Web3().Info().Str("url", ws.wsURL).Msg("✅ WebSocket 连接成功")

	return nil
}

// IsConnected 检查是否已连接
func (ws *WSClient) IsConnected() bool {
	return ws.isConnected && ws.client != nil
}

// AddWatchedPool 添加要监控的池地址
func (ws *WSClient) AddWatchedPool(poolAddress common.Address) {
	ws.poolMutex.Lock()
	defer ws.poolMutex.Unlock()
	ws.watchedPools[poolAddress] = true
}

// AddWatchedPools 批量添加要监控的池地址
func (ws *WSClient) AddWatchedPools(poolAddresses []common.Address) {
	ws.poolMutex.Lock()
	defer ws.poolMutex.Unlock()
	for _, addr := range poolAddresses {
		ws.watchedPools[addr] = true
	}
}

// OnPriceUpdate 注册价格更新回调
func (ws *WSClient) OnPriceUpdate(callback PriceUpdateCallback) {
	ws.callbackMutex.Lock()
	defer ws.callbackMutex.Unlock()
	ws.priceCallbacks = append(ws.priceCallbacks, callback)
}

// OnNewBlock 注册新区块回调
func (ws *WSClient) OnNewBlock(callback NewBlockCallback) {
	ws.callbackMutex.Lock()
	defer ws.callbackMutex.Unlock()
	ws.blockCallbacks = append(ws.blockCallbacks, callback)
}

// SubscribeNewBlocks 订阅新区块
func (ws *WSClient) SubscribeNewBlocks() error {
	if !ws.IsConnected() {
		if err := ws.Connect(); err != nil {
			return err
		}
	}

	headers := make(chan *types.Header)
	sub, err := ws.client.SubscribeNewHead(ws.ctx, headers)
	if err != nil {
		return fmt.Errorf("订阅新区块失败: %w", err)
	}

	ws.subMutex.Lock()
	ws.subscriptions["newHeads"] = sub
	ws.subMutex.Unlock()

	// 启动处理 goroutine
	go ws.handleNewBlocks(headers, sub)

	log.Web3().Info().Msg("✅ 已订阅新区块")
	return nil
}

// handleNewBlocks 处理新区块
func (ws *WSClient) handleNewBlocks(headers chan *types.Header, sub ethereum.Subscription) {
	for {
		select {
		case <-ws.ctx.Done():
			return
		case err := <-sub.Err():
			log.Web3().Warn().Err(err).Msg("⚠️ 新区块订阅错误")
			// 尝试重连
			go ws.reconnect()
			return
		case header := <-headers:
			ws.callbackMutex.RLock()
			for _, callback := range ws.blockCallbacks {
				go callback(header)
			}
			ws.callbackMutex.RUnlock()
		}
	}
}

// SubscribeSyncEvents 订阅 Sync 事件（Uniswap V2 储备量变化）
func (ws *WSClient) SubscribeSyncEvents() error {
	if !ws.IsConnected() {
		if err := ws.Connect(); err != nil {
			return err
		}
	}

	ws.poolMutex.RLock()
	poolAddresses := make([]common.Address, 0, len(ws.watchedPools))
	for addr := range ws.watchedPools {
		poolAddresses = append(poolAddresses, addr)
	}
	ws.poolMutex.RUnlock()

	if len(poolAddresses) == 0 {
		return fmt.Errorf("没有要监控的池地址，请先调用 AddWatchedPool")
	}

	// 构建过滤器：同时监听 Sync + Swap 事件
	query := ethereum.FilterQuery{
		Addresses: poolAddresses,
		Topics: [][]common.Hash{
			{SyncEventSignature, SwapV2EventSignature, SwapV3EventSignature},
		},
	}

	logs := make(chan types.Log)
	sub, err := ws.client.SubscribeFilterLogs(ws.ctx, query, logs)
	if err != nil {
		return fmt.Errorf("订阅 Sync 事件失败: %w", err)
	}

	ws.subMutex.Lock()
	ws.subscriptions["syncEvents"] = sub
	ws.subMutex.Unlock()

	// 启动处理 goroutine
	go ws.handleSyncEvents(logs, sub)

	log.Web3().Info().Int("count", len(poolAddresses)).Msg("✅ 已订阅池的 Sync 事件")
	return nil
}

// handleSyncEvents 处理 Sync + Swap 事件
func (ws *WSClient) handleSyncEvents(logs chan types.Log, sub ethereum.Subscription) {
	for {
		select {
		case <-ws.ctx.Done():
			return
		case err := <-sub.Err():
			log.Web3().Warn().Err(err).Msg("⚠️ 事件订阅错误")
			go ws.reconnect()
			return
		case vLog := <-logs:
			if len(vLog.Topics) == 0 {
				continue
			}
			switch vLog.Topics[0] {
			case SyncEventSignature:
				event := ws.parseSyncEvent(vLog)
				if event != nil {
					ws.callbackMutex.RLock()
					for _, callback := range ws.priceCallbacks {
						go callback(event)
					}
					ws.callbackMutex.RUnlock()
				}
			case SwapV2EventSignature, SwapV3EventSignature:
				swapEvent := ws.parseSwapEvent(vLog)
				if swapEvent != nil {
					ws.callbackMutex.RLock()
					for _, callback := range ws.swapCallbacks {
						go callback(swapEvent)
					}
					ws.callbackMutex.RUnlock()
				}
			}
		}
	}
}

// parseSyncEvent 解析 Sync 事件
func (ws *WSClient) parseSyncEvent(vLog types.Log) *SyncEvent {
	if len(vLog.Data) < 64 {
		return nil
	}

	// Sync 事件数据: reserve0 (uint112) + reserve1 (uint112)
	// 在 EVM 中都是 32 字节对齐
	reserve0 := new(big.Int).SetBytes(vLog.Data[0:32])
	reserve1 := new(big.Int).SetBytes(vLog.Data[32:64])

	return &SyncEvent{
		PairAddress: vLog.Address,
		Reserve0:    reserve0,
		Reserve1:    reserve1,
		BlockNumber: vLog.BlockNumber,
		TxHash:      vLog.TxHash,
		Timestamp:   time.Now(),
	}
}

// parseSwapEvent 解析 V2/V3 Swap 事件（提取交易金额）
func (ws *WSClient) parseSwapEvent(vLog types.Log) *SwapEvent {
	isV3 := vLog.Topics[0] == SwapV3EventSignature
	event := &SwapEvent{
		PoolAddress: vLog.Address,
		IsV3:        isV3,
		BlockNumber: vLog.BlockNumber,
		TxHash:      vLog.TxHash,
		Timestamp:   time.Now(),
	}

	if isV3 {
		// V3 Swap(address sender, address recipient, int256 amount0, int256 amount1, uint160 sqrtPriceX96, uint128 liquidity, int24 tick)
		if len(vLog.Data) < 160 {
			return nil
		}
		amount0 := new(big.Int).SetBytes(vLog.Data[0:32])
		amount1 := new(big.Int).SetBytes(vLog.Data[32:64])
		// int256 两补码：如果最高位为 1，则为负数
		if vLog.Data[0]&0x80 != 0 {
			amount0.Sub(amount0, new(big.Int).Lsh(big.NewInt(1), 256))
		}
		if vLog.Data[32]&0x80 != 0 {
			amount1.Sub(amount1, new(big.Int).Lsh(big.NewInt(1), 256))
		}
		event.AmountIn = new(big.Int).Abs(amount0)
		event.AmountOut = new(big.Int).Abs(amount1)
	} else {
		// V2 Swap(address sender, uint amount0In, uint amount1In, uint amount0Out, uint amount1Out, address to)
		if len(vLog.Data) < 128 {
			return nil
		}
		amount0In := new(big.Int).SetBytes(vLog.Data[0:32])
		amount1In := new(big.Int).SetBytes(vLog.Data[32:64])
		amount0Out := new(big.Int).SetBytes(vLog.Data[64:96])
		amount1Out := new(big.Int).SetBytes(vLog.Data[96:128])
		// 取较大的输入/输出
		if amount0In.Cmp(amount1In) > 0 {
			event.AmountIn = amount0In
			event.AmountOut = amount1Out
		} else {
			event.AmountIn = amount1In
			event.AmountOut = amount0Out
		}
	}
	return event
}

// OnSwap 注册 Swap 事件回调（用于鲸鱼交易检测）
func (ws *WSClient) OnSwap(callback SwapCallback) {
	ws.callbackMutex.Lock()
	defer ws.callbackMutex.Unlock()
	ws.swapCallbacks = append(ws.swapCallbacks, callback)
}

// reconnect 重新连接
func (ws *WSClient) reconnect() {
	ws.subMutex.Lock()
	// 清理旧订阅
	for name, sub := range ws.subscriptions {
		sub.Unsubscribe()
		delete(ws.subscriptions, name)
	}
	ws.subMutex.Unlock()

	if ws.client != nil {
		ws.client.Close()
	}
	ws.isConnected = false

	// 重试连接
	for i := 0; i < 5; i++ {
		time.Sleep(time.Duration(i+1) * 2 * time.Second)
		log.Web3().Info().Int("retry", i+1).Msg("🔄 尝试重新连接 WebSocket...")

		if err := ws.Connect(); err != nil {
			log.Web3().Warn().Err(err).Msg("⚠️ 重连失败")
			continue
		}

		// 重新订阅
		if err := ws.SubscribeNewBlocks(); err != nil {
			log.Web3().Warn().Err(err).Msg("⚠️ 重新订阅新区块失败")
			continue
		}

		if len(ws.watchedPools) > 0 {
			if err := ws.SubscribeSyncEvents(); err != nil {
				log.Web3().Warn().Err(err).Msg("⚠️ 重新订阅 Sync 事件失败")
				continue
			}
		}

		log.Web3().Info().Msg("✅ WebSocket 重连成功")
		return
	}

	log.Web3().Error().Msg("❌ WebSocket 重连失败，已达到最大重试次数")
}

// GetClient 获取底层客户端
func (ws *WSClient) GetClient() *ethclient.Client {
	return ws.client
}

// Close 关闭 WebSocket 连接
func (ws *WSClient) Close() {
	ws.cancel()

	ws.subMutex.Lock()
	for _, sub := range ws.subscriptions {
		sub.Unsubscribe()
	}
	ws.subscriptions = make(map[string]ethereum.Subscription)
	ws.subMutex.Unlock()

	if ws.client != nil {
		ws.client.Close()
	}
	ws.isConnected = false

	log.Web3().Info().Msg("WebSocket 连接已关闭")
}

// GetWatchedPoolCount 获取监控的池数量
func (ws *WSClient) GetWatchedPoolCount() int {
	ws.poolMutex.RLock()
	defer ws.poolMutex.RUnlock()
	return len(ws.watchedPools)
}

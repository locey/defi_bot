// internal/collector/fast_collector.go
// 高性能数据采集器，集成WebSocket事件订阅和Multicall批量查询
package collector

import (
	"context"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/pkg/cache"
	"github.com/defi-bot/backend/pkg/log"
	"github.com/defi-bot/backend/pkg/web3"
	"github.com/ethereum/go-ethereum/common"
)

// TierConfig 分层配置
type TierConfig struct {
	MinTVL       float64       // 最小TVL ($)
	MinVolume24h float64       // 最小24h交易量 ($)
	Method       string        // "websocket" 或 "multicall"
	Interval     time.Duration // 轮询间隔（仅multicall）
}

// FastCollectorConfig 采集器配置
type FastCollectorConfig struct {
	Tier1 TierConfig
	Tier2 TierConfig
	Tier3 TierConfig
	// CoreTokens 核心代币地址（用于分层判断）。如果为空则使用内置默认列表。
	CoreTokens []common.Address
}

// PoolTier 池子分层信息
type PoolTier struct {
	PoolAddress string
	Token0      common.Address
	Token1      common.Address
	Decimals0   uint8 // Token0 精度
	Decimals1   uint8 // Token1 精度
	DexName     string
	Protocol    string
	Fee         uint64
	TVL         float64
	Volume24h   float64
	Tier        int // 1, 2, 3
}

// FastCollector 高性能采集器
type FastCollector struct {
	// 依赖组件
	wsClient   *web3.WSClient    // WebSocket客户端（Tier1事件订阅）
	multicall  *web3.Multicall   // Multicall批量查询（Tier2/3）
	priceCache *cache.PriceCache // 内存价格缓存

	// 分层池子
	mu         sync.RWMutex
	tier1Pools []*PoolTier // ~50个，WebSocket订阅
	tier2Pools []*PoolTier // ~500个，1秒Multicall
	tier3Pools []*PoolTier // ~2000个，5分钟Multicall

	// 配置
	config *FastCollectorConfig

	// 状态
	running bool
	stopCh  chan struct{}

	// 鲸鱼交易检测
	whaleCallbacks []WhaleSwapCallback
	whaleThreshold *big.Int // 大额 swap 阈值（默认 1 ETH = 1e18 wei）

	// 统计
	stats   CollectorStats
	statsMu sync.RWMutex
}

// WhaleSwapCallback 鲸鱼交易回调（通知外部系统立即检测套利机会）
type WhaleSwapCallback func(poolAddr string, amountWei *big.Int, isV3 bool)

// CollectorStats 采集器统计
type CollectorStats struct {
	Tier1Updates  uint64
	Tier2Updates  uint64
	Tier3Updates  uint64
	TotalUpdates  uint64
	FailedUpdates uint64
	LastTier1Time time.Time
	LastTier2Time time.Time
	LastTier3Time time.Time
}

// NewFastCollector 创建高性能采集器
func NewFastCollector(
	wsClient *web3.WSClient,
	multicall *web3.Multicall,
	priceCache *cache.PriceCache,
	config *FastCollectorConfig,
) *FastCollector {
	defaults := defaultFastCollectorConfig()
	if config == nil {
		config = defaults
	} else {
		// 保留调用者设置的 CoreTokens，其余用默认值补齐
		if config.Tier1.Method == "" {
			config.Tier1 = defaults.Tier1
		}
		if config.Tier2.Method == "" {
			config.Tier2 = defaults.Tier2
		}
		if config.Tier3.Method == "" {
			config.Tier3 = defaults.Tier3
		}
	}

	return &FastCollector{
		wsClient:   wsClient,
		multicall:  multicall,
		priceCache: priceCache,
		tier1Pools: make([]*PoolTier, 0),
		tier2Pools: make([]*PoolTier, 0),
		tier3Pools: make([]*PoolTier, 0),
		config:     config,
		stopCh:     make(chan struct{}),
	}
}

// defaultFastCollectorConfig 默认配置
func defaultFastCollectorConfig() *FastCollectorConfig {
	return &FastCollectorConfig{
		Tier1: TierConfig{
			MinTVL:       10000000, // $10M
			MinVolume24h: 5000000,  // $5M
			Method:       "websocket",
			Interval:     0,
		},
		Tier2: TierConfig{
			MinTVL:       500000, // $500K
			MinVolume24h: 100000, // $100K
			Method:       "multicall",
			Interval:     2 * time.Second, // 2秒（平衡速度与 RPC 限流，1s 导致 429）
		},
		Tier3: TierConfig{
			MinTVL:       50000, // $50K
			MinVolume24h: 10000, // $10K
			Method:       "multicall",
			Interval:     5 * time.Minute, // 5分钟
		},
	}
}

// Start 启动采集器
func (c *FastCollector) Start(ctx context.Context) error {
	if c.running {
		return nil
	}
	c.running = true

	log.Collector().Info().Msg("FastCollector: Starting high-performance collector...")

	// 1. 从数据库加载池子并分层
	if err := c.LoadAndClassifyPools(); err != nil {
		return err
	}

	// 2. 启动Tier1 WebSocket订阅
	if c.wsClient != nil && len(c.tier1Pools) > 0 {
		go c.startTier1Subscription(ctx)
	}

	// 3. 启动Tier2轮询
	if len(c.tier2Pools) > 0 {
		go c.startTier2Polling(ctx)
	}

	// 4. 启动Tier3轮询
	if len(c.tier3Pools) > 0 {
		go c.startTier3Polling(ctx)
	}

	log.Collector().Info().Int("tier1", len(c.tier1Pools)).Int("tier2", len(c.tier2Pools)).Int("tier3", len(c.tier3Pools)).Msgf("FastCollector: Started with Tier1=%d, Tier2=%d, Tier3=%d pools",
		len(c.tier1Pools), len(c.tier2Pools), len(c.tier3Pools))

	return nil
}

// Stop 停止采集器
func (c *FastCollector) Stop() {
	if !c.running {
		return
	}
	c.running = false
	close(c.stopCh)
	log.Collector().Info().Msg("FastCollector: Stopped")
}

// LoadAndClassifyPools 从数据库加载池子并分层
func (c *FastCollector) LoadAndClassifyPools() error {
	db := database.GetDB()
	if db == nil {
		log.Collector().Warn().Msg("FastCollector: Database not available, using empty pool list")
		return nil
	}

	// 查询所有活跃的交易对（包含 token decimals）
	type PoolRow struct {
		PairAddress    string `gorm:"column:pair_address"`
		Token0Address  string `gorm:"column:token0_address"`
		Token1Address  string `gorm:"column:token1_address"`
		Token0Decimals int    `gorm:"column:token0_decimals"`
		Token1Decimals int    `gorm:"column:token1_decimals"`
		DexName        string `gorm:"column:dex_name"`
		Protocol       string `gorm:"column:protocol"`
		Fee            int    `gorm:"column:fee"`
	}

	var pools []PoolRow
	err := db.Table("trading_pairs tp").
		Select(`
			tp.pair_address,
			t0.address as token0_address,
			t1.address as token1_address,
			t0.decimals as token0_decimals,
			t1.decimals as token1_decimals,
			ex.name as dex_name,
			ex.protocol as protocol,
			ex.fee as fee
		`).
		Joins("JOIN exchanges ex ON ex.id = tp.exchange_id").
		Joins("JOIN tokens t0 ON t0.id = tp.token0_id").
		Joins("JOIN tokens t1 ON t1.id = tp.token1_id").
		Where("tp.is_active = true").
		Where("tp.pair_address <> ''").
		Scan(&pools).Error

	if err != nil {
		log.Collector().Error().Err(err).Msg("FastCollector: Load pools from DB failed")
		// 返回空列表而不是错误，允许后续手动添加
		return nil
	}

	// 分层
	c.mu.Lock()
	defer c.mu.Unlock()

	c.tier1Pools = make([]*PoolTier, 0)
	c.tier2Pools = make([]*PoolTier, 0)
	c.tier3Pools = make([]*PoolTier, 0)

	// 主流代币地址（用于分层优先级判断）
	coreTokens := make(map[string]bool)
	if len(c.config.CoreTokens) > 0 {
		// 使用配置中传入的核心代币（支持多链）
		for _, addr := range c.config.CoreTokens {
			coreTokens[strings.ToLower(addr.Hex())] = true
		}
	} else {
		// 默认 Arbitrum 核心代币
		for _, addr := range []string{
			"0x82aF49447D8a07e3bd95BD0d56f35241523fBab1", // WETH (Arbitrum)
			"0xaf88d065e77c8cC2239327C5EDb3A432268e5831", // USDC
			"0xFF970A61A04b1cA14834A43f5dE4533eBDDB5CC8", // USDCe
			"0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9", // USDT
			"0x2f2a2543B76A4166549F7aaB2e75Bef0aefC5B0f", // WBTC
			"0x912CE59144191C1204E64559FE8253a0e49E6548", // ARB
		} {
			coreTokens[strings.ToLower(addr)] = true
		}
	}

	for _, p := range pools {
		pool := &PoolTier{
			PoolAddress: p.PairAddress,
			Token0:      common.HexToAddress(p.Token0Address),
			Token1:      common.HexToAddress(p.Token1Address),
			Decimals0:   uint8(p.Token0Decimals),
			Decimals1:   uint8(p.Token1Decimals),
			DexName:     p.DexName,
			Protocol:    p.Protocol,
			Fee:         uint64(p.Fee),
		}

		// 智能分层：
		// Tier1 (WebSocket 实时): 主流代币对（两个代币都是核心代币）→ 延迟 ~100ms
		// Tier2 (Multicall 3s):  至少一个核心代币的池子 → 延迟 ~3s
		t0Core := coreTokens[strings.ToLower(p.Token0Address)]
		t1Core := coreTokens[strings.ToLower(p.Token1Address)]

		if t0Core && t1Core && len(c.tier1Pools) < 80 {
			// 两个都是核心代币 → Tier1（WebSocket 实时推送）
			pool.Tier = 1
			c.tier1Pools = append(c.tier1Pools, pool)
		} else if (t0Core || t1Core) && len(c.tier2Pools) < 100 {
			// 至少一个核心代币 → Tier2
			pool.Tier = 2
			c.tier2Pools = append(c.tier2Pools, pool)
		}
		// 其余忽略（减少 RPC 负载）
	}

	// 初始化价格缓存索引（包含 decimals）
	allPrices := make([]*cache.PoolPrice, 0, len(c.tier1Pools)+len(c.tier2Pools)+len(c.tier3Pools))
	for _, pool := range append(append(c.tier1Pools, c.tier2Pools...), c.tier3Pools...) {
		allPrices = append(allPrices, &cache.PoolPrice{
			PoolAddress: pool.PoolAddress,
			Token0:      pool.Token0,
			Token1:      pool.Token1,
			Decimals0:   pool.Decimals0,
			Decimals1:   pool.Decimals1,
			DexName:     pool.DexName,
			Protocol:    pool.Protocol,
			Fee:         pool.Fee,
			IsV3:        isV3Protocol(pool.Protocol),
		})
	}
	c.priceCache.BuildIndex(allPrices)

	log.Collector().Info().Int("total", len(pools)).Int("tier1", len(c.tier1Pools)).Int("tier2", len(c.tier2Pools)).Int("tier3", len(c.tier3Pools)).Msg("FastCollector: Pools classified")

	return nil
}

// startTier1Subscription 启动Tier1 WebSocket订阅
func (c *FastCollector) startTier1Subscription(ctx context.Context) {
	// 连接WebSocket
	if !c.wsClient.IsConnected() {
		if err := c.wsClient.Connect(); err != nil {
			log.Collector().Error().Err(err).Msg("FastCollector: WebSocket connect failed")
			// 降级到Multicall
			c.fallbackTier1ToMulticall(ctx)
			return
		}
	}

	// 添加监控池
	c.mu.RLock()
	for _, pool := range c.tier1Pools {
		c.wsClient.AddWatchedPool(common.HexToAddress(pool.PoolAddress))
	}
	c.mu.RUnlock()

	// 注册价格更新回调
	c.wsClient.OnPriceUpdate(func(event *web3.SyncEvent) {
		c.handleSyncEvent(event)
	})

	// 注册鲸鱼 Swap 检测回调
	if c.whaleThreshold == nil {
		c.whaleThreshold = new(big.Int).SetUint64(1_000_000_000_000_000_000) // 1 ETH
	}
	c.wsClient.OnSwap(func(event *web3.SwapEvent) {
		c.handleSwapEvent(event)
	})

	// 订阅 Sync + Swap 事件
	if err := c.wsClient.SubscribeSyncEvents(); err != nil {
		log.Collector().Error().Err(err).Msg("FastCollector: Subscribe Sync events failed")
		c.fallbackTier1ToMulticall(ctx)
		return
	}

	log.Collector().Info().Int("pools", len(c.tier1Pools)).Msg("FastCollector: Tier1 WebSocket subscription started")
}

// handleSyncEvent 处理Sync事件
func (c *FastCollector) handleSyncEvent(event *web3.SyncEvent) {
	// 更新价格缓存
	c.priceCache.Update(
		event.PairAddress.Hex(),
		event.Reserve0,
		event.Reserve1,
		event.BlockNumber,
	)

	// 更新统计
	c.statsMu.Lock()
	c.stats.Tier1Updates++
	c.stats.TotalUpdates++
	c.stats.LastTier1Time = time.Now()
	c.statsMu.Unlock()
}

// handleSwapEvent 处理 Swap 事件（鲸鱼交易检测）
func (c *FastCollector) handleSwapEvent(event *web3.SwapEvent) {
	// 只关注大额交易（超过阈值）
	if event.AmountIn == nil || event.AmountIn.Cmp(c.whaleThreshold) < 0 {
		return
	}

	poolAddr := event.PoolAddress.Hex()
	log.Collector().Info().
		Str("pool", poolAddr[:14]).
		Str("amount", event.AmountIn.String()).
		Bool("v3", event.IsV3).
		Msg("🐋 Whale swap detected")

	c.statsMu.Lock()
	c.stats.TotalUpdates++
	c.statsMu.Unlock()

	// 通知所有注册的回调
	for _, cb := range c.whaleCallbacks {
		go cb(poolAddr, event.AmountIn, event.IsV3)
	}
}

// OnWhaleSwap 注册鲸鱼交易回调
func (c *FastCollector) OnWhaleSwap(callback WhaleSwapCallback) {
	c.whaleCallbacks = append(c.whaleCallbacks, callback)
}

// SetWhaleThreshold 设置鲸鱼交易阈值（wei）
func (c *FastCollector) SetWhaleThreshold(threshold *big.Int) {
	c.whaleThreshold = threshold
}

// fallbackTier1ToMulticall Tier1降级到Multicall
func (c *FastCollector) fallbackTier1ToMulticall(ctx context.Context) {
	log.Collector().Warn().Msg("FastCollector: Tier1 falling back to Multicall polling")

	ticker := time.NewTicker(time.Second) // 1秒轮询
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.pollTier1()
		}
	}
}

// pollTier1 轮询Tier1池子
func (c *FastCollector) pollTier1() {
	c.mu.RLock()
	pools := make([]string, len(c.tier1Pools))
	for i, p := range c.tier1Pools {
		pools[i] = p.PoolAddress
	}
	c.mu.RUnlock()

	c.pollPools(pools, 1)
}

// startTier2Polling 启动Tier2轮询
func (c *FastCollector) startTier2Polling(ctx context.Context) {
	ticker := time.NewTicker(c.config.Tier2.Interval)
	defer ticker.Stop()

	// 立即执行一次
	c.pollTier2()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.pollTier2()
		}
	}
}

// pollTier2 轮询Tier2池子
func (c *FastCollector) pollTier2() {
	c.mu.RLock()
	pools := make([]string, len(c.tier2Pools))
	for i, p := range c.tier2Pools {
		pools[i] = p.PoolAddress
	}
	c.mu.RUnlock()

	c.pollPools(pools, 2)
}

// startTier3Polling 启动Tier3轮询
func (c *FastCollector) startTier3Polling(ctx context.Context) {
	ticker := time.NewTicker(c.config.Tier3.Interval)
	defer ticker.Stop()

	// 立即执行一次
	c.pollTier3()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.pollTier3()
		}
	}
}

// pollTier3 轮询Tier3池子
func (c *FastCollector) pollTier3() {
	c.mu.RLock()
	pools := make([]string, len(c.tier3Pools))
	for i, p := range c.tier3Pools {
		pools[i] = p.PoolAddress
	}
	c.mu.RUnlock()

	c.pollPools(pools, 3)
}

// pollPools 使用Multicall批量轮询池子（自动区分V2和V3）
func (c *FastCollector) pollPools(pools []string, tier int) {
	if len(pools) == 0 || c.multicall == nil {
		return
	}

	// 分类池子
	var v2Pools, v3Pools []string

	c.mu.RLock()
	var allPools []*PoolTier
	switch tier {
	case 1:
		allPools = c.tier1Pools
	case 2:
		allPools = c.tier2Pools
	case 3:
		allPools = c.tier3Pools
	}

	// 创建地址到池子信息的映射
	poolMap := make(map[string]*PoolTier)
	for _, p := range allPools {
		poolMap[p.PoolAddress] = p
	}
	c.mu.RUnlock()

	for _, addr := range pools {
		pool := poolMap[addr]
		if pool == nil {
			continue
		}
		if isV3Protocol(pool.Protocol) {
			v3Pools = append(v3Pools, addr)
		} else {
			v2Pools = append(v2Pools, addr)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	startTime := time.Now()
	var successCount int

	// 处理 V2 池子
	if len(v2Pools) > 0 {
		v2Results, err := c.multicall.GetReservesBatch(ctx, v2Pools)
		if err != nil {
			log.Collector().Error().Int("tier", tier).Err(err).Msg("FastCollector: V2 Multicall failed")
		} else {
			for _, result := range v2Results {
				if result.Success && result.Reserve0 != nil && result.Reserve1 != nil {
					c.priceCache.Update(
						result.PoolAddress,
						result.Reserve0,
						result.Reserve1,
						0,
					)
					successCount++
				}
			}
		}
	}

	// 处理 V3 池子
	if len(v3Pools) > 0 {
		v3Results, err := c.multicall.GetV3SlotBatch(ctx, v3Pools)
		if err != nil {
			log.Collector().Error().Int("tier", tier).Err(err).Msg("FastCollector: V3 Multicall failed")
		} else {
			for _, result := range v3Results {
				if result.Success && result.SqrtPriceX96 != nil && result.SqrtPriceX96.Sign() > 0 {
					// 使用 V3 专用的更新方法
					c.priceCache.UpdateV3(
						result.PoolAddress,
						result.SqrtPriceX96,
						result.Liquidity,
						0,
					)
					successCount++
				}
			}
		}
	}

	// 首次轮询完成后打印日志
	log.Collector().Info().
		Int("tier", tier).
		Int("success", successCount).
		Int("total", len(pools)).
		Int("v2", len(v2Pools)).
		Int("v3", len(v3Pools)).
		Msg("FastCollector: Tier polling complete")

	// 更新统计
	c.statsMu.Lock()
	switch tier {
	case 1:
		c.stats.Tier1Updates += uint64(successCount)
		c.stats.LastTier1Time = time.Now()
	case 2:
		c.stats.Tier2Updates += uint64(successCount)
		c.stats.LastTier2Time = time.Now()
	case 3:
		c.stats.Tier3Updates += uint64(successCount)
		c.stats.LastTier3Time = time.Now()
	}
	c.stats.TotalUpdates += uint64(successCount)
	c.stats.FailedUpdates += uint64(len(pools) - successCount)
	c.statsMu.Unlock()

	duration := time.Since(startTime)
	log.Collector().Debug().Int("tier", tier).Int("success", successCount).Int("total", len(pools)).Dur("duration", duration).Msg("FastCollector: Tier polling complete")
}

// isV3Protocol 判断是否为 V3 协议
func isV3Protocol(protocol string) bool {
	return protocol == "uniswap_v3" ||
		protocol == "pancakeswap_v3" ||
		protocol == "sushiswap_v3" ||
		strings.Contains(strings.ToLower(protocol), "_v3") ||
		strings.Contains(strings.ToLower(protocol), "v3")
}

// AddPool 手动添加池子到监控
func (c *FastCollector) AddPool(pool *PoolTier) {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch pool.Tier {
	case 1:
		c.tier1Pools = append(c.tier1Pools, pool)
		if c.wsClient != nil && c.wsClient.IsConnected() {
			c.wsClient.AddWatchedPool(common.HexToAddress(pool.PoolAddress))
		}
	case 2:
		c.tier2Pools = append(c.tier2Pools, pool)
	case 3:
		c.tier3Pools = append(c.tier3Pools, pool)
	}

	// 添加到价格缓存
	c.priceCache.UpdateWithMetadata(&cache.PoolPrice{
		PoolAddress: pool.PoolAddress,
		Token0:      pool.Token0,
		Token1:      pool.Token1,
		DexName:     pool.DexName,
		Protocol:    pool.Protocol,
		Fee:         pool.Fee,
	})
}

// GetStats 获取统计信息
func (c *FastCollector) GetStats() CollectorStats {
	c.statsMu.RLock()
	defer c.statsMu.RUnlock()
	return c.stats
}

// GetPoolCounts 获取各层池子数量
func (c *FastCollector) GetPoolCounts() (tier1, tier2, tier3 int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.tier1Pools), len(c.tier2Pools), len(c.tier3Pools)
}

// ============================================================
// 工厂方法
// ============================================================

// CreateFastCollector 创建FastCollector的工厂方法
func CreateFastCollector(
	rpcClient *web3.Client,
	wsURL string,
	chainID int64,
	config *FastCollectorConfig,
) (*FastCollector, error) {
	// 创建价格缓存
	priceCache := cache.NewPriceCache(0.0001, 1000)

	// 创建WebSocket客户端
	var wsClient *web3.WSClient
	if wsURL != "" {
		var err error
		wsClient, err = web3.NewWSClient(wsURL, chainID)
		if err != nil {
			log.Collector().Error().Err(err).Msg("FastCollector: Failed to create WebSocket client")
			// 继续，不使用WebSocket
		}
	}

	// 创建Multicall客户端
	multicall, err := web3.NewMulticall(rpcClient, 500, 10*time.Second)
	if err != nil {
		return nil, err
	}

	return NewFastCollector(wsClient, multicall, priceCache, config), nil
}

// ============================================================
// 辅助类型
// ============================================================

// SyncEventToReserves 将Sync事件转换为储备量
func SyncEventToReserves(event *web3.SyncEvent) (*big.Int, *big.Int) {
	return event.Reserve0, event.Reserve1
}

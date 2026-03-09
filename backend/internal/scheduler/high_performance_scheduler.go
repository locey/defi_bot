// internal/scheduler/high_performance_scheduler.go
// 高性能调度器 - 事件驱动架构，替代定时轮询
package scheduler

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/defi-bot/backend/internal/collector"
	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/executor"
	"github.com/defi-bot/backend/internal/metrics"
	"github.com/defi-bot/backend/internal/strategy"
	"github.com/defi-bot/backend/pkg/cache"
	"github.com/defi-bot/backend/pkg/log"
	"github.com/defi-bot/backend/pkg/web3"
	"github.com/ethereum/go-ethereum/common"
	"gorm.io/gorm"
)

// HighPerformanceScheduler 高性能调度器
// 采用事件驱动架构，实现毫秒级套利检测
type HighPerformanceScheduler struct {
	// 核心组件
	priceCache     *cache.PriceCache           // 内存价格缓存
	collector      *collector.FastCollector    // 高速采集器
	detector       *strategy.ArbitrageDetector // 增量检测器（预设路径）
	spreadScanner  *strategy.SpreadScanner     // 动态价差扫描器（自动发现）
	executor       *executor.ArbitrageExecutor // 执行器
	simulator      *executor.Simulator         // eth_call 模拟器

	// 依赖
	db         *gorm.DB
	web3Client *web3.Client
	wsClient   *web3.WSClient
	multicall  *web3.Multicall

	// 配置
	config *HighPerformanceConfig

	// 状态
	running bool
	mu      sync.RWMutex
	ctx     context.Context
	cancel  context.CancelFunc

	// 统计
	stats   SchedulerStats
	statsMu sync.RWMutex

	// 去重：防止同一路径短时间内重复 eth_call
	recentPaths sync.Map // key: dedup key string, value: time.Time

	// RPC 限流：控制 eth_call 调用频率（自适应）
	lastEthCall      time.Time
	ethCallMu        sync.Mutex
	ethCallMinDelay  time.Duration // 自适应最小间隔
	consecutive429   int           // 连续 429 计数

	// Vault 余额缓存：异步更新，避免每次阻塞 eth_call
	vaultBalanceCache sync.Map // asset address hex -> *big.Int
	vaultCacheTime    sync.Map // asset address hex -> time.Time
}

// HighPerformanceConfig 高性能配置
type HighPerformanceConfig struct {
	// 采集器配置
	CollectorConfig *collector.FastCollectorConfig

	// 检测器配置
	DetectorConfig *strategy.DetectorConfig

	// WebSocket配置
	WSURL   string
	ChainID int64

	// 基础代币（套利路径起点）
	BaseTokens []common.Address

	// DEX 名称 → Router 地址映射
	DexRouters map[string]common.Address

	// 执行配置
	MaxConcurrentExecutions int           // 最大并发执行数
	ExecutionTimeout        time.Duration // 执行超时
	MinConfidence           float64       // 最小置信度

	// 功能开关
	EnableExecution bool // 是否启用自动执行
	DryRun          bool // 干运行模式（只检测不执行）

	// 模拟器配置
	ContractAddress  string // ArbitrageCore 合约地址
	KeeperPrivateKey string // Keeper 私钥（用于 eth_call 的 from 地址）
	EnableSimulation bool   // 是否启用 eth_call 模拟验证

	// Flash Loan 配置
	FlashLoanAddress string // FlashLoanArbitrage 合约地址（空则禁用 Flash Loan）
	EnableFlashLoan  bool   // 是否启用 Flash Loan 路径（Vault 不足时自动切换）

	// RPC 池（可选，多 RPC 节点轮询降低 429）
	RPCPool *web3.ClientPool

	// 动态价差扫描器配置
	EnableSpreadScanner bool // 是否启用自动发现跨 DEX 价差（不依赖预设代币列表）
	MinSpreadBps        int  // 触发价差阈值（basis points，默认 30 = 0.3%）
}

// SchedulerStats 调度器统计
type SchedulerStats struct {
	StartTime           time.Time
	OpportunitiesFound  int64
	ExecutionsAttempted int64
	ExecutionsSucceeded int64
	ExecutionsFailed    int64
	TotalProfit         string
	LastOpportunityTime time.Time
	LastExecutionTime   time.Time
}

// NewHighPerformanceScheduler 创建高性能调度器
func NewHighPerformanceScheduler(
	db *gorm.DB,
	web3Client *web3.Client,
	executor *executor.ArbitrageExecutor,
	cfg *HighPerformanceConfig,
) (*HighPerformanceScheduler, error) {
	if cfg == nil {
		cfg = defaultHighPerformanceConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	scheduler := &HighPerformanceScheduler{
		db:         db,
		web3Client: web3Client,
		executor:   executor,
		config:     cfg,
		ctx:        ctx,
		cancel:     cancel,
		stats: SchedulerStats{
			StartTime: time.Now(),
		},
	}

	// 初始化组件
	if err := scheduler.initComponents(); err != nil {
		cancel()
		return nil, fmt.Errorf("init components failed: %w", err)
	}

	return scheduler, nil
}

// defaultHighPerformanceConfig 默认配置
func defaultHighPerformanceConfig() *HighPerformanceConfig {
	return &HighPerformanceConfig{
		CollectorConfig:         nil, // 使用默认
		DetectorConfig:          nil, // 使用默认
		MaxConcurrentExecutions: 3,
		ExecutionTimeout:        30 * time.Second,
		MinConfidence:           0.15,  // 降低阈值，让更多机会到 eth_call（链上模拟做最终裁定）
		EnableExecution:         false, // 默认不自动执行
		DryRun:                  true,  // 默认干运行
	}
}

// initComponents 初始化所有组件
func (s *HighPerformanceScheduler) initComponents() error {
	log.Scheduler().Info().Msg("HighPerformanceScheduler: Initializing components...")

	// 1. 创建内存价格缓存
	s.priceCache = cache.NewPriceCache(0.0001, 1000)
	log.Scheduler().Info().Msg("  ✓ PriceCache initialized")

	// 2. 创建WebSocket客户端（如果配置了）
	if s.config.WSURL != "" {
		var err error
		s.wsClient, err = web3.NewWSClient(s.config.WSURL, s.config.ChainID)
		if err != nil {
			log.Scheduler().Warn().Err(err).Msg("  ⚠ WSClient creation failed (will use polling)")
		} else {
			log.Scheduler().Info().Msg("  ✓ WSClient initialized")
		}
	}

	// 3. 创建Multicall客户端
	var err error
	s.multicall, err = web3.NewMulticall(s.web3Client, 500, 10*time.Second)
	if err != nil {
		return fmt.Errorf("create multicall failed: %w", err)
	}
	log.Scheduler().Info().Msg("  ✓ Multicall initialized")

	// 4. 创建高速采集器
	s.collector = collector.NewFastCollector(
		s.wsClient,
		s.multicall,
		s.priceCache,
		s.config.CollectorConfig,
	)
	log.Scheduler().Info().Msg("  ✓ FastCollector initialized")

	// 5. 创建增量检测器
	s.detector = strategy.NewArbitrageDetector(
		s.priceCache,
		nil, // profitCalc 可选
		s.config.DetectorConfig,
	)
	// 设置 DEX Router 映射
	if s.config.DexRouters != nil {
		s.detector.SetDexRouters(s.config.DexRouters)
		log.Scheduler().Info().Int("dex_count", len(s.config.DexRouters)).Msg("  ✓ DexRouters configured")
	}
	log.Scheduler().Info().Msg("  ✓ ArbitrageDetector initialized")

	// 6. 创建动态价差扫描器（自动发现跨 DEX 套利，不依赖预设代币列表）
	if s.config.EnableSpreadScanner {
		minBps := s.config.MinSpreadBps
		if minBps <= 0 {
			minBps = 30 // 默认 0.3%
		}
		s.spreadScanner = strategy.NewSpreadScanner(s.priceCache, minBps)
		if s.config.DexRouters != nil {
			s.spreadScanner.SetDexRouters(s.config.DexRouters)
		}
		log.Scheduler().Info().Int("min_spread_bps", minBps).Msg("  ✓ SpreadScanner initialized (auto-discovery mode)")
	}

	// 7. 创建 eth_call 模拟器（如果配置了合约地址）
	if s.config.ContractAddress != "" && s.config.EnableSimulation {
		sim, simErr := executor.NewSimulator(
			s.web3Client,
			common.HexToAddress(s.config.ContractAddress),
			s.config.KeeperPrivateKey,
		)
		if simErr != nil {
			log.Scheduler().Warn().Err(simErr).Msg("  ⚠ Simulator creation failed (will skip simulation)")
		} else {
			// 如果有 RPC 池，设置到模拟器（429 时自动轮换节点）
			if s.config.RPCPool != nil {
				sim.SetClientPool(s.config.RPCPool)
				log.Scheduler().Info().Int("rpc_nodes", s.config.RPCPool.GetClientCount()).Msg("  ✓ Simulator RPC pool configured")
			}
			s.simulator = sim
			log.Scheduler().Info().Msg("  ✓ Simulator initialized (eth_call verification enabled)")
		}
	} else {
		log.Scheduler().Info().Msg("  ℹ Simulator not configured (simulation disabled)")
	}

	log.Scheduler().Info().Msg("HighPerformanceScheduler: All components initialized")
	return nil
}

// Start 启动调度器
func (s *HighPerformanceScheduler) Start() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = true
	s.mu.Unlock()

	log.Scheduler().Info().Msg("========================================")
	log.Scheduler().Info().Msg("HighPerformanceScheduler: Starting...")
	log.Scheduler().Info().Msg("========================================")

	// Step 1: 加载池子数据并构建索引
	log.Scheduler().Info().Msg("Step 1: Loading pools and building index...")
	if err := s.collector.LoadAndClassifyPools(); err != nil {
		return fmt.Errorf("load pools failed: %w", err)
	}

	tier1, tier2, tier3 := s.collector.GetPoolCounts()
	log.Scheduler().Info().Int("tier1", tier1).Int("tier2", tier2).Int("tier3", tier3).Msg("  Pools loaded")

	// Step 2: 预计算套利路径
	log.Scheduler().Info().Msg("Step 2: Precomputing arbitrage paths...")
	pools := s.priceCache.GetAll()
	s.detector.PrecomputePaths(pools, s.config.BaseTokens)
	log.Scheduler().Info().Int("paths", s.detector.GetPathCount()).Msg("  Paths computed")

	// Step 3: 启动数据采集器
	log.Scheduler().Info().Msg("Step 3: Starting data collector...")
	if err := s.collector.Start(s.ctx); err != nil {
		return fmt.Errorf("start collector failed: %w", err)
	}

	// Step 4: 启动套利检测器
	log.Scheduler().Info().Msg("Step 4: Starting arbitrage detector...")
	if err := s.detector.Start(s.ctx); err != nil {
		return fmt.Errorf("start detector failed: %w", err)
	}

	// Step 5: 启动执行循环（处理预设路径检测器的机会）
	log.Scheduler().Info().Msg("Step 5: Starting execution loop...")
	go s.executionLoop()

	// Step 5b: 启动动态价差扫描器（自动发现跨 DEX 机会）
	if s.spreadScanner != nil {
		log.Scheduler().Info().Msg("Step 5b: Starting SpreadScanner (auto-discovery)...")
		s.spreadScanner.Start(s.ctx)
		go s.spreadScannerLoop()
	}

	// Step 5c: 注册鲸鱼交易回调（大额 Swap 时立即触发全路径扫描）
	if s.collector != nil {
		s.collector.OnWhaleSwap(func(poolAddr string, amount *big.Int, isV3 bool) {
			log.Scheduler().Info().
				Str("pool", poolAddr[:14]).
				Str("amount", amount.String()).
				Msg("🐋 Whale detected — triggering immediate path scan")
			// 通知 SpreadScanner 立即重扫该池子相关的代币对
			if s.spreadScanner != nil {
				go s.spreadScanner.ScanNow()
			}
		})
		log.Scheduler().Info().Msg("  ✓ Whale swap detection registered")
	}

	// Step 6: 启动统计打印
	go s.statsLoop()

	log.Scheduler().Info().Msg("========================================")
	log.Scheduler().Info().Msg("HighPerformanceScheduler: Started successfully!")
	log.Scheduler().Info().Str("mode", s.getModeString()).Msg("  Mode")
	log.Scheduler().Info().Msg("========================================")

	return nil
}

// Stop 停止调度器
func (s *HighPerformanceScheduler) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	s.mu.Unlock()

	log.Scheduler().Info().Msg("HighPerformanceScheduler: Stopping...")

	// 取消context
	s.cancel()

	// 停止组件
	if s.collector != nil {
		s.collector.Stop()
	}
	if s.detector != nil {
		s.detector.Stop()
	}
	if s.spreadScanner != nil {
		s.spreadScanner.Stop()
	}
	if s.wsClient != nil {
		s.wsClient.Close()
	}

	log.Scheduler().Info().Msg("HighPerformanceScheduler: Stopped")
}

// spreadScannerLoop 处理动态价差扫描器发现的机会
func (s *HighPerformanceScheduler) spreadScannerLoop() {
	if s.spreadScanner == nil {
		return
	}
	semaphore := make(chan struct{}, 2) // 价差机会允许 2 并发

	for {
		select {
		case <-s.ctx.Done():
			return
		case spreadOpp, ok := <-s.spreadScanner.Opportunities():
			if !ok {
				return
			}
			arbOpp := s.convertSpreadOpportunity(spreadOpp)
			if arbOpp != nil {
				go s.handleOpportunity(arbOpp, semaphore)
			}
		}
	}
}

// buildDedupKey 生成路径去重键（token 组合 + dex 组合）
func (s *HighPerformanceScheduler) buildDedupKey(opp *strategy.ArbitrageOpportunity) string {
	var key string
	for _, addr := range opp.SwapPath {
		key += addr.Hex()[:10]
	}
	for _, addr := range opp.Dexes {
		key += addr.Hex()[:10]
	}
	return key
}

// convertSpreadOpportunity 将跨 DEX 价差机会转换为标准套利机会格式
func (s *HighPerformanceScheduler) convertSpreadOpportunity(opp *strategy.SpreadOpportunity) *strategy.ArbitrageOpportunity {
	if opp == nil || len(opp.SwapPath) < 3 || len(opp.DexPath) < 2 {
		return nil
	}

	// 过滤虚假高价差（>10% 的价差几乎都是低流动性池子的噪音）
	if opp.SpreadFloat > 0.10 {
		return nil
	}

	// 过滤同 Router 不同 fee tier 的"伪套利"
	// 同一个 V3 SwapRouter 上不同 fee tier 的价差已被 MEV 压缩到无利可图
	if len(opp.DexPath) >= 2 && opp.DexPath[0] == opp.DexPath[1] {
		return nil
	}

	// 日志包含流动性信息，帮助诊断假机会
	buyLiq := "N/A"
	sellLiq := "N/A"
	if opp.BuyPool.IsV3 && opp.BuyPool.Liquidity != nil {
		buyLiq = opp.BuyPool.Liquidity.String()
	}
	if opp.SellPool.IsV3 && opp.SellPool.Liquidity != nil {
		sellLiq = opp.SellPool.Liquidity.String()
	}
	log.Scheduler().Info().
		Str("token0", opp.Token0.Hex()[:14]).
		Str("token1", opp.Token1.Hex()[:14]).
		Str("buy_dex", opp.BuyPool.DexName).
		Str("sell_dex", opp.SellPool.DexName).
		Float64("spread_pct", opp.SpreadFloat*100).
		Int("spread_bps", opp.SpreadBps).
		Str("buy_liq", buyLiq).
		Str("sell_liq", sellLiq).
		Msg("🔍 SpreadScanner: Auto-discovered cross-DEX opportunity")

	// amountIn 设为 nil，由 handleOpportunity 的 Vault 余额逻辑动态设置
	// ExpectProfit 将在 handleOpportunity 中根据实际 amountIn 重新计算
	// 从 BuyPool/SellPool 提取 fee tier
	// V3 池子: PriceCache.Fee 是 bps (5=0.05%), 合约需要 raw fee (500=0.05%)
	// V2 池子: feeTier 必须传 0，告诉合约用 swapExactTokensForTokens
	// 关键: feeTier>0 会让合约调用 V3 exactInputSingle，V2 router 没有这个函数!
	var buyFee, sellFee uint32
	if opp.BuyPool.IsV3 {
		buyFee = uint32(opp.BuyPool.Fee) * 100 // bps→raw: 5→500, 30→3000
	}
	if opp.SellPool.IsV3 {
		sellFee = uint32(opp.SellPool.Fee) * 100
	}
	feeTiers := []uint32{buyFee, sellFee}

	return &strategy.ArbitrageOpportunity{
		ID:         opp.ID,
		SwapPath:   opp.SwapPath,
		Dexes:      opp.DexPath,
		DexNames:   opp.DexNames,
		AmountIn:   nil,
		ProfitRate: opp.SpreadFloat,
		PathLength: len(opp.SwapPath) - 1,
		Confidence: calculateSpreadConfidence(opp.SpreadFloat),
		Timestamp:  opp.DiscoveredAt,
		ValidUntil: opp.DiscoveredAt.Add(15 * time.Second),
		MinProfit:  big.NewInt(0),
		ExpectProfit: nil,
		FeeTiers:   feeTiers,
	}
}

// calculateSpreadConfidence 根据价差计算置信度
func calculateSpreadConfidence(spread float64) float64 {
	if spread > 0.05 { return 0.9 }   // >5%
	if spread > 0.02 { return 0.8 }   // >2%
	if spread > 0.01 { return 0.7 }   // >1%
	if spread > 0.005 { return 0.6 }  // >0.5%
	return 0.5
}

// executionLoop 执行循环（批量收集 + 按利润排序）
func (s *HighPerformanceScheduler) executionLoop() {
	// 并发控制：允许多个 eth_call 模拟并行运行
	semaphore := make(chan struct{}, s.config.MaxConcurrentExecutions)

	// 每 500ms 收集一批机会，按 ExpectProfit 降序排序后处理
	batchTicker := time.NewTicker(500 * time.Millisecond)
	defer batchTicker.Stop()

	var batch []*strategy.ArbitrageOpportunity

	for {
		select {
		case <-s.ctx.Done():
			return

		case opp, ok := <-s.detector.Opportunities():
			if !ok {
				return
			}
			batch = append(batch, opp)

		case <-batchTicker.C:
			if len(batch) == 0 {
				continue
			}

			// 过滤已过期的机会（ValidUntil 已过）
			now := time.Now()
			alive := batch[:0]
			for _, o := range batch {
				if !o.ValidUntil.IsZero() && now.After(o.ValidUntil) {
					continue // 过期丢弃
				}
				alive = append(alive, o)
			}
			batch = alive

			if len(batch) == 0 {
				continue
			}

			// 按 ExpectProfit 降序排序（最赚钱的优先处理）
			sort.Slice(batch, func(i, j int) bool {
				pi := batch[i].ExpectProfit
				pj := batch[j].ExpectProfit
				if pi == nil {
					return false
				}
				if pj == nil {
					return true
				}
				return pi.Cmp(pj) > 0
			})

			// 每批最多处理 top-5，避免大量并发 eth_call 触发 429
			maxBatch := 5
			if len(batch) > maxBatch {
				batch = batch[:maxBatch]
			}

			// 处理排序后的批次
			for _, opp := range batch {
				go s.handleOpportunity(opp, semaphore)
			}
			batch = batch[:0] // 清空
		}
	}
}

// handleOpportunity 处理套利机会
func (s *HighPerformanceScheduler) handleOpportunity(opp *strategy.ArbitrageOpportunity, semaphore chan struct{}) {
	// 过期检查（goroutine 调度延迟可能超过 ValidUntil）
	if !opp.ValidUntil.IsZero() && time.Now().After(opp.ValidUntil) {
		return
	}

	// 更新统计
	s.statsMu.Lock()
	s.stats.OpportunitiesFound++
	s.stats.LastOpportunityTime = time.Now()
	s.statsMu.Unlock()

	// Prometheus：检测次数
	m := metrics.GetMetrics()
	m.RecordOpportunity(opp.PathLength, "dex", opp.ProfitRate*100) // estimatedValueUSD 用利润率近似

	// Prometheus：记录发现的机会（用于每分钟检测次数）
	pathLen := opp.PathLength
	if pathLen <= 0 {
		pathLen = len(opp.SwapPath)
	}
	estUSD := 0.0
	if opp.ExpectProfit != nil && opp.ExpectProfit.Sign() > 0 {
		estUSD, _ = new(big.Float).SetInt(opp.ExpectProfit).Float64()
	}
	metrics.GetMetrics().RecordOpportunity(pathLen, "dex", estUSD)

	// 打印机会信息
	log.Scheduler().Info().Str("path", opp.ID).Float64("profit", opp.ProfitRate*100).Float64("confidence", opp.Confidence).Int("length", opp.PathLength).Msg("🎯 Opportunity found")

	// 检查置信度
	if opp.Confidence < s.config.MinConfidence {
		return // 低置信度直接丢弃（不再打印日志减少刷屏）
	}

	// 去重：同一路径（token 组合+DEX 组合）2s 内不重复 eth_call
	dedupKey := s.buildDedupKey(opp)
	if lastTime, ok := s.recentPaths.Load(dedupKey); ok {
		if t, _ := lastTime.(time.Time); time.Since(t) < 2*time.Second {
			return // 跳过重复
		}
	}
	s.recentPaths.Store(dedupKey, time.Now())

	// 限制 amountIn 并决定执行路径（Vault vs Flash Loan）
	useFlashLoan := false
	hardLimit := new(big.Int).SetUint64(20_000_000_000_000_000) // 自有资金绝对上限 0.02 ETH
	if opp.AmountIn == nil || opp.AmountIn.Sign() <= 0 {
		opp.AmountIn = new(big.Int).SetUint64(5_000_000_000_000_000) // 默认 0.005 ETH
	}

	// 查询 Vault 可用余额（优先用缓存，30s 内有效，避免阻塞 eth_call）
	if len(opp.SwapPath) > 0 && s.executor != nil {
		assetHex := opp.SwapPath[0].Hex()
		available := s.getCachedVaultBalance(assetHex)

		vaultInsufficient := false
		if available == nil || available.Sign() == 0 {
			vaultInsufficient = true
		} else {
			// 使用 Vault 余额的 50%（留 50% 缓冲，减少滑点）
			safeAmount := new(big.Int).Mul(available, big.NewInt(5))
			safeAmount.Div(safeAmount, big.NewInt(10))
			if opp.AmountIn.Cmp(safeAmount) > 0 {
				opp.AmountIn = safeAmount
			}
			// 如果 Vault 余额太低（< 0.001 ETH），也切换到 Flash Loan
			minVaultBalance := new(big.Int).SetUint64(1_000_000_000_000_000) // 0.001 ETH
			if available.Cmp(minVaultBalance) < 0 {
				vaultInsufficient = true
			}
		}

		// Vault 不足时切换到 Flash Loan 路径
		if vaultInsufficient && s.config.EnableFlashLoan && s.executor.HasFlashLoan() {
			useFlashLoan = true
			flashLoanAmount := new(big.Int).Mul(big.NewInt(50), big.NewInt(1_000_000_000_000_000_000)) // 50 ETH
			if opp.AmountIn != nil && opp.AmountIn.Sign() > 0 {
				if opp.AmountIn.Cmp(flashLoanAmount) > 0 {
					opp.AmountIn = flashLoanAmount
				}
			} else {
				opp.AmountIn = new(big.Int).SetUint64(1_000_000_000_000_000_000) // 默认 1 ETH
			}
			log.Scheduler().Info().
				Str("asset", assetHex[:14]).
				Str("flash_amount", opp.AmountIn.String()).
				Msg("⚡ Vault insufficient, switching to Flash Loan path")
		} else if vaultInsufficient {
			log.Scheduler().Debug().
				Str("asset", assetHex[:14]).
				Msg("  ⚠️ Vault insufficient and Flash Loan not enabled, skipping")
			return
		}
	}

	// 自有资金路径：检查绝对上限
	if !useFlashLoan && opp.AmountIn.Cmp(hardLimit) > 0 {
		opp.AmountIn = hardLimit
	}

	// 保留策略计算出的 MinProfit（合约利润保护），不再强制清零
	// MinProfit=0 会使合约 require(actProfit > 0) 形同虚设，亏损也不拦截
	if opp.MinProfit == nil {
		opp.MinProfit = big.NewInt(0)
	}
	// 根据实际 amountIn 和 spreadRate 计算 ExpectProfit
	if (opp.ExpectProfit == nil || opp.ExpectProfit.Sign() <= 0) && opp.AmountIn != nil && opp.ProfitRate > 0 {
		// ExpectProfit = amountIn * spreadRate（使用整数近似：乘以 bps 再除 10000）
		spreadBps := int64(opp.ProfitRate * 10000)
		if spreadBps > 0 {
			opp.ExpectProfit = new(big.Int).Mul(opp.AmountIn, big.NewInt(spreadBps))
			opp.ExpectProfit.Div(opp.ExpectProfit, big.NewInt(10000))
		}
	}
	if opp.ExpectProfit == nil || opp.ExpectProfit.Sign() <= 0 {
		opp.ExpectProfit = big.NewInt(1)
	}

	// MinProfit 动态计算：覆盖 gas 成本 + 安全边际
	// Arbitrum L2: gasUsed ~1M, gasPrice ~0.1 gwei → gasCost ≈ 0.0001 ETH
	// 加上 L1 calldata 成本，保守估计 0.0002 ETH
	// MinProfit = max(2×gasCost, amountIn×0.5%)，取较小值避免过度过滤
	if opp.MinProfit == nil || opp.MinProfit.Sign() == 0 {
		gasCostEstimate := big.NewInt(200_000_000_000_000) // 0.0002 ETH
		minProfitGas := new(big.Int).Mul(gasCostEstimate, big.NewInt(2)) // 2× gas cost = 0.0004 ETH

		// amountIn 的 0.5%
		minProfitPct := new(big.Int).Div(opp.AmountIn, big.NewInt(200))

		// 取较小值：避免小额交易被过度过滤
		if minProfitPct.Sign() > 0 && minProfitPct.Cmp(minProfitGas) < 0 {
			opp.MinProfit = minProfitPct
		} else {
			opp.MinProfit = minProfitGas
		}
	}

	// eth_call 模拟验证（免费，不消耗 Gas）
	// 有 RPC 池时降低限流（429 由 Simulator 内部轮换处理）
	// 无 RPC 池时保留自适应限流
	s.ethCallMu.Lock()
	if s.ethCallMinDelay == 0 {
		if s.config.RPCPool != nil && s.config.RPCPool.GetClientCount() > 1 {
			s.ethCallMinDelay = 50 * time.Millisecond // 多 RPC 节点，降低间隔
		} else {
			s.ethCallMinDelay = 100 * time.Millisecond
		}
	}
	elapsed := time.Since(s.lastEthCall)
	if elapsed < s.ethCallMinDelay {
		time.Sleep(s.ethCallMinDelay - elapsed)
	}
	s.lastEthCall = time.Now()
	s.ethCallMu.Unlock()

	if s.simulator != nil {
		simCtx, simCancel := context.WithTimeout(context.Background(), 8*time.Second)
		// 确保 FeeTiers 长度与 Dexes 一致
		feeTiers := opp.FeeTiers
		if len(feeTiers) != len(opp.Dexes) {
			feeTiers = make([]uint32, len(opp.Dexes))
		}
		simParams := &executor.ArbitrageParams{
			Asset:        opp.SwapPath[0],
			TokenOut:     opp.SwapPath[len(opp.SwapPath)-1],
			AmountIn:     opp.AmountIn,
			SwapPath:     opp.SwapPath,
			Dexes:        opp.Dexes,
			FeeTiers:     feeTiers,
			ExpectProfit: opp.ExpectProfit,
			MinProfit:    opp.MinProfit,
		}

		simResult, simErr := s.simulator.SimulateArbitrage(simCtx, simParams)
		simCancel()

		// 自适应 RPC 限流反馈
		s.ethCallMu.Lock()
		if simErr != nil && contains429(simErr.Error()) {
			s.consecutive429++
			s.ethCallMinDelay = min(s.ethCallMinDelay*2, 2*time.Second)
		} else {
			if s.consecutive429 > 0 {
				s.consecutive429 = 0
			}
			// 无 429 时逐步回落（不低于 150ms）
			if s.ethCallMinDelay > 150*time.Millisecond {
				s.ethCallMinDelay = s.ethCallMinDelay * 9 / 10
			}
		}
		s.ethCallMu.Unlock()

		if simErr != nil || !simResult.Profitable {
			metrics.GetMetrics().RecordSimFiltered()
			// 反馈失败给路径置信度
			if s.detector != nil {
				s.detector.RecordResult(opp.ID, false)
			}
			errMsg := ""
			if simErr != nil { errMsg = simErr.Error() } else { errMsg = simResult.Error }
			startToken := ""
			if len(opp.SwapPath) > 0 { startToken = opp.SwapPath[0].Hex()[:14] }
			log.Scheduler().Info().
				Str("path", opp.ID).
				Str("start_token", startToken).
				Str("reason", errMsg).
				Float64("profit_pct", opp.ProfitRate*100).
				Msg("  ❌ eth_call reverted")
			return
		}

		metrics.GetMetrics().RecordSimPassed()
		// 反馈成功给路径置信度
		if s.detector != nil {
			s.detector.RecordResult(opp.ID, true)
		}
		// 模拟通过了！这是一个链上此刻确实有利润的机会
		log.Scheduler().Info().
			Str("path", opp.ID).
			Float64("profit_pct", opp.ProfitRate*100).
			Str("net_profit", simResult.NetProfit.String()).
			Uint64("gas_used", simResult.GasUsed).
			Msg("✅ eth_call PASSED — real opportunity!")
	}

	// 检查是否启用执行
	if !s.config.EnableExecution || s.config.DryRun {
		mode := "Vault"
		if useFlashLoan {
			mode = "FlashLoan"
		}
		log.Scheduler().Info().Str("path", opp.ID).Str("mode", mode).Msg("  [dry-run] Would execute this verified opportunity")
		return
	}

	if s.executor == nil {
		return
	}

	// 日累计 Gas 损失检查
	if s.executor.DailyGasLossExceeded() {
		log.Scheduler().Warn().Msg("⛔ Daily gas loss limit exceeded, pausing execution")
		return
	}

	execMode := "Vault"
	if useFlashLoan {
		execMode = "FlashLoan"
	}
	log.Scheduler().Info().
		Str("path", opp.ID).
		Str("mode", execMode).
		Float64("profit_pct", opp.ProfitRate*100).
		Str("amount_in", opp.AmountIn.String()).
		Msg("🚀 Executing verified opportunity")

	select {
	case semaphore <- struct{}{}:
	default:
		return
	}

	if useFlashLoan {
		s.executeFlashLoanOpportunity(opp)
	} else {
		s.executeOpportunity(opp)
	}
	<-semaphore
}

// executeOpportunity 执行套利机会
func (s *HighPerformanceScheduler) executeOpportunity(opp *strategy.ArbitrageOpportunity) {
	startTime := time.Now()

	s.statsMu.Lock()
	s.stats.ExecutionsAttempted++
	s.statsMu.Unlock()

	// 使用独立的 context（避免被 scheduler 的 context 取消影响）
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 执行
	result, err := s.executor.Execute(ctx, opp)

	s.statsMu.Lock()
	s.stats.LastExecutionTime = time.Now()
	if err != nil || !result.Success {
		s.stats.ExecutionsFailed++
		log.Executor().Error().Err(err).Dur("duration", time.Since(startTime)).Msg("  ❌ Execution failed")
	} else {
		s.stats.ExecutionsSucceeded++
		log.Executor().Info().Str("tx_hash", result.TxHash).Str("profit", result.ActualProfit.String()).Dur("duration", time.Since(startTime)).Msg("  ✅ Execution succeeded")
	}
	s.statsMu.Unlock()

	// Prometheus：记录执行结果
	if result != nil {
		gasUsed := uint64(0)
		if result.GasUsed > 0 {
			gasUsed = result.GasUsed
		}
		metrics.GetMetrics().RecordExecution(result.Success, "dex", time.Since(startTime), gasUsed)
		if result.Success && result.ActualProfit != nil {
			profitWei, _ := new(big.Float).SetInt(result.ActualProfit).Float64()
			metrics.GetMetrics().RecordProfit(opp.SwapPath[0].Hex()[:10], profitWei, 0)
		}
	}
}

// executeFlashLoanOpportunity 通过 Flash Loan 执行套利机会
func (s *HighPerformanceScheduler) executeFlashLoanOpportunity(opp *strategy.ArbitrageOpportunity) {
	startTime := time.Now()

	s.statsMu.Lock()
	s.stats.ExecutionsAttempted++
	s.statsMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := s.executor.ExecuteWithFlashLoan(ctx, opp)

	s.statsMu.Lock()
	s.stats.LastExecutionTime = time.Now()
	if err != nil || !result.Success {
		s.stats.ExecutionsFailed++
		log.Executor().Error().Err(err).Dur("duration", time.Since(startTime)).Msg("  ❌ Flash Loan execution failed")
	} else {
		s.stats.ExecutionsSucceeded++
		log.Executor().Info().Str("tx_hash", result.TxHash).Str("profit", result.ActualProfit.String()).Dur("duration", time.Since(startTime)).Msg("  ✅ Flash Loan execution succeeded")
	}
	s.statsMu.Unlock()

	// Prometheus 记录
	if result != nil {
		gasUsed := uint64(0)
		if result.GasUsed > 0 {
			gasUsed = result.GasUsed
		}
		metrics.GetMetrics().RecordExecution(result.Success, "flash_loan", time.Since(startTime), gasUsed)
		if result.Success && result.ActualProfit != nil {
			profitWei, _ := new(big.Float).SetInt(result.ActualProfit).Float64()
			metrics.GetMetrics().RecordProfit(opp.SwapPath[0].Hex()[:10], profitWei, 0)
		}
	}
}

// statsLoop 统计打印循环
func (s *HighPerformanceScheduler) statsLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.printStats()
		}
	}
}

// printStats 打印统计信息
func (s *HighPerformanceScheduler) printStats() {
	s.statsMu.RLock()
	stats := s.stats
	s.statsMu.RUnlock()

	// 获取组件统计
	collectorStats := s.collector.GetStats()
	detectorStats := s.detector.GetStats()
	cacheUpdates, cacheEvents, poolCount, _ := s.priceCache.Stats()

	log.Scheduler().Info().Msg("========== Performance Stats ==========")
	log.Scheduler().Info().Dur("uptime", time.Since(stats.StartTime).Round(time.Second)).Msg("Uptime")
	log.Scheduler().Info().Int("pools", poolCount).Msg("Pools monitored")
	log.Scheduler().Info().Uint64("updates", cacheUpdates).Uint64("events", cacheEvents).Msg("Price updates")
	log.Scheduler().Info().Uint64("tier1", collectorStats.Tier1Updates).Uint64("tier2", collectorStats.Tier2Updates).Uint64("tier3", collectorStats.Tier3Updates).Msg("Collector updates")
	log.Scheduler().Info().Int64("events", detectorStats.EventsReceived).Int64("paths", detectorStats.PathsAffected).Int64("opportunities", detectorStats.OpportunitiesFound).Msg("Detector stats")
	log.Scheduler().Info().Int64("attempted", stats.ExecutionsAttempted).Int64("succeeded", stats.ExecutionsSucceeded).Int64("failed", stats.ExecutionsFailed).Msg("Executions stats")
	log.Scheduler().Info().Msg("=======================================")
}

// getModeString 获取模式字符串
func (s *HighPerformanceScheduler) getModeString() string {
	if s.config.DryRun {
		return "DRY RUN (detection only)"
	}
	if s.config.EnableExecution {
		return "LIVE (auto execution enabled)"
	}
	return "MONITOR (detection only)"
}

// GetStats 获取统计信息
func (s *HighPerformanceScheduler) GetStats() SchedulerStats {
	s.statsMu.RLock()
	defer s.statsMu.RUnlock()
	return s.stats
}

// IsRunning 检查是否运行中
func (s *HighPerformanceScheduler) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// GetPriceCache 获取价格缓存（用于 CEX-DEX 套利）
func (s *HighPerformanceScheduler) GetPriceCache() *cache.PriceCache {
	return s.priceCache
}

// getCachedVaultBalance 获取缓存的 Vault 余额（30s TTL，后台异步刷新）
func (s *HighPerformanceScheduler) getCachedVaultBalance(assetHex string) *big.Int {
	// 检查缓存是否有效（30s TTL）
	if cacheTime, ok := s.vaultCacheTime.Load(assetHex); ok {
		if t, _ := cacheTime.(time.Time); time.Since(t) < 30*time.Second {
			if bal, ok := s.vaultBalanceCache.Load(assetHex); ok {
				return bal.(*big.Int)
			}
		}
	}

	// 缓存过期或不存在：同步查询一次（仅首次阻塞）
	if s.executor != nil {
		vaultCtx, vaultCancel := context.WithTimeout(context.Background(), 3*time.Second)
		available, err := s.executor.GetVaultAvailable(vaultCtx, common.HexToAddress(assetHex))
		vaultCancel()
		if err == nil && available != nil {
			s.vaultBalanceCache.Store(assetHex, available)
			s.vaultCacheTime.Store(assetHex, time.Now())
			return available
		}
	}
	// 查询失败时返回缓存中的旧值（如果有）
	if bal, ok := s.vaultBalanceCache.Load(assetHex); ok {
		return bal.(*big.Int)
	}
	return nil
}

// contains429 检查错误消息是否包含 429 限流
func contains429(s string) bool {
	return len(s) > 0 && (strings.Contains(s, "429") || strings.Contains(s, "rate limit"))
}

// ============================================================
// 工厂方法
// ============================================================

// CreateHighPerformanceScheduler 从配置创建调度器
func CreateHighPerformanceScheduler(
	db *gorm.DB,
	web3Client *web3.Client,
	appConfig *config.Config,
) (*HighPerformanceScheduler, error) {
	// 解析基础代币
	baseTokens := make([]common.Address, 0)
	if appConfig != nil {
		for _, token := range appConfig.Tokens {
			baseTokens = append(baseTokens, common.HexToAddress(token.Address))
		}
	}

	// 构建配置
	chainID := int64(42161) // 默认 Arbitrum One
	if appConfig != nil {
		chainID = int64(appConfig.Blockchain.ChainID)
	}

	cfg := &HighPerformanceConfig{
		CollectorConfig:         nil, // 使用默认
		DetectorConfig:          nil, // 使用默认
		WSURL:                   "",  // 从appConfig获取（如果有）
		ChainID:                 chainID,
		BaseTokens:              baseTokens,
		MaxConcurrentExecutions: 3,
		ExecutionTimeout:        30 * time.Second,
		MinConfidence:           0.15,
		EnableExecution:         false,
		DryRun:                  true,
	}

	return NewHighPerformanceScheduler(db, web3Client, nil, cfg)
}

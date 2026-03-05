// internal/scheduler/high_performance_scheduler.go
// 高性能调度器 - 事件驱动架构，替代定时轮询
package scheduler

import (
	"context"
	"fmt"
	"math/big"
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
		MinConfidence:           0.3,   // 降低阈值，让更多机会通过（eth_call 模拟会做最终验证）
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
	semaphore := make(chan struct{}, 1) // 价差机会串行处理，防止并发

	for {
		select {
		case <-s.ctx.Done():
			return
		case spreadOpp, ok := <-s.spreadScanner.Opportunities():
			if !ok {
				return
			}
			// 将 SpreadOpportunity 转换为标准 ArbitrageOpportunity 然后处理
			arbOpp := s.convertSpreadOpportunity(spreadOpp)
			if arbOpp != nil {
				s.handleOpportunity(arbOpp, semaphore)
			}
		}
	}
}

// convertSpreadOpportunity 将跨 DEX 价差机会转换为标准套利机会格式
func (s *HighPerformanceScheduler) convertSpreadOpportunity(opp *strategy.SpreadOpportunity) *strategy.ArbitrageOpportunity {
	if opp == nil || len(opp.SwapPath) < 3 || len(opp.DexPath) < 2 {
		return nil
	}

	log.Scheduler().Info().
		Str("token0", opp.Token0.Hex()[:14]).
		Str("token1", opp.Token1.Hex()[:14]).
		Str("buy_dex", opp.BuyPool.DexName).
		Str("sell_dex", opp.SellPool.DexName).
		Float64("spread_pct", opp.SpreadFloat*100).
		Int("spread_bps", opp.SpreadBps).
		Msg("🔍 SpreadScanner: Auto-discovered cross-DEX opportunity")

	// amountIn 设为 nil，由 handleOpportunity 的 Vault 余额逻辑动态设置
	// ExpectProfit 将在 handleOpportunity 中根据实际 amountIn 重新计算
	// 从 BuyPool/SellPool 提取 fee tier (V3=500/3000/10000, V2=0)
	feeTiers := []uint32{uint32(opp.BuyPool.Fee), uint32(opp.SellPool.Fee)}

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

// executionLoop 执行循环
func (s *HighPerformanceScheduler) executionLoop() {
	// 并发控制
	semaphore := make(chan struct{}, s.config.MaxConcurrentExecutions)

	for {
		select {
		case <-s.ctx.Done():
			return

		case opp, ok := <-s.detector.Opportunities():
			if !ok {
				return
			}

			s.handleOpportunity(opp, semaphore)
		}
	}
}

// handleOpportunity 处理套利机会
func (s *HighPerformanceScheduler) handleOpportunity(opp *strategy.ArbitrageOpportunity, semaphore chan struct{}) {
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

	// 限制 amountIn 并决定执行路径（Vault vs Flash Loan）
	useFlashLoan := false
	hardLimit := new(big.Int).SetUint64(100_000_000_000_000_000) // 自有资金绝对上限 0.1 ETH
	if opp.AmountIn == nil || opp.AmountIn.Sign() <= 0 {
		opp.AmountIn = new(big.Int).SetUint64(10_000_000_000_000_000) // 默认 0.01 ETH
	}

	// 查询链上 Vault 可用余额并决定执行路径
	if len(opp.SwapPath) > 0 && s.executor != nil {
		vaultCtx, vaultCancel := context.WithTimeout(context.Background(), 3*time.Second)
		available, vaultErr := s.executor.GetVaultAvailable(vaultCtx, opp.SwapPath[0])
		vaultCancel()

		vaultInsufficient := false
		if vaultErr != nil || available == nil || available.Sign() == 0 {
			vaultInsufficient = true
		} else {
			// 使用 Vault 余额的 90%（留 10% 缓冲）
			safeAmount := new(big.Int).Mul(available, big.NewInt(9))
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
			// Flash Loan 金额上限 50 ETH（避免价格冲击）
			flashLoanAmount := new(big.Int).Mul(big.NewInt(50), big.NewInt(1_000_000_000_000_000_000)) // 50 ETH
			// 使用策略引擎计算的最优金额（如果有），否则用保守值
			if opp.AmountIn != nil && opp.AmountIn.Sign() > 0 {
				// 保持策略计算的金额，但限制在 Flash Loan 上限内
				if opp.AmountIn.Cmp(flashLoanAmount) > 0 {
					opp.AmountIn = flashLoanAmount
				}
			} else {
				opp.AmountIn = new(big.Int).SetUint64(1_000_000_000_000_000_000) // 默认 1 ETH
			}
			log.Scheduler().Info().
				Str("asset", opp.SwapPath[0].Hex()[:14]).
				Str("flash_amount", opp.AmountIn.String()).
				Msg("⚡ Vault insufficient, switching to Flash Loan path")
		} else if vaultInsufficient {
			log.Scheduler().Debug().
				Str("asset", opp.SwapPath[0].Hex()[:14]).
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

	// eth_call 模拟验证（免费，不消耗 Gas）
	// 只有模拟通过的机会才值得花 Gas 执行
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

		if simErr != nil || !simResult.Profitable {
			metrics.GetMetrics().RecordSimFiltered()
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
	time.Sleep(50 * time.Millisecond)
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
		MinConfidence:           0.3,
		EnableExecution:         false,
		DryRun:                  true,
	}

	return NewHighPerformanceScheduler(db, web3Client, nil, cfg)
}

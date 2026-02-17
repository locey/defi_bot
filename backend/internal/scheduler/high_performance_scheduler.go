// internal/scheduler/high_performance_scheduler.go
// 高性能调度器 - 事件驱动架构，替代定时轮询
package scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/defi-bot/backend/internal/collector"
	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/executor"
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
	priceCache *cache.PriceCache           // 内存价格缓存
	collector  *collector.FastCollector    // 高速采集器
	detector   *strategy.ArbitrageDetector // 增量检测器
	executor   *executor.ArbitrageExecutor // 执行器

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

	// 执行配置
	MaxConcurrentExecutions int           // 最大并发执行数
	ExecutionTimeout        time.Duration // 执行超时
	MinConfidence           float64       // 最小置信度

	// 功能开关
	EnableExecution bool // 是否启用自动执行
	DryRun          bool // 干运行模式（只检测不执行）
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
	log.Scheduler().Info().Msg("  ✓ ArbitrageDetector initialized")

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

	// Step 5: 启动执行循环
	log.Scheduler().Info().Msg("Step 5: Starting execution loop...")
	go s.executionLoop()

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
	if s.wsClient != nil {
		s.wsClient.Close()
	}

	log.Scheduler().Info().Msg("HighPerformanceScheduler: Stopped")
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

	// 打印机会信息
	log.Scheduler().Info().Str("path", opp.ID).Float64("profit", opp.ProfitRate*100).Float64("confidence", opp.Confidence).Int("length", opp.PathLength).Msg("🎯 Opportunity found")

	// 检查置信度
	if opp.Confidence < s.config.MinConfidence {
		log.Scheduler().Warn().Float64("confidence", opp.Confidence).Float64("min", s.config.MinConfidence).Msg("  ⚠ Skipped: low confidence")
		return
	}

	// 检查是否启用执行
	if !s.config.EnableExecution || s.config.DryRun {
		log.Scheduler().Info().Str("path", opp.ID).Msg("  📋 Dry run mode: would execute path")
		return
	}

	// 检查执行器
	if s.executor == nil {
		log.Scheduler().Warn().Msg("  ⚠ Skipped: executor not configured")
		return
	}

	// 获取信号量
	select {
	case semaphore <- struct{}{}:
	default:
		log.Scheduler().Warn().Msg("  ⚠ Skipped: max concurrent executions reached")
		return
	}

	// 异步执行
	go func() {
		defer func() { <-semaphore }()
		s.executeOpportunity(opp)
	}()
}

// executeOpportunity 执行套利机会
func (s *HighPerformanceScheduler) executeOpportunity(opp *strategy.ArbitrageOpportunity) {
	startTime := time.Now()

	s.statsMu.Lock()
	s.stats.ExecutionsAttempted++
	s.statsMu.Unlock()

	// 创建超时context
	ctx, cancel := context.WithTimeout(s.ctx, s.config.ExecutionTimeout)
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
	cfg := &HighPerformanceConfig{
		CollectorConfig:         nil, // 使用默认
		DetectorConfig:          nil, // 使用默认
		WSURL:                   "",  // 从appConfig获取（如果有）
		ChainID:                 1,   // 默认以太坊主网
		BaseTokens:              baseTokens,
		MaxConcurrentExecutions: 3,
		ExecutionTimeout:        30 * time.Second,
		MinConfidence:           0.3,
		EnableExecution:         false,
		DryRun:                  true,
	}

	if appConfig != nil {
		cfg.ChainID = int64(appConfig.Blockchain.ChainID)
	}

	return NewHighPerformanceScheduler(db, web3Client, nil, cfg)
}

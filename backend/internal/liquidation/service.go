// internal/liquidation/service.go
// 清算服务 — 协调采集、检测、执行的主循环
package liquidation

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/defi-bot/backend/pkg/log"
	"github.com/defi-bot/backend/pkg/web3"
	"github.com/ethereum/go-ethereum/common"
)

// ServiceConfig 清算服务配置
type ServiceConfig struct {
	// Aave V3 地址
	AavePool         common.Address
	AaveDataProvider common.Address
	AaveOracle       common.Address // Aave V3 Oracle（用于查询 Chainlink feed 地址）

	// 清算合约地址
	LiquidatorContract        common.Address // FlashLoanLiquidator（Aave 闪电贷 0.05%）
	BalancerLiquidatorContract common.Address // BalancerLiquidator（Balancer 免费闪电贷，优先使用）

	// Keeper
	KeeperPrivateKey string
	ChainID          int64

	// 模式
	DryRun          bool
	EnableExecution bool

	// 检测参数
	WatchThreshold float64 // 健康因子监控阈值
	MinDebtUSD     float64 // 最小债务额

	// 扫描参数
	ScanInterval      time.Duration // 主循环间隔
	EventScanBlocks   uint64        // 每次扫描多少个区块的事件
	HealthCheckBatch  int           // 每批检查多少用户

	// DEX swap 参数
	DefaultSwapRouter common.Address
	DefaultSwapFee    uint32

	// WebSocket URL（用于预言机订阅）
	WSURL string
}

// DefaultServiceConfig 默认配置
func DefaultServiceConfig() *ServiceConfig {
	return &ServiceConfig{
		WatchThreshold:  1.05,
		MinDebtUSD:      50,
		ScanInterval:    10 * time.Second,
		EventScanBlocks: 1000,
		DryRun:          true,
	}
}

// Service 清算服务
type Service struct {
	collector *AaveCollector
	detector  *LiquidationDetector
	executor  *LiquidationExecutor
	config    *ServiceConfig

	web3Client *web3.Client

	// 预言机监控（事件驱动，替代轮询）
	oracleMonitor *OracleMonitor
	// 预言机价格更新时触发快速检查
	oracleTrigger chan struct{}

	// 预计算缓存：高风险用户的清算参数
	precomputedOpps   map[common.Address]*LiquidationOpportunity
	precomputedOppsMu sync.RWMutex

	// 统计
	totalChecked      int64
	totalDetected     int64
	totalExecuted     int64
	totalProfit       *big.Int
	oracleTriggers    int64 // 预言机触发的快速检查次数
	statsMu           sync.Mutex

	// 控制
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewService 创建清算服务
func NewService(web3Client *web3.Client, config *ServiceConfig) (*Service, error) {
	if config == nil {
		config = DefaultServiceConfig()
	}

	// 创建采集器
	collector, err := NewAaveCollector(
		web3Client,
		config.AavePool,
		config.AaveDataProvider,
	)
	if err != nil {
		return nil, fmt.Errorf("create aave collector: %w", err)
	}

	// 创建检测器
	detectorCfg := &DetectorConfig{
		WatchThreshold:   config.WatchThreshold,
		MinDebtUSD:       config.MinDebtUSD,
		DefaultSwapRouter: config.DefaultSwapRouter,
		DefaultSwapFee:   config.DefaultSwapFee,
		EstimatedGasUsed: 500_000,
	}
	detector := NewLiquidationDetector(collector, detectorCfg)

	// 创建执行器
	var executor *LiquidationExecutor
	if config.EnableExecution && config.LiquidatorContract != (common.Address{}) {
		executor, err = NewLiquidationExecutor(
			web3Client,
			config.LiquidatorContract,
			config.BalancerLiquidatorContract,
			config.KeeperPrivateKey,
			config.ChainID,
			config.DryRun,
		)
		if err != nil {
			log.Strategy().Warn().Err(err).Msg("清算：执行器创建失败（将仅检测）")
		}
	}

	return &Service{
		collector:       collector,
		detector:        detector,
		executor:        executor,
		config:          config,
		web3Client:      web3Client,
		oracleTrigger:   make(chan struct{}, 1),
		precomputedOpps: make(map[common.Address]*LiquidationOpportunity),
		totalProfit:     big.NewInt(0),
		stopCh:          make(chan struct{}),
	}, nil
}

// Start 启动清算服务
func (s *Service) Start(ctx context.Context) error {
	log.Strategy().Info().
		Str("aave_pool", s.config.AavePool.Hex()).
		Bool("dry_run", s.config.DryRun).
		Bool("execution_enabled", s.config.EnableExecution).
		Msg("清算服务启动中...")

	// 初始化：获取储备资产列表
	if _, err := s.collector.FetchReserves(ctx); err != nil {
		return fmt.Errorf("fetch reserves: %w", err)
	}

	// 初始化：扫描最近的 Borrow 事件
	if err := s.initialBorrowerScan(ctx); err != nil {
		log.Strategy().Warn().Err(err).Msg("清算：初始借款人扫描失败")
	}

	// 启动预言机监控（事件驱动快速通道）
	if s.config.AaveOracle != (common.Address{}) && s.config.WSURL != "" {
		oracleMonitor, err := NewOracleMonitor(
			s.web3Client,
			s.config.WSURL,
			s.config.AaveOracle,
			s.onOraclePriceChange,
		)
		if err != nil {
			log.Strategy().Warn().Err(err).Msg("清算：预言机监控创建失败（将使用轮询模式）")
		} else {
			reserves := s.collector.GetReserves()
			if err := oracleMonitor.Start(ctx, reserves); err != nil {
				log.Strategy().Warn().Err(err).Msg("清算：预言机监控启动失败（将使用轮询模式）")
			} else {
				s.oracleMonitor = oracleMonitor
			}
		}
	}

	// 启动主循环
	s.wg.Add(1)
	go s.mainLoop(ctx)

	mode := "轮询"
	if s.oracleMonitor != nil {
		mode = "预言机事件驱动 + 轮询"
	}
	log.Strategy().Info().
		Int("reserves", len(s.collector.GetReserves())).
		Int("borrowers", s.collector.GetBorrowerCount()).
		Str("mode", mode).
		Msg("清算服务已启动")

	return nil
}

// Stop 停止清算服务
func (s *Service) Stop() {
	close(s.stopCh)
	if s.oracleMonitor != nil {
		s.oracleMonitor.Stop()
	}
	s.wg.Wait()
	log.Strategy().Info().Msg("清算服务已停止")
}

// initialBorrowerScan 初始扫描借款人
func (s *Service) initialBorrowerScan(ctx context.Context) error {
	client := s.web3Client.GetClient()
	currentBlock, err := client.BlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("get block number: %w", err)
	}

	// 扫描最近 N 个区块（从配置读取，默认 50000 ≈ 14h on Arbitrum）
	scanBlocks := s.config.EventScanBlocks
	if scanBlocks == 0 {
		scanBlocks = 50000
	}
	fromBlock := uint64(0)
	if currentBlock > scanBlocks {
		fromBlock = currentBlock - scanBlocks
	}

	// 分批扫描
	batchSize := uint64(10000)
	for from := fromBlock; from < currentBlock; from += batchSize {
		to := from + batchSize - 1
		if to > currentBlock {
			to = currentBlock
		}

		_, err := s.collector.ScanBorrowEvents(ctx, from, to)
		if err != nil {
			log.Strategy().Warn().Err(err).
				Uint64("from", from).Uint64("to", to).
				Msg("清算：事件扫描批次失败")
			continue
		}
	}

	return nil
}

// mainLoop 主循环（轮询 + 预言机事件双驱动）
func (s *Service) mainLoop(ctx context.Context) {
	defer s.wg.Done()

	ticker := time.NewTicker(s.config.ScanInterval)
	defer ticker.Stop()

	// 增量事件扫描的起始区块
	var lastScannedBlock uint64

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-ticker.C:
			// 常规轮询：全量检查 + 增量事件扫描
			s.runCycle(ctx, &lastScannedBlock)
			// 轮询周期结束后，预计算高风险用户的清算参数
			s.precomputeAtRiskOpportunities(ctx)
		case <-s.oracleTrigger:
			// 预言机触发：仅快速检查高风险用户（跳过全量扫描）
			s.statsMu.Lock()
			s.oracleTriggers++
			s.statsMu.Unlock()
			s.fastCheckAtRiskUsers(ctx)
		}
	}
}

// onOraclePriceChange 预言机价格变动回调
func (s *Service) onOraclePriceChange(asset common.Address, newPrice *big.Int) {
	// 非阻塞触发快速检查
	select {
	case s.oracleTrigger <- struct{}{}:
	default:
		// 已有触发排队，跳过
	}
}

// fastCheckAtRiskUsers 快速检查高风险用户（预言机触发，仅查 HF < 1.15 的用户）
func (s *Service) fastCheckAtRiskUsers(ctx context.Context) {
	// 获取已知高风险用户
	atRisk := s.collector.GetAtRiskPositions(1.15) // 比 WatchThreshold 宽松一点
	if len(atRisk) == 0 {
		return
	}

	// 提取地址，仅查这些用户
	users := make([]common.Address, len(atRisk))
	for i, p := range atRisk {
		users[i] = p.User
	}

	multicallAddr := common.HexToAddress(web3.Multicall3Address)
	positions, err := s.collector.batchCheckHealthFactorsBatch(ctx, multicallAddr, users)
	if err != nil {
		return
	}

	// 更新缓存
	s.collector.positionsMu.Lock()
	for _, p := range positions {
		s.collector.positions[p.User] = p
	}
	s.collector.positionsMu.Unlock()

	// 检查是否有刚跌破 HF=1 的用户
	for _, p := range positions {
		if !p.IsLiquidatable() {
			continue
		}

		// 检查是否有预计算的清算参数
		s.precomputedOppsMu.RLock()
		precomputed, exists := s.precomputedOpps[p.User]
		s.precomputedOppsMu.RUnlock()

		if exists && precomputed != nil {
			// 直接使用预计算参数，立即执行！
			precomputed.HealthFactor = p.HealthFactorFloat()
			precomputed.Timestamp = time.Now()
			precomputed.ValidUntil = time.Now().Add(15 * time.Second)

			debtUSD := baseToUSD(p.TotalDebtBase)
			log.Strategy().Info().
				Str("user", p.User.Hex()[:10]+"...").
				Float64("health_factor", p.HealthFactorFloat()).
				Float64("debt_usd", debtUSD).
				Msg("🚨 预言机触发：发现可清算仓位（使用预计算参数）")

			s.statsMu.Lock()
			s.totalDetected++
			s.statsMu.Unlock()

			s.executeLiquidation(ctx, precomputed)
		} else {
			// 没有预计算参数，走常规检测
			debtUSD := baseToUSD(p.TotalDebtBase)
			log.Strategy().Info().
				Str("user", p.User.Hex()[:10]+"...").
				Float64("health_factor", p.HealthFactorFloat()).
				Float64("debt_usd", debtUSD).
				Msg("🚨 预言机触发：发现可清算仓位（走常规检测）")

			opps, err := s.detector.Detect(ctx)
			if err != nil || len(opps) == 0 {
				continue
			}
			s.statsMu.Lock()
			s.totalDetected += int64(len(opps))
			s.statsMu.Unlock()
			for _, opp := range opps {
				s.executeLiquidation(ctx, opp)
			}
		}
	}
}

// precomputeAtRiskOpportunities 预计算高风险用户的清算参数
// 每轮轮询后调用，对 HF < 1.1 的用户提前算好参数
func (s *Service) precomputeAtRiskOpportunities(ctx context.Context) {
	atRisk := s.collector.GetAtRiskPositions(1.10)
	if len(atRisk) == 0 {
		s.precomputedOppsMu.Lock()
		s.precomputedOpps = make(map[common.Address]*LiquidationOpportunity)
		s.precomputedOppsMu.Unlock()
		return
	}

	reserves := s.collector.GetReserves()
	if len(reserves) == 0 {
		return
	}

	reserveMap := make(map[common.Address]*ReserveInfo)
	var assetAddrs []common.Address
	for i := range reserves {
		reserveMap[reserves[i].Asset] = &reserves[i]
		assetAddrs = append(assetAddrs, reserves[i].Asset)
	}

	newPrecomputed := make(map[common.Address]*LiquidationOpportunity)

	for _, pos := range atRisk {
		debtUSD := baseToUSD(pos.TotalDebtBase)
		if debtUSD < s.detector.config.MinDebtUSD {
			continue
		}

		// 获取用户各资产头寸
		userReserves, err := s.collector.FetchUserReserveData(ctx, pos.User, assetAddrs)
		if err != nil {
			continue
		}

		// 计算最佳清算对
		opp := s.detector.findBestLiquidationPair(pos, userReserves, reserveMap)
		if opp != nil {
			newPrecomputed[pos.User] = opp
		}
	}

	s.precomputedOppsMu.Lock()
	s.precomputedOpps = newPrecomputed
	s.precomputedOppsMu.Unlock()

	if len(newPrecomputed) > 0 {
		log.Strategy().Debug().
			Int("precomputed", len(newPrecomputed)).
			Int("at_risk", len(atRisk)).
			Msg("清算：已预计算高风险用户清算参数")
	}
}

// executeLiquidation 执行清算（抽取公共逻辑）
func (s *Service) executeLiquidation(ctx context.Context, opp *LiquidationOpportunity) {
	if s.executor == nil || !s.config.EnableExecution {
		log.Strategy().Info().
			Str("user", opp.User.Hex()).
			Str("collateral", opp.CollateralSymbol).
			Str("debt", opp.DebtSymbol).
			Float64("health_factor", opp.HealthFactor).
			Str("expected_profit", opp.ExpectedProfit.String()).
			Msg("清算机会（仅检测，未执行）")
		return
	}

	result, err := s.executor.Execute(ctx, opp)
	if err != nil {
		log.Executor().Warn().Err(err).
			Str("opp_id", opp.ID).
			Msg("清算执行失败")
		return
	}

	if result.Success {
		s.statsMu.Lock()
		s.totalExecuted++
		if result.Profit != nil {
			s.totalProfit.Add(s.totalProfit, result.Profit)
		}
		s.statsMu.Unlock()
	}
}

// runCycle 单次检测-执行循环
func (s *Service) runCycle(ctx context.Context, lastScannedBlock *uint64) {
	cycleStart := time.Now()

	// Step 1: 增量扫描新的 Borrow 事件
	client := s.web3Client.GetClient()
	currentBlock, err := client.BlockNumber(ctx)
	if err != nil {
		log.Strategy().Warn().Err(err).Msg("清算：获取区块号失败")
		return
	}

	if *lastScannedBlock > 0 && currentBlock > *lastScannedBlock {
		_, err := s.collector.ScanBorrowEvents(ctx, *lastScannedBlock+1, currentBlock)
		if err != nil {
			log.Strategy().Debug().Err(err).Msg("清算：增量事件扫描失败")
		}
	}
	*lastScannedBlock = currentBlock

	// Step 2: 批量查询健康因子
	positions, err := s.collector.BatchCheckHealthFactors(ctx)
	if err != nil {
		log.Strategy().Warn().Err(err).Msg("清算：批量健康因子查询失败")
		return
	}

	s.statsMu.Lock()
	s.totalChecked += int64(len(positions))
	s.statsMu.Unlock()

	// 统计高风险仓位
	atRisk := s.collector.GetAtRiskPositions(s.config.WatchThreshold)
	liquidatable := s.collector.GetLiquidatablePositions()

	// 每次都输出简洁状态（方便 dry-run 监控）
	borrowerCount := s.collector.GetBorrowerCount()
	if len(atRisk) > 0 || len(liquidatable) > 0 {
		log.Strategy().Info().
			Int("borrowers", borrowerCount).
			Int("with_debt", len(positions)).
			Int("at_risk", len(atRisk)).
			Int("liquidatable", len(liquidatable)).
			Dur("elapsed", time.Since(cycleStart)).
			Msg("清算：⚠️ 发现高风险仓位")

		// 输出 Top 5 高风险仓位（避免日志刷屏）
		maxShow := 5
		if len(atRisk) < maxShow {
			maxShow = len(atRisk)
		}
		for i := 0; i < maxShow; i++ {
			p := atRisk[i]
			debtUSD := baseToUSD(p.TotalDebtBase)
			collUSD := baseToUSD(p.TotalCollateralBase)
			log.Strategy().Info().
				Str("user", p.User.Hex()[:10]+"...").
				Float64("health_factor", p.HealthFactorFloat()).
				Float64("debt_usd", debtUSD).
				Float64("collateral_usd", collUSD).
				Bool("liquidatable", p.IsLiquidatable()).
				Msg("清算：高风险仓位")
		}
	} else if borrowerCount > 0 {
		// 正常运行时每次简洁输出
		log.Strategy().Debug().
			Int("borrowers", borrowerCount).
			Int("with_debt", len(positions)).
			Dur("elapsed", time.Since(cycleStart)).
			Msg("清算：周期完成，无高风险仓位")
	}

	// Step 3: 检测清算机会
	if len(liquidatable) == 0 {
		return
	}

	opportunities, err := s.detector.Detect(ctx)
	if err != nil {
		log.Strategy().Warn().Err(err).Msg("清算：机会检测失败")
		return
	}

	if len(opportunities) == 0 {
		return
	}

	s.statsMu.Lock()
	s.totalDetected += int64(len(opportunities))
	s.statsMu.Unlock()

	// Step 4: 执行清算
	for _, opp := range opportunities {
		s.executeLiquidation(ctx, opp)
	}
}

// Stats 返回统计信息
func (s *Service) Stats() map[string]interface{} {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()

	s.precomputedOppsMu.RLock()
	precomputed := len(s.precomputedOpps)
	s.precomputedOppsMu.RUnlock()

	stats := map[string]interface{}{
		"total_checked":    s.totalChecked,
		"total_detected":   s.totalDetected,
		"total_executed":   s.totalExecuted,
		"total_profit":     s.totalProfit.String(),
		"borrowers":        s.collector.GetBorrowerCount(),
		"reserves":         len(s.collector.GetReserves()),
		"at_risk":          len(s.collector.GetAtRiskPositions(s.config.WatchThreshold)),
		"liquidatable":     len(s.collector.GetLiquidatablePositions()),
		"precomputed_opps": precomputed,
		"oracle_triggers":  s.oracleTriggers,
	}

	if s.oracleMonitor != nil {
		for k, v := range s.oracleMonitor.Stats() {
			stats["oracle_"+k] = v
		}
	}

	return stats
}

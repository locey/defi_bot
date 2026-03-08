package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"math/big"

	"github.com/defi-bot/backend/internal/api"
	"github.com/defi-bot/backend/internal/cexdex"
	"github.com/defi-bot/backend/internal/collector"
	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/internal/executor"
	"github.com/defi-bot/backend/internal/metrics"
	"github.com/defi-bot/backend/internal/models"
	"github.com/defi-bot/backend/internal/scheduler"
	"github.com/defi-bot/backend/internal/strategy"
	"github.com/defi-bot/backend/pkg/cache"
	"github.com/defi-bot/backend/pkg/cex"
	"github.com/defi-bot/backend/pkg/log"
	"github.com/defi-bot/backend/pkg/web3"
	"github.com/ethereum/go-ethereum/common"
)

var (
	configPath = flag.String("config", "configs/config.yaml", "配置文件路径")
	migrate    = flag.Bool("migrate", false, "执行数据库迁移")
	seed       = flag.Bool("seed", false, "初始化种子数据")
)

func main() {
	flag.Parse()

	// 1. 加载配置
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		fmt.Printf("加载配置失败: %v\n", err)
		os.Exit(1)
	}

	// 2. 初始化日志
	if err := log.Init(&cfg.Log); err != nil {
		fmt.Printf("日志初始化失败: %v\n", err)
		os.Exit(1)
	}

	log.Main().Info().Msg("========================================")
	log.Main().Info().Msg("DeFi 套利机器人后端服务")
	log.Main().Info().Msg("========================================")

	// 2.1 启动 Prometheus 指标服务（可选）
	if cfg.Metrics.Enabled {
		port := cfg.Metrics.Port
		if port <= 0 {
			port = 9090
		}
		metrics.StartMetricsServer(fmt.Sprintf(":%d", port))
	}

	// 3. 初始化数据库
	log.Main().Info().Msg("初始化数据库...")
	if err := database.InitDB(&cfg.Database); err != nil {
		log.Main().Fatal().Err(err).Msg("数据库初始化失败")
	}
	defer database.CloseDB()

	// 3. 执行数据库迁移
	if *migrate {
		log.Main().Info().Msg("执行数据库迁移...")
		if err := database.AutoMigrate(); err != nil {
			log.Main().Fatal().Err(err).Msg("数据库迁移失败")
		}
		log.Main().Info().Msg("数据库迁移完成")

		if !*seed {
			return
		}
	}

	// 4. 初始化种子数据
	if *seed {
		log.Main().Info().Msg("初始化种子数据...")
		if err := database.SeedData(cfg); err != nil {
			log.Main().Fatal().Err(err).Msg("种子数据初始化失败")
		}
		return
	}

	// 4.1 ✅ 自动检测：如果数据库没有基础数据，自动初始化
	db := database.GetDB()
	var tokenCount, dexCount int64
	db.Model(&models.Token{}).Count(&tokenCount)
	db.Model(&models.Exchange{}).Where("exchange_type = ?", "dex").Count(&dexCount)

	if tokenCount == 0 || dexCount == 0 {
		log.Main().Warn().Int64("tokens", tokenCount).Int64("dexes", dexCount).Msg("⚠️  检测到数据库缺少基础数据")
		log.Main().Info().Msg("🔧 自动初始化种子数据...")
		if err := database.SeedData(cfg); err != nil {
			log.Main().Fatal().Err(err).Msg("自动种子数据初始化失败")
		}
		log.Main().Info().Msg("✅ 种子数据初始化完成")
	} else {
		log.Main().Info().Int64("tokens", tokenCount).Int64("dexes", dexCount).Msg("✅ 数据库检查通过")
	}

	// 5. 初始化 Web3 客户端
	log.Main().Info().Msg("初始化 Web3 客户端...")
	web3Client, err := web3.NewClient(
		cfg.Blockchain.RPCURL,
		cfg.Blockchain.ChainID,
		cfg.Blockchain.Timeout,
	)
	if err != nil {
		log.Main().Fatal().Err(err).Msg("Web3 客户端初始化失败")
	}
	defer web3Client.Close()

	// 6. 初始化 Redis 缓存（可选）
	var redisCache *cache.RedisCache
	if cfg.Redis.Enabled {
		log.Main().Info().Msg("初始化 Redis 缓存...")
		var err error
		redisCache, err = cache.NewRedisCache(&cache.RedisConfig{
			Host:     cfg.Redis.Host,
			Port:     cfg.Redis.Port,
			Password: cfg.Redis.Password,
			DB:       cfg.Redis.DB,
			TTL:      time.Duration(cfg.Redis.TTL) * time.Second,
		})
		if err != nil {
			log.Main().Warn().Err(err).Msg("Redis 初始化失败（将不使用缓存）")
			redisCache = nil
		} else {
			defer redisCache.Close()
		}
	}

	// 7. 初始化 CEX 采集器（如果启用）
	var cexCollector *collector.CexCollector
	if cfg.Cex.Enabled && cfg.Cex.Binance.Enabled {
		log.Main().Info().Msg("初始化 CEX 采集器（币安）...")
		cexCollector = collector.NewCexCollector(
			&cex.BinanceConfig{
				APIEndpoint: cfg.Cex.Binance.APIEndpoint,
				WSEndpoint:  cfg.Cex.Binance.WSEndpoint,
				APIKey:      cfg.Cex.Binance.APIKey,
				APISecret:   cfg.Cex.Binance.APISecret,
				RateLimit:   cfg.Cex.Binance.RateLimit,
			},
			redisCache,
		)
	}

	// 8. 创建数据采集器（集成 DEX + CEX）
	log.Main().Info().Msg("创建数据采集器...")
	dataCollector := collector.NewCollector(web3Client, cexCollector, redisCache)

	// 9. 初始化策略引擎
	log.Main().Info().Msg("初始化策略引擎...")
	strategyConfig := &strategy.StrategyConfig{
		MinProfitRate:      cfg.Arbitrage.MinProfitRate / 100.0, // 转换为小数
		MaxPathLength:      5,
		MinPathLength:      3,
		MaxSlippage:        cfg.Arbitrage.MaxSlippage / 100.0,
		GasMultiplier:      2.0,
		ValidityDuration:   30 * time.Second,
		MaxConcurrentPaths: 20,
	}

	// ✅ 从配置文件注入 BaseTokens & SupportedDexes（否则路径发现永远为空）
	for _, t := range cfg.Tokens {
		if t.Address == "" {
			continue
		}
		strategyConfig.BaseTokens = append(strategyConfig.BaseTokens, common.HexToAddress(t.Address))
	}
	for _, d := range cfg.Dexes {
		if d.Router == "" {
			continue
		}
		dexCfg := strategy.DexConfig{
			Name:           d.Name,
			RouterAddress:  common.HexToAddress(d.Router),
			FactoryAddress: common.HexToAddress(d.Factory),
			Type:           d.Protocol,
			Fee:            uint64(d.Fee),
		}
		strategyConfig.SupportedDexes = append(strategyConfig.SupportedDexes, dexCfg)
	}

	strategyEngine := strategy.NewStrategyEngine(
		strategyConfig,
		web3Client,
		db,
		redisCache,
	)

	// 启动策略引擎
	ctx := context.Background()
	if err := strategyEngine.Start(ctx); err != nil {
		log.Main().Warn().Err(err).Msg("策略引擎启动失败")
	}

	// 10. 初始化执行器（如果配置了 Keeper 私钥）
	var arbitrageExecutor *executor.ArbitrageExecutor
	if cfg.Keeper.PrivateKey != "" && cfg.Contracts.ArbitrageCore != "" {
		log.Main().Info().Msg("初始化套利执行器...")
		arbitrageExecutor = executor.NewArbitrageExecutor(
			web3Client,
			common.HexToAddress(cfg.Contracts.ArbitrageCore),
			cfg.Keeper.PrivateKey,
		)
		// 设置数据库连接，用于保存执行记录
		arbitrageExecutor.SetDB(db)

		// 配置 Flash Loan 合约地址（如果有）
		if cfg.Contracts.FlashLoanArbitrage != "" {
			arbitrageExecutor.SetFlashLoanAddress(common.HexToAddress(cfg.Contracts.FlashLoanArbitrage))
			log.Main().Info().Str("address", cfg.Contracts.FlashLoanArbitrage).Msg("✅ Flash Loan 合约已配置")
		}

		log.Main().Info().Msg("✅ 套利执行器已初始化（自动执行模式）")
	} else {
		log.Main().Warn().Msg("未配置 Keeper 私钥或合约地址，仅分析模式（不会自动执行）")
	}

	// 11. 创建调度器（Phase 1.1: 支持高性能模式）
	schedulerMode := cfg.Scheduler.Mode
	if schedulerMode == "" {
		schedulerMode = "high_performance" // 默认使用高性能模式
	}

	// Phase 1.1: 通用停止接口
	type Stopper interface {
		Stop()
	}
	var activeScheduler Stopper
	var highPerfScheduler *scheduler.HighPerformanceScheduler // 用于 CEX-DEX 套利

	switch schedulerMode {
	case "high_performance":
		log.Main().Info().Msg("创建高性能事件驱动调度器...")

		// 构建基础代币列表
		var baseTokens []common.Address
		for _, t := range cfg.Tokens {
			if t.Address != "" {
				baseTokens = append(baseTokens, common.HexToAddress(t.Address))
			}
		}

		// 构建 DEX 名称 → Router 地址映射
		dexRouters := make(map[string]common.Address)
		for _, d := range cfg.Dexes {
			if d.Router != "" && d.Name != "" {
				dexRouters[d.Name] = common.HexToAddress(d.Router)
			}
		}

		hpConfig := &scheduler.HighPerformanceConfig{
			WSURL:                   cfg.Blockchain.WSURL,
			ChainID:                 cfg.Blockchain.ChainID,
			BaseTokens:              baseTokens,
			DexRouters:              dexRouters,
			MaxConcurrentExecutions: cfg.Scheduler.MaxConcurrentExec,
			MinConfidence:           cfg.Scheduler.MinConfidence,
			EnableExecution:         cfg.Scheduler.EnableExecution,
			DryRun:                  cfg.Scheduler.DryRun,
			// eth_call 模拟配置
			ContractAddress:  cfg.Contracts.ArbitrageCore,
			KeeperPrivateKey: cfg.Keeper.PrivateKey,
			EnableSimulation: cfg.Contracts.ArbitrageCore != "",
			// Flash Loan 配置
			FlashLoanAddress: cfg.Contracts.FlashLoanArbitrage,
			EnableFlashLoan:  cfg.Contracts.FlashLoanArbitrage != "",
			// 动态价差扫描器（自动发现跨 DEX 套利，不依赖预设代币列表）
			EnableSpreadScanner: true,
			MinSpreadBps:        15, // 0.15% 最小触发价差（让更多机会进入 eth_call 验证）
		}
		if hpConfig.MaxConcurrentExecutions == 0 {
			hpConfig.MaxConcurrentExecutions = 3
		}
		if hpConfig.MinConfidence == 0 {
			hpConfig.MinConfidence = 0.3 // 降低阈值让更多机会通过，eth_call 模拟做最终验证
		}

		hpScheduler, err := scheduler.NewHighPerformanceScheduler(
			db,
			web3Client,
			arbitrageExecutor,
			hpConfig,
		)
		if err != nil {
			log.Main().Warn().Err(err).Msg("高性能调度器创建失败，回退到标准模式")
			schedulerMode = "standard" // 回退
		} else {
			if err := hpScheduler.Start(); err != nil {
				log.Main().Warn().Err(err).Msg("高性能调度器启动失败，回退到标准模式")
				schedulerMode = "standard"
			} else {
				activeScheduler = hpScheduler
				highPerfScheduler = hpScheduler // 保存引用供 CEX-DEX 使用
				log.Main().Info().Msg("高性能事件驱动调度器已启动")
			}
		}
	}

	// 标准模式（或高性能模式回退时）
	if schedulerMode == "standard" || activeScheduler == nil {
		log.Main().Info().Msg("创建标准定时任务调度器...")
		taskScheduler := scheduler.NewScheduler(
			dataCollector,
			strategyEngine,
			arbitrageExecutor,
			&cfg.Scheduler,
		)

		if err := taskScheduler.Start(ctx); err != nil {
			log.Main().Fatal().Err(err).Msg("启动调度器失败")
		}
		activeScheduler = taskScheduler

		// 标准模式需要初始数据采集
		log.Main().Info().Msg("执行初始数据采集...")
		if err := dataCollector.CollectAllData(); err != nil {
			log.Main().Warn().Err(err).Msg("初始数据采集失败")
		}
	}

	// 13. CEX-DEX 套利检测器（如果配置了 Binance 且 CEXDEX 启用）
	if cfg.CEXDEX.Enabled && cfg.Cex.Enabled && cfg.Cex.Binance.Enabled {
		log.Main().Info().Msg("初始化 CEX-DEX 套利检测器...")

		// 创建 CEX 价格监控器
		cexMonitor := cexdex.NewPriceMonitor(&cexdex.PriceMonitorConfig{
			Symbols:        cfg.Cex.Binance.Symbols,
			BinanceWSURL:   cfg.Cex.Binance.WSEndpoint,
			ReconnectDelay: 5 * time.Second,
			PingInterval:   30 * time.Second,
			BufferSize:     1000,
		})

		// 创建 DEX 价格适配器（连接到 PriceCache）
		var dexPriceProvider cexdex.DEXPriceProvider
		if highPerfScheduler != nil {
			priceCache := highPerfScheduler.GetPriceCache()
			if priceCache != nil {
				dexPriceProvider = cexdex.NewDEXPriceAdapter(priceCache)
				log.Main().Info().Msg("✅ DEX 价格适配器已创建，连接到 PriceCache")
			}
		}

		// 创建 CEX-DEX 检测器
		ttlDuration := time.Duration(cfg.CEXDEX.OpportunityTTL) * time.Second
		cexdexDetector := cexdex.NewDetector(
			&cexdex.DetectorConfig{
				MinProfitRate:   cfg.CEXDEX.MinProfitRate,
				MinProfitAmount: cfg.CEXDEX.MinProfitAmount,
				MaxTradeAmount:  cfg.CEXDEX.MaxTradeAmount,
				MinTradeAmount:  cfg.CEXDEX.MinTradeAmount,
				OpportunityTTL:  ttlDuration,
			},
			cexMonitor,
			dexPriceProvider,
		)

		// 启动 CEX 价格监控
		go func() {
			if err := cexMonitor.Start(ctx); err != nil {
				log.Main().Warn().Err(err).Msg("CEX 价格监控启动失败")
			}
		}()

		// 启动 CEX-DEX 检测器
		go func() {
			if err := cexdexDetector.Start(ctx); err != nil {
				log.Main().Warn().Err(err).Msg("CEX-DEX 检测器启动失败")
			}
		}()

		// 桥接 CEX-DEX 机会到主执行管道
		go func() {
			for opp := range cexdexDetector.GetOpportunityChan() {
				// 验证 TokenIn/TokenOut 已填充（Step 4 修复）
				emptyAddr := common.Address{}
				if opp.TokenIn == emptyAddr || opp.TokenOut == emptyAddr {
					log.Main().Warn().Str("id", opp.ID).Msg("CEX-DEX: TokenIn/TokenOut empty, skipping")
					continue
				}

				// 构建正确的 SwapPath: [asset, tokenOut, asset]
				// asset = TokenIn（套利起点/终点），中间代币 = TokenOut
				// 合约要求: swapPath[0] == swapPath[last] == asset
				asset := opp.TokenIn
				midToken := opp.TokenOut
				swapPath := []common.Address{asset, midToken, asset}

				// Dexes: 买入和卖出用同一个 DEX Router（2步 = 2个 router）
				dexes := []common.Address{opp.DEXRouter, opp.DEXRouter}

				// FeeTiers: 从 DEXPriceAdapter 获取的池 fee tier
				feeTiers := []uint32{opp.FeeTier, opp.FeeTier}

				arbOpp := &strategy.ArbitrageOpportunity{
					ID:         opp.ID,
					SwapPath:   swapPath,
					Dexes:      dexes,
					DexNames:   []string{"CEX-DEX:" + opp.Direction, "CEX-DEX:" + opp.Direction},
					FeeTiers:   feeTiers,
					ProfitRate: opp.ProfitRate,
					Confidence: opp.Confidence,
					Timestamp:  opp.CreatedAt,
					ValidUntil: opp.ValidUntil,
					IsCex:      true,
					PathLength: 2,
				}
				// 转换金额 (USD -> wei 需要价格转换, 简化为直接设置)
				if opp.TradeAmount > 0 {
					arbOpp.AmountIn = new(big.Int).SetUint64(uint64(opp.TradeAmount * 1e6)) // USDC 精度
				}
				if opp.ExpectProfit > 0 {
					arbOpp.ExpectProfit = new(big.Int).SetUint64(uint64(opp.ExpectProfit * 1e6))
				}
				arbOpp.MinProfit = big.NewInt(0)

				log.Main().Info().
					Str("id", opp.ID).
					Str("direction", opp.Direction).
					Str("asset", asset.Hex()[:14]).
					Str("mid_token", midToken.Hex()[:14]).
					Int("path_len", len(swapPath)).
					Float64("profit_rate", opp.ProfitRate*100).
					Float64("net_profit_usd", opp.NetProfit).
					Msg("CEX-DEX opportunity detected (valid SwapPath)")

				// 尝试通过主执行器执行（需要 enable_execution=true）
				if arbitrageExecutor != nil && cfg.Scheduler.EnableExecution && !cfg.Scheduler.DryRun {
					execCtx, execCancel := context.WithTimeout(ctx, 30*time.Second)
					result, err := arbitrageExecutor.Execute(execCtx, arbOpp)
					execCancel()
					if err != nil {
						log.Main().Warn().Err(err).Str("id", opp.ID).Msg("CEX-DEX execution failed")
					} else if result != nil && result.Success {
						log.Main().Info().
							Str("tx_hash", result.TxHash).
							Str("profit", result.ActualProfit.String()).
							Msg("✅ CEX-DEX execution succeeded")
					}
				} else {
					log.Main().Info().
						Str("id", opp.ID).
						Str("direction", opp.Direction).
						Float64("net_profit_usd", opp.NetProfit).
						Msg("📊 CEX-DEX opportunity (dry-run, not executing)")
				}
			}
		}()

		log.Main().Info().Msg("✅ CEX-DEX 套利检测器已启动")
	} else {
		log.Main().Info().Msg("CEX-DEX 套利未启用（需要配置 Binance API 并启用 cexdex.enabled）")
	}

	// 14. 启动 API 服务器
	apiPort := cfg.Server.APIPort
	if apiPort == 0 {
		apiPort = 8080 // 默认端口
	}
	apiServer := api.NewAPIServer(db)
	go func() {
		apiAddr := fmt.Sprintf(":%d", apiPort)
		log.API().Info().Str("addr", apiAddr).Msg("🚀 启动 API 服务器")
		if err := apiServer.Run(apiAddr); err != nil {
			log.API().Warn().Err(err).Msg("API 服务器启动失败")
		}
	}()

	// 15. 等待退出信号
	log.Main().Info().Msg("========================================")
	log.Main().Info().Msg("服务已启动，按 Ctrl+C 退出")
	log.Main().Info().Msg("========================================")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	// 15. 优雅关闭
	log.Main().Info().Msg("正在关闭服务...")
	if activeScheduler != nil {
		activeScheduler.Stop()
	}
	strategyEngine.Stop()
	log.Main().Info().Msg("服务已关闭")
	log.Close() // 关闭日志系统
}

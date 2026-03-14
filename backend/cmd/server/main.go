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
	"github.com/defi-bot/backend/internal/discovery"
	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/internal/executor"
	"github.com/defi-bot/backend/internal/liquidation"
	"github.com/defi-bot/backend/internal/metrics"
	"github.com/defi-bot/backend/internal/models"
	"github.com/defi-bot/backend/internal/scheduler"
	"github.com/defi-bot/backend/internal/solver"
	"github.com/defi-bot/backend/internal/strategy"
	"github.com/defi-bot/backend/pkg/aggregator"
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

	// 5b. 创建 RPC 客户端池（多节点轮询，降低 429 限流）
	var rpcPool *web3.ClientPool
	if cfg.Blockchain.UsePool && len(cfg.Blockchain.RPCURLs) > 1 {
		log.Main().Info().Int("rpc_count", len(cfg.Blockchain.RPCURLs)).Msg("创建 RPC 客户端池...")
		poolCfg := &web3.ClientPoolConfig{
			RPCURLs:       cfg.Blockchain.RPCURLs,
			ChainID:       cfg.Blockchain.ChainID,
			Timeout:       cfg.Blockchain.Timeout,
			HealthCheck:   true,
			CheckInterval: 60 * time.Second,
		}
		var poolErr error
		rpcPool, poolErr = web3.NewClientPool(poolCfg)
		if poolErr != nil {
			log.Main().Warn().Err(poolErr).Msg("RPC 客户端池创建失败，将使用单节点")
		} else {
			defer rpcPool.Close()
			log.Main().Info().Int("nodes", rpcPool.GetClientCount()).Msg("✅ RPC 客户端池已创建")
		}
	}

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

	// 启动策略引擎（使用可取消的 context，支持 graceful shutdown）
	ctx, ctxCancel := context.WithCancel(context.Background())
	defer ctxCancel()
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

		// Pre-flight: 如果启用了执行，验证 Keeper 地址有 ETH 余额
		if cfg.Scheduler.EnableExecution && !cfg.Scheduler.DryRun {
			log.Main().Info().Msg("========== 🚀 LIVE MODE PRE-FLIGHT CHECK ==========")
			keeperAddr := common.HexToAddress(cfg.Keeper.Address)
			balance, balErr := web3Client.GetClient().BalanceAt(context.Background(), keeperAddr, nil)
			if balErr != nil {
				log.Main().Error().Err(balErr).Msg("❌ 无法查询 Keeper ETH 余额")
			} else {
				balFloat := new(big.Float).Quo(new(big.Float).SetInt(balance), new(big.Float).SetFloat64(1e18))
				log.Main().Info().Str("address", keeperAddr.Hex()).Str("balance_eth", balFloat.Text('f', 6)).Msg("Keeper 地址余额")
				// 最低要求 0.001 ETH（约 $2.5，够执行几十笔 Arbitrum 交易）
				minBalance := new(big.Int).SetUint64(1_000_000_000_000_000) // 0.001 ETH
				if balance.Cmp(minBalance) < 0 {
					log.Main().Error().Msg("❌ Keeper ETH 余额不足（最少 0.001 ETH），将回退到 dry-run 模式")
					cfg.Scheduler.DryRun = true
				}
			}
			log.Main().Info().Bool("execution", cfg.Scheduler.EnableExecution).Bool("dry_run", cfg.Scheduler.DryRun).Msg("执行模式确认")
			log.Main().Info().Msg("====================================================")
		}
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

	// DEX 名称 → Router 地址映射（供调度器 + CEX-DEX 跨 DEX 搜索共用）
	dexRouters := make(map[string]common.Address)

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

		// 填充 DEX Router 映射，提取 QuoterV2 地址
		quoterAddr := ""
		for _, d := range cfg.Dexes {
			if d.Router != "" && d.Name != "" {
				dexRouters[d.Name] = common.HexToAddress(d.Router)
			}
			// 取第一个非空 Quoter 地址（同链所有 V3 DEX 共用同一个 QuoterV2）
			if d.Quoter != "" && quoterAddr == "" {
				quoterAddr = d.Quoter
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
			// RPC 客户端池（多节点轮询，eth_call 429 时自动轮换）
			RPCPool: rpcPool,
			// QuoterV2 精确报价（消除 V3 单 tick 880x 高估）
			QuoterAddress: quoterAddr,
		}
		if hpConfig.MaxConcurrentExecutions == 0 {
			hpConfig.MaxConcurrentExecutions = 3
		}
		if hpConfig.MinConfidence == 0 {
			hpConfig.MinConfidence = 0.3 // 降低阈值让更多机会通过，eth_call 模拟做最终验证
		}

		// 高性能模式也需要初始交易对发现（通过 factory 合约查询链上池）
		log.Main().Info().Msg("高性能模式：执行初始交易对发现...")
		if err := dataCollector.CollectTradingPairs(); err != nil {
			log.Main().Warn().Err(err).Msg("初始交易对发现失败（将使用已有数据）")
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

	// 13x. 1inch 路由验证 + 池发现服务（依赖高性能调度器）
	var discoveryService *discovery.DiscoveryService
	if highPerfScheduler != nil {
		// 注入 1inch 路由验证器（在 Quoter 之后、Simulator 之前过滤虚假利润）
		oneInchForRouter := aggregator.NewOneInchClient(&aggregator.OneInchConfig{
			ChainID: int64(cfg.Blockchain.ChainID),
		})
		aggRouter := strategy.NewAggregatorRouter(oneInchForRouter)
		highPerfScheduler.SetAggregatorRouter(aggRouter)
		log.Main().Info().Msg("✅ 1inch 路由验证已注入调度器")

		// 启动池发现服务（监听工厂事件，自动扩展 FastCollector 监控池）
		if cfg.Blockchain.WSURL != "" {
			// 构建工厂列表
			type dexInfo struct {
				Name     string
				Factory  string
				Protocol string
			}
			var dexInfos []dexInfo
			for _, d := range cfg.Dexes {
				dexInfos = append(dexInfos, dexInfo{Name: d.Name, Factory: d.Factory, Protocol: d.Protocol})
			}
			// 转换为 BuildFactories 需要的格式
			var factoryInputs []struct {
				Name     string
				Factory  string
				Protocol string
			}
			for _, di := range dexInfos {
				factoryInputs = append(factoryInputs, struct {
					Name     string
					Factory  string
					Protocol string
				}{Name: di.Name, Factory: di.Factory, Protocol: di.Protocol})
			}

			// 构建核心代币
			var tokenAddrs []string
			for _, t := range cfg.Tokens {
				if t.Address != "" {
					tokenAddrs = append(tokenAddrs, t.Address)
				}
			}

			discoveryCfg := &discovery.ServiceConfig{
				WSURL:      cfg.Blockchain.WSURL,
				Factories:  discovery.BuildFactories(factoryInputs),
				CoreTokens: discovery.BuildCoreTokens(tokenAddrs),
			}

			fc := highPerfScheduler.GetCollector()
			if fc != nil {
				discoveryService = discovery.NewDiscoveryService(discoveryCfg, fc)
				if err := discoveryService.Start(ctx); err != nil {
					log.Main().Warn().Err(err).Msg("池发现服务启动失败")
				} else {
					log.Main().Info().Int("factories", len(discoveryCfg.Factories)).Msg("✅ 池发现服务已启动")
				}
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
	var cexMonitor *cexdex.PriceMonitor
	var cexdexDetector *cexdex.Detector
	if cfg.CEXDEX.Enabled && cfg.Cex.Enabled && cfg.Cex.Binance.Enabled {
		log.Main().Info().Msg("初始化 CEX-DEX 套利检测器...")

		// 创建 CEX 价格监控器
		cexMonitor = cexdex.NewPriceMonitor(&cexdex.PriceMonitorConfig{
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
				// 从配置构建代币映射（多链支持）
				var tokenConfigs []cexdex.TokenConfig
				for _, t := range cfg.Tokens {
					tokenConfigs = append(tokenConfigs, cexdex.TokenConfig{
						Symbol:    t.Symbol,
						Address:   t.Address,
						CEXSymbol: t.CEXSymbol,
					})
				}
				dexPriceProvider = cexdex.NewDEXPriceAdapter(priceCache, tokenConfigs)
				log.Main().Info().Msg("✅ DEX 价格适配器已创建，连接到 PriceCache")
			}
		}

		// 创建 CEX-DEX 检测器
		ttlDuration := time.Duration(cfg.CEXDEX.OpportunityTTL) * time.Second
		cexdexDetector = cexdex.NewDetector(
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
		// CEX-DEX 策略: CEX 价格作为预言机，在 DEX 间寻找跨池价差
		// 买入腿: 用检测到的最低价池
		// 卖出腿: 在 PriceCache 中搜索同 token pair 的最高价池
		go func() {
			for opp := range cexdexDetector.GetOpportunityChan() {
				// 验证 TokenIn/TokenOut 已填充
				emptyAddr := common.Address{}
				if opp.TokenIn == emptyAddr || opp.TokenOut == emptyAddr {
					log.Main().Warn().Str("id", opp.ID).Msg("CEX-DEX: TokenIn/TokenOut empty, skipping")
					continue
				}

				asset := opp.TokenIn
				midToken := opp.TokenOut
				swapPath := []common.Address{asset, midToken, asset}

				// 跨 DEX 搜索: 为卖出腿找一个不同的池（价格更高）
				buyRouter := opp.DEXRouter
				sellRouter := opp.DEXRouter
				buyFeeTier := opp.FeeTier
				sellFeeTier := opp.FeeTier
				crossDEX := false

				pcache := highPerfScheduler.GetPriceCache()
				if pcache != nil {
					pools := pcache.GetByTokenPair(midToken.Hex(), asset.Hex())
					if len(pools) > 1 {
						// 找到卖出池: 不同 DEX、价格最高的池
						var bestSellPrice float64
						for _, p := range pools {
							if p.PoolAddress == opp.DEXPool.Hex() {
								continue // 跳过买入池
							}
							if p.Price <= 0 {
								continue
							}
							// 从 dexRouters map 查找 router 地址
							router, hasRouter := dexRouters[p.DexName]
							if !hasRouter {
								continue
							}
							if p.Price > bestSellPrice {
								bestSellPrice = p.Price
								sellRouter = router
								if p.IsV3 {
									sellFeeTier = uint32(p.Fee * 100) // bps→raw
									if sellFeeTier == 0 {
										sellFeeTier = 3000
									}
								} else {
									sellFeeTier = 0
								}
							}
						}
						if sellRouter != buyRouter {
							crossDEX = true
						}
					}
				}

				dexes := []common.Address{buyRouter, sellRouter}
				feeTiers := []uint32{buyFeeTier, sellFeeTier}
				dexLabel := "CEX-DEX"
				if crossDEX {
					dexLabel = "CrossDEX"
				}

				arbOpp := &strategy.ArbitrageOpportunity{
					ID:         opp.ID,
					SwapPath:   swapPath,
					Dexes:      dexes,
					DexNames:   []string{dexLabel + ":" + opp.Direction, dexLabel + ":sell"},
					FeeTiers:   feeTiers,
					ProfitRate: opp.ProfitRate,
					Confidence: opp.Confidence,
					Timestamp:  opp.CreatedAt,
					ValidUntil: opp.ValidUntil,
					IsCex:      true,
					PathLength: 2,
				}
				// 转换金额 (USD -> wei)
				if opp.TradeAmount > 0 {
					arbOpp.AmountIn = new(big.Int).SetUint64(uint64(opp.TradeAmount * 1e6)) // USDC 精度
				}
				if opp.ExpectProfit > 0 {
					arbOpp.ExpectProfit = new(big.Int).SetUint64(uint64(opp.ExpectProfit * 1e6))
				}
				// MinProfit = 2×gasCost
				gasCostWei := big.NewInt(60_000_000_000_000) // 0.00006 ETH
				arbOpp.MinProfit = gasCostWei

				log.Main().Info().
					Str("id", opp.ID).
					Str("direction", opp.Direction).
					Str("mode", dexLabel).
					Bool("cross_dex", crossDEX).
					Float64("profit_rate", opp.ProfitRate*100).
					Float64("net_profit_usd", opp.NetProfit).
					Msg("CEX-DEX opportunity detected")

				// 非跨 DEX 时：尝试真正的 CEX-DEX 执行（需要 CEXDEXExecutor）
				if !crossDEX {
					// CEXDEXExecutor 会在 13c 中创建（需要 Binance API key + 余额）
					// 这里只记录，执行在后续 goroutine 中处理
					log.Main().Info().
						Str("id", opp.ID).
						Str("direction", opp.Direction).
						Float64("spread_pct", opp.ProfitRate*100).
						Float64("net_profit_usd", opp.NetProfit).
						Msg("📊 CEX-DEX spread detected (needs CEX funds to execute)")
					continue
				}

				// 跨 DEX 路由：通过链上合约执行
				if arbitrageExecutor != nil && cfg.Scheduler.EnableExecution && !cfg.Scheduler.DryRun {
					execCtx, execCancel := context.WithTimeout(ctx, 30*time.Second)
					result, err := arbitrageExecutor.Execute(execCtx, arbOpp)
					execCancel()
					if err != nil {
						log.Main().Warn().Err(err).Str("id", opp.ID).Msg("CrossDEX execution failed")
					} else if result != nil && result.Success {
						log.Main().Info().
							Str("tx_hash", result.TxHash).
							Str("profit", result.ActualProfit.String()).
							Msg("✅ CrossDEX execution succeeded")
					}
				} else {
					log.Main().Info().
						Str("id", opp.ID).
						Bool("cross_dex", crossDEX).
						Float64("net_profit_usd", opp.NetProfit).
						Msg("📊 CEX-DEX opportunity (dry-run)")
				}
			}
		}()

		log.Main().Info().Msg("✅ CEX-DEX 套利检测器已启动")
	} else {
		log.Main().Info().Msg("CEX-DEX 套利未启用（需要配置 Binance API 并启用 cexdex.enabled）")
	}

	// 13b. 清算机器人（Aave V3 Flash Loan Liquidation）
	var liquidationService *liquidation.Service
	if cfg.Liquidation.Enabled && cfg.Contracts.AaveLendingPool != "" {
		log.Main().Info().Msg("初始化清算机器人...")

		scanInterval := time.Duration(cfg.Liquidation.ScanIntervalSec) * time.Second
		if scanInterval == 0 {
			scanInterval = 10 * time.Second
		}

		liqConfig := &liquidation.ServiceConfig{
			AavePool:          common.HexToAddress(cfg.Contracts.AaveLendingPool),
			AaveDataProvider:  common.HexToAddress(cfg.Contracts.AaveDataProvider),
			KeeperPrivateKey:  cfg.Keeper.PrivateKey,
			ChainID:           cfg.Blockchain.ChainID,
			DryRun:            cfg.Liquidation.DryRun,
			EnableExecution:   cfg.Scheduler.EnableExecution,
			WatchThreshold:    cfg.Liquidation.WatchThreshold,
			MinDebtUSD:        cfg.Liquidation.MinDebtUSD,
			ScanInterval:      scanInterval,
			EventScanBlocks:   uint64(cfg.Liquidation.EventScanBlocks),
			DefaultSwapRouter: common.HexToAddress(cfg.Liquidation.SwapRouter),
			DefaultSwapFee:    uint32(cfg.Liquidation.SwapFeeTier),
		}

		if cfg.Contracts.FlashLoanLiquidator != "" {
			liqConfig.LiquidatorContract = common.HexToAddress(cfg.Contracts.FlashLoanLiquidator)
		}
		if cfg.Contracts.BalancerLiquidator != "" {
			liqConfig.BalancerLiquidatorContract = common.HexToAddress(cfg.Contracts.BalancerLiquidator)
		}

		var liqErr error
		liquidationService, liqErr = liquidation.NewService(web3Client, liqConfig)
		if liqErr != nil {
			log.Main().Warn().Err(liqErr).Msg("清算服务创建失败")
		} else {
			if err := liquidationService.Start(ctx); err != nil {
				log.Main().Warn().Err(err).Msg("清算服务启动失败")
				liquidationService = nil
			} else {
				log.Main().Info().
					Bool("dry_run", cfg.Liquidation.DryRun).
					Float64("watch_threshold", cfg.Liquidation.WatchThreshold).
					Msg("✅ 清算机器人已启动")
			}
		}
	} else {
		log.Main().Info().Msg("清算机器人未启用（需要配置 liquidation.enabled 和 aave_lending_pool）")
	}

	// 13b-2. Compound V3 清算监控（Arbitrum 上 4 个 Comet 市场）
	var compoundV3Monitor *liquidation.CompoundV3Monitor
	if cfg.Liquidation.Enabled {
		markets := liquidation.DefaultArbitrumMarkets()
		var monErr error
		compoundV3Monitor, monErr = liquidation.NewCompoundV3Monitor(
			web3Client,
			markets,
			time.Duration(cfg.Liquidation.ScanIntervalSec)*time.Second,
		)
		if monErr != nil {
			log.Main().Warn().Err(monErr).Msg("Compound V3 监控创建失败")
		} else {
			if err := compoundV3Monitor.Start(ctx); err != nil {
				log.Main().Warn().Err(err).Msg("Compound V3 监控启动失败")
				compoundV3Monitor = nil
			} else {
				log.Main().Info().Int("markets", len(markets)).Msg("✅ Compound V3 清算监控已启动")
			}
		}
	}

	// 13b-3. Silo Finance V1 清算监控（隔离借贷市场，内置闪电清算）
	var siloV1Monitor *liquidation.SiloV1Monitor
	if cfg.Liquidation.Enabled {
		var siloErr error
		siloV1Monitor, siloErr = liquidation.NewSiloV1Monitor(
			web3Client,
			time.Duration(cfg.Liquidation.ScanIntervalSec)*time.Second,
		)
		if siloErr != nil {
			log.Main().Warn().Err(siloErr).Msg("Silo V1 监控创建失败")
		} else {
			if err := siloV1Monitor.Start(ctx); err != nil {
				log.Main().Warn().Err(err).Msg("Silo V1 监控启动失败")
				siloV1Monitor = nil
			} else {
				log.Main().Info().Msg("✅ Silo V1 清算监控已启动")
			}
		}
	}

	// 13c. Binance 交易客户端 + CEX-DEX 执行器
	var binanceTrader *cex.BinanceTrader
	var cexdexExecutor *cexdex.CEXDEXExecutor
	if cfg.Cex.Enabled && cfg.Cex.Binance.Enabled &&
		cfg.Cex.Binance.APIKey != "" && cfg.Cex.Binance.APISecret != "" {
		log.Main().Info().Msg("初始化 Binance 交易客户端...")
		binanceTrader = cex.NewBinanceTrader(&cex.BinanceConfig{
			APIEndpoint: cfg.Cex.Binance.APIEndpoint,
			APIKey:      cfg.Cex.Binance.APIKey,
			APISecret:   cfg.Cex.Binance.APISecret,
			RateLimit:   cfg.Cex.Binance.RateLimit,
		})
		log.Main().Info().Msg("✅ Binance 交易客户端已创建")

		// 创建 CEX-DEX 执行器（连接 Binance 交易 + DEX on-chain 交易）
		if cfg.Keeper.PrivateKey != "" && cexdexDetector != nil {
			txMgrForCEXDEX := executor.NewTxManager(
				web3Client.GetClient(),
				big.NewInt(int64(cfg.Blockchain.ChainID)),
				executor.DefaultTxManagerConfig(),
			)
			execConfig := cexdex.DefaultCEXDEXExecutorConfig()
			execConfig.PrivateKey = cfg.Keeper.PrivateKey
			execConfig.KeeperAddress = common.HexToAddress(cfg.Keeper.Address)

			cexdexExecutor = cexdex.NewCEXDEXExecutor(execConfig, txMgrForCEXDEX, web3Client.GetClient(), binanceTrader)
			if err := cexdexExecutor.Start(ctx); err != nil {
				log.Main().Warn().Err(err).Msg("CEX-DEX 执行器启动失败")
			} else {
				log.Main().Info().Msg("✅ CEX-DEX 执行器已启动")

				// 消费执行结果日志
				go func() {
					for result := range cexdexExecutor.GetResultChan() {
						if result.Success {
							log.Main().Info().
								Str("id", result.OpportunityID).
								Float64("profit_usd", result.ActualProfit).
								Float64("gas_usd", result.GasCost).
								Str("dex_tx", result.DEXTxHash.Hex()).
								Int64("cex_order", result.CEXOrderID).
								Dur("duration", result.ExecutionTime).
								Msg("✅ CEX-DEX 执行成功")
						} else {
							log.Main().Warn().
								Str("id", result.OpportunityID).
								Str("error", result.Error).
								Msg("❌ CEX-DEX 执行失败")
						}
					}
				}()
			}
		}
	}

	// 13d. DEX 聚合器客户端（1inch + ParaSwap + 0x → MultiAggregator 竞价）
	var oneInchClient *aggregator.OneInchClient
	oneInchClient = aggregator.NewOneInchClient(&aggregator.OneInchConfig{
		ChainID: int64(cfg.Blockchain.ChainID),
	})

	paraSwapClient := aggregator.NewParaSwapClient(&aggregator.ParaSwapConfig{
		ChainID: int64(cfg.Blockchain.ChainID),
	})

	// 0x 需要 API key，没有则跳过
	var zeroXClient *aggregator.ZeroXClient
	// 可通过环境变量 ZEROX_API_KEY 配置
	// if zeroXKey := os.Getenv("ZEROX_API_KEY"); zeroXKey != "" {
	//     zeroXClient = aggregator.NewZeroXClient(&aggregator.ZeroXConfig{
	//         ChainID: int64(cfg.Blockchain.ChainID),
	//         APIKey:  zeroXKey,
	//     })
	// }

	multiAggregator := aggregator.NewMultiAggregator(oneInchClient, paraSwapClient, zeroXClient)
	_ = multiAggregator

	log.Main().Info().
		Int64("chain_id", int64(cfg.Blockchain.ChainID)).
		Bool("1inch", oneInchClient != nil).
		Bool("paraswap", paraSwapClient != nil).
		Bool("0x", zeroXClient != nil).
		Msg("✅ DEX 聚合器已创建")

	// 13e. UniswapX Filler — 当前已禁用
	// 原因：2026-03-13 测试发现 Arbitrum 和以太坊主网均无 open orders，UniswapX 订单量极低
	// 代码保留在 internal/solver/uniswapx_filler.go，待 UniswapX 在 L2 活跃后可快速启用
	// 启用方式：取消下方注释，确保 Keeper 私钥和 1inch 客户端已配置
	var uniswapxFiller *solver.UniswapXFiller
	_ = uniswapxFiller
	/*
	if cfg.Keeper.PrivateKey != "" && oneInchClient != nil {
		txMgr := executor.NewTxManager(web3Client.GetClient(), big.NewInt(int64(cfg.Blockchain.ChainID)), executor.DefaultTxManagerConfig())
		fillerConfig := solver.DefaultFillerConfig()
		fillerConfig.ChainID = int64(cfg.Blockchain.ChainID)
		fillerConfig.PrivateKey = cfg.Keeper.PrivateKey
		fillerConfig.KeeperAddress = common.HexToAddress(cfg.Keeper.Address)

		uniswapxFiller = solver.NewUniswapXFiller(fillerConfig, oneInchClient, txMgr, web3Client.GetClient())
		go func() {
			if err := uniswapxFiller.Start(ctx); err != nil {
				log.Main().Warn().Err(err).Msg("UniswapX Filler 启动失败")
			}
		}()
		log.Main().Info().Msg("✅ UniswapX Filler 已启动")
	}
	*/

	// Suppress unused warnings
	_ = binanceTrader
	_ = oneInchClient
	_ = cexdexExecutor

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
	ctxCancel() // 取消全局 context，通知所有 goroutine 退出
	if discoveryService != nil {
		discoveryService.Stop()
	}
	if cexdexExecutor != nil {
		cexdexExecutor.Stop()
	}
	// UniswapX Filler 已禁用，无需 Stop
	// if uniswapxFiller != nil {
	// 	uniswapxFiller.Stop()
	// }
	if liquidationService != nil {
		liquidationService.Stop()
	}
	if compoundV3Monitor != nil {
		compoundV3Monitor.Stop()
	}
	if siloV1Monitor != nil {
		siloV1Monitor.Stop()
	}
	if cexMonitor != nil {
		cexMonitor.Stop()
	}
	if cexdexDetector != nil {
		cexdexDetector.Stop()
	}
	if activeScheduler != nil {
		activeScheduler.Stop()
	}
	strategyEngine.Stop()
	log.Main().Info().Msg("服务已关闭")
	log.Close() // 关闭日志系统
}

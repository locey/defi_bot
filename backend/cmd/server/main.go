package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/defi-bot/backend/internal/api"
	"github.com/defi-bot/backend/internal/collector"
	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/internal/executor"
	"github.com/defi-bot/backend/internal/models"
	"github.com/defi-bot/backend/internal/scheduler"
	"github.com/defi-bot/backend/internal/strategy"
	"github.com/defi-bot/backend/pkg/cache"
	"github.com/defi-bot/backend/pkg/cex"
	"github.com/defi-bot/backend/pkg/log" // 导入自定义log包
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
		log.Main().Info().Msg("✅ 套利执行器已初始化（自动执行模式）")
	} else {
		log.Main().Warn().Msg("未配置 Keeper 私钥或合约地址，仅分析模式（不会自动执行）")
	}

	// 11. 创建定时任务调度器
	log.Main().Info().Msg("创建定时任务调度器...")
	taskScheduler := scheduler.NewScheduler(
		dataCollector,
		strategyEngine,
		arbitrageExecutor,
		&cfg.Scheduler,
	)

	// 12. 启动调度器
	if err := taskScheduler.Start(ctx); err != nil {
		log.Main().Fatal().Err(err).Msg("启动调度器失败")
	}

	// 13. 立即执行一次数据采集（DEX + CEX）
	log.Main().Info().Msg("执行初始数据采集...")
	if err := dataCollector.CollectAllData(); err != nil {
		log.Main().Warn().Err(err).Msg("初始数据采集失败")
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
	taskScheduler.Stop()
	strategyEngine.Stop()
	log.Main().Info().Msg("服务已关闭")
	log.Close() // 关闭日志系统
}

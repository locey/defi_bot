package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/defi-bot/backend/internal/collector"
	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/internal/executor"
	"github.com/defi-bot/backend/internal/strategy"
	"github.com/defi-bot/backend/pkg/cache"
	"github.com/defi-bot/backend/pkg/cex"
	"github.com/defi-bot/backend/pkg/web3"
	"github.com/ethereum/go-ethereum/common"
)

var (
	configPath = flag.String("config", "configs/config.test.yaml", "配置文件路径")
	migrate    = flag.Bool("migrate", false, "执行数据库迁移")
	seed       = flag.Bool("seed", false, "初始化种子数据")
	cleanup    = flag.Bool("cleanup", false, "清理其他链的残留数据（根据配置的 chain_id）")
	executeTx  = flag.Bool("execute", false, "是否实际发送交易（需要 keeper 私钥 + 合约地址 + 合约 backCaller/owner 权限）")
	minProfitRateOverride = flag.Float64("min-profit-rate", -1, "覆盖最小利润率阈值（百分比，例 0.5；-1 表示使用 config.arbitrage.min_profit_rate）")
	gasMultiplierOverride = flag.Float64("gas-multiplier", 2.0, "minProfit = gasCost * multiplier（用于测试网调参）")
	skipPairDiscovery = flag.Bool("skip-pairs", false, "跳过交易对发现（使用已有的交易对数据）")
	skipDepth = flag.Bool("skip-depth", false, "跳过V3深度采集（加速测试）")
)

func main() {
	flag.Parse()

	// 让 run_terminal_cmd 的输出更可见：把 log 输出到 stdout
	log.SetOutput(os.Stdout)

	log.Println("========================================")
	log.Println("DeFi Bot E2E Runner (采集→分析→(可选)执行)")
	log.Println("========================================")

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	log.Println("初始化数据库...")
	if err := database.InitDB(&cfg.Database); err != nil {
		log.Fatalf("数据库初始化失败: %v", err)
	}
	defer database.CloseDB()

	if *migrate {
		log.Println("执行数据库迁移...")
		if err := database.AutoMigrate(); err != nil {
			log.Fatalf("数据库迁移失败: %v", err)
		}
		log.Println("数据库迁移完成")
	}
	if *seed {
		log.Println("初始化种子数据...")
		if err := database.SeedData(cfg); err != nil {
			log.Fatalf("种子数据初始化失败: %v", err)
		}
		log.Println("种子数据初始化完成")
	}

	if *cleanup {
		log.Printf("清理非 ChainID=%d 的残留数据...", cfg.Blockchain.ChainID)
		if err := database.CleanupTestnetData(cfg.Blockchain.ChainID); err != nil {
			log.Printf("⚠️ 数据清理失败: %v", err)
		}
	}

	log.Println("初始化 Web3 客户端...")
	web3Client, err := web3.NewClient(cfg.Blockchain.RPCURL, cfg.Blockchain.ChainID, cfg.Blockchain.Timeout)
	if err != nil {
		log.Fatalf("Web3 客户端初始化失败: %v", err)
	}
	defer web3Client.Close()

	// Redis（可选）
	var redisCache *cache.RedisCache
	if cfg.Redis.Enabled {
		redisCache, err = cache.NewRedisCache(&cache.RedisConfig{
			Host:     cfg.Redis.Host,
			Port:     cfg.Redis.Port,
			Password: cfg.Redis.Password,
			DB:       cfg.Redis.DB,
			TTL:      time.Duration(cfg.Redis.TTL) * time.Second,
		})
		if err != nil {
			log.Printf("⚠️  Redis 初始化失败（将不使用缓存）: %v", err)
			redisCache = nil
		} else {
			defer redisCache.Close()
		}
	}

	// CEX（可选）
	var cexCollector *collector.CexCollector
	if cfg.Cex.Enabled && cfg.Cex.Binance.Enabled {
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

	dataCollector := collector.NewCollector(web3Client, cexCollector, redisCache)

	// StrategyConfig：必须注入 BaseTokens + SupportedDexes
	minProfitRate := cfg.Arbitrage.MinProfitRate
	if *minProfitRateOverride >= 0 {
		minProfitRate = *minProfitRateOverride
	}

	strategyConfig := &strategy.StrategyConfig{
		MinProfitRate:      minProfitRate / 100.0,
		MaxPathLength:      5,
		MinPathLength:      3,
		MaxSlippage:        cfg.Arbitrage.MaxSlippage / 100.0,
		GasMultiplier:      *gasMultiplierOverride,
		ValidityDuration:   30 * time.Second,
		MaxConcurrentPaths: 20,
	}
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
		strategyConfig.SupportedDexes = append(strategyConfig.SupportedDexes, strategy.DexConfig{
			Name:           d.Name,
			RouterAddress:  common.HexToAddress(d.Router),
			FactoryAddress: common.HexToAddress(d.Factory),
			Type:           d.Protocol,
			Fee:            uint64(d.Fee),
		})
	}

	ctx := context.Background()
	db := database.GetDB()
	strategyEngine := strategy.NewStrategyEngine(strategyConfig, web3Client, db, redisCache)
	if err := strategyEngine.Start(ctx); err != nil {
		log.Printf("⚠️  策略引擎启动失败: %v", err)
	}
	defer strategyEngine.Stop()

	log.Println("执行一次数据采集（DEX + CEX）...")
	if *skipPairDiscovery {
		log.Println("⏭️  跳过交易对发现（使用已有数据）")
		// 只采集价格数据
		if err := dataCollector.CollectPriceOnly(*skipDepth); err != nil {
			log.Printf("价格采集失败: %v", err)
		}
	} else {
		if err := dataCollector.CollectAllData(); err != nil {
			log.Printf("数据采集失败: %v", err)
		}
	}

	log.Println("执行一次套利分析...")
	opps, err := strategyEngine.FindOpportunities(ctx)
	if err != nil {
		log.Fatalf("查找机会失败: %v", err)
	}
	if len(opps) == 0 {
		log.Println("未发现套利机会（可能是测试网流动性不足/交易对未发现/价格未采集）")
		return
	}

	best := opps[0]
	log.Printf("Best opportunity: profitRate=%.4f%% expectProfit=%s amountIn=%s pathLen=%d",
		best.ProfitRate*100, best.ExpectProfit.String(), best.AmountIn.String(), best.PathLength)

	if cfg.Contracts.ArbitrageCore == "" {
		log.Println("未配置 contracts.arbitrage_core，跳过执行。")
		return
	}

	arbCore := common.HexToAddress(cfg.Contracts.ArbitrageCore)
	cc := executor.NewContractCaller(web3Client, arbCore)
	// 套利路径环形：tokenOut = 路径最后一个地址
	tokenOut := best.SwapPath[0]
	if len(best.SwapPath) > 1 {
		tokenOut = best.SwapPath[len(best.SwapPath)-1]
	}
	// 确保 FeeTiers 长度与 Dexes 一致
	feeTiers := best.FeeTiers
	if len(feeTiers) != len(best.Dexes) {
		feeTiers = make([]uint32, len(best.Dexes))
	}
	params := &executor.ArbitrageParams{
		Asset:        best.SwapPath[0],
		TokenOut:     tokenOut,
		AmountIn:     best.AmountIn,
		SwapPath:     best.SwapPath,
		Dexes:        best.Dexes,
		FeeTiers:     feeTiers,
		ExpectProfit: best.ExpectProfit,
		MinProfit:    best.MinProfit,
		IsCex:        best.IsCex,
	}

	calldata, err := cc.DebugCallData(params)
	if err != nil {
		log.Printf("生成 calldata 失败: %v", err)
	} else {
		log.Printf("executeStrategy calldata: %s", calldata)
	}

	if !*executeTx {
		log.Println("dry-run 模式结束（未发送交易）。如需发交易，加 --execute，并确保 keeper 私钥/权限/合约部署正确。")
		return
	}

	if cfg.Keeper.PrivateKey == "" {
		log.Fatalf("execute 模式需要 keeper.private_key")
	}

	exec := executor.NewArbitrageExecutor(web3Client, arbCore, cfg.Keeper.PrivateKey)
	res, err := exec.Execute(ctx, best)
	if err != nil {
		log.Fatalf("执行失败: %v", err)
	}

	if res.Success {
		log.Printf("✅ 执行成功 tx=%s profit=%s gasCost=%s", res.TxHash, res.ActualProfit.String(), res.GasCost.String())
	} else {
		log.Printf("❌ 执行失败 tx=%s err=%s", res.TxHash, res.Error)
	}

	fmt.Println("Done.")
}



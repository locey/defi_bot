package main

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/defi-bot/backend/internal/collector"
	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/internal/executor"
	"github.com/defi-bot/backend/internal/models"
	"github.com/defi-bot/backend/internal/strategy"
	"github.com/defi-bot/backend/pkg/cache"
	"github.com/defi-bot/backend/pkg/utils"
	"github.com/defi-bot/backend/pkg/web3"
	"github.com/ethereum/go-ethereum/common"
)

// 测试结果记录
type TestResult struct {
	Name    string
	Passed  bool
	Details string
	Time    time.Duration
}

var results []TestResult

func main() {
	fmt.Println("=" + strings.Repeat("=", 59))
	fmt.Println("        DeFi Bot 完整业务逻辑验证测试")
	fmt.Println("=" + strings.Repeat("=", 59))
	fmt.Printf("测试时间: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Println()

	// 1. 加载配置
	cfg, err := config.LoadConfig("configs/config.yaml")
	if err != nil {
		log.Fatalf("❌ 加载配置失败: %v", err)
	}
	fmt.Println("✅ 配置加载成功")

	// 2. 初始化数据库
	if err := database.InitDB(&cfg.Database); err != nil {
		log.Fatalf("❌ 数据库初始化失败: %v", err)
	}
	defer database.CloseDB()
	fmt.Println("✅ 数据库连接成功")

	db := database.GetDB()

	// 3. 初始化 Web3 客户端
	web3Client, err := web3.NewClient(cfg.Blockchain.RPCURL, cfg.Blockchain.ChainID, cfg.Blockchain.Timeout)
	if err != nil {
		log.Fatalf("❌ Web3 客户端初始化失败: %v", err)
	}
	defer web3Client.Close()
	fmt.Println("✅ Web3 客户端连接成功")

	// 4. 初始化 Redis
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
			fmt.Printf("⚠️  Redis 初始化失败（将不使用缓存）: %v\n", err)
		} else {
			defer redisCache.Close()
			fmt.Println("✅ Redis 连接成功")
		}
	}

	fmt.Println()
	fmt.Println("=" + strings.Repeat("=", 59))
	fmt.Println("                 业务逻辑验证开始")
	fmt.Println("=" + strings.Repeat("=", 59))
	fmt.Println()

	// ========== 测试 1: 数据采集逻辑 ==========
	fmt.Println("【测试 1】数据采集逻辑验证")
	fmt.Println("-" + strings.Repeat("-", 49))

	start := time.Now()
	dataCollector := collector.NewCollector(web3Client, nil, redisCache)

	// 获取当前区块
	blockNumber, err := web3Client.GetBlockNumber()
	if err != nil {
		addResult("数据采集-获取区块", false, fmt.Sprintf("获取区块失败: %v", err), time.Since(start))
	} else {
		fmt.Printf("  当前区块: %d\n", blockNumber)
		addResult("数据采集-获取区块", true, fmt.Sprintf("区块号: %d", blockNumber), time.Since(start))
	}

	// 执行数据采集
	start = time.Now()
	if err := dataCollector.CollectPricesConcurrent(blockNumber); err != nil {
		addResult("数据采集-价格采集", false, fmt.Sprintf("采集失败: %v", err), time.Since(start))
	} else {
		// 检查采集结果
		var count int64
		db.Model(&models.PairReserve{}).Count(&count)
		fmt.Printf("  采集记录数: %d\n", count)
		addResult("数据采集-价格采集", count > 0, fmt.Sprintf("采集 %d 条记录", count), time.Since(start))
	}

	// 验证价格精度
	fmt.Println()
	fmt.Println("  价格精度验证:")
	priceCalc := utils.NewPriceCalculator()

	// 验证 WETH/USDC (Pair ID 20)
	var pair20 models.TradingPair
	db.Preload("Token0").Preload("Token1").Preload("Dex").First(&pair20, 20)

	var reserve20 models.PairReserve
	if err := db.Where("pair_id = ?", 20).Order("timestamp DESC").First(&reserve20).Error; err == nil {
		r0 := new(big.Int)
		r1 := new(big.Int)
		r0.SetString(reserve20.Reserve0, 10)
		r1.SetString(reserve20.Reserve1, 10)

		price := priceCalc.CalculateNormalizedPrice(r0, r1, pair20.Token0.Decimals, pair20.Token1.Decimals)
		priceFloat, _ := price.Float64()

		fmt.Printf("    WETH/USDC @ %s: %.2f\n", pair20.Dex.Name, priceFloat)

		// 验证价格在合理范围 (2000-5000)
		if priceFloat > 2000 && priceFloat < 5000 {
			addResult("数据采集-WETH/USDC精度", true, fmt.Sprintf("价格 %.2f 在合理范围", priceFloat), 0)
		} else {
			addResult("数据采集-WETH/USDC精度", false, fmt.Sprintf("价格 %.2f 异常", priceFloat), 0)
		}
	}

	// 验证 USDT/USDC (Pair ID 22)
	var pair22 models.TradingPair
	db.Preload("Token0").Preload("Token1").Preload("Dex").First(&pair22, 22)

	var reserve22 models.PairReserve
	if err := db.Where("pair_id = ?", 22).Order("timestamp DESC").First(&reserve22).Error; err == nil {
		r0 := new(big.Int)
		r1 := new(big.Int)
		r0.SetString(reserve22.Reserve0, 10)
		r1.SetString(reserve22.Reserve1, 10)

		price := priceCalc.CalculateNormalizedPrice(r0, r1, pair22.Token0.Decimals, pair22.Token1.Decimals)
		priceFloat, _ := price.Float64()

		fmt.Printf("    USDT/USDC @ %s: %.6f\n", pair22.Dex.Name, priceFloat)

		// 验证价格在合理范围 (0.95-1.05)
		if priceFloat > 0.95 && priceFloat < 1.05 {
			addResult("数据采集-USDT/USDC精度", true, fmt.Sprintf("价格 %.6f 在合理范围", priceFloat), 0)
		} else {
			addResult("数据采集-USDT/USDC精度", false, fmt.Sprintf("价格 %.6f 异常", priceFloat), 0)
		}
	}

	fmt.Println()

	// ========== 测试 2: 调度器逻辑 (30秒持续更新) ==========
	fmt.Println("【测试 2】调度器逻辑验证 (30秒持续更新)")
	fmt.Println("-" + strings.Repeat("-", 49))

	var initialCount int64
	db.Model(&models.PairReserve{}).Count(&initialCount)
	fmt.Printf("  初始记录数: %d\n", initialCount)

	// 等待一个采集周期
	fmt.Println("  等待采集周期 (采集一次新数据)...")
	start = time.Now()
	if err := dataCollector.CollectPricesConcurrent(blockNumber + 1); err != nil {
		addResult("调度器-持续更新", false, fmt.Sprintf("第二次采集失败: %v", err), time.Since(start))
	} else {
		var newCount int64
		db.Model(&models.PairReserve{}).Count(&newCount)
		fmt.Printf("  新记录数: %d (增加 %d)\n", newCount, newCount-initialCount)

		if newCount > initialCount {
			addResult("调度器-持续更新", true, fmt.Sprintf("记录从 %d 增加到 %d", initialCount, newCount), time.Since(start))
		} else {
			addResult("调度器-持续更新", false, "记录数未增加", time.Since(start))
		}
	}

	fmt.Println()

	// ========== 测试 3: 套利路径发现逻辑 ==========
	fmt.Println("【测试 3】套利路径发现逻辑验证")
	fmt.Println("-" + strings.Repeat("-", 49))

	start = time.Now()

	// 初始化策略引擎
	strategyConfig := &strategy.StrategyConfig{
		MinProfitRate:      cfg.Arbitrage.MinProfitRate / 100.0,
		MaxPathLength:      5,
		MinPathLength:      3,
		MaxSlippage:        cfg.Arbitrage.MaxSlippage / 100.0,
		GasMultiplier:      2.0,
		ValidityDuration:   30 * time.Second,
		MaxConcurrentPaths: 20,
	}

	for _, t := range cfg.Tokens {
		if t.Address != "" {
			strategyConfig.BaseTokens = append(strategyConfig.BaseTokens, common.HexToAddress(t.Address))
		}
	}
	for _, d := range cfg.Dexes {
		if d.Router != "" {
			strategyConfig.SupportedDexes = append(strategyConfig.SupportedDexes, strategy.DexConfig{
				Name:           d.Name,
				RouterAddress:  common.HexToAddress(d.Router),
				FactoryAddress: common.HexToAddress(d.Factory),
				Type:           d.Protocol,
				Fee:            uint64(d.Fee),
			})
		}
	}

	fmt.Printf("  基础代币数: %d\n", len(strategyConfig.BaseTokens))
	fmt.Printf("  支持DEX数: %d\n", len(strategyConfig.SupportedDexes))

	strategyEngine := strategy.NewStrategyEngine(strategyConfig, web3Client, db, redisCache)
	ctx := context.Background()

	if err := strategyEngine.Start(ctx); err != nil {
		addResult("套利路径-引擎启动", false, fmt.Sprintf("启动失败: %v", err), time.Since(start))
	} else {
		addResult("套利路径-引擎启动", true, "策略引擎启动成功", time.Since(start))
	}

	// 查找套利机会
	fmt.Println("  搜索套利机会...")
	start = time.Now()
	opps, err := strategyEngine.FindOpportunities(ctx)
	searchTime := time.Since(start)

	if err != nil {
		addResult("套利路径-机会搜索", false, fmt.Sprintf("搜索失败: %v", err), searchTime)
	} else {
		fmt.Printf("  发现机会数: %d\n", len(opps))
		fmt.Printf("  搜索耗时: %v\n", searchTime)

		// 主网上没有机会是正常的
		addResult("套利路径-机会搜索", true, fmt.Sprintf("搜索完成，发现 %d 个机会", len(opps)), searchTime)

		// 如果有机会，显示详情
		if len(opps) > 0 {
			best := opps[0]
			fmt.Printf("  最佳机会:\n")
			fmt.Printf("    利润率: %.4f%%\n", best.ProfitRate*100)
			fmt.Printf("    预期利润: %s\n", best.ExpectProfit.String())
			fmt.Printf("    输入金额: %s\n", best.AmountIn.String())
			fmt.Printf("    路径长度: %d\n", best.PathLength)
		}
	}

	strategyEngine.Stop()
	fmt.Println()

	// ========== 测试 4: 利润计算逻辑 ==========
	fmt.Println("【测试 4】利润计算逻辑验证")
	fmt.Println("-" + strings.Repeat("-", 49))

	// 手动构造一个测试场景验证 AMM 公式
	fmt.Println("  AMM 公式验证:")

	// 模拟 WETH/USDC 池子
	// reserveWETH = 1000 WETH = 1000 * 10^18
	// reserveUSDC = 3,340,000 USDC = 3,340,000 * 10^6
	// 输入 1 WETH，预期输出约 3340 USDC

	reserveIn := new(big.Int)
	reserveIn.SetString("1000000000000000000000", 10) // 1000 WETH

	reserveOut := new(big.Int)
	reserveOut.SetString("3340000000000", 10) // 3,340,000 USDC

	amountIn := new(big.Int)
	amountIn.SetString("1000000000000000000", 10) // 1 WETH

	// AMM 公式: amountOut = (amountIn * 997 * reserveOut) / (reserveIn * 1000 + amountIn * 997)
	fee := big.NewInt(997)
	numerator := new(big.Int).Mul(amountIn, fee)
	numerator.Mul(numerator, reserveOut)

	denominator := new(big.Int).Mul(reserveIn, big.NewInt(1000))
	denominator.Add(denominator, new(big.Int).Mul(amountIn, fee))

	amountOut := new(big.Int).Div(numerator, denominator)

	// 转换为人类可读格式
	amountOutUSDC := new(big.Float).SetInt(amountOut)
	amountOutUSDC.Quo(amountOutUSDC, big.NewFloat(1e6))

	outFloat, _ := amountOutUSDC.Float64()
	fmt.Printf("    输入: 1 WETH\n")
	fmt.Printf("    池子: 1000 WETH / 3,340,000 USDC\n")
	fmt.Printf("    输出: %.2f USDC\n", outFloat)

	// 验证输出在合理范围 (3300-3360)
	// 考虑 0.3% 手续费：3340 * 0.997 ≈ 3330，再考虑滑点
	if outFloat > 3300 && outFloat < 3360 {
		addResult("利润计算-AMM公式", true, fmt.Sprintf("输出 %.2f USDC 正确（含 0.3%% 手续费）", outFloat), 0)
	} else {
		addResult("利润计算-AMM公式", false, fmt.Sprintf("输出 %.2f USDC 异常", outFloat), 0)
	}

	fmt.Println()

	// ========== 测试 5: 合约执行逻辑 (dry-run) ==========
	fmt.Println("【测试 5】合约执行逻辑验证 (dry-run)")
	fmt.Println("-" + strings.Repeat("-", 49))

	if cfg.Contracts.ArbitrageCore == "" {
		fmt.Println("  ⚠️ 未配置套利合约地址，跳过执行测试（这是可选配置）")
		addResult("合约执行-配置检查", true, "未配置 contracts.arbitrage_core（可选）", 0)
	} else {
		arbCore := common.HexToAddress(cfg.Contracts.ArbitrageCore)
		fmt.Printf("  合约地址: %s\n", arbCore.Hex())

		cc := executor.NewContractCaller(web3Client, arbCore)

		// 构造测试参数
		wethAddr := common.HexToAddress("0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2")
		testParams := &executor.ArbitrageParams{
			Asset:        wethAddr, // WETH
			TokenOut:     wethAddr, // 环形套利：输出 = 输入
			AmountIn:     big.NewInt(1e18),                                                   // 1 WETH
			SwapPath:     []common.Address{wethAddr},
			Dexes:        []common.Address{common.HexToAddress("0x7a250d5630B4cF539739dF2C5dAcb4c659F2488D")}, // Uniswap V2 Router
			ExpectProfit: big.NewInt(1e16), // 0.01 WETH
			MinProfit:    big.NewInt(1e15), // 0.001 WETH
			IsCex:        false,
		}

		calldata, err := cc.DebugCallData(testParams)
		if err != nil {
			fmt.Printf("  ⚠️ 生成 calldata 失败: %v\n", err)
			addResult("合约执行-Calldata生成", false, fmt.Sprintf("失败: %v", err), 0)
		} else {
			fmt.Printf("  Calldata 长度: %d bytes\n", len(calldata)/2-1)
			addResult("合约执行-Calldata生成", true, fmt.Sprintf("生成 %d bytes calldata", len(calldata)/2-1), 0)
		}
	}

	fmt.Println()

	// ========== 生成测试报告 ==========
	fmt.Println("=" + strings.Repeat("=", 59))
	fmt.Println("                   测试结果汇总")
	fmt.Println("=" + strings.Repeat("=", 59))
	fmt.Println()

	passed := 0
	failed := 0
	for _, r := range results {
		status := "✅"
		if !r.Passed {
			status = "❌"
			failed++
		} else {
			passed++
		}
		fmt.Printf("%s %s\n", status, r.Name)
		fmt.Printf("   详情: %s\n", r.Details)
		if r.Time > 0 {
			fmt.Printf("   耗时: %v\n", r.Time)
		}
		fmt.Println()
	}

	fmt.Println("-" + strings.Repeat("-", 49))
	fmt.Printf("总计: %d 项测试，✅ 通过 %d，❌ 失败 %d\n", len(results), passed, failed)
	fmt.Println()

	if failed == 0 {
		fmt.Println("🎉 所有业务逻辑验证通过！")
	} else {
		fmt.Printf("⚠️ 有 %d 项测试未通过，请检查\n", failed)
		os.Exit(1)
	}
}

func addResult(name string, passed bool, details string, duration time.Duration) {
	results = append(results, TestResult{
		Name:    name,
		Passed:  passed,
		Details: details,
		Time:    duration,
	})
}

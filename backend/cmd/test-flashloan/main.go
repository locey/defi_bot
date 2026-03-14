// cmd/test-flashloan/main.go
// Flash Loan 实盘测试 — 完整管道，执行 1 笔后退出
// Flash Loan 零本金风险：不盈利的交易自动 revert，只损失 gas (~$0.01 on Arbitrum)
package main

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/internal/executor"
	"github.com/defi-bot/backend/internal/scheduler"
	"github.com/defi-bot/backend/internal/strategy"
	"github.com/defi-bot/backend/pkg/log"
	"github.com/defi-bot/backend/pkg/web3"
	"github.com/ethereum/go-ethereum/common"
)

func main() {
	configPath := "configs/config.yaml"
	maxWait := 5 * time.Minute // 最多等 5 分钟
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		fmt.Printf("❌ 配置加载失败: %v\n", err)
		os.Exit(1)
	}
	_ = log.Init(&cfg.Log)

	fmt.Println("========================================")
	fmt.Println("⚡ Flash Loan 实盘测试")
	fmt.Println("   零本金风险：不盈利自动 revert")
	fmt.Println("   最大 gas 损失：~$0.01/次 (Arbitrum)")
	fmt.Println("========================================")

	// 验证必要配置
	if cfg.Keeper.PrivateKey == "" {
		fmt.Println("❌ 未配置 KEEPER_PRIVATE_KEY")
		os.Exit(1)
	}
	if cfg.Contracts.FlashLoanArbitrage == "" {
		fmt.Println("❌ 未配置 flash_loan_arbitrage 合约地址")
		os.Exit(1)
	}

	// 初始化
	web3Client, err := web3.NewClient(cfg.Blockchain.RPCURL, cfg.Blockchain.ChainID, cfg.Blockchain.Timeout)
	if err != nil {
		fmt.Printf("❌ Web3 连接失败: %v\n", err)
		os.Exit(1)
	}
	defer web3Client.Close()

	// 检查 Keeper 余额
	keeperAddr := common.HexToAddress(cfg.Keeper.Address)
	balance, err := web3Client.GetClient().BalanceAt(context.Background(), keeperAddr, nil)
	if err != nil {
		fmt.Printf("❌ 无法查询余额: %v\n", err)
		os.Exit(1)
	}
	balFloat := new(big.Float).Quo(new(big.Float).SetInt(balance), new(big.Float).SetFloat64(1e18))
	fmt.Printf("Keeper: %s\n", keeperAddr.Hex())
	fmt.Printf("Balance: %s ETH\n", balFloat.Text('f', 6))

	minBalance := new(big.Int).SetUint64(1_000_000_000_000_000) // 0.001 ETH
	if balance.Cmp(minBalance) < 0 {
		fmt.Println("❌ 余额不足 0.001 ETH，无法支付 gas")
		os.Exit(1)
	}

	// RPC pool
	var rpcPool *web3.ClientPool
	if cfg.Blockchain.UsePool && len(cfg.Blockchain.RPCURLs) > 1 {
		poolCfg := &web3.ClientPoolConfig{
			RPCURLs:       cfg.Blockchain.RPCURLs,
			ChainID:       cfg.Blockchain.ChainID,
			Timeout:       cfg.Blockchain.Timeout,
			HealthCheck:   true,
			CheckInterval: 60 * time.Second,
		}
		rpcPool, _ = web3.NewClientPool(poolCfg)
		if rpcPool != nil {
			defer rpcPool.Close()
		}
	}

	// 数据库
	if err := database.InitDB(&cfg.Database); err != nil {
		fmt.Printf("❌ 数据库连接失败: %v\n", err)
		os.Exit(1)
	}
	defer database.CloseDB()
	db := database.GetDB()

	// 初始化执行器
	arbiExecutor := executor.NewArbitrageExecutor(
		web3Client,
		common.HexToAddress(cfg.Contracts.ArbitrageCore),
		cfg.Keeper.PrivateKey,
	)
	arbiExecutor.SetDB(db)
	arbiExecutor.SetFlashLoanAddress(common.HexToAddress(cfg.Contracts.FlashLoanArbitrage))
	fmt.Printf("FlashLoan: %s\n", cfg.Contracts.FlashLoanArbitrage)

	// 构建调度器配置
	var baseTokens []common.Address
	for _, t := range cfg.Tokens {
		if t.Address != "" {
			baseTokens = append(baseTokens, common.HexToAddress(t.Address))
		}
	}
	dexRouters := make(map[string]common.Address)
	quoterAddr := ""
	for _, d := range cfg.Dexes {
		if d.Router != "" && d.Name != "" {
			dexRouters[d.Name] = common.HexToAddress(d.Router)
		}
		if d.Quoter != "" && quoterAddr == "" {
			quoterAddr = d.Quoter
		}
	}

	hpConfig := &scheduler.HighPerformanceConfig{
		WSURL:                   cfg.Blockchain.WSURL,
		ChainID:                 cfg.Blockchain.ChainID,
		BaseTokens:              baseTokens,
		DexRouters:              dexRouters,
		MaxConcurrentExecutions: 1,
		MinConfidence:           0.15,
		EnableExecution:         true,  // 真实执行
		DryRun:                  false, // 非 dry-run
		ContractAddress:         cfg.Contracts.ArbitrageCore,
		KeeperPrivateKey:        cfg.Keeper.PrivateKey,
		EnableSimulation:        true,
		FlashLoanAddress:        cfg.Contracts.FlashLoanArbitrage,
		EnableFlashLoan:         true,
		EnableSpreadScanner:     true,
		MinSpreadBps:            15,
		RPCPool:                 rpcPool,
		QuoterAddress:           quoterAddr,
	}

	hpScheduler, err := scheduler.NewHighPerformanceScheduler(db, web3Client, arbiExecutor, hpConfig)
	if err != nil {
		fmt.Printf("❌ 调度器创建失败: %v\n", err)
		os.Exit(1)
	}

	aggRouter := strategy.NewAggregatorRouter(nil)
	hpScheduler.SetAggregatorRouter(aggRouter)

	if err := hpScheduler.Start(); err != nil {
		fmt.Printf("❌ 调度器启动失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n✅ 系统已启动，等待 Flash Loan 机会（最多 %s）...\n\n", maxWait)

	ctx, cancel := context.WithTimeout(context.Background(), maxWait)
	defer cancel()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// 每 15 秒检查统计
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	startTime := time.Now()

loop:
	for {
		select {
		case <-ctx.Done():
			fmt.Println("\n⏳ 超时，未找到可执行的机会")
			break loop
		case <-quit:
			fmt.Println("\n⏹️  手动中断")
			break loop
		case <-ticker.C:
			stats := hpScheduler.GetStats()
			elapsed := time.Since(startTime).Round(time.Second)
			fmt.Printf("[%s] opportunities=%d attempted=%d succeeded=%d failed=%d\n",
				elapsed, stats.OpportunitiesFound, stats.ExecutionsAttempted,
				stats.ExecutionsSucceeded, stats.ExecutionsFailed)

			// 有执行结果后退出
			if stats.ExecutionsAttempted > 0 {
				fmt.Println("\n🎯 检测到执行尝试，等待结果...")
				time.Sleep(10 * time.Second) // 等待 tx 确认
				break loop
			}
		}
	}

	hpScheduler.Stop()
	stats := hpScheduler.GetStats()

	// 查询最终余额
	newBalance, _ := web3Client.GetClient().BalanceAt(context.Background(), keeperAddr, nil)
	balDiff := new(big.Int).Sub(newBalance, balance)
	diffFloat := new(big.Float).Quo(new(big.Float).SetInt(balDiff), new(big.Float).SetFloat64(1e18))

	fmt.Println("\n========================================")
	fmt.Println("Flash Loan 测试结果")
	fmt.Println("========================================")
	fmt.Printf("运行时间:      %s\n", time.Since(startTime).Round(time.Second))
	fmt.Printf("发现机会:      %d\n", stats.OpportunitiesFound)
	fmt.Printf("尝试执行:      %d\n", stats.ExecutionsAttempted)
	fmt.Printf("执行成功:      %d\n", stats.ExecutionsSucceeded)
	fmt.Printf("执行失败:      %d\n", stats.ExecutionsFailed)

	newBalFloat := new(big.Float).Quo(new(big.Float).SetInt(newBalance), new(big.Float).SetFloat64(1e18))
	fmt.Printf("初始余额:      %s ETH\n", balFloat.Text('f', 6))
	fmt.Printf("最终余额:      %s ETH\n", newBalFloat.Text('f', 6))
	fmt.Printf("余额变化:      %s ETH\n", diffFloat.Text('f', 6))

	if stats.ExecutionsSucceeded > 0 {
		fmt.Println("\n🎉 Flash Loan 套利成功！")
	} else if stats.ExecutionsAttempted > 0 {
		fmt.Println("\n⚠️  有执行尝试但未成功（tx 可能 revert，仅损失 gas）")
	} else {
		fmt.Println("\n📊 未找到可执行机会（继续等待或调低阈值）")
	}
	fmt.Println("========================================")
}

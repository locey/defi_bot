// cmd/test-hp-scheduler/main.go
// 高性能调度器完整测试程序
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/internal/scheduler"
	"github.com/defi-bot/backend/pkg/log"
	"github.com/defi-bot/backend/pkg/web3"
	"github.com/ethereum/go-ethereum/common"
)

func main() {
	fmt.Println("========================================")
	fmt.Println("High Performance Scheduler Test")
	fmt.Println("========================================")

	// 1. 加载配置
	cfg, err := config.LoadConfig("configs/config.yaml")
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
	log.Main().Info().Msg("High Performance Scheduler Test")
	log.Main().Info().Msg("========================================")

	// 3. 初始化数据库
	log.Main().Info().Msg("初始化数据库...")
	if err := database.InitDB(&cfg.Database); err != nil {
		log.Main().Fatal().Err(err).Msg("数据库初始化失败")
	}
	defer database.CloseDB()

	db := database.GetDB()

	// 4. 初始化 Web3 客户端
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

	// 5. 构建 BaseTokens
	baseTokens := make([]common.Address, 0)
	for _, token := range cfg.Tokens {
		if token.Address != "" {
			baseTokens = append(baseTokens, common.HexToAddress(token.Address))
		}
	}
	log.Main().Info().Int("count", len(baseTokens)).Msg("BaseTokens loaded")

	// 6. 创建高性能调度器配置
	hpConfig := &scheduler.HighPerformanceConfig{
		WSURL:                   cfg.Blockchain.WSURL, // 如果配置了 WebSocket URL
		ChainID:                 cfg.Blockchain.ChainID,
		BaseTokens:              baseTokens,
		MaxConcurrentExecutions: 3,
		ExecutionTimeout:        30 * time.Second,
		MinConfidence:           0.6,
		EnableExecution:         false, // 测试模式不执行
		DryRun:                  true,  // 干运行模式
	}

	// 7. 创建高性能调度器
	log.Main().Info().Msg("创建高性能调度器...")
	hpScheduler, err := scheduler.NewHighPerformanceScheduler(
		db,
		web3Client,
		nil, // 不传入执行器（测试模式）
		hpConfig,
	)
	if err != nil {
		log.Main().Fatal().Err(err).Msg("创建高性能调度器失败")
	}

	// 8. 启动调度器
	log.Main().Info().Msg("启动高性能调度器...")
	if err := hpScheduler.Start(); err != nil {
		log.Main().Fatal().Err(err).Msg("启动高性能调度器失败")
	}

	// 9. 启动监控循环
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				stats := hpScheduler.GetStats()
				log.Main().Info().Msg("========== 测试统计 ==========")
				log.Main().Info().
					Dur("uptime", time.Since(stats.StartTime).Round(time.Second)).
					Int64("opportunities", stats.OpportunitiesFound).
					Int64("executions", stats.ExecutionsAttempted).
					Msg("Status")
			}
		}
	}()

	// 10. 等待退出信号
	log.Main().Info().Msg("========================================")
	log.Main().Info().Msg("高性能调度器正在运行，按 Ctrl+C 退出")
	log.Main().Info().Msg("========================================")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	// 11. 优雅关闭
	log.Main().Info().Msg("正在关闭...")
	hpScheduler.Stop()
	log.Main().Info().Msg("测试完成")
	log.Close()
}

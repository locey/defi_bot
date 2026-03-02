package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/defi-bot/backend/internal/collector"
	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/internal/models"
	"github.com/defi-bot/backend/internal/scheduler"
	"github.com/defi-bot/backend/pkg/cache"
	pkglog "github.com/defi-bot/backend/pkg/log"
	"github.com/defi-bot/backend/pkg/web3"
)

func main() {
	fmt.Println("=== 测试调度器 - 检查 pair_reserves 是否会持续更新 ===")

	// 1. 加载配置（测试时可以使用测试网或主网配置）
	// 测试网: "configs/config.test.yaml"
	// 主网: "configs/config.yaml"
	cfg, err := config.LoadConfig("configs/config.yaml")
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 2. 初始化日志
	if err := pkglog.Init(&cfg.Log); err != nil {
		log.Fatalf("日志初始化失败: %v", err)
	}

	pkglog.Info("测试调度器启动...")
	pkglog.Info("采集间隔: %d 秒", cfg.Scheduler.CollectInterval)

	// 3. 初始化数据库
	if err := database.InitDB(&cfg.Database); err != nil {
		log.Fatalf("数据库初始化失败: %v", err)
	}
	defer database.CloseDB()

	db := database.GetDB()

	// 4. 获取初始记录数
	var initialCount int64
	db.Model(&models.PairReserve{}).Count(&initialCount)
	pkglog.Info("初始记录数: %d", initialCount)

	// 5. 初始化 Web3 客户端
	pkglog.Info("正在连接 RPC: %s", cfg.Blockchain.RPCURL)
	web3Client, err := web3.NewClient(
		cfg.Blockchain.RPCURL,
		cfg.Blockchain.ChainID,
		cfg.Blockchain.Timeout,
	)
	if err != nil {
		pkglog.Error("Web3 客户端初始化失败: %v", err)
		pkglog.Error("RPC URL: %s", cfg.Blockchain.RPCURL)
		pkglog.Error("Chain ID: %d", cfg.Blockchain.ChainID)
		pkglog.Error("Timeout: %d", cfg.Blockchain.Timeout)
		log.Fatalf("Web3 客户端初始化失败: %v", err)
	}
	pkglog.Info("Web3 客户端初始化成功")

	// 6. 初始化 Redis 缓存（如果配置了）
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
			pkglog.Warn("Redis 初始化失败，将不使用缓存: %v", err)
			redisCache = nil
		} else {
			pkglog.Info("Redis 缓存已启用")
		}
	}

	// 7. 初始化 CEX 采集器（可选，测试时可以不启用）
	var cexCollector *collector.CexCollector
	// 为了简化测试，这里不初始化 CEX 采集器
	// cexCollector = nil

	// 8. 初始化采集器
	dataCollector := collector.NewCollector(web3Client, cexCollector, redisCache)

	// 9. 创建调度器（不传入策略引擎和执行器，仅测试数据采集）
	schedulerInstance := scheduler.NewScheduler(
		dataCollector,
		nil, // 不需要策略引擎
		nil, // 不需要执行器
		&cfg.Scheduler,
	)

	// 10. 启动调度器
	ctx := context.Background()
	if err := schedulerInstance.Start(ctx); err != nil {
		log.Fatalf("调度器启动失败: %v", err)
	}

	// 11. 启动监控协程 - 每30秒检查一次记录数
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				var currentCount int64
				db.Model(&models.PairReserve{}).Count(&currentCount)
				
				// 查询最新记录的时间戳
				var latestRecord models.PairReserve
				db.Order("timestamp DESC").First(&latestRecord)

				pkglog.Info("📊 监控报告:")
				pkglog.Info("   总记录数: %d (增加了 %d 条)", currentCount, currentCount-initialCount)
				if latestRecord.ID > 0 {
					timeDiff := time.Since(latestRecord.Timestamp)
					pkglog.Info("   最新记录时间: %s (距现在 %v)",
						latestRecord.Timestamp.Format("15:04:05"), timeDiff)
				}
			}
		}
	}()

	// 12. 等待中断信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	pkglog.Info("调度器正在运行... (按 Ctrl+C 停止)")
	pkglog.Info("每 %d 秒采集一次数据，请观察记录数是否持续增加", cfg.Scheduler.CollectInterval)
	<-quit

	pkglog.Info("收到停止信号，正在关闭...")
	schedulerInstance.Stop()

	// 13. 最终统计
	var finalCount int64
	db.Model(&models.PairReserve{}).Count(&finalCount)
	pkglog.Info("========================================")
	pkglog.Info("测试完成:")
	pkglog.Info("  初始记录数: %d", initialCount)
	pkglog.Info("  最终记录数: %d", finalCount)
	pkglog.Info("  新增记录数: %d", finalCount-initialCount)
	pkglog.Info("========================================")
}

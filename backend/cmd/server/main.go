package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/defi-bot/backend/internal/collector"
	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/internal/scheduler"
	"github.com/defi-bot/backend/pkg/cache"
	"github.com/defi-bot/backend/pkg/web3"
)

var (
	configPath = flag.String("config", "configs/config.yaml", "配置文件路径")
	migrate    = flag.Bool("migrate", false, "执行数据库迁移")
	seed       = flag.Bool("seed", false, "初始化种子数据")
)

func main() {
	flag.Parse()

	// 1. 加载配置
	log.Println("========================================")
	log.Println("DeFi 套利机器人后端服务")
	log.Println("========================================")

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 2. 初始化数据库
	log.Println("初始化数据库...")
	if err := database.InitDB(&cfg.Database); err != nil {
		log.Fatalf("数据库初始化失败: %v", err)
	}
	defer database.CloseDB()

	// 3. 执行数据库迁移
	if *migrate {
		log.Println("执行数据库迁移...")
		if err := database.AutoMigrate(); err != nil {
			log.Fatalf("数据库迁移失败: %v", err)
		}
		log.Println("数据库迁移完成")

		if !*seed {
			return
		}
	}

	// 4. 初始化种子数据
	if *seed {
		log.Println("初始化种子数据...")
		if err := database.SeedData(cfg); err != nil {
			log.Fatalf("种子数据初始化失败: %v", err)
		}
		return
	}

	// 5. 初始化 Web3 客户端
	log.Println("初始化 Web3 客户端...")
	web3Client, err := web3.NewClient(
		cfg.Blockchain.RPCURL,
		cfg.Blockchain.ChainID,
		cfg.Blockchain.Timeout,
	)
	if err != nil {
		log.Fatalf("Web3 客户端初始化失败: %v", err)
	}
	defer web3Client.Close()

	// 6. 初始化 Redis 缓存（可选）
	var redisCache *cache.RedisCache
	if cfg.Redis.Enabled {
		log.Println("初始化 Redis 缓存...")
		var err error
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

	// 7. 创建数据采集器
	log.Println("创建数据采集器...")
	dataCollector := collector.NewCollector(web3Client, redisCache)

	// 8. 创建定时任务调度器
	log.Println("创建定时任务调度器...")
	taskScheduler := scheduler.NewScheduler(dataCollector, &cfg.Scheduler)

	// 9. 启动调度器
	if err := taskScheduler.Start(); err != nil {
		log.Fatalf("启动调度器失败: %v", err)
	}

	// 10. 立即执行一次数据采集
	log.Println("执行初始数据采集...")
	if err := dataCollector.CollectAllData(); err != nil {
		log.Printf("初始数据采集失败: %v", err)
	}

	// 11. 等待退出信号
	log.Println("========================================")
	log.Println("服务已启动，按 Ctrl+C 退出")
	log.Println("========================================")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	// 12. 优雅关闭
	log.Println("\n正在关闭服务...")
	taskScheduler.Stop()
	log.Println("服务已关闭")
}

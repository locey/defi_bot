// 数据采集模块测试
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/defi-bot/backend/internal/collector"
	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/pkg/cache"
	"github.com/defi-bot/backend/pkg/web3"
)

var (
	configPath = flag.String("config", "configs/config.yaml", "配置文件路径")
)

func main() {
	flag.Parse()

	fmt.Println("=" + strings.Repeat("=", 59))
	fmt.Println("         DeFi 套利机器人 - 数据采集测试")
	fmt.Println("=" + strings.Repeat("=", 59))
	fmt.Printf("配置文件: %s\n", *configPath)
	fmt.Printf("测试时间: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Println(strings.Repeat("-", 60))

	result := make(map[string]interface{})

	// 1. 加载配置
	fmt.Print("\n📋 加载配置... ")
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		fmt.Printf("❌ 失败: %v\n", err)
		return
	}
	fmt.Println("✅ 成功")

	// 2. 初始化数据库
	fmt.Print("🗄️ 连接数据库... ")
	err = database.InitDB(&cfg.Database)
	if err != nil {
		fmt.Printf("❌ 失败: %v\n", err)
		return
	}
	defer database.CloseDB()
	fmt.Println("✅ 成功")

	// 3. 初始化 Web3 客户端
	fmt.Print("🔗 连接 RPC... ")
	web3Client, err := web3.NewClient(cfg.Blockchain.RPCURL, cfg.Blockchain.ChainID, cfg.Blockchain.Timeout)
	if err != nil {
		fmt.Printf("❌ 失败: %v\n", err)
		return
	}
	defer web3Client.Close()
	fmt.Println("✅ 成功")

	// 4. 初始化缓存（可选）
	var cacheClient *cache.RedisCache
	if cfg.Redis.Enabled {
		fmt.Print("📦 连接 Redis... ")
		cacheClient, err = cache.NewRedisCache(&cache.RedisConfig{
			Host:     cfg.Redis.Host,
			Port:     cfg.Redis.Port,
			Password: cfg.Redis.Password,
			DB:       cfg.Redis.DB,
		})
		if err != nil {
			fmt.Printf("⚠️ 警告: %v (继续不使用缓存)\n", err)
		} else {
			defer cacheClient.Close()
			fmt.Println("✅ 成功")
		}
	}

	// 5. 创建 Collector
	fmt.Println("\n📊 [模块 4] 数据采集测试")
	fmt.Println(strings.Repeat("-", 40))

	db := database.GetDB()
	coll := collector.NewCollector(web3Client, nil, cacheClient) // CEX collector 暂时为 nil

	// 6. 测试获取区块号
	fmt.Print("  4.1 获取当前区块号... ")
	blockNum, err := web3Client.GetBlockNumber()
	if err != nil {
		fmt.Printf("❌ 失败: %v\n", err)
		result["block_number"] = map[string]interface{}{"success": false, "error": err.Error()}
	} else {
		fmt.Printf("✅ 区块 #%d\n", blockNum)
		result["block_number"] = map[string]interface{}{"success": true, "block": blockNum}
	}

	// 7. 测试采集交易对
	fmt.Print("  4.2 采集交易对... ")
	startPairs := time.Now()
	err = coll.CollectTradingPairs()
	if err != nil {
		fmt.Printf("❌ 失败: %v\n", err)
		result["trading_pairs"] = map[string]interface{}{"success": false, "error": err.Error()}
	} else {
		// 统计采集到的交易对
		var pairCount int64
		db.Table("trading_pairs").Count(&pairCount)
		duration := time.Since(startPairs)
		fmt.Printf("✅ 成功 (采集到 %d 个交易对，耗时 %v)\n", pairCount, duration)
		result["trading_pairs"] = map[string]interface{}{
			"success":  true,
			"count":    pairCount,
			"duration": duration.String(),
		}
	}

	// 8. 测试采集价格（跳过深度数据）
	fmt.Print("  4.3 采集价格数据... ")
	startPrice := time.Now()
	err = coll.CollectPriceOnly(true) // skipDepth = true
	if err != nil {
		fmt.Printf("⚠️ 部分失败: %v\n", err)
		result["prices"] = map[string]interface{}{"success": false, "error": err.Error()}
	} else {
		// 统计价格记录
		var priceCount int64
		db.Table("price_records").Count(&priceCount)
		duration := time.Since(startPrice)
		fmt.Printf("✅ 成功 (采集到 %d 条价格记录，耗时 %v)\n", priceCount, duration)
		result["prices"] = map[string]interface{}{
			"success":  true,
			"count":    priceCount,
			"duration": duration.String(),
		}
	}

	// 9. 测试采集 Gas 价格
	fmt.Print("  4.4 采集 Gas 价格... ")
	startGas := time.Now()
	err = coll.CollectGasData()
	if err != nil {
		fmt.Printf("❌ 失败: %v\n", err)
		result["gas_price"] = map[string]interface{}{"success": false, "error": err.Error()}
	} else {
		// 获取最新 Gas 价格
		var gasCount int64
		db.Table("gas_price_history").Count(&gasCount)
		duration := time.Since(startGas)
		fmt.Printf("✅ 成功 (共 %d 条记录，耗时 %v)\n", gasCount, duration)
		result["gas_price"] = map[string]interface{}{
			"success":  true,
			"count":    gasCount,
			"duration": duration.String(),
		}
	}

	// 10. 输出结果
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("测试结果 (JSON):")
	fmt.Println(strings.Repeat("-", 60))
	jsonResult, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(jsonResult))
}

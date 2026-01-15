// 套利计算模块测试
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/internal/strategy"
	"github.com/defi-bot/backend/pkg/cache"
	"github.com/defi-bot/backend/pkg/web3"
	"github.com/ethereum/go-ethereum/common"
)

var (
	configPath = flag.String("config", "configs/config.yaml", "配置文件路径")
)

func main() {
	flag.Parse()

	fmt.Println("=" + strings.Repeat("=", 59))
	fmt.Println("         DeFi 套利机器人 - 套利计算测试")
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
	db := database.GetDB()

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

	// 5. 创建策略引擎
	fmt.Println("\n📈 [模块 5] 套利计算测试")
	fmt.Println(strings.Repeat("-", 40))

	fmt.Print("  5.1 创建策略引擎... ")

	// 构建策略配置
	baseTokens := make([]common.Address, 0)
	for _, token := range cfg.Tokens {
		baseTokens = append(baseTokens, common.HexToAddress(token.Address))
	}

	dexConfigs := make([]strategy.DexConfig, 0)
	for _, dex := range cfg.Dexes {
		dexConfigs = append(dexConfigs, strategy.DexConfig{
			Name:           dex.Name,
			RouterAddress:  common.HexToAddress(dex.Router),
			FactoryAddress: common.HexToAddress(dex.Factory),
			Type:           dex.Protocol,
			Fee:            uint64(dex.Fee),
		})
	}

	strategyConfig := &strategy.StrategyConfig{
		MinProfitRate:      cfg.Arbitrage.MinProfitRate,
		MaxPathLength:      4,
		MinPathLength:      3,
		MaxSlippage:        cfg.Arbitrage.MaxSlippage,
		GasMultiplier:      2.0,
		ValidityDuration:   30 * time.Second,
		BaseTokens:         baseTokens,
		SupportedDexes:     dexConfigs,
		MaxConcurrentPaths: 50,
	}

	engine := strategy.NewStrategyEngine(strategyConfig, web3Client, db, cacheClient)
	fmt.Println("✅ 成功")

	// 6. 获取当前区块号
	fmt.Print("  5.2 获取当前区块... ")
	blockNum, err := web3Client.GetBlockNumber()
	if err != nil {
		fmt.Printf("❌ 失败: %v\n", err)
		result["block_number"] = map[string]interface{}{"success": false, "error": err.Error()}
	} else {
		fmt.Printf("✅ 区块 #%d\n", blockNum)
		result["block_number"] = map[string]interface{}{"success": true, "block": blockNum}
	}

	// 7. 查找套利机会
	fmt.Print("  5.3 查找套利机会... ")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	startFind := time.Now()
	opportunities, err := engine.FindOpportunities(ctx)
	findDuration := time.Since(startFind)

	if err != nil {
		fmt.Printf("❌ 失败: %v\n", err)
		result["find_opportunities"] = map[string]interface{}{"success": false, "error": err.Error()}
	} else {
		fmt.Printf("✅ 发现 %d 个机会 (耗时 %v)\n", len(opportunities), findDuration)
		result["find_opportunities"] = map[string]interface{}{
			"success":  true,
			"count":    len(opportunities),
			"duration": findDuration.String(),
		}

		// 显示前 10 个机会
		if len(opportunities) > 0 {
			fmt.Println("\n  发现的套利机会 (前10个):")
			showCount := 10
			if len(opportunities) < showCount {
				showCount = len(opportunities)
			}

			oppDetails := make([]map[string]interface{}, 0)
			for i := 0; i < showCount; i++ {
				opp := opportunities[i]
				fmt.Printf("    %d. 利润率: %.4f%%, 预期利润: %s, 路径: %s\n",
					i+1,
					opp.ProfitRate*100,
					opp.ExpectProfit.String(),
					formatPath(opp.SwapPath),
				)
				oppDetails = append(oppDetails, map[string]interface{}{
					"profit_rate":   opp.ProfitRate * 100,
					"expect_profit": opp.ExpectProfit.String(),
					"path":          formatPath(opp.SwapPath),
					"confidence":    opp.Confidence,
				})
			}
			result["opportunities"] = oppDetails
		}
	}

	// 8. 保存机会到数据库
	if len(opportunities) > 0 {
		fmt.Print("  5.4 保存机会到数据库... ")
		startSave := time.Now()
		err = engine.SaveOpportunitiesToDB(ctx, opportunities)
		saveDuration := time.Since(startSave)
		if err != nil {
			fmt.Printf("❌ 失败: %v\n", err)
			result["save_opportunities"] = map[string]interface{}{"success": false, "error": err.Error()}
		} else {
			fmt.Printf("✅ 成功 (耗时 %v)\n", saveDuration)
			result["save_opportunities"] = map[string]interface{}{
				"success":  true,
				"count":    len(opportunities),
				"duration": saveDuration.String(),
			}
		}
	}

	// 9. 统计数据库中的机会
	fmt.Print("  5.5 统计数据库机会... ")
	var totalOpps int64
	db.Table("arbitrage_opportunities").Count(&totalOpps)
	var recentOpps int64
	oneHourAgo := time.Now().Add(-1 * time.Hour)
	db.Table("arbitrage_opportunities").Where("created_at > ?", oneHourAgo).Count(&recentOpps)
	fmt.Printf("✅ 总计 %d 个，最近1小时 %d 个\n", totalOpps, recentOpps)
	result["db_stats"] = map[string]interface{}{
		"total_opportunities":  totalOpps,
		"recent_opportunities": recentOpps,
	}

	// 10. 输出结果
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("测试结果 (JSON):")
	fmt.Println(strings.Repeat("-", 60))
	jsonResult, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(jsonResult))
}

// formatPath 格式化交换路径
func formatPath(path []common.Address) string {
	if len(path) == 0 {
		return "N/A"
	}
	result := ""
	for i, addr := range path {
		if i > 0 {
			result += " -> "
		}
		addrStr := addr.Hex()
		if len(addrStr) > 10 {
			result += addrStr[:6] + "..." + addrStr[len(addrStr)-4:]
		} else {
			result += addrStr
		}
	}
	return result
}

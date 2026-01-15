// 模块测试脚本 - 用于逐个测试各模块功能
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/pkg/web3"
)

var (
	configPath = flag.String("config", "configs/config.yaml", "配置文件路径")
	testModule = flag.String("module", "all", "测试模块: config, rpc, db, all")
)

func main() {
	flag.Parse()

	fmt.Println("=" + strings.Repeat("=", 59))
	fmt.Println("         DeFi 套利机器人 - 模块测试")
	fmt.Println("=" + strings.Repeat("=", 59))
	fmt.Printf("配置文件: %s\n", *configPath)
	fmt.Printf("测试模块: %s\n", *testModule)
	fmt.Printf("测试时间: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Println(strings.Repeat("-", 60))

	results := make(map[string]interface{})

	switch *testModule {
	case "config":
		results["config"] = testConfig()
	case "rpc":
		results["config"] = testConfig()
		if results["config"].(map[string]interface{})["success"].(bool) {
			results["rpc"] = testRPC()
		}
	case "db":
		results["config"] = testConfig()
		if results["config"].(map[string]interface{})["success"].(bool) {
			results["db"] = testDB()
		}
	case "all":
		results["config"] = testConfig()
		if results["config"].(map[string]interface{})["success"].(bool) {
			results["rpc"] = testRPC()
			results["db"] = testDB()
		}
	default:
		fmt.Printf("未知模块: %s\n", *testModule)
		os.Exit(1)
	}

	// 输出 JSON 结果
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("测试结果 (JSON):")
	fmt.Println(strings.Repeat("-", 60))
	jsonResult, _ := json.MarshalIndent(results, "", "  ")
	fmt.Println(string(jsonResult))
}

// testConfig 测试配置加载
func testConfig() map[string]interface{} {
	result := map[string]interface{}{
		"module":    "config",
		"timestamp": time.Now().Format(time.RFC3339),
	}

	fmt.Println("\n📋 [模块 1] 配置加载测试")
	fmt.Println(strings.Repeat("-", 40))

	// 1. 加载主配置
	fmt.Print("  1.1 加载主配置... ")
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		fmt.Printf("❌ 失败: %v\n", err)
		result["success"] = false
		result["error"] = err.Error()
		return result
	}
	fmt.Println("✅ 成功")

	// 2. 验证配置内容
	fmt.Print("  1.2 验证配置内容... ")
	configDetails := map[string]interface{}{
		"chain_id":    cfg.Blockchain.ChainID,
		"rpc_url":     maskString(cfg.Blockchain.RPCURL, 20),
		"rpc_count":   len(cfg.Blockchain.RPCURLs),
		"dex_count":   len(cfg.Dexes),
		"token_count": len(cfg.Tokens),
		"db_host":     cfg.Database.Host,
		"db_name":     cfg.Database.DBName,
	}
	fmt.Println("✅ 成功")

	// 3. 验证 DEX 配置
	fmt.Print("  1.3 验证 DEX 配置... ")
	if len(cfg.Dexes) > 0 {
		configDetails["dex_validated"] = true
		fmt.Printf("✅ %d 个 DEX 配置有效\n", len(cfg.Dexes))
	} else {
		configDetails["dex_validated"] = false
		fmt.Println("⚠️ 无 DEX 配置")
	}

	// 4. 打印配置摘要
	fmt.Println("\n  配置摘要:")
	fmt.Printf("    - Chain ID: %d\n", cfg.Blockchain.ChainID)
	fmt.Printf("    - RPC URLs: %d 个\n", len(cfg.Blockchain.RPCURLs))
	fmt.Printf("    - DEX 数量: %d 个\n", len(cfg.Dexes))
	fmt.Printf("    - Token 数量: %d 个\n", len(cfg.Tokens))

	if len(cfg.Dexes) > 0 {
		fmt.Println("    - DEX 列表:")
		for i, dex := range cfg.Dexes {
			if i >= 5 {
				fmt.Printf("      ... 还有 %d 个\n", len(cfg.Dexes)-5)
				break
			}
			fmt.Printf("      %d. %s (%s)\n", i+1, dex.Name, dex.Protocol)
		}
	}

	result["success"] = true
	result["details"] = configDetails
	return result
}

// testRPC 测试 RPC 连接
func testRPC() map[string]interface{} {
	result := map[string]interface{}{
		"module":    "rpc",
		"timestamp": time.Now().Format(time.RFC3339),
	}

	fmt.Println("\n🔗 [模块 2] RPC 连接测试")
	fmt.Println(strings.Repeat("-", 40))

	cfg := config.GetConfig()

	// 1. 测试主 RPC
	fmt.Print("  2.1 测试主 RPC 连接... ")
	client, err := web3.NewClient(cfg.Blockchain.RPCURL, cfg.Blockchain.ChainID, cfg.Blockchain.Timeout)
	if err != nil {
		fmt.Printf("❌ 失败: %v\n", err)
		result["success"] = false
		result["error"] = err.Error()
		return result
	}
	defer client.Close()
	fmt.Println("✅ 成功")

	// 2. 获取区块号
	fmt.Print("  2.2 获取当前区块号... ")
	blockNum, err := client.GetBlockNumber()
	if err != nil {
		fmt.Printf("❌ 失败: %v\n", err)
		result["success"] = false
		result["error"] = err.Error()
		return result
	}
	fmt.Printf("✅ 区块 #%d\n", blockNum)

	// 3. 获取 Gas 价格 (通过原生 ethclient)
	fmt.Print("  2.3 获取 Gas 价格... ")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	gasPrice, err := client.GetClient().SuggestGasPrice(ctx)
	if err != nil {
		fmt.Printf("⚠️ 失败: %v\n", err)
	} else {
		gasPriceGwei := float64(gasPrice.Int64()) / 1e9
		fmt.Printf("✅ %.2f Gwei\n", gasPriceGwei)
		result["gas_price_gwei"] = gasPriceGwei
	}

	// 4. 获取 Chain ID
	fmt.Print("  2.4 验证 Chain ID... ")
	chainID := client.GetChainID()
	if chainID.Int64() != cfg.Blockchain.ChainID {
		fmt.Printf("⚠️ 不匹配 (配置: %d, 实际: %d)\n", cfg.Blockchain.ChainID, chainID.Int64())
	} else {
		fmt.Printf("✅ Chain ID = %d\n", chainID.Int64())
	}

	result["success"] = true
	result["block_number"] = blockNum
	result["chain_id"] = chainID.Int64()
	return result
}

// testDB 测试数据库连接
func testDB() map[string]interface{} {
	result := map[string]interface{}{
		"module":    "db",
		"timestamp": time.Now().Format(time.RFC3339),
	}

	fmt.Println("\n🗄️ [模块 3] 数据库连接测试")
	fmt.Println(strings.Repeat("-", 40))

	cfg := config.GetConfig()

	// 1. 连接数据库
	fmt.Print("  3.1 连接数据库... ")
	err := database.InitDB(&cfg.Database)
	if err != nil {
		fmt.Printf("❌ 失败: %v\n", err)
		result["success"] = false
		result["error"] = err.Error()
		return result
	}
	defer database.CloseDB()
	fmt.Println("✅ 成功")

	// 2. 获取 DB 实例
	db := database.GetDB()
	if db == nil {
		fmt.Println("  ❌ 获取 DB 实例失败")
		result["success"] = false
		result["error"] = "获取 DB 实例失败"
		return result
	}

	// 3. 检查表
	fmt.Print("  3.2 检查数据库表... ")
	sqlDB, err := db.DB()
	if err != nil {
		fmt.Printf("❌ 获取 SQL DB 失败: %v\n", err)
		result["success"] = false
		result["error"] = err.Error()
		return result
	}

	var tableCount int
	err = sqlDB.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public'").Scan(&tableCount)
	if err != nil {
		fmt.Printf("❌ 查询失败: %v\n", err)
		result["success"] = false
		result["error"] = err.Error()
		return result
	}
	fmt.Printf("✅ 发现 %d 个表\n", tableCount)

	// 4. 列出表名
	fmt.Println("  3.3 数据库表列表:")
	rows, err := sqlDB.Query("SELECT table_name FROM information_schema.tables WHERE table_schema = 'public' ORDER BY table_name")
	if err == nil {
		defer rows.Close()
		var tables []string
		for rows.Next() {
			var tableName string
			rows.Scan(&tableName)
			tables = append(tables, tableName)
			fmt.Printf("      - %s\n", tableName)
		}
		result["tables"] = tables
	}

	result["success"] = true
	result["table_count"] = tableCount
	result["db_name"] = cfg.Database.DBName
	return result
}

// maskString 遮盖字符串
func maskString(s string, visibleLen int) string {
	if len(s) <= visibleLen {
		return s
	}
	return s[:visibleLen] + "..."
}

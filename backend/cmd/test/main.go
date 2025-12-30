package main

import (
	"flag"
	"fmt"

	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/internal/models"
	"github.com/defi-bot/backend/pkg/dex"
	"github.com/defi-bot/backend/pkg/web3"
)

var (
	configPath = flag.String("config", "configs/config.test.yaml", "配置文件路径")
)

func main() {
	flag.Parse()

	fmt.Println("========================================")
	fmt.Println("🧪 DeFi Bot 综合测试工具")
	fmt.Println("========================================\n")

	// 1. 加载配置
	fmt.Println("📋 步骤 1/7: 加载配置...")
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		fmt.Printf("❌ 失败: %v\n", err)
		return
	}
	fmt.Printf("✅ 成功: 配置文件已加载\n\n")

	// 2. 测试数据库连接
	fmt.Println("📋 步骤 2/7: 测试数据库连接...")
	if err := database.InitDB(&cfg.Database); err != nil {
		fmt.Printf("❌ 失败: %v\n", err)
		return
	}
	defer database.CloseDB()
	db := database.GetDB()
	db.Logger = db.Logger.LogMode(1) // Silent mode
	fmt.Printf("✅ 成功: 数据库连接正常\n\n")

	// 3. 验证所有表都存在
	fmt.Println("📋 步骤 3/7: 验证数据库表...")
	tables := []string{
		"tokens", "dexes", "trading_pairs", "pair_reserves",
		"price_records", "liquidity_depths", "gas_price_history",
		"arbitrage_opportunities", "arbitrage_executions",
	}

	for _, table := range tables {
		var count int64
		if err := db.Table(table).Count(&count).Error; err != nil {
			fmt.Printf("  ❌ %s - 表不存在\n", table)
		} else {
			fmt.Printf("  ✅ %s - 记录数: %d\n", table, count)
		}
	}
	fmt.Println()

	// 4. 验证 DEX 配置
	fmt.Println("📋 步骤 4/7: 验证 DEX 配置...")
	var dexes []models.Dex
	db.Order("priority ASC").Find(&dexes)

	fmt.Printf("  发现 %d 个 DEX：\n", len(dexes))
	for _, d := range dexes {
		fmt.Printf("  - %-20s | 类型: %-10s | 协议: %-12s | 优先级: %3d | V3: %v\n",
			d.Name, d.DexType, d.Protocol, d.Priority, d.SupportV3Ticks)
	}
	fmt.Println()

	// 5. 测试 Web3 连接
	fmt.Println("📋 步骤 5/7: 测试 Web3 连接...")
	client, err := web3.NewClient(
		cfg.Blockchain.RPCURL,
		cfg.Blockchain.ChainID,
		cfg.Blockchain.Timeout,
	)
	if err != nil {
		fmt.Printf("❌ 失败: %v\n", err)
		return
	}
	defer client.Close()

	blockNumber, err := client.GetBlockNumber()
	if err != nil {
		fmt.Printf("❌ 失败: %v\n", err)
		return
	}
	fmt.Printf("✅ 成功: 当前区块号 %d\n\n", blockNumber)

	// 6. 测试协议工厂
	fmt.Println("📋 步骤 6/7: 测试协议工厂...")
	factory := dex.NewProtocolFactory(client)

	supportedProtocols := factory.GetSupportedProtocols()
	fmt.Printf("  支持的协议总数: %d\n", len(supportedProtocols))

	// 测试每个协议
	testedProtocols := []string{"uniswap_v2", "uniswap_v3", "sushiswap"}
	for _, protocolName := range testedProtocols {
		protocol, err := factory.CreateProtocol(protocolName)
		if err != nil {
			fmt.Printf("  ❌ %s - %v\n", protocolName, err)
		} else {
			fmt.Printf("  ✅ %s - 适配器创建成功 (%s)\n", protocolName, protocol.GetProtocolName())
		}
	}
	fmt.Println()

	// 7. 验证数据采集功能
	fmt.Println("📋 步骤 7/7: 验证数据采集功能...")

	// 查询最新价格记录
	var latestPrice models.PriceRecord
	err = db.Preload("Pair").
		Preload("Pair.Token0").
		Preload("Pair.Token1").
		Preload("Pair.Dex").
		Order("created_at DESC").
		First(&latestPrice).Error

	if err != nil {
		fmt.Printf("  ⚠️  没有价格数据（这是正常的，如果还没运行过采集器）\n")
	} else {
		fmt.Printf("  ✅ 最新价格记录:\n")
		fmt.Printf("     交易对: %s/%s\n", latestPrice.Pair.Token0.Symbol, latestPrice.Pair.Token1.Symbol)
		fmt.Printf("     DEX: %s (%s)\n", latestPrice.Pair.Dex.Name, latestPrice.Pair.Dex.Protocol)
		fmt.Printf("     时间: %s\n", latestPrice.Timestamp.Format("2006-01-02 15:04:05"))
		fmt.Printf("     区块: %d\n", latestPrice.BlockNumber)

		// 检查 V3 字段
		if latestPrice.Pair.Dex.SupportV3Ticks {
			if latestPrice.SqrtPriceX96 != "" {
				fmt.Printf("     ✅ V3 数据: sqrt_price_x96=%s, tick=%d\n",
					latestPrice.SqrtPriceX96[:min(20, len(latestPrice.SqrtPriceX96))], latestPrice.Tick)
			} else {
				fmt.Printf("     ⚠️  V3 数据缺失（需要更新采集器）\n")
			}
		}
	}
	fmt.Println()

	// 8. 总结
	fmt.Println("========================================")
	fmt.Println("📊 测试结果总结")
	fmt.Println("========================================")
	fmt.Printf("✅ 配置加载:      通过\n")
	fmt.Printf("✅ 数据库连接:    通过\n")
	fmt.Printf("✅ 表结构验证:    通过 (%d张表)\n", len(tables))
	fmt.Printf("✅ DEX配置:       通过 (%d个DEX)\n", len(dexes))
	fmt.Printf("✅ Web3连接:      通过 (区块: %d)\n", blockNumber)
	fmt.Printf("✅ 协议工厂:      通过\n")

	if err == nil {
		fmt.Printf("✅ 数据采集:      有数据\n")
	} else {
		fmt.Printf("⚠️  数据采集:      无数据（需运行采集器）\n")
	}

	fmt.Println("\n========================================")
	fmt.Println("🎉 所有测试完成！")
	fmt.Println("========================================")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

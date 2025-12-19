package main

import (
	"flag"
	"fmt"
	"math/big"

	"github.com/defi-bot/backend/internal/collector"
	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/internal/models"
	"github.com/defi-bot/backend/pkg/web3"
)

var (
	configPath = flag.String("config", "configs/config.test.yaml", "配置文件路径")
)

func main() {
	flag.Parse()

	fmt.Println("========================================")
	fmt.Println("🧪 改进功能测试工具")
	fmt.Println("========================================\n")

	// 1. 加载配置
	fmt.Println("📋 步骤 1/5: 加载配置...")
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		fmt.Printf("❌ 失败: %v\n", err)
		return
	}
	fmt.Println("✅ 成功\n")

	// 2. 初始化数据库
	fmt.Println("📋 步骤 2/5: 初始化数据库...")
	if err := database.InitDB(&cfg.Database); err != nil {
		fmt.Printf("❌ 失败: %v\n", err)
		return
	}
	defer database.CloseDB()
	db := database.GetDB()
	db.Logger = db.Logger.LogMode(1)
	fmt.Println("✅ 成功\n")

	// 3. 初始化 Web3 客户端
	fmt.Println("📋 步骤 3/5: 初始化 Web3 客户端...")
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
	fmt.Println("✅ 成功\n")

	// 4. 测试 V3 深度采集
	fmt.Println("========================================")
	fmt.Println("🔬 测试 1: V3 流动性深度采集")
	fmt.Println("========================================")

	col := collector.NewCollector(client, nil)

	fmt.Println("开始采集V3深度数据...")
	if err := col.CollectV3Depths(); err != nil {
		fmt.Printf("❌ 失败: %v\n", err)
	} else {
		fmt.Println("✅ 采集成功")

		// 查询结果
		var count int64
		db.Model(&models.LiquidityDepth{}).Count(&count)
		fmt.Printf("📊 liquidity_depths 表记录数: %d\n", count)

		if count > 0 {
			var depth models.LiquidityDepth
			db.Preload("Pair").
				Preload("Pair.Token0").
				Preload("Pair.Token1").
				Preload("Pair.Dex").
				Order("created_at DESC").
				First(&depth)

			fmt.Printf("最新深度记录:\n")
			fmt.Printf("  交易对: %s/%s\n", depth.Pair.Token0.Symbol, depth.Pair.Token1.Symbol)
			fmt.Printf("  DEX: %s\n", depth.Pair.Dex.Name)
			fmt.Printf("  方向: %s\n", depth.Direction)
			fmt.Printf("  输入: %s\n", depth.AmountIn[:min(20, len(depth.AmountIn))])
			fmt.Printf("  输出: %s\n", depth.AmountOut[:min(20, len(depth.AmountOut))])
			fmt.Printf("  滑点: %.4f%%\n", depth.PriceImpact)
		}
	}
	fmt.Println()

	// 5. 测试 Gas 价格采集
	fmt.Println("========================================")
	fmt.Println("🔬 测试 2: Gas 价格采集")
	fmt.Println("========================================")

	fmt.Println("开始采集Gas价格...")
	if err := col.CollectGasData(); err != nil {
		fmt.Printf("❌ 失败: %v\n", err)
	} else {
		fmt.Println("✅ 采集成功")

		// 查询结果
		var count int64
		db.Model(&models.GasPriceHistory{}).Count(&count)
		fmt.Printf("📊 gas_price_history 表记录数: %d\n", count)

		if count > 0 {
			var gasPrice models.GasPriceHistory
			db.Order("created_at DESC").First(&gasPrice)

			fmt.Printf("最新Gas价格:\n")
			fmt.Printf("  标准价格: %s Gwei\n", weiToGwei(gasPrice.StandardPrice))
			fmt.Printf("  快速价格: %s Gwei\n", weiToGwei(gasPrice.FastPrice))
			fmt.Printf("  慢速价格: %s Gwei\n", weiToGwei(gasPrice.SlowPrice))
			fmt.Printf("  网络负载: %s\n", gasPrice.NetworkLoad)
			fmt.Printf("  区块号: %d\n", gasPrice.BlockNumber)
		}
	}
	fmt.Println()

	// 6. 测试 V3 价格记录
	fmt.Println("========================================")
	fmt.Println("🔬 测试 3: V3 价格数据")
	fmt.Println("========================================")

	fmt.Println("查询最新的V3价格记录...")
	var v3Prices []models.PriceRecord
	err = db.Preload("Pair").
		Preload("Pair.Token0").
		Preload("Pair.Token1").
		Preload("Pair.Dex").
		Joins("JOIN trading_pairs ON trading_pairs.id = price_records.pair_id").
		Joins("JOIN dexes ON dexes.id = trading_pairs.dex_id").
		Where("dexes.support_v3_ticks = ?", true).
		Order("price_records.created_at DESC").
		Limit(3).
		Find(&v3Prices).Error

	if err != nil {
		fmt.Printf("❌ 查询失败: %v\n", err)
	} else if len(v3Prices) == 0 {
		fmt.Println("⚠️  没有V3价格记录（需要先运行一次数据采集）")
	} else {
		fmt.Printf("找到 %d 条V3价格记录\n\n", len(v3Prices))

		for i, price := range v3Prices {
			fmt.Printf("[%d] %s/%s @ %s\n",
				i+1,
				price.Pair.Token0.Symbol,
				price.Pair.Token1.Symbol,
				price.Pair.Dex.Name)

			if price.SqrtPriceX96 != "" {
				fmt.Printf("  ✅ V3 数据完整:\n")
				fmt.Printf("     sqrt_price_x96: %s\n", price.SqrtPriceX96[:min(20, len(price.SqrtPriceX96))])
				fmt.Printf("     tick: %d\n", price.Tick)
				fmt.Printf("     liquidity: %s\n", price.Liquidity[:min(20, len(price.Liquidity))])
			} else {
				fmt.Printf("  ⚠️  V3 数据缺失\n")
			}
			fmt.Println()
		}
	}

	// 7. 总结
	fmt.Println("========================================")
	fmt.Println("📊 测试结果总结")
	fmt.Println("========================================")

	var liquidityCount, gasCount int64
	db.Model(&models.LiquidityDepth{}).Count(&liquidityCount)
	db.Model(&models.GasPriceHistory{}).Count(&gasCount)

	fmt.Printf("✅ V3 深度采集:   %s (%d条记录)\n",
		getStatusEmoji(liquidityCount > 0), liquidityCount)
	fmt.Printf("✅ Gas 价格采集:  %s (%d条记录)\n",
		getStatusEmoji(gasCount > 0), gasCount)
	fmt.Printf("✅ V3 价格数据:   %s (%d条记录)\n",
		getStatusEmoji(len(v3Prices) > 0 && v3Prices[0].SqrtPriceX96 != ""), len(v3Prices))

	fmt.Println("\n========================================")

	if liquidityCount > 0 && gasCount > 0 {
		fmt.Println("🎉 所有改进功能测试通过！")
	} else {
		fmt.Println("⚠️  部分功能需要运行数据采集器")
	}

	fmt.Println("========================================")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func weiToGwei(weiStr string) string {
	wei, ok := new(big.Int).SetString(weiStr, 10)
	if !ok {
		return "0"
	}
	gwei := new(big.Float).Quo(
		new(big.Float).SetInt(wei),
		big.NewFloat(1e9),
	)
	return gwei.Text('f', 2)
}

func getStatusEmoji(success bool) string {
	if success {
		return "通过"
	}
	return "待采集"
}

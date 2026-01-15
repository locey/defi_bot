package main

import (
	"fmt"
	"log"

	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/internal/models"
)

func main() {
	fmt.Println("=== 检查交易对和储备量数据 ===\n")

	// 加载配置
	cfg, err := config.LoadConfig("configs/config.test.yaml")
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 连接数据库
	if err := database.InitDB(&cfg.Database); err != nil {
		log.Fatalf("数据库连接失败: %v", err)
	}
	defer database.CloseDB()

	db := database.GetDB()

	// 1. 统计各表记录数
	var tokenCount, exchangeCount, pairCount, reserveCount, priceCount int64
	db.Model(&models.Token{}).Count(&tokenCount)
	db.Model(&models.Exchange{}).Count(&exchangeCount)
	db.Model(&models.TradingPair{}).Count(&pairCount)
	db.Model(&models.PairReserve{}).Count(&reserveCount)
	db.Model(&models.PriceRecord{}).Count(&priceCount)

	fmt.Printf("📊 数据库统计:\n")
	fmt.Printf("   代币 (tokens): %d\n", tokenCount)
	fmt.Printf("   交易所 (exchanges): %d\n", exchangeCount)
	fmt.Printf("   交易对 (trading_pairs): %d\n", pairCount)
	fmt.Printf("   储备量 (pair_reserves): %d\n", reserveCount)
	fmt.Printf("   价格记录 (price_records): %d\n\n", priceCount)

	// 2. 查看所有交易对
	var pairs []models.TradingPair
	db.Preload("Token0").Preload("Token1").Preload("Dex").Find(&pairs)

	if len(pairs) > 0 {
		fmt.Printf("📋 已发现的交易对 (%d个):\n", len(pairs))
		fmt.Println("----------------------------------------")
		for i, p := range pairs {
			fmt.Printf("%d. %s/%s @ %s\n",
				i+1,
				p.Token0.Symbol,
				p.Token1.Symbol,
				p.Dex.Name)
			fmt.Printf("   Pair地址: %s\n", p.PairAddress)
			fmt.Printf("   是否活跃: %v\n\n", p.IsActive)
		}
	} else {
		fmt.Println("⚠️  还没有发现任何交易对")
	}

	// 3. 查看储备量记录
	if reserveCount > 0 {
		var reserves []models.PairReserve
		db.Order("timestamp DESC").Limit(5).Preload("Pair.Token0").Preload("Pair.Token1").Preload("Pair.Dex").Find(&reserves)

		fmt.Printf("📈 最新储备量记录 (前5条):\n")
		fmt.Println("----------------------------------------")
		for i, r := range reserves {
			fmt.Printf("%d. %s/%s @ %s\n",
				i+1,
				r.Pair.Token0.Symbol,
				r.Pair.Token1.Symbol,
				r.Pair.Dex.Name)
			fmt.Printf("   时间: %s\n", r.Timestamp.Format("2006-01-02 15:04:05"))
			fmt.Printf("   Reserve0: %s\n", r.Reserve0[:min(30, len(r.Reserve0))])
			fmt.Printf("   Reserve1: %s\n\n", r.Reserve1[:min(30, len(r.Reserve1))])
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

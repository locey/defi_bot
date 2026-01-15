package main

import (
	"fmt"
	"log"

	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/internal/models"
)

func main() {
	fmt.Println("=== 检查价格记录 ===\n")

	cfg, err := config.LoadConfig("configs/config.yaml")
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	if err := database.InitDB(&cfg.Database); err != nil {
		log.Fatalf("数据库初始化失败: %v", err)
	}
	defer database.CloseDB()

	db := database.GetDB()

	// 查询 WETH/USDC 相关的价格记录
	pairIDs := []uint{20, 21, 26, 27} // WETH/USDC, WETH/DAI, WETH/USDC@Sushi, WETH/DAI@Sushi

	for _, pairID := range pairIDs {
		var pair models.TradingPair
		if err := db.Preload("Token0").Preload("Token1").Preload("Dex").First(&pair, pairID).Error; err != nil {
			fmt.Printf("Pair %d 不存在\n", pairID)
			continue
		}

		var records []models.PriceRecord
		db.Where("pair_id = ?", pairID).Order("timestamp DESC").Limit(1).Find(&records)

		if len(records) == 0 {
			fmt.Printf("Pair %d (%s/%s @ %s): 无记录\n\n", pairID, pair.Token0.Symbol, pair.Token1.Symbol, pair.Dex.Name)
			continue
		}

		r := records[0]
		fmt.Printf("Pair %d: %s/%s @ %s\n", pairID, pair.Token0.Symbol, pair.Token1.Symbol, pair.Dex.Name)
		fmt.Printf("  Reserve0: %s\n", r.Reserve0)
		fmt.Printf("  Reserve1: %s\n", r.Reserve1)
		fmt.Printf("  Price: %s\n", r.Price.StringFixed(8))
		fmt.Printf("  InversePrice: %s\n", r.InversePrice.StringFixed(8))
		fmt.Printf("  BlockNumber: %d\n", r.BlockNumber)
		fmt.Println()
	}

	// 检查有多少价格为0的记录
	var zeroCount int64
	db.Model(&models.PriceRecord{}).Where("price = 0").Count(&zeroCount)
	
	var totalCount int64
	db.Model(&models.PriceRecord{}).Count(&totalCount)
	
	fmt.Printf("价格为0的记录: %d / %d\n", zeroCount, totalCount)
}

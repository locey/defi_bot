package main

import (
	"fmt"
	"log"
	"math/big"

	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/internal/models"
	"github.com/defi-bot/backend/pkg/utils"
)

func main() {
	fmt.Println("=== 检查代币精度和价格计算 ===")

	// 1. 加载配置
	cfg, err := config.LoadConfig("configs/config.yaml")
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 2. 初始化数据库
	if err := database.InitDB(&cfg.Database); err != nil {
		log.Fatalf("数据库初始化失败: %v", err)
	}
	defer database.CloseDB()

	db := database.GetDB()

	// 3. 查询所有代币
	var tokens []models.Token
	if err := db.Find(&tokens).Error; err != nil {
		log.Fatalf("查询代币失败: %v", err)
	}

	fmt.Println("📝 代币列表及精度:")
	fmt.Println("----------------------------------------")
	for _, t := range tokens {
		fmt.Printf("ID: %d | Symbol: %-6s | Decimals: %d | Address: %s\n",
			t.ID, t.Symbol, t.Decimals, t.Address)
	}
	fmt.Println()

	// 4. 查询涉及 USDC 的交易对
	var pairs []models.TradingPair
	if err := db.Preload("Token0").Preload("Token1").Preload("Dex").
		Where("is_active = ?", true).Find(&pairs).Error; err != nil {
		log.Fatalf("查询交易对失败: %v", err)
	}

	fmt.Println("📝 交易对精度检查 (仅显示涉及 USDC 的交易对):")
	fmt.Println("--------------------------------------------------------------------------------")

	priceCalc := utils.NewPriceCalculator()

	for _, p := range pairs {
		if p.Token0.Symbol == "USDC" || p.Token1.Symbol == "USDC" {
			fmt.Printf("Pair ID: %d | %s/%s @ %s\n",
				p.ID, p.Token0.Symbol, p.Token1.Symbol, p.Dex.Name)
			fmt.Printf("  Token0: %s (decimals=%d)\n", p.Token0.Symbol, p.Token0.Decimals)
			fmt.Printf("  Token1: %s (decimals=%d)\n", p.Token1.Symbol, p.Token1.Decimals)

			// 如果有储备数据，计算价格
			var reserve models.PairReserve
			if err := db.Where("pair_id = ?", p.ID).Order("timestamp DESC").First(&reserve).Error; err == nil {
				r0 := new(big.Int)
				r1 := new(big.Int)
				r0.SetString(reserve.Reserve0, 10)
				r1.SetString(reserve.Reserve1, 10)

				price := priceCalc.CalculateNormalizedPrice(r0, r1, p.Token0.Decimals, p.Token1.Decimals)
				fmt.Printf("  Reserve0: %s\n", reserve.Reserve0)
				fmt.Printf("  Reserve1: %s\n", reserve.Reserve1)
				fmt.Printf("  Calculated Price: %s\n", price.StringFixed(8))

				// 检查价格是否合理
				if p.Token0.Symbol == "WETH" && p.Token1.Symbol == "USDC" {
					// WETH/USDC 应该约为 3000-4000
					if price.GreaterThan(price.Abs().Mul(price.Abs())) {
						fmt.Printf("  ⚠️ 价格异常！预期约 3000-4000 USDC/ETH\n")
					}
				}
			}
			fmt.Println()
		}
	}

	// 5. 手动测试价格计算
	fmt.Println("\n📝 手动测试价格计算:")
	fmt.Println("----------------------------------------")

	// 模拟 WETH/USDC 数据
	// 假设 reserve0 = 100 WETH = 100 * 10^18
	// 假设 reserve1 = 334700 USDC = 334700 * 10^6
	testReserve0, _ := new(big.Int).SetString("100000000000000000000", 10) // 100 * 10^18
	testReserve1, _ := new(big.Int).SetString("334700000000", 10)         // 334700 * 10^6

	fmt.Printf("测试数据:\n")
	fmt.Printf("  Reserve0 (WETH): %s (100 WETH)\n", testReserve0.String())
	fmt.Printf("  Reserve1 (USDC): %s (334,700 USDC)\n", testReserve1.String())
	fmt.Printf("  WETH decimals: 18\n")
	fmt.Printf("  USDC decimals: 6\n")

	testPrice := priceCalc.CalculateNormalizedPrice(testReserve0, testReserve1, 18, 6)
	fmt.Printf("  计算结果: %s USDC/WETH (预期约 3347)\n", testPrice.StringFixed(8))

	// 测试反向 USDC/WETH
	testPriceInverse := priceCalc.CalculateNormalizedPrice(testReserve1, testReserve0, 6, 18)
	fmt.Printf("  反向价格: %s WETH/USDC (预期约 0.0003)\n", testPriceInverse.StringFixed(8))
}

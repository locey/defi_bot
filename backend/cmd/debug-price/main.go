package main

import (
	"fmt"
	"log"
	"math/big"
	"strings"

	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/internal/models"
	"github.com/defi-bot/backend/pkg/utils"
)

func main() {
	fmt.Println("=== 调试价格计算 ===")

	cfg, err := config.LoadConfig("configs/config.yaml")
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	if err := database.InitDB(&cfg.Database); err != nil {
		log.Fatalf("数据库初始化失败: %v", err)
	}
	defer database.CloseDB()

	db := database.GetDB()

	// 只检查 WETH/USDC @ Uniswap V2 (Pair ID 20)
	var pair models.TradingPair
	if err := db.Preload("Token0").Preload("Token1").First(&pair, 20).Error; err != nil {
		log.Fatalf("查询交易对失败: %v", err)
	}

	fmt.Printf("交易对: %s/%s (Pair ID: %d)\n", pair.Token0.Symbol, pair.Token1.Symbol, pair.ID)
	fmt.Printf("Token0: %s, 地址: %s, 精度: %d\n", pair.Token0.Symbol, pair.Token0.Address, pair.Token0.Decimals)
	fmt.Printf("Token1: %s, 地址: %s, 精度: %d\n", pair.Token1.Symbol, pair.Token1.Address, pair.Token1.Decimals)

	// 比较地址
	dbToken0Addr := strings.ToLower(pair.Token0.Address)
	dbToken1Addr := strings.ToLower(pair.Token1.Address)
	fmt.Printf("\n地址比较:\n")
	fmt.Printf("  dbToken0Addr: %s\n", dbToken0Addr)
	fmt.Printf("  dbToken1Addr: %s\n", dbToken1Addr)
	fmt.Printf("  dbToken0Addr > dbToken1Addr: %v\n", dbToken0Addr > dbToken1Addr)

	if dbToken0Addr > dbToken1Addr {
		fmt.Println("  → 需要交换 reserve!")
	} else {
		fmt.Println("  → 不需要交换 reserve")
	}

	// 获取最新储备数据
	var reserve models.PairReserve
	if err := db.Where("pair_id = ?", pair.ID).Order("timestamp DESC").First(&reserve).Error; err != nil {
		log.Fatalf("查询储备失败: %v", err)
	}

	fmt.Printf("\n数据库中存储的 Reserve:\n")
	fmt.Printf("  Reserve0: %s\n", reserve.Reserve0)
	fmt.Printf("  Reserve1: %s\n", reserve.Reserve1)

	r0 := new(big.Int)
	r1 := new(big.Int)
	r0.SetString(reserve.Reserve0, 10)
	r1.SetString(reserve.Reserve1, 10)

	priceCalc := utils.NewPriceCalculator()

	// 按照数据库顺序计算（当前方式）
	price := priceCalc.CalculateNormalizedPrice(r0, r1, pair.Token0.Decimals, pair.Token1.Decimals)
	fmt.Printf("\n按数据库顺序计算价格（错误）:\n")
	fmt.Printf("  价格 = reserve1 / reserve0 (考虑精度)\n")
	fmt.Printf("  价格 = %s\n", price.StringFixed(8))

	// 交换后计算（正确方式）
	priceSwapped := priceCalc.CalculateNormalizedPrice(r1, r0, pair.Token1.Decimals, pair.Token0.Decimals)
	fmt.Printf("\n交换后计算价格（正确）:\n")
	fmt.Printf("  价格 = reserve0 / reserve1 (考虑精度)\n")
	fmt.Printf("  价格 = %s\n", priceSwapped.StringFixed(8))

	// 手动分析
	fmt.Printf("\n手动分析:\n")
	// 链上: token0 = USDC (地址小), token1 = WETH (地址大)
	// 链上: reserve0 = USDC储备, reserve1 = WETH储备
	// 数据库: Token0 = WETH, Token1 = USDC
	// 所以数据库中存储的 Reserve0 实际是链上的 reserve0 = USDC储备
	// 数据库中存储的 Reserve1 实际是链上的 reserve1 = WETH储备

	// 如果 Reserve0 是 USDC 储备（6位精度），应该较小
	// 如果 Reserve1 是 WETH 储备（18位精度），应该较大
	fmt.Printf("  Reserve0 (应该是USDC，6位精度): %s\n", reserve.Reserve0)
	fmt.Printf("  Reserve1 (应该是WETH，18位精度): %s\n", reserve.Reserve1)

	// 验证：USDC 储备 / 10^6 应该是美元数量
	usdc := new(big.Float).SetInt(r0)
	usdc.Quo(usdc, big.NewFloat(1e6))
	fmt.Printf("  USDC 数量（如果 Reserve0 是 USDC）: %s\n", usdc.Text('f', 2))

	// 验证：WETH 储备 / 10^18 应该是 ETH 数量
	weth := new(big.Float).SetInt(r1)
	weth.Quo(weth, big.NewFloat(1e18))
	fmt.Printf("  WETH 数量（如果 Reserve1 是 WETH）: %s\n", weth.Text('f', 6))
}

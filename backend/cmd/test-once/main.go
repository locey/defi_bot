package main

import (
	"fmt"
	"log"
	"math/big"

	"github.com/defi-bot/backend/internal/collector"
	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/internal/models"
	"github.com/defi-bot/backend/pkg/cache"
	"github.com/defi-bot/backend/pkg/utils"
	"github.com/defi-bot/backend/pkg/web3"
)

func main() {
	fmt.Println("=== 单次采集测试 ===\n")

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

	// 3. 检查初始状态
	var initialCount int64
	db.Model(&models.PairReserve{}).Count(&initialCount)
	fmt.Printf("初始记录数: %d\n\n", initialCount)

	// 4. 初始化 Web3 客户端
	web3Client, err := web3.NewClient(
		cfg.Blockchain.RPCURL,
		cfg.Blockchain.ChainID,
		cfg.Blockchain.Timeout,
	)
	if err != nil {
		log.Fatalf("Web3 客户端初始化失败: %v", err)
	}

	// 5. 初始化 Redis 缓存（禁用缓存以确保干净测试）
	var redisCache *cache.RedisCache = nil

	// 6. 初始化采集器
	dataCollector := collector.NewCollector(web3Client, nil, redisCache)

	// 7. 采集一次数据
	fmt.Println("开始采集数据...")
	blockNumber, _ := web3Client.GetBlockNumber()
	if err := dataCollector.CollectPricesConcurrent(blockNumber); err != nil {
		log.Printf("采集失败: %v", err)
	}

	// 8. 检查采集后状态
	var finalCount int64
	db.Model(&models.PairReserve{}).Count(&finalCount)
	fmt.Printf("\n最终记录数: %d (新增 %d 条)\n\n", finalCount, finalCount-initialCount)

	// 9. 检查 WETH/USDC 价格
	fmt.Println("=== 检查 WETH/USDC 价格 ===")
	
	var pair models.TradingPair
	db.Preload("Token0").Preload("Token1").First(&pair, 20) // Pair ID 20 = WETH/USDC @ Uniswap V2

	var reserve models.PairReserve
	db.Where("pair_id = ?", pair.ID).Order("timestamp DESC").First(&reserve)

	r0 := new(big.Int)
	r1 := new(big.Int)
	r0.SetString(reserve.Reserve0, 10)
	r1.SetString(reserve.Reserve1, 10)

	priceCalc := utils.NewPriceCalculator()
	price := priceCalc.CalculateNormalizedPrice(r0, r1, pair.Token0.Decimals, pair.Token1.Decimals)

	fmt.Printf("交易对: %s/%s @ Uniswap V2\n", pair.Token0.Symbol, pair.Token1.Symbol)
	fmt.Printf("Token0 (%s) 精度: %d\n", pair.Token0.Symbol, pair.Token0.Decimals)
	fmt.Printf("Token1 (%s) 精度: %d\n", pair.Token1.Symbol, pair.Token1.Decimals)
	fmt.Printf("数据库 Reserve0: %s\n", reserve.Reserve0)
	fmt.Printf("数据库 Reserve1: %s\n", reserve.Reserve1)
	fmt.Printf("计算价格: %s\n", price.StringFixed(8))

	// 计算实际 token 数量
	token0Amount := new(big.Float).SetInt(r0)
	token0Amount.Quo(token0Amount, big.NewFloat(float64(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(pair.Token0.Decimals)), nil).Int64())))
	
	token1Amount := new(big.Float).SetInt(r1)
	token1Amount.Quo(token1Amount, big.NewFloat(float64(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(pair.Token1.Decimals)), nil).Int64())))

	fmt.Printf("\n实际数量:\n")
	fmt.Printf("  %s: %s\n", pair.Token0.Symbol, token0Amount.Text('f', 6))
	fmt.Printf("  %s: %s\n", pair.Token1.Symbol, token1Amount.Text('f', 6))

	// 检查价格是否合理
	priceFloat, _ := price.Float64()
	if priceFloat > 2000 && priceFloat < 5000 {
		fmt.Println("\n✅ 价格正确！(约 3000-4000 USDC/ETH)")
	} else if priceFloat > 0 && priceFloat < 1 {
		fmt.Println("\n⚠️ 价格看起来是反向的 (ETH/USDC)")
	} else {
		fmt.Println("\n❌ 价格异常！")
	}
}

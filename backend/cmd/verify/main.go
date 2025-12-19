package main

import (
	"flag"
	"fmt"
	"log"
	"math/big"
	"time"

	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/internal/models"
	"github.com/defi-bot/backend/pkg/dex"
	"github.com/defi-bot/backend/pkg/web3"
	"gorm.io/gorm"
)

var (
	configPath = flag.String("config", "configs/config.test.yaml", "配置文件路径")
	limit      = flag.Int("limit", 10, "验证数据条数")
)

func main() {
	flag.Parse()

	fmt.Println("========================================")
	fmt.Println("🔍 数据真实性验证工具")
	fmt.Println("========================================")

	// 1. 加载配置
	fmt.Printf("加载配置文件: %s\n", *configPath)
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		fmt.Printf("❌ 加载配置失败: %v\n", err)
		return
	}
	fmt.Println("✅ 配置加载成功")

	// 2. 初始化数据库
	if err := database.InitDB(&cfg.Database); err != nil {
		log.Fatalf("❌ 数据库初始化失败: %v", err)
	}
	defer database.CloseDB()
	db := database.GetDB()

	// 3. 初始化 Web3 客户端
	client, err := web3.NewClient(
		cfg.Blockchain.RPCURL,
		cfg.Blockchain.ChainID,
		cfg.Blockchain.Timeout,
	)
	if err != nil {
		log.Fatalf("❌ Web3 客户端初始化失败: %v", err)
	}
	defer client.Close()

	// 4. 创建协议工厂
	protocolFactory := dex.NewProtocolFactory(client)

	// 5. 设置数据库日志为静默模式
	db.Logger = db.Logger.LogMode(1) // Silent mode

	// 6. 查询最新的价格记录
	var prices []models.PriceRecord
	err = db.Preload("Pair").
		Preload("Pair.Token0").
		Preload("Pair.Token1").
		Preload("Pair.Dex").
		Order("created_at DESC").
		Limit(*limit).
		Find(&prices).Error

	if err != nil {
		log.Fatalf("❌ 查询价格记录失败: %v", err)
	}

	if len(prices) == 0 {
		log.Println("⚠️  数据库中没有价格记录")
		log.Println("提示：请先运行数据采集服务")
		return
	}

	log.Printf("\n📊 开始验证最近 %d 条价格记录...\n", len(prices))
	log.Println("========================================")

	// 7. 验证每条记录
	successCount := 0
	failCount := 0

	for i, price := range prices {
		log.Printf("\n[%d/%d] 验证中...", i+1, len(prices))
		log.Println("----------------------------------------")

		// 验证单条记录
		if verifyPriceRecord(client, protocolFactory, &price) {
			successCount++
		} else {
			failCount++
		}
	}

	// 8. 输出统计结果
	log.Println("\n========================================")
	log.Println("📈 验证统计")
	log.Println("========================================")
	log.Printf("✅ 通过验证: %d 条", successCount)
	log.Printf("❌ 验证失败: %d 条", failCount)
	log.Printf("📊 准确率: %.2f%%", float64(successCount)/float64(len(prices))*100)

	// 9. 检查区块延迟
	log.Println("\n========================================")
	log.Println("⏱️  区块延迟检查")
	log.Println("========================================")
	checkBlockDelay(client, db)

	log.Println("\n========================================")
	log.Println("✅ 验证完成")
	log.Println("========================================")
}

// verifyPriceRecord 验证单条价格记录
func verifyPriceRecord(client *web3.Client, factory *dex.ProtocolFactory, price *models.PriceRecord) bool {
	pair := &price.Pair
	if pair.ID == 0 {
		log.Println("❌ 错误：交易对信息缺失")
		return false
	}

	log.Printf("交易对: %s/%s", pair.Token0.Symbol, pair.Token1.Symbol)
	log.Printf("DEX: %s (%s)", pair.Dex.Name, pair.Dex.Protocol)
	log.Printf("地址: %s", pair.PairAddress)
	log.Printf("数据库记录时间: %s", price.Timestamp.Format("2006-01-02 15:04:05"))

	// 获取协议适配器
	protocol, err := factory.CreateProtocol(pair.Dex.Protocol)
	if err != nil {
		log.Printf("❌ 获取协议适配器失败: %v", err)
		return false
	}

	// 从链上查询当前储备量
	priceInfo, err := protocol.GetPrice(pair.PairAddress)
	if err != nil {
		log.Printf("❌ 查询链上数据失败: %v", err)
		return false
	}

	// 解析数据库中的储备量
	dbReserve0, ok := new(big.Int).SetString(price.Reserve0, 10)
	if !ok {
		log.Printf("❌ 解析 Reserve0 失败")
		return false
	}

	dbReserve1, ok := new(big.Int).SetString(price.Reserve1, 10)
	if !ok {
		log.Printf("❌ 解析 Reserve1 失败")
		return false
	}

	// 计算误差
	log.Println("\n📊 储备量对比：")
	log.Printf("Reserve0:")
	log.Printf("  链上:    %s", priceInfo.Reserve0.String())
	log.Printf("  数据库:  %s", dbReserve0.String())

	errorRate0 := calculateErrorRate(priceInfo.Reserve0, dbReserve0)
	log.Printf("  误差率:  %.4f%%", errorRate0)

	log.Printf("\nReserve1:")
	log.Printf("  链上:    %s", priceInfo.Reserve1.String())
	log.Printf("  数据库:  %s", dbReserve1.String())

	errorRate1 := calculateErrorRate(priceInfo.Reserve1, dbReserve1)
	log.Printf("  误差率:  %.4f%%", errorRate1)

	// 判断是否通过验证（误差率 < 5% 认为合理）
	maxErrorRate := 5.0
	if abs(errorRate0) < maxErrorRate && abs(errorRate1) < maxErrorRate {
		log.Println("\n✅ 验证通过：数据真实可靠")
		return true
	} else {
		log.Println("\n⚠️  警告：数据存在偏差（可能是时间差导致）")
		return false
	}
}

// calculateErrorRate 计算误差率
func calculateErrorRate(chainValue, dbValue *big.Int) float64 {
	if chainValue.Sign() == 0 {
		return 0
	}

	// 计算差值
	diff := new(big.Int).Sub(chainValue, dbValue)
	diffFloat := new(big.Float).SetInt(diff)
	chainFloat := new(big.Float).SetInt(chainValue)

	// 计算误差率 = (差值 / 链上值) * 100
	errorRate := new(big.Float).Quo(diffFloat, chainFloat)
	errorRate.Mul(errorRate, big.NewFloat(100))

	result, _ := errorRate.Float64()
	return result
}

// checkBlockDelay 检查区块延迟
func checkBlockDelay(client *web3.Client, db *gorm.DB) {
	// 查询数据库最新区块号
	var latestRecord models.PriceRecord
	err := db.Order("block_number DESC").First(&latestRecord).Error
	if err != nil {
		log.Printf("❌ 查询数据库失败: %v", err)
		return
	}

	// 查询链上最新区块号
	latestBlock, err := client.GetBlockNumber()
	if err != nil {
		log.Printf("❌ 查询链上区块失败: %v", err)
		return
	}

	blockDiff := latestBlock - latestRecord.BlockNumber
	timeDiff := time.Since(latestRecord.Timestamp)

	log.Printf("数据库最新区块: %d", latestRecord.BlockNumber)
	log.Printf("链上最新区块:   %d", latestBlock)
	log.Printf("区块差距:       %d 个区块", blockDiff)
	log.Printf("时间差距:       %s", timeDiff.Round(time.Second))

	// 判断延迟情况
	if blockDiff < 10 {
		log.Println("\n✅ 数据实时性良好（延迟 < 10 个区块）")
	} else if blockDiff < 50 {
		log.Println("\n⚠️  数据有轻微延迟（10-50 个区块）")
	} else {
		log.Println("\n❌ 数据延迟较大（> 50 个区块）")
	}
}

// abs 返回绝对值
func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

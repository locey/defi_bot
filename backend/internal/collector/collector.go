package collector

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"time"

	"github.com/shopspring/decimal"
	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/internal/models"
	"github.com/defi-bot/backend/pkg/cache"
	"github.com/defi-bot/backend/pkg/dex"
	"github.com/defi-bot/backend/pkg/utils"
	"github.com/defi-bot/backend/pkg/web3"
)

// Collector 数据采集器（统一 DEX 和 CEX）
type Collector struct {
	web3Client      *web3.Client
	protocolFactory *dex.ProtocolFactory
	cexCollector    *CexCollector // ← 新增: CEX 采集器
	cache           *cache.RedisCache
}

// NewCollector 创建新的采集器
func NewCollector(web3Client *web3.Client, cexCollector *CexCollector, redisCache *cache.RedisCache) *Collector {
	return &Collector{
		web3Client:      web3Client,
		protocolFactory: dex.NewProtocolFactory(web3Client),
		cexCollector:    cexCollector, // ← 初始化 CEX 采集器
		cache:           redisCache,
	}
}

// CollectAllData 采集所有数据（DEX + CEX，使用并发优化）
func (c *Collector) CollectAllData() error {
	log.Println("开始采集数据（DEX + CEX）...")

	startTime := time.Now()

	// 1. DEX 数据采集
	if err := c.CollectDexData(); err != nil {
		log.Printf("DEX 数据采集失败: %v", err)
	}

	// 2. ✅ CEX 数据采集
	if c.cexCollector != nil {
		ctx := context.Background()
		if err := c.cexCollector.CollectAllCexData(ctx); err != nil {
			log.Printf("CEX 数据采集失败: %v", err)
		}
	}

	duration := time.Since(startTime)
	log.Printf("数据采集完成，耗时: %v", duration)

	return nil
}

// CollectDexData 采集 DEX 链上数据（原有逻辑）
func (c *Collector) CollectDexData() error {
	log.Println("开始采集 DEX 链上数据...")

	// 1. 获取当前区块号
	blockNumber, err := c.web3Client.GetBlockNumber()
	if err != nil {
		return fmt.Errorf("获取区块号失败: %w", err)
	}
	log.Printf("当前区块号: %d", blockNumber)

	// 2. 采集交易对数据
	if err := c.CollectTradingPairs(); err != nil {
		log.Printf("采集交易对数据失败: %v", err)
	}

	// 3. 采集价格数据（使用并发优化）
	if err := c.CollectPricesConcurrent(blockNumber); err != nil {
		log.Printf("采集价格数据失败: %v", err)
	}

	// 4. 采集 V3 流动性深度数据
	if err := c.CollectV3Depths(); err != nil {
		log.Printf("采集V3深度数据失败: %v", err)
	}

	return nil
}

// CollectGasData 采集 Gas 价格数据（单独调用）
func (c *Collector) CollectGasData() error {
	gasCollector := NewGasCollector(c.web3Client)
	return gasCollector.CollectGasPrice()
}

// CollectTradingPairs 采集交易对数据
func (c *Collector) CollectTradingPairs() error {
	db := database.GetDB()

	// 获取所有活跃的 DEX（只查询 DEX 类型的交易所）
	var dexes []models.Exchange
	if err := db.Where("is_active = ? AND exchange_type = ?", true, "dex").Find(&dexes).Error; err != nil {
		return fmt.Errorf("查询 DEX 失败: %w", err)
	}

	// 获取所有活跃的代币
	var tokens []models.Token
	if err := db.Where("is_active = ?", true).Find(&tokens).Error; err != nil {
		return fmt.Errorf("查询代币失败: %w", err)
	}

	log.Printf("开始采集交易对数据: %d 个 DEX, %d 个代币", len(dexes), len(tokens))

	// 遍历所有 DEX 和代币组合，查找交易对
	for _, dexInfo := range dexes {
		// 获取协议适配器
		protocol, err := c.protocolFactory.CreateProtocol(dexInfo.Protocol)
		if err != nil {
			log.Printf("不支持的协议 %s: %v", dexInfo.Protocol, err)
			continue
		}

		for i := 0; i < len(tokens); i++ {
			for j := i + 1; j < len(tokens); j++ {
				token0 := tokens[i]
				token1 := tokens[j]

				// 根据协议类型获取交易对地址
				var pairAddress string
				protocolType := c.protocolFactory.GetProtocolType(dexInfo.Protocol)
				if protocolType == "v3" {
					// V3 需要提供 fee tier
					pairAddress, err = protocol.GetPairAddress(
						dexInfo.FactoryAddress,
						token0.Address,
						token1.Address,
						dexInfo.FeeTier,
					)
				} else {
					// V2 不需要额外参数
					pairAddress, err = protocol.GetPairAddress(
						dexInfo.FactoryAddress,
						token0.Address,
						token1.Address,
					)
				}

				if err != nil {
					continue
				}

				if pairAddress == "" {
					continue
				}

				// 检查交易对是否已存在
				var existingPair models.TradingPair
				result := db.Where("pair_address = ?", pairAddress).First(&existingPair)

				if result.Error != nil {
					// 创建新的交易对记录
					pair := models.TradingPair{
						ExchangeID:  dexInfo.ID,
						Token0ID:    token0.ID,
						Token1ID:    token1.ID,
						PairAddress: pairAddress,
						IsActive:    true,
					}

					if err := db.Create(&pair).Error; err != nil {
						log.Printf("创建交易对失败: %v", err)
						continue
					}

					log.Printf("发现新交易对: %s/%s on %s (%s)",
						token0.Symbol, token1.Symbol, dexInfo.Name, pairAddress)
				}
			}
		}
	}

	return nil
}

// GetPairAddress 获取交易对地址
// 调用 Factory 合约的 getPair 方法
func (c *Collector) GetPairAddress(factoryAddress, token0Address, token1Address string) (string, error) {
	pairAddress, err := c.web3Client.GetPairFromFactory(factoryAddress, token0Address, token1Address)
	if err != nil {
		return "", fmt.Errorf("获取交易对地址失败: %w", err)
	}
	return pairAddress, nil
}

// CalculatePrice 计算标准化价格（业界标准方法）
// 使用 decimal.Decimal 保证精度，返回人类可读的价格
func (c *Collector) CalculatePrice(
	reserve0, reserve1 *big.Int,
	decimals0, decimals1 int,
) (decimal.Decimal, decimal.Decimal) {
	
	priceCalc := utils.NewPriceCalculator()
	
	// 计算标准化价格
	price := priceCalc.CalculateNormalizedPrice(reserve0, reserve1, decimals0, decimals1)
	inversePrice := priceCalc.CalculateInversePrice(price)
	
	return price, inversePrice
}

// CleanupOldData 清理过期数据
func (c *Collector) CleanupOldData(keepDays int) error {
	db := database.GetDB()

	cutoffTime := time.Now().AddDate(0, 0, -keepDays)

	log.Printf("清理 %d 天前的历史数据...", keepDays)

	// 清理过期的储备量记录
	result := db.Where("timestamp < ?", cutoffTime).Delete(&models.PairReserve{})
	if result.Error != nil {
		return fmt.Errorf("清理储备量记录失败: %w", result.Error)
	}
	log.Printf("清理了 %d 条储备量记录", result.RowsAffected)

	// 清理过期的价格记录
	result = db.Where("timestamp < ?", cutoffTime).Delete(&models.PriceRecord{})
	if result.Error != nil {
		return fmt.Errorf("清理价格记录失败: %w", result.Error)
	}
	log.Printf("清理了 %d 条价格记录", result.RowsAffected)

	// 清理过期的套利机会
	result = db.Where("expires_at < ?", time.Now()).Delete(&models.ArbitrageOpportunity{})
	if result.Error != nil {
		return fmt.Errorf("清理套利机会失败: %w", result.Error)
	}
	log.Printf("清理了 %d 条过期的套利机会", result.RowsAffected)

	return nil
}

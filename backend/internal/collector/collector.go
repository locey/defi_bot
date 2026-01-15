package collector

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/shopspring/decimal"
	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/internal/models"
	"github.com/defi-bot/backend/pkg/cache"
	"github.com/defi-bot/backend/pkg/dex"
	"github.com/defi-bot/backend/pkg/log"
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
	log.Collector().Info().Msg("开始采集数据（DEX + CEX）...")

	startTime := time.Now()

	// 1. DEX 数据采集
	if err := c.CollectDexData(); err != nil {
		log.Collector().Error().Err(err).Msg("DEX 数据采集失败")
	}

	// 2. ✅ CEX 数据采集
	if c.cexCollector != nil {
		ctx := context.Background()
		if err := c.cexCollector.CollectAllCexData(ctx); err != nil {
			log.Collector().Error().Err(err).Msg("CEX 数据采集失败")
		}
	}

	duration := time.Since(startTime)
	log.Collector().Info().Dur("duration", duration).Msg("数据采集完成")

	return nil
}

// CollectDexData 采集 DEX 链上数据（原有逻辑）
func (c *Collector) CollectDexData() error {
	log.Collector().Info().Msg("开始采集 DEX 链上数据...")

	// 1. 获取当前区块号
	blockNumber, err := c.web3Client.GetBlockNumber()
	if err != nil {
		return fmt.Errorf("获取区块号失败: %w", err)
	}
	log.Collector().Info().Uint64("block", blockNumber).Msg("当前区块号")

	// 2. 采集交易对数据
	if err := c.CollectTradingPairs(); err != nil {
		log.Collector().Error().Err(err).Msg("采集交易对数据失败")
	}

	// 3. 采集价格数据（使用并发优化）
	if err := c.CollectPricesConcurrent(blockNumber); err != nil {
		log.Collector().Error().Err(err).Msg("采集价格数据失败")
	}

	// 4. 采集 V3 流动性深度数据（使用并发优化版）
	if err := c.CollectV3DepthsConcurrent(); err != nil {
		log.Collector().Error().Err(err).Msg("采集V3深度数据失败")
	}

	return nil
}

// CollectGasData 采集 Gas 价格数据（单独调用）
func (c *Collector) CollectGasData() error {
	gasCollector := NewGasCollector(c.web3Client)
	return gasCollector.CollectGasPrice()
}

// CollectPriceOnly 只采集价格数据（跳过交易对发现，用于快速测试）
func (c *Collector) CollectPriceOnly(skipDepth bool) error {
	log.Collector().Info().Msg("开始采集价格数据（快速模式）...")
	startTime := time.Now()

	// 1. 获取当前区块号
	blockNumber, err := c.web3Client.GetBlockNumber()
	if err != nil {
		return fmt.Errorf("获取区块号失败: %w", err)
	}
	log.Collector().Info().Uint64("block", blockNumber).Msg("当前区块号")

	// 2. 采集价格数据（使用并发优化）
	if err := c.CollectPricesConcurrent(blockNumber); err != nil {
		log.Collector().Error().Err(err).Msg("采集价格数据失败")
	}

	// 3. 采集 V3 流动性深度数据（可选）
	if !skipDepth {
		if err := c.CollectV3DepthsConcurrent(); err != nil {
			log.Collector().Error().Err(err).Msg("采集V3深度数据失败")
		}
	} else {
		log.Collector().Info().Msg("⏭️  跳过V3深度采集")
	}

	log.Collector().Info().Dur("duration", time.Since(startTime)).Msg("价格采集完成")
	return nil
}

// CollectTradingPairs 采集交易对数据（优化版）
// 只对数据库中不存在的组合进行 RPC 查询，避免重复调用
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

	// 🔧 优化：预加载已存在的交易对，避免重复 RPC 调用
	var existingPairs []models.TradingPair
	if err := db.Find(&existingPairs).Error; err != nil {
		return fmt.Errorf("查询已有交易对失败: %w", err)
	}

	// 构建已存在的组合集合 (exchange_id + token0_id + token1_id)
	existingSet := make(map[string]bool)
	for _, p := range existingPairs {
		// 两个方向都标记
		key1 := fmt.Sprintf("%d_%d_%d", p.ExchangeID, p.Token0ID, p.Token1ID)
		key2 := fmt.Sprintf("%d_%d_%d", p.ExchangeID, p.Token1ID, p.Token0ID)
		existingSet[key1] = true
		existingSet[key2] = true
	}

	log.Collector().Info().
		Int("dex_count", len(dexes)).
		Int("token_count", len(tokens)).
		Int("existing_pairs", len(existingPairs)).
		Msg("开始采集交易对数据")

	newPairsCount := 0

	// 遍历所有 DEX 和代币组合，查找交易对
	for _, dexInfo := range dexes {
		// 获取协议适配器
		protocol, err := c.protocolFactory.CreateProtocol(dexInfo.Protocol)
		if err != nil {
			log.Collector().Warn().Str("protocol", dexInfo.Protocol).Err(err).Msg("不支持的协议")
			continue
		}

		for i := 0; i < len(tokens); i++ {
			for j := i + 1; j < len(tokens); j++ {
				token0 := tokens[i]
				token1 := tokens[j]

				// 🔧 优化：跳过已存在的组合
				key := fmt.Sprintf("%d_%d_%d", dexInfo.ID, token0.ID, token1.ID)
				if existingSet[key] {
					continue // 已存在，跳过 RPC 调用
				}

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

				// 创建新的交易对记录
				pair := models.TradingPair{
					ExchangeID:  dexInfo.ID,
					Token0ID:    token0.ID,
					Token1ID:    token1.ID,
					PairAddress: pairAddress,
					IsActive:    true,
				}

				if err := db.Create(&pair).Error; err != nil {
					log.Collector().Error().Err(err).Msg("创建交易对失败")
					continue
				}

				newPairsCount++
				log.Collector().Info().
					Str("pair", fmt.Sprintf("%s/%s", token0.Symbol, token1.Symbol)).
					Str("dex", dexInfo.Name).
					Str("address", pairAddress).
					Msg("发现新交易对")
			}
		}
	}

	if newPairsCount > 0 {
		log.Collector().Info().Int("count", newPairsCount).Msg("✅ 新发现交易对")
	} else {
		log.Collector().Info().Msg("✅ 交易对已是最新，无需更新")
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

	log.Collector().Info().Int("keep_days", keepDays).Msg("清理历史数据...")

	// 清理过期的储备量记录
	result := db.Where("timestamp < ?", cutoffTime).Delete(&models.PairReserve{})
	if result.Error != nil {
		return fmt.Errorf("清理储备量记录失败: %w", result.Error)
	}
	log.Collector().Info().Int64("count", result.RowsAffected).Msg("清理储备量记录")

	// 清理过期的价格记录
	result = db.Where("timestamp < ?", cutoffTime).Delete(&models.PriceRecord{})
	if result.Error != nil {
		return fmt.Errorf("清理价格记录失败: %w", result.Error)
	}
	log.Collector().Info().Int64("count", result.RowsAffected).Msg("清理价格记录")

	// 清理过期的套利机会
	result = db.Where("expires_at < ?", time.Now()).Delete(&models.ArbitrageOpportunity{})
	if result.Error != nil {
		return fmt.Errorf("清理套利机会失败: %w", result.Error)
	}
	log.Collector().Info().Int64("count", result.RowsAffected).Msg("清理过期的套利机会")

	return nil
}

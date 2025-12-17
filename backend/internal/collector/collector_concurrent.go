package collector

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/internal/models"
	"gorm.io/gorm"
)

// PriceData 价格数据结构（用于并发采集）
type PriceData struct {
	PairID       uint
	Token0Symbol string
	Token1Symbol string
	DexName      string
	Reserve0     string
	Reserve1     string
	Price        string
	InversePrice string
	BlockNumber  uint64
	Timestamp    time.Time
}

// CollectPricesConcurrent 并发采集价格数据
func (c *Collector) CollectPricesConcurrent(blockNumber uint64) error {
	db := database.GetDB()

	// 获取所有活跃的交易对
	var pairs []models.TradingPair
	if err := db.Preload("Token0").Preload("Token1").Preload("Dex").
		Where("is_active = ?", true).Find(&pairs).Error; err != nil {
		return fmt.Errorf("查询交易对失败: %w", err)
	}

	if len(pairs) == 0 {
		log.Println("没有活跃的交易对")
		return nil
	}

	log.Printf("开始并发采集 %d 个交易对的价格数据...", len(pairs))
	startTime := time.Now()

	// 并发控制
	concurrency := 20 // 同时处理20个交易对
	semaphore := make(chan struct{}, concurrency)

	var wg sync.WaitGroup
	resultsChan := make(chan *PriceData, len(pairs))
	errorsChan := make(chan error, len(pairs))

	timestamp := time.Now()

	// 并发采集
	for _, pair := range pairs {
		wg.Add(1)
		go func(p models.TradingPair) {
			defer wg.Done()

			// 限流
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			// 采集数据（带重试）
			data, err := c.fetchPairDataWithRetry(p, blockNumber, timestamp)
			if err != nil {
				errorsChan <- fmt.Errorf("采集 %s/%s 失败: %w", p.Token0.Symbol, p.Token1.Symbol, err)
				return
			}

			resultsChan <- data
		}(pair)
	}

	// 等待所有goroutine完成
	go func() {
		wg.Wait()
		close(resultsChan)
		close(errorsChan)
	}()

	// 批量写入数据库
	err := c.batchInsertResults(resultsChan, errorsChan)

	duration := time.Since(startTime)
	log.Printf("并发采集完成，耗时: %v", duration)

	return err
}

// fetchPairDataWithRetry 带重试的数据采集
func (c *Collector) fetchPairDataWithRetry(pair models.TradingPair, blockNumber uint64, timestamp time.Time) (*PriceData, error) {
	// 尝试从缓存获取
	if c.cache != nil {
		cacheKey := fmt.Sprintf("price:%s", pair.PairAddress)
		var cachedData PriceData
		if err := c.cache.Get(cacheKey, &cachedData); err == nil {
			// 检查缓存是否过期（60秒内有效）
			if time.Since(cachedData.Timestamp) < 60*time.Second {
				log.Printf("🔥 从缓存获取: %s/%s @ %s", pair.Token0.Symbol, pair.Token1.Symbol, pair.Dex.Name)
				cachedData.BlockNumber = blockNumber // 更新区块号
				cachedData.Timestamp = timestamp     // 更新时间戳
				return &cachedData, nil
			}
		}
	}

	maxRetries := 3
	var lastErr error

	// 获取协议适配器
	protocol, err := c.protocolFactory.CreateProtocol(pair.Dex.Protocol)
	if err != nil {
		return nil, fmt.Errorf("获取协议适配器失败: %w", err)
	}

	for i := 0; i < maxRetries; i++ {
		// 使用协议适配器获取价格信息
		priceInfo, err := protocol.GetPrice(pair.PairAddress)
		if err != nil {
			lastErr = err
			time.Sleep(time.Millisecond * 100 * time.Duration(i+1)) // 指数退避
			continue
		}

		// 检查流动性
		if priceInfo.Reserve0.Sign() == 0 || priceInfo.Reserve1.Sign() == 0 {
			return nil, fmt.Errorf("无流动性")
		}

		// 计算价格（考虑精度调整）
		price, inversePrice := c.CalculatePrice(
			priceInfo.Reserve0, priceInfo.Reserve1,
			pair.Token0.Decimals, pair.Token1.Decimals,
		)

		priceData := &PriceData{
			PairID:       pair.ID,
			Token0Symbol: pair.Token0.Symbol,
			Token1Symbol: pair.Token1.Symbol,
			DexName:      pair.Dex.Name,
			Reserve0:     priceInfo.Reserve0.String(),
			Reserve1:     priceInfo.Reserve1.String(),
			Price:        price.String(),
			InversePrice: inversePrice.String(),
			BlockNumber:  blockNumber,
			Timestamp:    timestamp,
		}

		// 缓存数据（5分钟过期）
		if c.cache != nil {
			cacheKey := fmt.Sprintf("price:%s", pair.PairAddress)
			if err := c.cache.Set(cacheKey, priceData, 5*time.Minute); err != nil {
				log.Printf("⚠️  缓存写入失败: %v", err)
			}
		}

		return priceData, nil
	}

	return nil, fmt.Errorf("重试%d次后失败: %w", maxRetries, lastErr)
}

// batchInsertResults 批量插入结果
func (c *Collector) batchInsertResults(resultsChan chan *PriceData, errorsChan chan error) error {
	db := database.GetDB()

	reserves := make([]models.PairReserve, 0, 100)
	prices := make([]models.PriceRecord, 0, 100)

	successCount := 0
	errorCount := 0

	// 收集结果
	for data := range resultsChan {
		reserves = append(reserves, models.PairReserve{
			PairID:      data.PairID,
			Reserve0:    data.Reserve0,
			Reserve1:    data.Reserve1,
			BlockNumber: data.BlockNumber,
			Timestamp:   data.Timestamp,
		})

		prices = append(prices, models.PriceRecord{
			PairID:       data.PairID,
			Price:        data.Price,
			InversePrice: data.InversePrice,
			Reserve0:     data.Reserve0,
			Reserve1:     data.Reserve1,
			BlockNumber:  data.BlockNumber,
			Timestamp:    data.Timestamp,
		})

		log.Printf("✅ 采集成功: %s/%s @ %s - Price: %s",
			data.Token0Symbol, data.Token1Symbol, data.DexName,
			data.Price[:min(15, len(data.Price))])

		successCount++
	}

	// 收集错误
	for err := range errorsChan {
		log.Printf("⚠️  %v", err)
		errorCount++
	}

	log.Printf("采集统计: 成功=%d, 失败=%d", successCount, errorCount)

	// 批量插入（使用事务）
	if len(reserves) == 0 {
		log.Println("没有数据需要写入")
		return nil
	}

	log.Printf("开始批量写入 %d 条记录...", len(reserves))

	err := db.Transaction(func(tx *gorm.DB) error {
		// 批量插入储备量（每次1000条）
		batchSize := 1000
		for i := 0; i < len(reserves); i += batchSize {
			end := i + batchSize
			if end > len(reserves) {
				end = len(reserves)
			}
			if err := tx.CreateInBatches(reserves[i:end], batchSize).Error; err != nil {
				return fmt.Errorf("批量插入储备量失败: %w", err)
			}
		}

		// 批量插入价格
		for i := 0; i < len(prices); i += batchSize {
			end := i + batchSize
			if end > len(prices) {
				end = len(prices)
			}
			if err := tx.CreateInBatches(prices[i:end], batchSize).Error; err != nil {
				return fmt.Errorf("批量插入价格失败: %w", err)
			}
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("数据库写入失败: %w", err)
	}

	log.Printf("✅ 批量写入完成: %d 条储备量, %d 条价格记录", len(reserves), len(prices))
	return nil
}

// min 返回两个整数中的最小值
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

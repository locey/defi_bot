package collector

import (
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/internal/models"
	"github.com/defi-bot/backend/pkg/log"
)

// DepthResult 深度采集结果
type DepthResult struct {
	PairID  uint
	Symbol  string
	Depths  []models.LiquidityDepth
	Error   error
}

// CollectV3DepthsConcurrent 并发采集 V3 流动性深度数据
// 优化版本：使用并发提升效率，预计从 26秒 降低到 5-8秒
func (c *Collector) CollectV3DepthsConcurrent() error {
	db := database.GetDB()

	// 获取所有 V3 交易对
	var pairs []models.TradingPair
	err := db.Preload("Token0").
		Preload("Token1").
		Preload("Exchange").
		Joins("JOIN exchanges ON exchanges.id = trading_pairs.exchange_id").
		Where("exchanges.support_v3_ticks = ? AND exchanges.quoter_address != ? AND trading_pairs.is_active = ?",
			true, "", true).
		Find(&pairs).Error

	if err != nil {
		return fmt.Errorf("查询V3交易对失败: %w", err)
	}

	if len(pairs) == 0 {
		log.Collector().Info().Msg("没有V3交易对需要采集深度")
		return nil
	}

	log.Collector().Info().Int("count", len(pairs)).Msg("🚀 开始并发采集 V3 池的流动性深度")
	startTime := time.Now()

	// 定义测试金额（业界标准）
	testAmounts := []*big.Int{
		parseEther("0.1"), // 0.1 ETH - 小额交易
		parseEther("1"),   // 1 ETH - 中等交易
		parseEther("10"),  // 10 ETH - 大额交易
		parseEther("100"), // 100 ETH - 巨额交易
	}

	blockNumber, _ := c.web3Client.GetBlockNumber()
	timestamp := time.Now()

	// 并发控制：限制同时进行的 RPC 调用数量
	// 过高会导致 RPC 限流，过低会降低效率
	concurrency := 5 // 同时处理 5 个交易对
	semaphore := make(chan struct{}, concurrency)

	var wg sync.WaitGroup
	resultsChan := make(chan DepthResult, len(pairs))

	// 并发采集
	for _, pair := range pairs {
		if pair.Exchange.QuoterAddress == "" {
			continue
		}

		wg.Add(1)
		go func(p models.TradingPair) {
			defer wg.Done()

			// 获取信号量
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			symbol := fmt.Sprintf("%s/%s @ %s", p.Token0.Symbol, p.Token1.Symbol, p.Exchange.Name)

			depths, err := c.collectPairDepth(p, testAmounts, blockNumber, timestamp)
			resultsChan <- DepthResult{
				PairID: p.ID,
				Symbol: symbol,
				Depths: depths,
				Error:  err,
			}
		}(pair)
	}

	// 等待所有采集完成后关闭结果通道
	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	// 收集结果并批量写入
	allDepths := make([]models.LiquidityDepth, 0, len(pairs)*8)
	successCount := 0
	errorCount := 0

	for result := range resultsChan {
		if result.Error != nil {
			log.Collector().Warn().Str("symbol", result.Symbol).Err(result.Error).Msg("⚠️  采集深度失败")
			errorCount++
			continue
		}

		if len(result.Depths) > 0 {
			allDepths = append(allDepths, result.Depths...)
			log.Collector().Info().Str("symbol", result.Symbol).Int("points", len(result.Depths)).Msg("✅ 采集深度")
			successCount++
		}
	}

	// 批量写入数据库（一次性写入，减少 DB 开销）
	if len(allDepths) > 0 {
		if err := db.CreateInBatches(allDepths, 100).Error; err != nil {
			log.Collector().Warn().Err(err).Msg("⚠️  批量写入深度数据失败")
		}
	}

	duration := time.Since(startTime)
	log.Collector().Info().Int("success", successCount).Int("failed", errorCount).Int("records", len(allDepths)).Dur("duration", duration).Msg("✅ 并发深度采集完成")

	return nil
}

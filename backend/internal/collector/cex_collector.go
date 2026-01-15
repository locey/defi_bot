package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/internal/models"
	"github.com/defi-bot/backend/pkg/cache"
	"github.com/defi-bot/backend/pkg/cex"
	"github.com/defi-bot/backend/pkg/log"
	"github.com/defi-bot/backend/pkg/validation"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// CexCollector CEX 数据采集器
type CexCollector struct {
	binanceClient *cex.BinanceClient
	cache         *cache.RedisCache
}

// NewCexCollector 创建 CEX 采集器
func NewCexCollector(
	binanceConfig *cex.BinanceConfig,
	redisCache *cache.RedisCache,
) *CexCollector {
	return &CexCollector{
		binanceClient: cex.NewBinanceClient(binanceConfig),
		cache:         redisCache,
	}
}

// CollectAllCexData 采集所有 CEX 数据
func (cc *CexCollector) CollectAllCexData(ctx context.Context) error {
	log.CEX().Info().Msg("开始采集 CEX 数据...")

	startTime := time.Now()

	// 1. 采集币安行情数据
	if err := cc.CollectBinanceTickers(ctx); err != nil {
		log.CEX().Error().Err(err).Msg("采集币安行情失败")
		return err
	}

	// 2. 采集订单簿（可选，深度分析用）
	// if err := cc.CollectBinanceOrderBooks(ctx); err != nil {
	// 	log.Printf("采集订单簿失败: %v", err)
	// }

	duration := time.Since(startTime)
	log.CEX().Info().Dur("duration", duration).Msg("CEX 数据采集完成")

	return nil
}

// CollectBinanceTickers 采集币安行情数据
func (cc *CexCollector) CollectBinanceTickers(ctx context.Context) error {
	db := database.GetDB()

	// 1. 获取所有币安交易对
	var pairs []models.TradingPair
	if err := db.Preload("Token0").Preload("Token1").Preload("Exchange").
		Joins("JOIN exchanges ON exchanges.id = trading_pairs.exchange_id").
		Where("exchanges.protocol = ? AND trading_pairs.is_active = ?", "binance_spot", true).
		Find(&pairs).Error; err != nil {
		return fmt.Errorf("查询币安交易对失败: %w", err)
	}

	if len(pairs) == 0 {
		log.CEX().Info().Msg("没有活跃的币安交易对，跳过 CEX 采集")
		return nil
	}

	log.CEX().Info().Int("count", len(pairs)).Msg("开始并发采集币安交易对")

	// 2. 并发采集
	var wg sync.WaitGroup
	resultsChan := make(chan *CexPriceData, len(pairs))
	errorsChan := make(chan error, len(pairs))

	// 限制并发数（避免超过速率限制）
	concurrency := 10
	semaphore := make(chan struct{}, concurrency)

	timestamp := time.Now()

	for _, pair := range pairs {
		wg.Add(1)
		go func(p models.TradingPair) {
			defer wg.Done()

			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			// 采集单个交易对
			data, err := cc.fetchBinanceTicker(p, timestamp)
			if err != nil {
				errorsChan <- fmt.Errorf("采集 %s 失败: %w", p.Symbol, err)
				return
			}

			resultsChan <- data
		}(pair)
	}

	// 等待完成
	go func() {
		wg.Wait()
		close(resultsChan)
		close(errorsChan)
	}()

	// 3. 批量写入
	return cc.batchInsertCexResults(resultsChan, errorsChan)
}

// fetchBinanceTicker 获取单个币安交易对数据
func (cc *CexCollector) fetchBinanceTicker(
	pair models.TradingPair,
	timestamp time.Time,
) (*CexPriceData, error) {

	// 1. 尝试从缓存获取（10秒内有效）
	if cc.cache != nil {
		cacheKey := fmt.Sprintf("cex:binance:%s", pair.Symbol)
		var cachedData CexPriceData
		if err := cc.cache.Get(cacheKey, &cachedData); err == nil {
			if time.Since(cachedData.Timestamp) < 10*time.Second {
				log.CEX().Debug().Str("symbol", pair.Symbol).Msg("🔥 从缓存获取")
				cachedData.Timestamp = timestamp // 更新时间戳
				return &cachedData, nil
			}
		}
	}

	// 2. 调用币安 API
	ticker, err := cc.binanceClient.GetTicker(pair.Symbol)
	if err != nil {
		return nil, fmt.Errorf("API调用失败: %w", err)
	}

	// 3. 解析价格（币安返回的是标准格式字符串）
	lastPrice, err := decimal.NewFromString(ticker.LastPrice)
	if err != nil {
		return nil, fmt.Errorf("解析价格失败: %w", err)
	}

	// 验证价格（币安价格为 0 说明交易对不存在或已下线）
	if lastPrice.IsZero() {
		return nil, fmt.Errorf("价格为 0，交易对可能不存在: %s", pair.Symbol)
	}

	// 验证价格合理性
	validator := validation.NewPriceValidator()
	pairType := validation.DeterminePairType(pair.BaseAsset, pair.QuoteAsset)
	if err := validator.Validate(pair.Symbol, lastPrice, pairType); err != nil {
		log.CEX().Warn().Str("symbol", pair.Symbol).Err(err).Msg("⚠️  CEX 价格验证失败")
		// 继续处理，但记录警告
	}

	// 解析其他价格字段
	bidPrice, _ := decimal.NewFromString(ticker.BidPrice)
	askPrice, _ := decimal.NewFromString(ticker.AskPrice)
	highPrice, _ := decimal.NewFromString(ticker.HighPrice)
	lowPrice, _ := decimal.NewFromString(ticker.LowPrice)
	openPrice, _ := decimal.NewFromString(ticker.OpenPrice)
	weightedAvgPrice, _ := decimal.NewFromString(ticker.WeightedAvgPrice)
	volume, _ := decimal.NewFromString(ticker.Volume)
	quoteVolume, _ := decimal.NewFromString(ticker.QuoteVolume)
	priceChange, _ := decimal.NewFromString(ticker.PriceChange)
	priceChangePercent, _ := strconv.ParseFloat(ticker.PriceChangePercent, 64)

	// 4. 构造价格数据
	priceData := &CexPriceData{
		PairID:             pair.ID,
		Symbol:             pair.Symbol,
		ExchangeName:       "Binance",
		BaseAsset:          pair.BaseAsset,
		QuoteAsset:         pair.QuoteAsset,
		LastPrice:          lastPrice,
		BidPrice:           bidPrice,
		AskPrice:           askPrice,
		HighPrice:          highPrice,
		LowPrice:           lowPrice,
		OpenPrice:          openPrice,
		WeightedAvgPrice:   weightedAvgPrice,
		Volume24h:          volume,
		QuoteVolume24h:     quoteVolume,
		PriceChange:        priceChange,
		PriceChangePercent: priceChangePercent,
		TradeCount:         ticker.Count,
		OpenTime:           ticker.OpenTime,
		CloseTime:          ticker.CloseTime,
		Timestamp:          timestamp,
	}

	// 5. 缓存数据（10秒过期）
	if cc.cache != nil {
		cacheKey := fmt.Sprintf("cex:binance:%s", pair.Symbol)
		if err := cc.cache.Set(cacheKey, priceData, 10*time.Second); err != nil {
			log.CEX().Warn().Err(err).Msg("⚠️  缓存写入失败")
		}
	}

	return priceData, nil
}

// CollectBinanceOrderBooks 采集币安订单簿深度（可选）
func (cc *CexCollector) CollectBinanceOrderBooks(ctx context.Context) error {
	db := database.GetDB()

	// 获取需要深度分析的交易对（如主要交易对）
	var pairs []models.TradingPair
	if err := db.Preload("Exchange").
		Joins("JOIN exchanges ON exchanges.id = trading_pairs.exchange_id").
		Where("exchanges.protocol = ? AND trading_pairs.is_active = ?", "binance_spot", true).
		Limit(10). // 只采集前10个主要交易对（避免超过速率限制）
		Find(&pairs).Error; err != nil {
		return err
	}

	if len(pairs) == 0 {
		return nil
	}

	log.CEX().Info().Int("count", len(pairs)).Msg("开始采集币安订单簿")

	var wg sync.WaitGroup
	resultsChan := make(chan *OrderBookData, len(pairs))
	errorsChan := make(chan error, len(pairs))

	for _, pair := range pairs {
		wg.Add(1)
		go func(p models.TradingPair) {
			defer wg.Done()

			// 获取订单簿（20档深度）
			orderBook, err := cc.binanceClient.GetOrderBook(p.Symbol, 20)
			if err != nil {
				errorsChan <- fmt.Errorf("获取订单簿失败 %s: %w", p.Symbol, err)
				return
			}

			// 计算深度统计
			bidDepth, askDepth := calculateDepth(orderBook.Bids, orderBook.Asks)
			bid5Depth, ask5Depth := calculateDepthN(orderBook.Bids, orderBook.Asks, 5)
			bid10Depth, ask10Depth := calculateDepthN(orderBook.Bids, orderBook.Asks, 10)

			// 计算价差
			spread := 0.0
			if len(orderBook.Bids) > 0 && len(orderBook.Asks) > 0 {
				bidPrice, _ := strconv.ParseFloat(orderBook.Bids[0][0], 64)
				askPrice, _ := strconv.ParseFloat(orderBook.Asks[0][0], 64)
				if bidPrice > 0 {
					spread = ((askPrice - bidPrice) / bidPrice) * 100
				}
			}

			// 转换为JSON字符串
			bidsJSON, _ := json.Marshal(orderBook.Bids)
			asksJSON, _ := json.Marshal(orderBook.Asks)

			resultsChan <- &OrderBookData{
				PairID:     p.ID,
				Bids:       string(bidsJSON),
				Asks:       string(asksJSON),
				BidDepth:   bidDepth,
				AskDepth:   askDepth,
				Bid5Depth:  bid5Depth,
				Ask5Depth:  ask5Depth,
				Bid10Depth: bid10Depth,
				Ask10Depth: ask10Depth,
				BidPrice1:  orderBook.Bids[0][0],
				AskPrice1:  orderBook.Asks[0][0],
				Spread:     spread,
				SequenceID: orderBook.LastUpdateID,
				Timestamp:  time.Now(),
			}
		}(pair)
	}

	go func() {
		wg.Wait()
		close(resultsChan)
		close(errorsChan)
	}()

	// 批量写入
	return cc.batchInsertOrderBooks(resultsChan, errorsChan)
}

// batchInsertCexResults 批量插入 CEX 行情结果
func (cc *CexCollector) batchInsertCexResults(
	resultsChan chan *CexPriceData,
	errorsChan chan error,
) error {

	db := database.GetDB()

	// 收集数据
	tickers := []models.CexTicker{}
	priceRecords := []models.PriceRecord{}

	successCount := 0
	errorCount := 0

	for data := range resultsChan {
		// 1. CEX Ticker 记录
		ticker := models.CexTicker{
			PairID:             data.PairID,
			LastPrice:          data.LastPrice,
			BidPrice:           &data.BidPrice,
			AskPrice:           &data.AskPrice,
			HighPrice:          &data.HighPrice,
			LowPrice:           &data.LowPrice,
			OpenPrice:          &data.OpenPrice,
			WeightedAvgPrice:   &data.WeightedAvgPrice,
			Volume24h:          &data.Volume24h,
			QuoteVolume24h:     &data.QuoteVolume24h,
			PriceChange:        &data.PriceChange,
			PriceChangePercent: data.PriceChangePercent,
			TradeCount:         data.TradeCount,
			OpenTime:           data.OpenTime,
			CloseTime:          data.CloseTime,
			ExchangeName:       data.ExchangeName,
			Timestamp:          data.Timestamp,
		}
		tickers = append(tickers, ticker)

		// 2. 同时写入 PriceRecord（统一查询用）
		// 计算反向价格
		var inversePrice decimal.Decimal
		if !data.LastPrice.IsZero() {
			inversePrice = decimal.NewFromInt(1).Div(data.LastPrice)
		}

		priceRecord := models.PriceRecord{
			PairID:       data.PairID,
			Price:        data.LastPrice, // decimal.Decimal
			InversePrice: &inversePrice,  // 指针
			Reserve0:     "",             // CEX 没有储备量概念
			Reserve1:     "",
			BlockNumber:  0,               // CEX 没有区块号
			Volume24h:    &data.Volume24h, // 指针
			Timestamp:    data.Timestamp,
		}
		priceRecords = append(priceRecords, priceRecord)

		log.CEX().Info().Str("symbol", data.Symbol).Str("price", data.LastPrice.StringFixed(2)).Msg("✅ 采集成功")
		successCount++
	}

	// 收集错误
	for err := range errorsChan {
		log.CEX().Warn().Err(err).Msg("⚠️  采集失败")
		errorCount++
	}

	log.CEX().Info().Int("success", successCount).Int("failed", errorCount).Msg("CEX 采集统计")

	// 批量写入
	if len(tickers) == 0 {
		log.CEX().Info().Msg("没有 CEX 数据需要写入")
		return nil
	}

	log.CEX().Info().Int("count", len(tickers)).Msg("开始批量写入 CEX 记录")

	err := db.Transaction(func(tx *gorm.DB) error {
		// 批量插入 CEX Tickers
		if err := tx.CreateInBatches(tickers, 100).Error; err != nil {
			return fmt.Errorf("批量插入 CEX Ticker 失败: %w", err)
		}

		// 批量插入 Price Records（统一查询用）
		if err := tx.CreateInBatches(priceRecords, 100).Error; err != nil {
			return fmt.Errorf("批量插入价格记录失败: %w", err)
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("数据库写入失败: %w", err)
	}

	log.CEX().Info().Int("tickers", len(tickers)).Int("prices", len(priceRecords)).Msg("✅ 批量写入完成")
	return nil
}

// batchInsertOrderBooks 批量插入订单簿数据
func (cc *CexCollector) batchInsertOrderBooks(
	resultsChan chan *OrderBookData,
	errorsChan chan error,
) error {

	db := database.GetDB()

	orderbooks := []models.OrderBook{}
	successCount := 0
	errorCount := 0

	for data := range resultsChan {
		orderbook := models.OrderBook{
			PairID:     data.PairID,
			Bids:       data.Bids,
			Asks:       data.Asks,
			BidDepth:   data.BidDepth,
			AskDepth:   data.AskDepth,
			Bid5Depth:  data.Bid5Depth,
			Ask5Depth:  data.Ask5Depth,
			Bid10Depth: data.Bid10Depth,
			Ask10Depth: data.Ask10Depth,
			BidPrice1:  data.BidPrice1,
			AskPrice1:  data.AskPrice1,
			MidPrice:   data.MidPrice,
			Spread:     data.Spread,
			SequenceID: data.SequenceID,
			Timestamp:  data.Timestamp,
		}
		orderbooks = append(orderbooks, orderbook)

		log.CEX().Info().Str("symbol", data.Symbol).Float64("spread", data.Spread).Msg("✅ 订单簿")
		successCount++
	}

	for err := range errorsChan {
		log.CEX().Warn().Err(err).Msg("⚠️  采集失败")
		errorCount++
	}

	log.CEX().Info().Int("success", successCount).Int("failed", errorCount).Msg("订单簿采集统计")

	if len(orderbooks) == 0 {
		return nil
	}

	// 批量写入
	if err := db.CreateInBatches(orderbooks, 100).Error; err != nil {
		return fmt.Errorf("批量插入订单簿失败: %w", err)
	}

	log.CEX().Info().Int("count", len(orderbooks)).Msg("✅ 订单簿写入完成")
	return nil
}

// StartBinanceWebSocket 启动币安 WebSocket 实时流（后台任务）
func (cc *CexCollector) StartBinanceWebSocket(ctx context.Context, symbols []string) error {
	log.CEX().Info().Interface("symbols", symbols).Msg("启动币安 WebSocket 实时流")

	return cc.binanceClient.SubscribeTicker(ctx, symbols, func(update *cex.TickerUpdate) {
		// 处理实时更新
		cc.handleTickerUpdate(update)
	})
}

// handleTickerUpdate 处理实时行情更新
func (cc *CexCollector) handleTickerUpdate(update *cex.TickerUpdate) {
	// 1. 更新缓存
	if cc.cache != nil {
		// 解析价格字段
		lastPrice, _ := decimal.NewFromString(update.LastPrice)
		bidPrice, _ := decimal.NewFromString(update.BidPrice)
		askPrice, _ := decimal.NewFromString(update.AskPrice)
		highPrice, _ := decimal.NewFromString(update.HighPrice)
		lowPrice, _ := decimal.NewFromString(update.LowPrice)
		openPrice, _ := decimal.NewFromString(update.OpenPrice)
		weightedAvgPrice, _ := decimal.NewFromString(update.WeightedAvgPrice)
		volume, _ := decimal.NewFromString(update.Volume)
		quoteVolume, _ := decimal.NewFromString(update.QuoteVolume)
		priceChange, _ := decimal.NewFromString(update.PriceChange)

		cacheKey := fmt.Sprintf("cex:binance:%s", update.Symbol)
		cacheData := CexPriceData{
			Symbol:             update.Symbol,
			ExchangeName:       "Binance",
			LastPrice:          lastPrice,
			BidPrice:           bidPrice,
			AskPrice:           askPrice,
			HighPrice:          highPrice,
			LowPrice:           lowPrice,
			OpenPrice:          openPrice,
			WeightedAvgPrice:   weightedAvgPrice,
			Volume24h:          volume,
			QuoteVolume24h:     quoteVolume,
			PriceChange:        priceChange,
			PriceChangePercent: parseFloat(update.PriceChangePercent),
			TradeCount:         update.TradeCount,
			OpenTime:           update.OpenTime,
			CloseTime:          update.CloseTime,
			Timestamp:          time.Now(),
		}

		cc.cache.Set(cacheKey, cacheData, 10*time.Second)
		log.CEX().Debug().Str("symbol", update.Symbol).Str("price", lastPrice.StringFixed(2)).Msg("📡 实时更新")
	}

	// 2. 异步写入数据库（可选，避免频繁写入）
	// 可以使用批量缓冲区，每10秒写入一次
}

// CexPriceData CEX 价格数据结构（内部使用）
type CexPriceData struct {
	PairID       uint
	Symbol       string
	ExchangeName string
	BaseAsset    string // 新增：用于验证
	QuoteAsset   string // 新增：用于验证

	// 标准化价格（decimal.Decimal）
	LastPrice        decimal.Decimal
	BidPrice         decimal.Decimal
	AskPrice         decimal.Decimal
	HighPrice        decimal.Decimal
	LowPrice         decimal.Decimal
	OpenPrice        decimal.Decimal
	WeightedAvgPrice decimal.Decimal
	Volume24h        decimal.Decimal
	QuoteVolume24h   decimal.Decimal
	PriceChange      decimal.Decimal

	PriceChangePercent float64
	TradeCount         int64
	OpenTime           int64
	CloseTime          int64
	Timestamp          time.Time
}

// OrderBookData 订单簿数据结构（内部使用）
type OrderBookData struct {
	PairID     uint
	Symbol     string
	Bids       string // JSON字符串
	Asks       string // JSON字符串
	BidDepth   string
	AskDepth   string
	Bid5Depth  string
	Ask5Depth  string
	Bid10Depth string
	Ask10Depth string
	BidPrice1  string
	AskPrice1  string
	MidPrice   string
	Spread     float64
	SequenceID int64
	Timestamp  time.Time
}

// 辅助函数

// calculateInversePrice 计算反向价格
// calculateInversePrice 已废弃，现在直接使用 decimal.Decimal 计算
// 保留此函数以防其他地方引用
func calculateInversePrice(price string) string {
	p, err := decimal.NewFromString(price)
	if err != nil || p.IsZero() {
		return "0"
	}
	inverse := decimal.NewFromInt(1).Div(p)
	return inverse.String()
}

// parseFloat 解析浮点数
func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// calculateDepth 计算总深度
func calculateDepth(bids, asks [][2]string) (string, string) {
	bidTotal := 0.0
	for _, bid := range bids {
		amount, _ := strconv.ParseFloat(bid[1], 64)
		bidTotal += amount
	}

	askTotal := 0.0
	for _, ask := range asks {
		amount, _ := strconv.ParseFloat(ask[1], 64)
		askTotal += amount
	}

	return fmt.Sprintf("%.8f", bidTotal), fmt.Sprintf("%.8f", askTotal)
}

// calculateDepthN 计算前N档深度
func calculateDepthN(bids, asks [][2]string, n int) (string, string) {
	bidTotal := 0.0
	for i := 0; i < len(bids) && i < n; i++ {
		amount, _ := strconv.ParseFloat(bids[i][1], 64)
		bidTotal += amount
	}

	askTotal := 0.0
	for i := 0; i < len(asks) && i < n; i++ {
		amount, _ := strconv.ParseFloat(asks[i][1], 64)
		askTotal += amount
	}

	return fmt.Sprintf("%.8f", bidTotal), fmt.Sprintf("%.8f", askTotal)
}

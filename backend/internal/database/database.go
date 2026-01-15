package database

import (
	"fmt"
	"time"

	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/models"
	"github.com/defi-bot/backend/pkg/log"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var db *gorm.DB

// InitDB 初始化数据库连接
func InitDB(cfg *config.DatabaseConfig) error {
	var err error

	dsn := cfg.GetDSN()

	// 配置 GORM
	gormConfig := &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
		NowFunc: func() time.Time {
			return time.Now().Local()
		},
	}

	// 连接数据库
	db, err = gorm.Open(postgres.Open(dsn), gormConfig)
	if err != nil {
		return fmt.Errorf("连接数据库失败: %w", err)
	}

	// 获取底层的 sql.DB
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("获取数据库实例失败: %w", err)
	}

	// 设置连接池
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetConnMaxLifetime(time.Duration(cfg.ConnMaxLifetime) * time.Second)

	// 测试连接
	if err := sqlDB.Ping(); err != nil {
		return fmt.Errorf("数据库连接测试失败: %w", err)
	}

	log.Database().Info().Msg("数据库连接成功")
	return nil
}

// GetDB 获取数据库实例
func GetDB() *gorm.DB {
	if db == nil {
		log.Database().Fatal().Msg("数据库未初始化")
	}
	return db
}

// AutoMigrate 自动迁移数据库表
func AutoMigrate() error {
	log.Database().Info().Msg("开始数据库迁移...")

	// 迁移所有模型
	err := db.AutoMigrate(
		&models.Token{},
		&models.Exchange{},          // ✅ 更新：统一 DEX 和 CEX
		&models.TradingPair{},
		&models.PairReserve{},
		&models.PriceRecord{},
		&models.LiquidityDepth{},    // 流动性深度表
		&models.GasPriceHistory{},   // Gas价格历史表
		&models.OrderBook{},         // ✅ 新增：CEX 订单簿表
		&models.CexTicker{},         // ✅ 新增：CEX 行情表
		&models.ArbitrageOpportunity{},
		&models.ArbitrageExecution{},
	)

	if err != nil {
		return fmt.Errorf("数据库迁移失败: %w", err)
	}

	// 执行额外的字段类型迁移（解决 numeric overflow 问题）
	if err := migrateNumericFields(); err != nil {
		log.Database().Warn().Err(err).Msg("⚠️ 字段类型迁移失败（可忽略）")
	}

	log.Database().Info().Msg("数据库迁移完成")
	return nil
}

// migrateNumericFields 修改 numeric 字段类型以支持更大的数值
func migrateNumericFields() error {
	// 修改 price_records 表的价格字段类型
	alterStatements := []string{
		`ALTER TABLE price_records ALTER COLUMN price TYPE numeric(78,18)`,
		`ALTER TABLE price_records ALTER COLUMN inverse_price TYPE numeric(78,18)`,
		`ALTER TABLE price_records ALTER COLUMN price_usd TYPE numeric(78,18)`,
	}

	for _, stmt := range alterStatements {
		if err := db.Exec(stmt).Error; err != nil {
			// 忽略"已经是目标类型"的错误
			log.Database().Warn().Err(err).Msg("字段迁移")
		}
	}

	return nil
}

// CleanupTestnetData 清理测试网残留数据
// 用于切换到主网时清理旧的测试网数据
func CleanupTestnetData(targetChainID int64) error {
	log.Database().Info().Int64("chain_id", targetChainID).Msg("开始清理非当前链的残留数据")

	// 1. 获取需要保留的 token IDs（当前链的代币）
	var validTokenIDs []uint
	if err := db.Model(&models.Token{}).
		Where("chain_id = ?", targetChainID).
		Pluck("id", &validTokenIDs).Error; err != nil {
		return fmt.Errorf("查询有效代币失败: %w", err)
	}
	log.Database().Info().Int("count", len(validTokenIDs)).Msg("当前链有效代币数")

	if len(validTokenIDs) == 0 {
		log.Database().Warn().Msg("⚠️ 没有找到当前链的代币，跳过清理")
		return nil
	}

	// 2. 获取需要保留的 exchange IDs（当前链的交易所）
	var validExchangeIDs []uint
	if err := db.Model(&models.Exchange{}).
		Where("chain_id = ?", targetChainID).
		Pluck("id", &validExchangeIDs).Error; err != nil {
		return fmt.Errorf("查询有效交易所失败: %w", err)
	}
	log.Database().Info().Int("count", len(validExchangeIDs)).Msg("当前链有效交易所数")

	// 3. 获取需要保留的 trading_pair IDs
	var validPairIDs []uint
	if err := db.Model(&models.TradingPair{}).
		Where("exchange_id IN ?", validExchangeIDs).
		Where("token0_id IN ? AND token1_id IN ?", validTokenIDs, validTokenIDs).
		Pluck("id", &validPairIDs).Error; err != nil {
		return fmt.Errorf("查询有效交易对失败: %w", err)
	}
	log.Database().Info().Int("count", len(validPairIDs)).Msg("当前链有效交易对数")

	// 4. 删除无效的交易对相关数据（使用事务）
	return db.Transaction(func(tx *gorm.DB) error {
		// 删除无效的 pair_reserves
		result := tx.Where("pair_id NOT IN ?", validPairIDs).Delete(&models.PairReserve{})
		if result.Error != nil {
			return fmt.Errorf("删除无效储备量失败: %w", result.Error)
		}
		log.Database().Info().Int64("count", result.RowsAffected).Msg("删除无效储备量记录")

		// 删除无效的 price_records
		result = tx.Where("pair_id NOT IN ?", validPairIDs).Delete(&models.PriceRecord{})
		if result.Error != nil {
			return fmt.Errorf("删除无效价格记录失败: %w", result.Error)
		}
		log.Database().Info().Int64("count", result.RowsAffected).Msg("删除无效价格记录")

		// 删除无效的 liquidity_depths
		result = tx.Where("pair_id NOT IN ?", validPairIDs).Delete(&models.LiquidityDepth{})
		if result.Error != nil {
			return fmt.Errorf("删除无效深度记录失败: %w", result.Error)
		}
		log.Database().Info().Int64("count", result.RowsAffected).Msg("删除无效深度记录")

		// 删除无效的 trading_pairs
		result = tx.Where("id NOT IN ?", validPairIDs).Delete(&models.TradingPair{})
		if result.Error != nil {
			return fmt.Errorf("删除无效交易对失败: %w", result.Error)
		}
		log.Database().Info().Int64("count", result.RowsAffected).Msg("删除无效交易对")

		// 删除无效的 tokens
		result = tx.Where("chain_id != ?", targetChainID).Delete(&models.Token{})
		if result.Error != nil {
			return fmt.Errorf("删除无效代币失败: %w", result.Error)
		}
		log.Database().Info().Int64("count", result.RowsAffected).Msg("删除无效代币")

		// 删除无效的 exchanges
		result = tx.Where("chain_id != ?", targetChainID).Delete(&models.Exchange{})
		if result.Error != nil {
			return fmt.Errorf("删除无效交易所失败: %w", result.Error)
		}
		log.Database().Info().Int64("count", result.RowsAffected).Msg("删除无效交易所")

		log.Database().Info().Msg("✅ 测试网数据清理完成")
		return nil
	})
}

// CloseDB 关闭数据库连接
func CloseDB() error {
	if db != nil {
		sqlDB, err := db.DB()
		if err != nil {
			return err
		}
		return sqlDB.Close()
	}
	return nil
}

// SeedData 初始化种子数据
func SeedData(cfg *config.Config) error {
	log.Database().Info().Msg("开始初始化种子数据...")

	// 初始化代币数据
	for _, tokenCfg := range cfg.Tokens {
		var token models.Token
		result := db.Where("address = ?", tokenCfg.Address).First(&token)

		if result.Error == gorm.ErrRecordNotFound {
			// 代币不存在，创建新记录
			token = models.Token{
				Address:  tokenCfg.Address,
				Symbol:   tokenCfg.Symbol,
				Name:     tokenCfg.Symbol, // 可以后续更新
				Decimals: tokenCfg.Decimals,
				ChainID:  cfg.Blockchain.ChainID,
				IsActive: true,
			}
			if err := db.Create(&token).Error; err != nil {
				log.Database().Error().Str("symbol", tokenCfg.Symbol).Err(err).Msg("创建代币失败")
				continue
			}
			log.Database().Info().Str("symbol", tokenCfg.Symbol).Str("address", tokenCfg.Address).Msg("创建代币")
		}
	}

	// 初始化 DEX 数据（交易所类型为 dex）
	for _, dexCfg := range cfg.Dexes {
		var dex models.Exchange
		result := db.Where("name = ?", dexCfg.Name).First(&dex)

		// 设置默认值
		protocol := dexCfg.Protocol
		if protocol == "" {
			protocol = "uniswap_v2"
		}

		version := dexCfg.Version
		if version == "" {
			version = "v2"
		}

		chainID := dexCfg.ChainID
		if chainID == 0 {
			chainID = cfg.Blockchain.ChainID
		}

		dexType := dexCfg.DexType
		if dexType == "" {
			dexType = "amm" // 默认为 AMM
		}

		priority := dexCfg.Priority
		if priority == 0 {
			priority = 100 // 默认优先级
		}

		if result.Error == gorm.ErrRecordNotFound {
			// DEX 不存在，创建新记录
			dex = models.Exchange{
				Name:             dexCfg.Name,
				ExchangeType:     "dex", // ← DEX 类型
				DexType:          dexType,
				Protocol:         protocol,
				RouterAddress:    dexCfg.Router,
				FactoryAddress:   dexCfg.Factory,
				QuoterAddress:    dexCfg.Quoter,
				Fee:              dexCfg.Fee,
				FeeTier:          dexCfg.FeeTier,
				DynamicFee:       dexCfg.DynamicFee,
				ChainID:          chainID,
				IsActive:         true,
				SupportFlashLoan: dexCfg.SupportFlashLoan,
				SupportMultiHop:  dexCfg.SupportMultiHop,
				SupportV3Ticks:   dexCfg.SupportV3Ticks,
				Version:          version,
				Priority:         priority,
			}
			if err := db.Create(&dex).Error; err != nil {
				log.Database().Error().Str("name", dexCfg.Name).Err(err).Msg("创建 DEX 失败")
				continue
			}
			log.Database().Info().Str("name", dexCfg.Name).Str("type", dexType).Str("protocol", protocol).Str("version", version).Msg("✅ 创建 DEX")
		} else {
			// DEX 已存在，更新配置
			dex.DexType = dexType
			dex.Protocol = protocol
			dex.RouterAddress = dexCfg.Router
			dex.FactoryAddress = dexCfg.Factory
			dex.QuoterAddress = dexCfg.Quoter
			dex.Fee = dexCfg.Fee
			dex.FeeTier = dexCfg.FeeTier
			dex.DynamicFee = dexCfg.DynamicFee
			dex.Version = version
			dex.ChainID = chainID
			dex.SupportFlashLoan = dexCfg.SupportFlashLoan
			dex.SupportMultiHop = dexCfg.SupportMultiHop
			dex.SupportV3Ticks = dexCfg.SupportV3Ticks
			dex.Priority = priority

			if err := db.Save(&dex).Error; err != nil {
				log.Database().Error().Str("name", dexCfg.Name).Err(err).Msg("更新 DEX 失败")
				continue
			}
			log.Database().Info().Str("name", dexCfg.Name).Str("type", dexType).Str("protocol", protocol).Str("version", version).Msg("✅ 更新 DEX")
		}
	}

	// ✅ 初始化 CEX 数据（币安）
	if cfg.Cex.Enabled && cfg.Cex.Binance.Enabled {
		var binance models.Exchange
		result := db.Where("name = ?", "Binance").First(&binance)

		if result.Error == gorm.ErrRecordNotFound {
			// 创建币安交易所
			binance = models.Exchange{
				Name:             "Binance",
				ExchangeType:     "cex",           // ← CEX 类型
				Protocol:         "binance_spot",  // ← 协议
				Version:          "v3",
				APIEndpoint:      cfg.Cex.Binance.APIEndpoint,
				WSEndpoint:       cfg.Cex.Binance.WSEndpoint,
				TakerFee:         0.001,  // 0.1%
				MakerFee:         0.001,  // 0.1%
				IsActive:         true,
				Priority:         50,     // CEX 优先级高于 DEX（通常流动性更好）
				SupportOrderbook: true,   // 支持订单簿
				SupportWebSocket: true,   // 支持 WebSocket
				RateLimit:        cfg.Cex.Binance.RateLimit,
				Description:      "Binance Spot Exchange - Global Crypto Exchange",
				WebsiteURL:       "https://www.binance.com",
			}

			if err := db.Create(&binance).Error; err != nil {
				log.Database().Error().Err(err).Msg("创建 Binance 失败")
			} else {
				log.Database().Info().Msg("✅ 创建 CEX: Binance (Spot)")

				// 创建币安交易对
				createBinanceTradingPairs(db, binance.ID, cfg)
			}
		} else {
			log.Database().Info().Msg("✅ 币安交易所已存在")
		}
	}

	log.Database().Info().Msg("种子数据初始化完成")
	return nil
}

// createBinanceTradingPairs 创建币安交易对
func createBinanceTradingPairs(db *gorm.DB, binanceID uint, cfg *config.Config) {
	log.Database().Info().Msg("创建币安交易对...")

	for _, symbol := range cfg.Cex.Binance.Symbols {
		// 解析符号（如 "ETHUSDT" → base="ETH", quote="USDT"）
		base, quote := parseSymbol(symbol)

		// 符号映射（CEX 的 ETH 对应链上的 WETH）
		base = mapSymbolToToken(base)
		quote = mapSymbolToToken(quote)

		// 查找对应的 Token
		var baseToken, quoteToken models.Token
		db.Where("symbol = ?", base).First(&baseToken)
		db.Where("symbol = ?", quote).First(&quoteToken)

		if baseToken.ID == 0 {
			log.Database().Warn().Str("base", base).Str("symbol", symbol).Msg("⚠️  找不到基础代币，跳过")
			continue
		}
		if quoteToken.ID == 0 {
			log.Database().Warn().Str("quote", quote).Str("symbol", symbol).Msg("⚠️  找不到报价代币，跳过")
			continue
		}

		// 检查交易对是否已存在
		var existingPair models.TradingPair
		result := db.Where("exchange_id = ? AND symbol = ?", binanceID, symbol).First(&existingPair)

		if result.Error == gorm.ErrRecordNotFound {
			// 创建交易对
			pair := models.TradingPair{
				ExchangeID: binanceID,
				Token0ID:   baseToken.ID,  // Base = Token0
				Token1ID:   quoteToken.ID, // Quote = Token1
				Symbol:     symbol,        // CEX 使用 Symbol
				BaseAsset:  base,
				QuoteAsset: quote,
				IsActive:   true,
			}

			if err := db.Create(&pair).Error; err != nil {
				log.Database().Error().Str("symbol", symbol).Err(err).Msg("创建币安交易对失败")
				continue
			}

			log.Database().Info().Str("symbol", symbol).Str("base", base).Str("quote", quote).Msg("✅ 创建币安交易对")
		}
	}
}

// parseSymbol 解析币安交易对符号
func parseSymbol(symbol string) (string, string) {
	// 常见的报价资产（按长度从长到短排序，避免误匹配）
	quotes := []string{"USDT", "USDC", "BUSD", "TUSD", "BTC", "ETH", "BNB", "DAI"}

	for _, quote := range quotes {
		if len(symbol) > len(quote) && symbol[len(symbol)-len(quote):] == quote {
			base := symbol[:len(symbol)-len(quote)]
			return base, quote
		}
	}

	// 默认：无法解析
	return symbol, ""
}

// mapSymbolToToken 将 CEX 符号映射到链上代币符号
func mapSymbolToToken(symbol string) string {
	mapping := map[string]string{
		"ETH":  "WETH", // CEX 的 ETH 对应链上的 WETH
		"BTC":  "WBTC", // CEX 的 BTC 对应链上的 WBTC（如果有）
		"USDT": "USDT",
		"USDC": "USDC",
		"DAI":  "DAI",
	}
	
	if mapped, ok := mapping[symbol]; ok {
		return mapped
	}
	return symbol
}

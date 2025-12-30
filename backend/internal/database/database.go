package database

import (
	"fmt"
	"log"
	"time"

	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/models"
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

	log.Println("数据库连接成功")
	return nil
}

// GetDB 获取数据库实例
func GetDB() *gorm.DB {
	if db == nil {
		log.Fatal("数据库未初始化")
	}
	return db
}

// AutoMigrate 自动迁移数据库表
func AutoMigrate() error {
	log.Println("开始数据库迁移...")

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

	log.Println("数据库迁移完成")
	return nil
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
	log.Println("开始初始化种子数据...")

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
				log.Printf("创建代币 %s 失败: %v", tokenCfg.Symbol, err)
				continue
			}
			log.Printf("创建代币: %s (%s)", tokenCfg.Symbol, tokenCfg.Address)
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
				log.Printf("创建 DEX %s 失败: %v", dexCfg.Name, err)
				continue
			}
			log.Printf("✅ 创建 DEX: %s (类型: %s, 协议: %s, 版本: %s)", dexCfg.Name, dexType, protocol, version)
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
				log.Printf("更新 DEX %s 失败: %v", dexCfg.Name, err)
				continue
			}
			log.Printf("✅ 更新 DEX: %s (类型: %s, 协议: %s, 版本: %s)", dexCfg.Name, dexType, protocol, version)
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
				log.Printf("创建 Binance 失败: %v", err)
			} else {
				log.Printf("✅ 创建 CEX: Binance (Spot)")

				// 创建币安交易对
				createBinanceTradingPairs(db, binance.ID, cfg)
			}
		} else {
			log.Printf("✅ 币安交易所已存在")
		}
	}

	log.Println("种子数据初始化完成")
	return nil
}

// createBinanceTradingPairs 创建币安交易对
func createBinanceTradingPairs(db *gorm.DB, binanceID uint, cfg *config.Config) {
	log.Println("创建币安交易对...")

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
			log.Printf("⚠️  找不到基础代币: %s (映射自 %s)，跳过 %s", base, symbol[:len(symbol)-len(quote)], symbol)
			continue
		}
		if quoteToken.ID == 0 {
			log.Printf("⚠️  找不到报价代币: %s，跳过 %s", quote, symbol)
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
				log.Printf("创建币安交易对失败 %s: %v", symbol, err)
				continue
			}

			log.Printf("✅ 创建币安交易对: %s (%s/%s)", symbol, base, quote)
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

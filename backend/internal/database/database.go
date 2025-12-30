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
		&models.Dex{},
		&models.TradingPair{},
		&models.PairReserve{},
		&models.PriceRecord{},
		&models.LiquidityDepth{},  // ✅ 新增：流动性深度表
		&models.GasPriceHistory{}, // ✅ 新增：Gas价格历史表
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

	// 初始化 DEX 数据
	for _, dexCfg := range cfg.Dexes {
		var dex models.Dex
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
			dex = models.Dex{
				Name:             dexCfg.Name,
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

	log.Println("种子数据初始化完成")
	return nil
}

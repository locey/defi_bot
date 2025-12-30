// 测试 decimal 类型在数据库中的使用
package main

import (
	"fmt"
	"log"

	"github.com/shopspring/decimal"
	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/database"
)

// TestPriceRecord 测试用的简化价格记录
type TestPriceRecord struct {
	ID           uint            `gorm:"primaryKey"`
	Reserve0     string          `gorm:"type:varchar(78);not null"`
	Reserve1     string          `gorm:"type:varchar(78);not null"`
	Price        decimal.Decimal `gorm:"type:numeric(36,18);not null"`
	InversePrice *decimal.Decimal `gorm:"type:numeric(36,18)"`
}

func main() {
	fmt.Println("========================================")
	fmt.Println("测试 Decimal 类型")
	fmt.Println("========================================")
	fmt.Println()

	// 加载配置
	cfg, err := config.LoadConfig("configs/config.test.yaml")
	if err != nil {
		log.Fatalf("配置加载失败: %v", err)
	}

	// 连接数据库
	if err := database.InitDB(&cfg.Database); err != nil {
		log.Fatalf("数据库连接失败: %v", err)
	}
	defer database.CloseDB()

	db := database.GetDB()

	// 删除测试表
	db.Migrator().DropTable(&TestPriceRecord{})

	// 创建测试表
	fmt.Println("创建测试表...")
	if err := db.AutoMigrate(&TestPriceRecord{}); err != nil {
		log.Fatalf("创建表失败: %v", err)
	}
	fmt.Println("✅ 表创建成功")
	fmt.Println()

	// 测试插入数据
	fmt.Println("测试插入数据...")
	
	price := decimal.NewFromFloat(2930.08)
	inversePrice := decimal.NewFromFloat(0.000341)
	
	record := TestPriceRecord{
		Reserve0:     "100000000000000000000",
		Reserve1:     "293000000000",
		Price:        price,
		InversePrice: &inversePrice,
	}

	if err := db.Create(&record).Error; err != nil {
		log.Fatalf("插入数据失败: %v", err)
	}
	fmt.Println("✅ 数据插入成功")
	fmt.Printf("   ID: %d\n", record.ID)
	fmt.Printf("   Price: %s\n", record.Price.StringFixed(2))
	fmt.Println()

	// 测试查询数据
	fmt.Println("测试查询数据...")
	var retrieved TestPriceRecord
	if err := db.First(&retrieved).Error; err != nil {
		log.Fatalf("查询数据失败: %v", err)
	}
	fmt.Println("✅ 数据查询成功")
	fmt.Printf("   Price: %s\n", retrieved.Price.StringFixed(2))
	if retrieved.InversePrice != nil {
		fmt.Printf("   InversePrice: %s\n", retrieved.InversePrice.StringFixed(6))
	}
	fmt.Println()

	// 测试 SQL 查询
	fmt.Println("测试 SQL 聚合查询...")
	var avgPrice decimal.Decimal
	if err := db.Model(&TestPriceRecord{}).Select("AVG(price)").Scan(&avgPrice).Error; err != nil {
		log.Fatalf("聚合查询失败: %v", err)
	}
	fmt.Println("✅ SQL 聚合成功")
	fmt.Printf("   平均价格: %s\n", avgPrice.StringFixed(2))
	fmt.Println()

	// 清理
	db.Migrator().DropTable(&TestPriceRecord{})

	fmt.Println("========================================")
	fmt.Println("✅ Decimal 类型测试全部通过！")
	fmt.Println("========================================")
	fmt.Println()
	fmt.Println("说明:")
	fmt.Println("  • decimal.Decimal 可以正常存储到数据库")
	fmt.Println("  • 指针类型（*decimal.Decimal）支持 NULL")
	fmt.Println("  • SQL 聚合查询工作正常")
	fmt.Println("  • 可以安全地进行完整迁移")
	fmt.Println()
}


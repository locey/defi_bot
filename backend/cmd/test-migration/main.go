// 测试迁移程序 - 逐步测试每个环节
package main

import (
	"fmt"
	"log"

	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/database"
)

func main() {
	fmt.Println("========================================")
	fmt.Println("测试数据库迁移")
	fmt.Println("========================================")
	fmt.Println()

	// 步骤 1: 加载配置
	fmt.Println("步骤 1: 加载配置...")
	cfg, err := config.LoadConfig("configs/config.test.yaml")
	if err != nil {
		log.Fatalf("❌ 配置加载失败: %v", err)
	}
	fmt.Println("✅ 配置加载成功")
	fmt.Printf("   数据库: %s:%d\n", cfg.Database.Host, cfg.Database.Port)
	fmt.Printf("   用户: %s\n", cfg.Database.User)
	fmt.Printf("   数据库名: %s\n", cfg.Database.DBName)
	fmt.Printf("   RPC URL: %s\n", cfg.Blockchain.RPCURL)
	fmt.Printf("   Chain ID: %d\n", cfg.Blockchain.ChainID)
	fmt.Println()

	// 步骤 2: 连接数据库
	fmt.Println("步骤 2: 连接数据库...")
	if err := database.InitDB(&cfg.Database); err != nil {
		log.Fatalf("❌ 数据库连接失败: %v", err)
	}
	fmt.Println("✅ 数据库连接成功")
	defer database.CloseDB()
	fmt.Println()

	// 步骤 3: 执行迁移
	fmt.Println("步骤 3: 执行数据库迁移...")
	if err := database.AutoMigrate(); err != nil {
		log.Fatalf("❌ 数据库迁移失败: %v", err)
	}
	fmt.Println("✅ 数据库迁移完成")
	fmt.Println()

	// 步骤 4: 初始化种子数据
	fmt.Println("步骤 4: 初始化种子数据...")
	if err := database.SeedData(cfg); err != nil {
		log.Fatalf("❌ 种子数据初始化失败: %v", err)
	}
	fmt.Println("✅ 种子数据初始化完成")
	fmt.Println()

	fmt.Println("========================================")
	fmt.Println("✅ 所有步骤完成！")
	fmt.Println("========================================")
	fmt.Println()
	fmt.Println("现在可以启动服务:")
	fmt.Println("  go run cmd/server/main.go -config=configs/config.test.yaml")
	fmt.Println()
}





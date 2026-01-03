// cmd/fix-nullable/main.go
// 修复数据库表的可空约束
package main

import (
	"flag"
	"log"

	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/database"
)

var configPath = flag.String("config", "configs/config.yaml", "配置文件路径")

func main() {
	flag.Parse()

	// 加载配置
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 初始化数据库
	if err := database.InitDB(&cfg.Database); err != nil {
		log.Fatalf("数据库初始化失败: %v", err)
	}
	defer database.CloseDB()

	db := database.GetDB()

	// 修复 arbitrage_opportunities 表
	log.Println("修复 arbitrage_opportunities 表...")
	
	if err := db.Exec("ALTER TABLE arbitrage_opportunities ALTER COLUMN token_in_id DROP NOT NULL").Error; err != nil {
		log.Printf("修改 token_in_id 失败: %v (可能已经是可空)", err)
	} else {
		log.Println("  ✅ token_in_id 已设为可空")
	}

	if err := db.Exec("ALTER TABLE arbitrage_opportunities ALTER COLUMN token_out_id DROP NOT NULL").Error; err != nil {
		log.Printf("修改 token_out_id 失败: %v (可能已经是可空)", err)
	} else {
		log.Println("  ✅ token_out_id 已设为可空")
	}

	// 设置默认值
	if err := db.Exec("ALTER TABLE arbitrage_opportunities ALTER COLUMN pool_addresses SET DEFAULT '[]'").Error; err != nil {
		log.Printf("设置 pool_addresses 默认值失败: %v", err)
	} else {
		log.Println("  ✅ pool_addresses 默认值已设为 '[]'")
	}

	if err := db.Exec("ALTER TABLE arbitrage_opportunities ALTER COLUMN fee_tiers SET DEFAULT '[]'").Error; err != nil {
		log.Printf("设置 fee_tiers 默认值失败: %v", err)
	} else {
		log.Println("  ✅ fee_tiers 默认值已设为 '[]'")
	}

	// 修复 arbitrage_executions 表
	log.Println("\n修复 arbitrage_executions 表...")
	
	if err := db.Exec("ALTER TABLE arbitrage_executions ALTER COLUMN token_in_id DROP NOT NULL").Error; err != nil {
		log.Printf("修改 token_in_id 失败: %v (可能已经是可空)", err)
	} else {
		log.Println("  ✅ token_in_id 已设为可空")
	}

	if err := db.Exec("ALTER TABLE arbitrage_executions ALTER COLUMN token_out_id DROP NOT NULL").Error; err != nil {
		log.Printf("修改 token_out_id 失败: %v (可能已经是可空)", err)
	} else {
		log.Println("  ✅ token_out_id 已设为可空")
	}

	log.Println("\n✅ 数据库约束修复完成!")
}


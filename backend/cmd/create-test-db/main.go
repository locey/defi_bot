package main

import (
	"fmt"
	"log"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	fmt.Println("=== 创建测试数据库 ===")

	// 连接到默认的 postgres 数据库
	dsn := "host=127.0.0.1 port=5432 user=defi_user password=defi_pass123 dbname=postgres sslmode=disable TimeZone=Asia/Shanghai"
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("连接数据库失败: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("获取数据库实例失败: %v", err)
	}
	defer sqlDB.Close()

	// 测试连接
	if err := sqlDB.Ping(); err != nil {
		log.Fatalf("数据库连接测试失败: %v", err)
	}
	fmt.Println("✅ 连接到 PostgreSQL 成功")

	// 检查测试数据库是否已存在
	var exists bool
	db.Raw("SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = 'defi_arbitrage_test')").Scan(&exists)

	if exists {
		fmt.Println("⚠️  测试数据库 'defi_arbitrage_test' 已存在")
		
		// 询问是否删除重建（这里直接删除重建）
		fmt.Println("正在删除旧的测试数据库...")
		
		// 先断开所有连接
		db.Exec(`
			SELECT pg_terminate_backend(pg_stat_activity.pid)
			FROM pg_stat_activity
			WHERE pg_stat_activity.datname = 'defi_arbitrage_test'
			AND pid <> pg_backend_pid()
		`)

		// 删除数据库
		if err := db.Exec("DROP DATABASE IF EXISTS defi_arbitrage_test").Error; err != nil {
			log.Fatalf("删除数据库失败: %v", err)
		}
		fmt.Println("✅ 删除旧数据库成功")
	}

	// 创建新的测试数据库
	fmt.Println("正在创建测试数据库 'defi_arbitrage_test'...")
	if err := db.Exec("CREATE DATABASE defi_arbitrage_test WITH OWNER = defi_user ENCODING = 'UTF8'").Error; err != nil {
		log.Fatalf("创建数据库失败: %v", err)
	}

	fmt.Println("✅ 测试数据库创建成功！")
	fmt.Println("\n数据库信息:")
	fmt.Println("  - 名称: defi_arbitrage_test")
	fmt.Println("  - 主机: 127.0.0.1")
	fmt.Println("  - 端口: 5432")
	fmt.Println("  - 用户: defi_user")
	fmt.Println("\n下一步:")
	fmt.Println("  1. 运行迁移: go run cmd/server/main.go -config=configs/config.test.yaml -migrate")
	fmt.Println("  2. 初始化种子数据: go run cmd/server/main.go -config=configs/config.test.yaml -seed")
}

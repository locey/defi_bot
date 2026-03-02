package main

import (
	"context"
	"fmt"
	"log"

	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/internal/models"
	"github.com/redis/go-redis/v9"
)

func main() {
	fmt.Println("=== 清理测试数据 ===")

	// 1. 加载配置
	cfg, err := config.LoadConfig("configs/config.yaml")
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 2. 初始化数据库
	if err := database.InitDB(&cfg.Database); err != nil {
		log.Fatalf("数据库初始化失败: %v", err)
	}
	defer database.CloseDB()

	db := database.GetDB()

	// 3. 清理 pair_reserves 表
	result := db.Unscoped().Where("1 = 1").Delete(&models.PairReserve{})
	if result.Error != nil {
		log.Printf("清理 pair_reserves 失败: %v", result.Error)
	} else {
		log.Printf("已清理 pair_reserves: %d 条", result.RowsAffected)
	}

	// 4. 清理 price_records 表
	result = db.Unscoped().Where("1 = 1").Delete(&models.PriceRecord{})
	if result.Error != nil {
		log.Printf("清理 price_records 失败: %v", result.Error)
	} else {
		log.Printf("已清理 price_records: %d 条", result.RowsAffected)
	}

	// 5. 清理 Redis 缓存
	if cfg.Redis.Enabled {
		rdb := redis.NewClient(&redis.Options{
			Addr:     fmt.Sprintf("%s:%d", cfg.Redis.Host, cfg.Redis.Port),
			Password: cfg.Redis.Password,
			DB:       cfg.Redis.DB,
		})
		defer rdb.Close()

		ctx := context.Background()
		// 删除所有 price: 开头的缓存键
		keys, err := rdb.Keys(ctx, "price:*").Result()
		if err != nil {
			log.Printf("查询 Redis 缓存失败: %v", err)
		} else if len(keys) > 0 {
			deleted, err := rdb.Del(ctx, keys...).Result()
			if err != nil {
				log.Printf("清理 Redis 缓存失败: %v", err)
			} else {
				log.Printf("已清理 Redis 缓存: %d 个键", deleted)
			}
		} else {
			log.Printf("Redis 缓存为空")
		}
	}

	fmt.Println("\n✅ 测试数据清理完成！")
}

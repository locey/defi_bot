package main

import (
	"fmt"
	"log"
	"time"

	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/internal/models"
)

func main() {
	fmt.Println("=== 检查主网数据库的采集历史 ===")

	// 连接主网数据库
	cfg := &config.DatabaseConfig{
		Host:            "127.0.0.1",
		Port:            5432,
		User:            "defi_user",
		Password:        "defi_pass123",
		DBName:          "defi_arbitrage", // 主网数据库
		SSLMode:         "disable",
		Timezone:        "Asia/Shanghai",
		MaxIdleConns:    5,
		MaxOpenConns:    20,
		ConnMaxLifetime: 3600,
	}

	if err := database.InitDB(cfg); err != nil {
		log.Fatalf("数据库连接失败: %v", err)
	}
	defer database.CloseDB()

	db := database.GetDB()

	// 查看 pair_reserves 的时间分布
	var timeStats []struct {
		Date  string
		Count int64
	}
	db.Raw(`
		SELECT 
			DATE(timestamp) as date,
			COUNT(*) as count
		FROM pair_reserves
		GROUP BY DATE(timestamp)
		ORDER BY date DESC
		LIMIT 10
	`).Scan(&timeStats)

	fmt.Println("📊 pair_reserves 按日期统计:")
	fmt.Println("----------------------------------------")
	for _, s := range timeStats {
		fmt.Printf("  %s: %d 条记录\n", s.Date, s.Count)
	}

	// 查看第一次和最后一次采集的时间
	var firstTime, lastTime time.Time
	db.Model(&models.PairReserve{}).Select("MIN(timestamp)").Scan(&firstTime)
	db.Model(&models.PairReserve{}).Select("MAX(timestamp)").Scan(&lastTime)

	fmt.Printf("\n📅 时间范围:\n")
	fmt.Printf("  首次采集: %s\n", firstTime.Format("2006-01-02 15:04:05"))
	fmt.Printf("  最后采集: %s\n", lastTime.Format("2006-01-02 15:04:05"))
	fmt.Printf("  持续时间: %v\n", lastTime.Sub(firstTime))
	fmt.Printf("  距今: %v\n", time.Since(lastTime))

	// 查看每小时的记录数（看看采集是否连续）
	var hourlyStats []struct {
		Hour  string
		Count int64
	}
	db.Raw(`
		SELECT 
			TO_CHAR(timestamp, 'YYYY-MM-DD HH24:00') as hour,
			COUNT(*) as count
		FROM pair_reserves
		WHERE timestamp > (SELECT MAX(timestamp) FROM pair_reserves) - INTERVAL '6 hours'
		GROUP BY hour
		ORDER BY hour
	`).Scan(&hourlyStats)

	if len(hourlyStats) > 0 {
		fmt.Printf("\n📈 最后6小时的采集情况:\n")
		fmt.Println("----------------------------------------")
		for _, s := range hourlyStats {
			fmt.Printf("  %s: %d 条记录\n", s.Hour, s.Count)
		}
	}

	// 查看 gas_price_history（看看服务器运行了多久）
	var gasCount int64
	db.Model(&models.GasPriceHistory{}).Count(&gasCount)
	
	if gasCount > 0 {
		var firstGas, lastGas time.Time
		db.Model(&models.GasPriceHistory{}).Select("MIN(timestamp)").Scan(&firstGas)
		db.Model(&models.GasPriceHistory{}).Select("MAX(timestamp)").Scan(&lastGas)
		
		fmt.Printf("\n⛽ Gas 价格采集历史:\n")
		fmt.Printf("  总记录数: %d\n", gasCount)
		fmt.Printf("  首次: %s\n", firstGas.Format("2006-01-02 15:04:05"))
		fmt.Printf("  最后: %s\n", lastGas.Format("2006-01-02 15:04:05"))
		fmt.Printf("  持续时间: %v\n", lastGas.Sub(firstGas))
	}
}

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
	fmt.Println("=== 诊断 pair_reserves 表更新问题 ===")

	// 1. 加载配置
	cfg, err := config.LoadConfig("configs/config.test.yaml")
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	fmt.Printf("📋 配置信息:\n")
	fmt.Printf("   - 采集间隔: %d 秒\n", cfg.Scheduler.CollectInterval)
	fmt.Printf("   - 分析间隔: %d 秒\n", cfg.Scheduler.AnalyzeInterval)
	fmt.Printf("   - 数据库: %s:%d/%s\n\n", cfg.Database.Host, cfg.Database.Port, cfg.Database.DBName)

	// 2. 连接数据库
	if err := database.InitDB(&cfg.Database); err != nil {
		log.Fatalf("数据库连接失败: %v", err)
	}
	defer database.CloseDB()

	db := database.GetDB()

	// 3. 检查交易对数量
	var pairCount int64
	db.Model(&models.TradingPair{}).Where("is_active = ?", true).Count(&pairCount)
	fmt.Printf("📊 活跃交易对数量: %d\n\n", pairCount)

	// 4. 检查 pair_reserves 表的记录数
	var reserveCount int64
	db.Model(&models.PairReserve{}).Count(&reserveCount)
	fmt.Printf("📊 pair_reserves 表总记录数: %d\n\n", reserveCount)

	// 5. 查询最近的记录
	var recentReserves []models.PairReserve
	db.Order("timestamp DESC").Limit(10).Preload("Pair.Token0").Preload("Pair.Token1").Preload("Pair.Dex").Find(&recentReserves)

	if len(recentReserves) == 0 {
		fmt.Println("⚠️  没有找到任何储备量记录！")
		fmt.Println("\n可能的原因:")
		fmt.Println("1. 调度器未启动或未正常运行")
		fmt.Println("2. 采集过程中出现错误")
		fmt.Println("3. 没有活跃的交易对")
		return
	}

	fmt.Printf("📈 最近 %d 条记录:\n", len(recentReserves))
	fmt.Println("----------------------------------------")

	for i, r := range recentReserves {
		timeDiff := time.Since(r.Timestamp)
		fmt.Printf("%d. PairID: %d | %s/%s @ %s\n",
			i+1, r.PairID,
			r.Pair.Token0.Symbol, r.Pair.Token1.Symbol,
			r.Pair.Dex.Name)
		fmt.Printf("   时间: %s (距现在 %v)\n",
			r.Timestamp.Format("2006-01-02 15:04:05"),
			timeDiff)
		fmt.Printf("   Reserve0: %s\n", r.Reserve0[:min(20, len(r.Reserve0))]+"...")
		fmt.Printf("   Reserve1: %s\n", r.Reserve1[:min(20, len(r.Reserve1))]+"...")
		fmt.Println()
	}

	// 6. 检查时间分布
	var timeGroups []struct {
		Hour  int
		Count int64
	}
	db.Raw(`
		SELECT 
			EXTRACT(HOUR FROM timestamp) as hour,
			COUNT(*) as count
		FROM pair_reserves
		WHERE timestamp > NOW() - INTERVAL '24 hours'
		GROUP BY hour
		ORDER BY hour
	`).Scan(&timeGroups)

	if len(timeGroups) > 0 {
		fmt.Println("📊 最近24小时每小时记录数:")
		for _, g := range timeGroups {
			fmt.Printf("   %02d:00 - %d 条记录\n", g.Hour, g.Count)
		}
	}

	// 7. 检查是否只有第一次的数据
	var firstTimestamp, lastTimestamp time.Time
	db.Model(&models.PairReserve{}).Select("MIN(timestamp) as first, MAX(timestamp) as last").
		Row().Scan(&firstTimestamp, &lastTimestamp)

	fmt.Printf("\n📅 时间范围:\n")
	fmt.Printf("   最早记录: %s\n", firstTimestamp.Format("2006-01-02 15:04:05"))
	fmt.Printf("   最新记录: %s\n", lastTimestamp.Format("2006-01-02 15:04:05"))
	fmt.Printf("   时间跨度: %v\n", lastTimestamp.Sub(firstTimestamp))

	if lastTimestamp.Sub(firstTimestamp) < 1*time.Minute {
		fmt.Println("\n⚠️  警告: 所有记录都在1分钟内产生！")
		fmt.Println("   这说明调度器可能只运行了一次，之后就停止了。")
		fmt.Println("\n建议检查:")
		fmt.Println("   1. 调度器是否正常运行")
		fmt.Println("   2. 查看日志中是否有错误信息")
		fmt.Println("   3. 确认配置文件中的 collect_interval 设置")
	}

	// 8. 按交易对统计记录数
	var pairStats []struct {
		PairID uint
		Count  int64
	}
	db.Raw(`
		SELECT pair_id, COUNT(*) as count
		FROM pair_reserves
		GROUP BY pair_id
		ORDER BY count DESC
		LIMIT 10
	`).Scan(&pairStats)

	if len(pairStats) > 0 {
		fmt.Println("\n📊 各交易对记录数 (Top 10):")
		for i, s := range pairStats {
			fmt.Printf("   %d. PairID %d: %d 条记录\n", i+1, s.PairID, s.Count)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

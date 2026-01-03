// internal/api/handlers/stats.go
package handlers

import (
	"net/http"
	"time"

	"github.com/defi-bot/backend/internal/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// StatsResponse 统计响应
type StatsResponse struct {
	TotalOpportunities   int64   `json:"total_opportunities"`
	PendingOpportunities int64   `json:"pending_opportunities"`
	ExecutedCount        int64   `json:"executed_count"`
	SuccessCount         int64   `json:"success_count"`
	FailedCount          int64   `json:"failed_count"`
	SuccessRate          float64 `json:"success_rate"`
	TotalProfit          string  `json:"total_profit"`
	AvgProfitRate        float64 `json:"avg_profit_rate"`
	Last24hProfit        string  `json:"last_24h_profit"`
	Last24hExecutions    int64   `json:"last_24h_executions"`
	Last24hSuccessRate   float64 `json:"last_24h_success_rate"`
}

// GetStats 获取统计数据
func GetStats(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var stats StatsResponse

		// 总机会数
		db.Model(&models.ArbitrageOpportunity{}).Count(&stats.TotalOpportunities)

		// 待处理机会数
		db.Model(&models.ArbitrageOpportunity{}).
			Where("status = ? AND expires_at > ?", "pending", time.Now()).
			Count(&stats.PendingOpportunities)

		// 执行统计
		db.Model(&models.ArbitrageExecution{}).Count(&stats.ExecutedCount)
		db.Model(&models.ArbitrageExecution{}).Where("status = ?", "success").Count(&stats.SuccessCount)
		db.Model(&models.ArbitrageExecution{}).Where("status = ?", "failed").Count(&stats.FailedCount)

		// 成功率
		if stats.ExecutedCount > 0 {
			stats.SuccessRate = float64(stats.SuccessCount) / float64(stats.ExecutedCount) * 100
		}

		// 总利润
		var totalProfit struct {
			Sum *string
		}
		db.Model(&models.ArbitrageExecution{}).
			Select("COALESCE(SUM(CAST(actual_profit AS NUMERIC)), 0)::text as sum").
			Where("status = ?", "success").
			Scan(&totalProfit)
		if totalProfit.Sum != nil {
			stats.TotalProfit = *totalProfit.Sum
		} else {
			stats.TotalProfit = "0"
		}

		// 平均利润率
		db.Model(&models.ArbitrageExecution{}).
			Select("COALESCE(AVG(profit_rate), 0)").
			Where("status = ?", "success").
			Scan(&stats.AvgProfitRate)

		// 24小时数据
		yesterday := time.Now().Add(-24 * time.Hour)

		// 24小时执行数
		db.Model(&models.ArbitrageExecution{}).
			Where("timestamp > ?", yesterday).
			Count(&stats.Last24hExecutions)

		// 24小时成功数
		var last24hSuccess int64
		db.Model(&models.ArbitrageExecution{}).
			Where("timestamp > ? AND status = ?", yesterday, "success").
			Count(&last24hSuccess)

		// 24小时成功率
		if stats.Last24hExecutions > 0 {
			stats.Last24hSuccessRate = float64(last24hSuccess) / float64(stats.Last24hExecutions) * 100
		}

		// 24小时利润
		var last24hProfit struct {
			Sum *string
		}
		db.Model(&models.ArbitrageExecution{}).
			Select("COALESCE(SUM(CAST(actual_profit AS NUMERIC)), 0)::text as sum").
			Where("timestamp > ? AND status = ?", yesterday, "success").
			Scan(&last24hProfit)
		if last24hProfit.Sum != nil {
			stats.Last24hProfit = *last24hProfit.Sum
		} else {
			stats.Last24hProfit = "0"
		}

		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data":    stats,
		})
	}
}

// DailyStatsResponse 每日统计
type DailyStatsResponse struct {
	Date        string  `json:"date"`
	Executions  int64   `json:"executions"`
	SuccessRate float64 `json:"success_rate"`
	Profit      string  `json:"profit"`
	GasSpent    string  `json:"gas_spent"`
}

// GetDailyStats 获取每日统计数据
func GetDailyStats(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var results []DailyStatsResponse

		// 使用原生 SQL 进行日期分组统计
		// 注意：PostgreSQL 的 INTERVAL 不支持参数化，直接写入30天
		rows, err := db.Raw(`
			SELECT 
				DATE(timestamp) as date,
				COUNT(*) as executions,
				COALESCE(AVG(CASE WHEN status = 'success' THEN 1.0 ELSE 0.0 END) * 100, 0) as success_rate,
				COALESCE(SUM(CASE WHEN status = 'success' THEN CAST(actual_profit AS NUMERIC) ELSE 0 END), 0)::text as profit,
				COALESCE(SUM(CAST(gas_used AS NUMERIC) * CAST(gas_price AS NUMERIC)), 0)::text as gas_spent
			FROM arbitrage_executions
			WHERE timestamp > NOW() - INTERVAL '30 days'
			GROUP BY DATE(timestamp)
			ORDER BY date DESC
		`).Rows()

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"error":   err.Error(),
			})
			return
		}
		defer rows.Close()

		for rows.Next() {
			var stat DailyStatsResponse
			if err := rows.Scan(&stat.Date, &stat.Executions, &stat.SuccessRate, &stat.Profit, &stat.GasSpent); err != nil {
				continue
			}
			results = append(results, stat)
		}

		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data":    results,
			"count":   len(results),
		})
	}
}


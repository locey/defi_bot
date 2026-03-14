// internal/api/handlers/opportunities.go
package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/defi-bot/backend/internal/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// GetOpportunities 获取套利机会列表
func GetOpportunities(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var opps []models.ArbitrageOpportunity

		// 查询参数
		status := c.DefaultQuery("status", "pending")
		limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
		offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
		arbType := c.Query("type") // cross_dex, triangular, etc.

		// 构建查询
		query := db.Model(&models.ArbitrageOpportunity{})

		// 状态过滤
		if status != "" && status != "all" {
			query = query.Where("status = ?", status)
			// 只对 pending 状态过滤过期
			if status == "pending" {
				query = query.Where("expires_at > ?", time.Now())
			}
		}

		// 类型过滤
		if arbType != "" {
			query = query.Where("arbitrage_type = ?", arbType)
		}

		// 获取总数
		var total int64
		query.Count(&total)

		// 按创建时间降序，分页
		if err := query.Order("created_at DESC").
			Offset(offset).Limit(limit).
			Preload("TokenIn").Preload("TokenOut").
			Find(&opps).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"error":   err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data":    opps,
			"count":   len(opps),
			"total":   total,
			"offset":  offset,
			"limit":   limit,
		})
	}
}

// GetOpportunityByID 获取单个套利机会
func GetOpportunityByID(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")

		var opp models.ArbitrageOpportunity
		if err := db.Preload("TokenIn").Preload("TokenOut").
			First(&opp, id).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{
				"success": false,
				"error":   "Opportunity not found",
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data":    opp,
		})
	}
}


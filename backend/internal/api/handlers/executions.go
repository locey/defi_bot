// internal/api/handlers/executions.go
package handlers

import (
	"net/http"
	"strconv"

	"github.com/defi-bot/backend/internal/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// GetExecutions 获取执行记录列表
func GetExecutions(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var executions []models.ArbitrageExecution

		// 查询参数
		status := c.Query("status") // success, failed, pending
		limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
		offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

		// 构建查询
		query := db.Model(&models.ArbitrageExecution{})

		// 状态过滤
		if status != "" && status != "all" {
			query = query.Where("status = ?", status)
		}

		// 获取总数
		var total int64
		query.Count(&total)

		// 按时间降序，分页
		if err := query.Order("timestamp DESC").
			Offset(offset).Limit(limit).
			Preload("TokenIn").Preload("TokenOut").
			Find(&executions).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"error":   err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data":    executions,
			"count":   len(executions),
			"total":   total,
			"offset":  offset,
			"limit":   limit,
		})
	}
}

// GetExecutionByID 获取单个执行记录
func GetExecutionByID(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")

		var execution models.ArbitrageExecution
		if err := db.Preload("TokenIn").Preload("TokenOut").Preload("Opportunity").
			First(&execution, id).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{
				"success": false,
				"error":   "Execution record not found",
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data":    execution,
		})
	}
}

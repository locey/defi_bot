// internal/api/handlers/tokens.go
package handlers

import (
	"net/http"

	"github.com/defi-bot/backend/internal/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// GetTokens 获取代币列表
func GetTokens(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var tokens []models.Token

		// 查询参数
		active := c.DefaultQuery("active", "true")

		// 构建查询
		query := db.Model(&models.Token{})

		if active == "true" {
			query = query.Where("is_active = ?", true)
		}

		if err := query.Order("symbol ASC").Find(&tokens).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"error":   err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data":    tokens,
			"count":   len(tokens),
		})
	}
}


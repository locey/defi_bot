// internal/api/api.go
package api

import (
	"log"
	"net/http"

	"github.com/defi-bot/backend/internal/api/handlers"
	"github.com/defi-bot/backend/internal/api/middleware"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// APIServer API 服务器
type APIServer struct {
	router *gin.Engine
	db     *gorm.DB
}

// NewAPIServer 创建 API 服务器
func NewAPIServer(db *gorm.DB) *APIServer {
	// 设置 Gin 模式
	gin.SetMode(gin.ReleaseMode)

	router := gin.New()

	// 中间件
	router.Use(gin.Recovery())
	router.Use(middleware.Logger())
	router.Use(middleware.CORS())

	server := &APIServer{
		router: router,
		db:     db,
	}

	server.setupRoutes()
	return server
}

// setupRoutes 设置路由
func (s *APIServer) setupRoutes() {
	// 健康检查
	s.router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"service": "defi-bot-api",
		})
	})

	// API v1 路由组
	v1 := s.router.Group("/api/v1")
	{
		// 套利机会
		v1.GET("/opportunities", handlers.GetOpportunities(s.db))
		v1.GET("/opportunities/:id", handlers.GetOpportunityByID(s.db))

		// 执行记录
		v1.GET("/executions", handlers.GetExecutions(s.db))
		v1.GET("/executions/:id", handlers.GetExecutionByID(s.db))

		// 统计数据
		v1.GET("/stats", handlers.GetStats(s.db))
		v1.GET("/stats/daily", handlers.GetDailyStats(s.db))

		// 金库
		v1.GET("/vault/:address", handlers.GetVaultInfo(s.db))

		// 代币
		v1.GET("/tokens", handlers.GetTokens(s.db))
	}
}

// Run 启动服务器
func (s *APIServer) Run(addr string) error {
	log.Printf("🚀 API server starting on %s", addr)
	return s.router.Run(addr)
}

// GetRouter 获取路由器（用于测试）
func (s *APIServer) GetRouter() *gin.Engine {
	return s.router
}


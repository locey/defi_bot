// internal/api/middleware/middleware.go
package middleware

import (
	"log"
	"time"

	"github.com/gin-gonic/gin"
)

// Logger 日志中间件
func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery

		// 处理请求
		c.Next()

		// 计算延迟
		latency := time.Since(start)

		// 记录日志
		clientIP := c.ClientIP()
		method := c.Request.Method
		statusCode := c.Writer.Status()

		if raw != "" {
			path = path + "?" + raw
		}

		log.Printf("[API] %s | %3d | %13v | %15s | %-7s %s",
			time.Now().Format("2006/01/02 - 15:04:05"),
			statusCode,
			latency,
			clientIP,
			method,
			path,
		)
	}
}

// CORS 跨域中间件
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
		c.Header("Access-Control-Allow-Methods", "POST, HEAD, PATCH, OPTIONS, GET, PUT, DELETE")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

// RateLimit 速率限制中间件（简单实现）
func RateLimit(requestsPerMinute int) gin.HandlerFunc {
	// 简单的内存计数器，生产环境建议使用 Redis
	type clientInfo struct {
		count     int
		resetTime time.Time
	}
	clients := make(map[string]*clientInfo)

	return func(c *gin.Context) {
		clientIP := c.ClientIP()
		now := time.Now()

		info, exists := clients[clientIP]
		if !exists || now.After(info.resetTime) {
			clients[clientIP] = &clientInfo{
				count:     1,
				resetTime: now.Add(time.Minute),
			}
		} else {
			info.count++
			if info.count > requestsPerMinute {
				c.JSON(429, gin.H{
					"success": false,
					"error":   "Too many requests",
				})
				c.Abort()
				return
			}
		}

		c.Next()
	}
}

package cex

import (
	"sync"
	"time"
)

// RateLimiter 速率限制器（令牌桶算法）
type RateLimiter struct {
	limit      int           // 限制次数
	interval   time.Duration // 时间间隔
	tokens     int           // 当前令牌数
	lastRefill time.Time     // 上次补充时间
	mu         sync.Mutex
}

// NewRateLimiter 创建速率限制器
// limit: 限制次数，interval: 时间间隔
// 例如: NewRateLimiter(1200, time.Minute) 表示每分钟最多1200次请求
func NewRateLimiter(limit int, interval time.Duration) *RateLimiter {
	return &RateLimiter{
		limit:      limit,
		interval:   interval,
		tokens:     limit,
		lastRefill: time.Now(),
	}
}

// Wait 等待获取令牌（阻塞直到有可用令牌）
func (rl *RateLimiter) Wait() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// 自动补充令牌
	rl.refillTokens()

	// 如果没有令牌，等待
	for rl.tokens <= 0 {
		rl.mu.Unlock()
		time.Sleep(100 * time.Millisecond)
		rl.mu.Lock()

		rl.refillTokens()
	}

	// 消耗一个令牌
	rl.tokens--
}

// TryAcquire 尝试获取令牌（非阻塞）
// 返回：是否成功获取令牌
func (rl *RateLimiter) TryAcquire() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.refillTokens()

	if rl.tokens > 0 {
		rl.tokens--
		return true
	}

	return false
}

// refillTokens 补充令牌（内部方法，调用前需加锁）
func (rl *RateLimiter) refillTokens() {
	now := time.Now()
	elapsed := now.Sub(rl.lastRefill)

	// 如果已过一个周期，补充满
	if elapsed >= rl.interval {
		rl.tokens = rl.limit
		rl.lastRefill = now
	}
}

// GetAvailableTokens 获取当前可用令牌数
func (rl *RateLimiter) GetAvailableTokens() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.refillTokens()
	return rl.tokens
}

// Reset 重置速率限制器
func (rl *RateLimiter) Reset() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.tokens = rl.limit
	rl.lastRefill = time.Now()
}


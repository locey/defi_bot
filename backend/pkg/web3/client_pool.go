package web3

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// ClientPool RPC 客户端池
// 支持多个 RPC 节点的负载均衡和故障转移
type ClientPool struct {
	clients     []*Client
	currentIdx  int
	mu          sync.RWMutex
	healthCheck bool // 是否启用健康检查
	checkTicker *time.Ticker
	stopCh      chan struct{}
}

// ClientPoolConfig 客户端池配置
type ClientPoolConfig struct {
	RPCURLs       []string      // RPC 节点列表
	ChainID       int64         // 链 ID
	Timeout       int           // 超时时间（秒）
	HealthCheck   bool          // 是否启用健康检查
	CheckInterval time.Duration // 健康检查间隔
}

// NewClientPool 创建客户端池
func NewClientPool(config *ClientPoolConfig) (*ClientPool, error) {
	if len(config.RPCURLs) == 0 {
		return nil, fmt.Errorf("至少需要一个 RPC URL")
	}

	pool := &ClientPool{
		clients:     make([]*Client, 0, len(config.RPCURLs)),
		currentIdx:  0,
		healthCheck: config.HealthCheck,
		stopCh:      make(chan struct{}),
	}

	// 创建所有客户端
	for i, rpcURL := range config.RPCURLs {
		client, err := NewClient(rpcURL, config.ChainID, config.Timeout)
		if err != nil {
			log.Printf("⚠️  创建客户端失败 [%d/%d] %s: %v", i+1, len(config.RPCURLs), rpcURL, err)
			continue
		}
		pool.clients = append(pool.clients, client)
		log.Printf("✅ 添加 RPC 节点 [%d/%d]: %s", i+1, len(config.RPCURLs), rpcURL)
	}

	if len(pool.clients) == 0 {
		return nil, fmt.Errorf("没有可用的 RPC 客户端")
	}

	// 启动健康检查
	if config.HealthCheck && config.CheckInterval > 0 {
		pool.checkTicker = time.NewTicker(config.CheckInterval)
		go pool.startHealthCheck()
	}

	log.Printf("✅ RPC 客户端池创建成功，共 %d 个节点", len(pool.clients))
	return pool, nil
}

// GetClient 获取一个可用的客户端（轮询）
func (p *ClientPool) GetClient() *Client {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.clients) == 0 {
		return nil
	}

	// 轮询获取下一个客户端
	client := p.clients[p.currentIdx]
	p.currentIdx = (p.currentIdx + 1) % len(p.clients)

	return client
}

// GetClientWithRetry 获取客户端并自动重试
// 如果当前客户端失败，会尝试下一个，直到所有客户端都尝试过
func (p *ClientPool) GetClientWithRetry(ctx context.Context, maxRetries int) (*Client, error) {
	p.mu.RLock()
	clientCount := len(p.clients)
	p.mu.RUnlock()

	if clientCount == 0 {
		return nil, fmt.Errorf("没有可用的 RPC 客户端")
	}

	// 限制重试次数不超过客户端数量
	if maxRetries > clientCount {
		maxRetries = clientCount
	}

	var lastErr error
	for i := 0; i < maxRetries; i++ {
		client := p.GetClient()
		if client == nil {
			return nil, fmt.Errorf("无法获取客户端")
		}

		// 测试客户端是否可用（获取区块号）
		_, err := client.GetBlockNumber()
		if err == nil {
			return client, nil
		}

		lastErr = err
		log.Printf("⚠️  客户端不可用，尝试下一个 [%d/%d]: %v", i+1, maxRetries, err)
	}

	return nil, fmt.Errorf("所有客户端都不可用，最后错误: %w", lastErr)
}

// ExecuteWithRetry 使用客户端池执行操作，自动重试
func (p *ClientPool) ExecuteWithRetry(ctx context.Context, operation func(*Client) error, maxRetries int) error {
	p.mu.RLock()
	clientCount := len(p.clients)
	p.mu.RUnlock()

	if clientCount == 0 {
		return fmt.Errorf("没有可用的 RPC 客户端")
	}

	// 限制重试次数
	if maxRetries > clientCount {
		maxRetries = clientCount
	}

	var lastErr error
	for i := 0; i < maxRetries; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		client := p.GetClient()
		if client == nil {
			return fmt.Errorf("无法获取客户端")
		}

		err := operation(client)
		if err == nil {
			return nil
		}

		lastErr = err
		log.Printf("⚠️  操作失败，尝试下一个客户端 [%d/%d]: %v", i+1, maxRetries, err)
		time.Sleep(time.Millisecond * 100) // 短暂延迟
	}

	return fmt.Errorf("所有客户端都失败，最后错误: %w", lastErr)
}

// startHealthCheck 启动健康检查
func (p *ClientPool) startHealthCheck() {
	log.Println("启动 RPC 节点健康检查...")

	for {
		select {
		case <-p.checkTicker.C:
			p.checkHealth()
		case <-p.stopCh:
			p.checkTicker.Stop()
			return
		}
	}
}

// checkHealth 检查所有客户端的健康状态
func (p *ClientPool) checkHealth() {
	p.mu.RLock()
	clients := make([]*Client, len(p.clients))
	copy(clients, p.clients)
	p.mu.RUnlock()

	healthyCount := 0
	for i, client := range clients {
		_, err := client.GetBlockNumber()
		if err == nil {
			healthyCount++
		} else {
			log.Printf("⚠️  RPC 节点 [%d/%d] 不健康: %v", i+1, len(clients), err)
		}
	}

	if healthyCount == 0 {
		log.Println("❌ 所有 RPC 节点都不可用！")
	} else {
		log.Printf("✅ RPC 节点健康检查: %d/%d 节点可用", healthyCount, len(clients))
	}
}

// GetClientCount 获取客户端数量
func (p *ClientPool) GetClientCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.clients)
}

// Close 关闭客户端池
func (p *ClientPool) Close() {
	// 停止健康检查
	if p.checkTicker != nil {
		close(p.stopCh)
	}

	// 关闭所有客户端
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, client := range p.clients {
		client.Close()
	}
	p.clients = nil

	log.Println("RPC 客户端池已关闭")
}

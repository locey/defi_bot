// Package collector 提供数据采集功能
package collector

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/defi-bot/backend/pkg/log"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

// BlockHandler 区块处理函数
type BlockHandler func(block *types.Block)

// BlockSubscriberConfig 区块订阅器配置
type BlockSubscriberConfig struct {
	ReconnectDelay  time.Duration // 重连延迟
	ProcessTimeout  time.Duration // 处理超时
	MaxHandlers     int           // 最大处理器数量
}

// DefaultBlockSubscriberConfig 默认配置
func DefaultBlockSubscriberConfig() *BlockSubscriberConfig {
	return &BlockSubscriberConfig{
		ReconnectDelay: 5 * time.Second,
		ProcessTimeout: 10 * time.Second,
		MaxHandlers:    10,
	}
}

// BlockSubscriber 区块订阅器
// 监听新区块，触发数据更新
type BlockSubscriber struct {
	wsURL       string
	client      *ethclient.Client
	config      *BlockSubscriberConfig
	handlers    []BlockHandler
	handlersMu  sync.RWMutex
	latestBlock uint64
	running     bool
	runningMu   sync.RWMutex
	cancelFunc  context.CancelFunc
}

// NewBlockSubscriber 创建区块订阅器
func NewBlockSubscriber(wsURL string, config *BlockSubscriberConfig) *BlockSubscriber {
	if config == nil {
		config = DefaultBlockSubscriberConfig()
	}

	return &BlockSubscriber{
		wsURL:    wsURL,
		config:   config,
		handlers: make([]BlockHandler, 0),
	}
}

// Start 启动区块订阅
func (s *BlockSubscriber) Start(ctx context.Context) error {
	s.runningMu.Lock()
	if s.running {
		s.runningMu.Unlock()
		return nil
	}
	s.running = true
	s.runningMu.Unlock()

	// 创建取消上下文
	ctx, cancel := context.WithCancel(ctx)
	s.cancelFunc = cancel

	// 连接 WebSocket
	if err := s.connect(ctx); err != nil {
		return err
	}

	// 启动订阅
	go s.subscribeLoop(ctx)

	log.Info("区块订阅已启动: %s", s.wsURL)
	return nil
}

// Stop 停止区块订阅
func (s *BlockSubscriber) Stop() {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()

	if !s.running {
		return
	}

	s.running = false
	if s.cancelFunc != nil {
		s.cancelFunc()
	}
	if s.client != nil {
		s.client.Close()
	}

	log.Info("区块订阅已停止")
}

// connect 连接 WebSocket
func (s *BlockSubscriber) connect(ctx context.Context) error {
	client, err := ethclient.DialContext(ctx, s.wsURL)
	if err != nil {
		return fmt.Errorf("连接 WebSocket 失败: %w", err)
	}
	s.client = client
	return nil
}

// subscribeLoop 订阅循环
func (s *BlockSubscriber) subscribeLoop(ctx context.Context) {
	for {
		s.runningMu.RLock()
		running := s.running
		s.runningMu.RUnlock()

		if !running {
			return
		}

		if err := s.subscribe(ctx); err != nil {
			log.Warn("区块订阅失败: %v, %v 后重试", err, s.config.ReconnectDelay)
			time.Sleep(s.config.ReconnectDelay)

			// 重新连接
			if s.client != nil {
				s.client.Close()
			}
			if err := s.connect(ctx); err != nil {
				log.Warn("重新连接失败: %v", err)
				continue
			}
		}
	}
}

// subscribe 订阅新区块头
func (s *BlockSubscriber) subscribe(ctx context.Context) error {
	headers := make(chan *types.Header)

	sub, err := s.client.SubscribeNewHead(ctx, headers)
	if err != nil {
		return fmt.Errorf("订阅区块头失败: %w", err)
	}

	log.Info("已订阅新区块")

	for {
		select {
		case <-ctx.Done():
			sub.Unsubscribe()
			return ctx.Err()

		case err := <-sub.Err():
			return fmt.Errorf("订阅错误: %w", err)

		case header := <-headers:
			s.handleNewBlock(ctx, header)
		}
	}
}

// handleNewBlock 处理新区块
func (s *BlockSubscriber) handleNewBlock(ctx context.Context, header *types.Header) {
	blockNum := header.Number.Uint64()
	s.latestBlock = blockNum

	log.Info("新区块: #%d, 时间: %s", blockNum, time.Unix(int64(header.Time), 0).Format(time.RFC3339))

	// 获取完整区块（如果有处理器需要）
	s.handlersMu.RLock()
	handlers := make([]BlockHandler, len(s.handlers))
	copy(handlers, s.handlers)
	s.handlersMu.RUnlock()

	if len(handlers) == 0 {
		return
	}

	// 获取完整区块
	block, err := s.client.BlockByHash(ctx, header.Hash())
	if err != nil {
		log.Warn("获取区块 #%d 失败: %v", blockNum, err)
		return
	}

	// 并发触发处理器
	var wg sync.WaitGroup
	for _, handler := range handlers {
		wg.Add(1)
		go func(h BlockHandler) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					log.Warn("区块处理器 panic: %v", r)
				}
			}()

			// 带超时的处理
			done := make(chan struct{})
			go func() {
				h(block)
				close(done)
			}()

			select {
			case <-done:
			case <-time.After(s.config.ProcessTimeout):
				log.Warn("区块处理器超时")
			}
		}(handler)
	}

	wg.Wait()
}

// AddHandler 添加区块处理器
func (s *BlockSubscriber) AddHandler(handler BlockHandler) error {
	s.handlersMu.Lock()
	defer s.handlersMu.Unlock()

	if len(s.handlers) >= s.config.MaxHandlers {
		return fmt.Errorf("已达到最大处理器数量 %d", s.config.MaxHandlers)
	}

	s.handlers = append(s.handlers, handler)
	log.Info("添加区块处理器，当前数量: %d", len(s.handlers))
	return nil
}

// GetLatestBlock 获取最新区块号
func (s *BlockSubscriber) GetLatestBlock() uint64 {
	return s.latestBlock
}

// IsRunning 是否正在运行
func (s *BlockSubscriber) IsRunning() bool {
	s.runningMu.RLock()
	defer s.runningMu.RUnlock()
	return s.running
}

// GetHandlerCount 获取处理器数量
func (s *BlockSubscriber) GetHandlerCount() int {
	s.handlersMu.RLock()
	defer s.handlersMu.RUnlock()
	return len(s.handlers)
}

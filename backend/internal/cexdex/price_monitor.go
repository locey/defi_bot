// Package cexdex 提供 CEX-DEX 套利功能
package cexdex

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"sync"
	"time"

	"github.com/defi-bot/backend/pkg/log"
	"github.com/gorilla/websocket"
)

// PriceMonitorConfig 价格监控器配置
type PriceMonitorConfig struct {
	// Binance WebSocket
	BinanceWSURL string
	Symbols      []string // 要监控的交易对，如 ["ETHUSDT", "BTCUSDT"]

	// 重连配置
	ReconnectDelay time.Duration
	PingInterval   time.Duration
	BufferSize     int
}

// DefaultPriceMonitorConfig 默认配置
func DefaultPriceMonitorConfig() *PriceMonitorConfig {
	return &PriceMonitorConfig{
		BinanceWSURL:   "wss://stream.binance.com:9443/ws",
		Symbols:        []string{"ethusdt", "btcusdt"},
		ReconnectDelay: 5 * time.Second,
		PingInterval:   30 * time.Second,
		BufferSize:     1000,
	}
}

// CEXPrice CEX 价格数据
type CEXPrice struct {
	Symbol     string    `json:"symbol"`
	BidPrice   float64   `json:"bid_price"`   // 买一价
	BidQty     float64   `json:"bid_qty"`     // 买一量
	AskPrice   float64   `json:"ask_price"`   // 卖一价
	AskQty     float64   `json:"ask_qty"`     // 卖一量
	LastPrice  float64   `json:"last_price"`  // 最新成交价
	Volume24h  float64   `json:"volume_24h"`  // 24h 成交量
	Exchange   string    `json:"exchange"`    // 交易所名称
	Timestamp  time.Time `json:"timestamp"`
}

// PriceMonitor CEX 价格监控器
type PriceMonitor struct {
	config     *PriceMonitorConfig
	conn       *websocket.Conn
	priceCh    chan *CEXPrice
	prices     map[string]*CEXPrice // symbol -> price
	pricesMu   sync.RWMutex
	running    bool
	runningMu  sync.RWMutex
	cancelFunc context.CancelFunc
}

// NewPriceMonitor 创建价格监控器
func NewPriceMonitor(config *PriceMonitorConfig) *PriceMonitor {
	if config == nil {
		config = DefaultPriceMonitorConfig()
	}

	return &PriceMonitor{
		config:  config,
		priceCh: make(chan *CEXPrice, config.BufferSize),
		prices:  make(map[string]*CEXPrice),
	}
}

// Start 启动价格监控
func (m *PriceMonitor) Start(ctx context.Context) error {
	m.runningMu.Lock()
	if m.running {
		m.runningMu.Unlock()
		return nil
	}
	m.running = true
	m.runningMu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	m.cancelFunc = cancel

	// 连接 WebSocket
	if err := m.connect(); err != nil {
		return err
	}

	// 订阅交易对
	if err := m.subscribe(); err != nil {
		return err
	}

	// 启动接收循环
	go m.receiveLoop(ctx)

	// 启动 Ping 循环
	go m.pingLoop(ctx)

	log.Info("CEX 价格监控已启动，监控 %d 个交易对", len(m.config.Symbols))
	return nil
}

// Stop 停止价格监控
func (m *PriceMonitor) Stop() {
	m.runningMu.Lock()
	defer m.runningMu.Unlock()

	if !m.running {
		return
	}

	m.running = false
	if m.cancelFunc != nil {
		m.cancelFunc()
	}
	if m.conn != nil {
		m.conn.Close()
	}

	log.Info("CEX 价格监控已停止")
}

// connect 连接 WebSocket
func (m *PriceMonitor) connect() error {
	u, err := url.Parse(m.config.BinanceWSURL)
	if err != nil {
		return fmt.Errorf("解析 WebSocket URL 失败: %w", err)
	}

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return fmt.Errorf("连接 Binance WebSocket 失败: %w", err)
	}

	m.conn = conn
	return nil
}

// subscribe 订阅交易对
func (m *PriceMonitor) subscribe() error {
	// 构建订阅消息
	// 订阅 bookTicker（最优买卖报价）
	streams := make([]string, len(m.config.Symbols))
	for i, symbol := range m.config.Symbols {
		streams[i] = symbol + "@bookTicker"
	}

	subscribeMsg := map[string]interface{}{
		"method": "SUBSCRIBE",
		"params": streams,
		"id":     1,
	}

	if err := m.conn.WriteJSON(subscribeMsg); err != nil {
		return fmt.Errorf("发送订阅消息失败: %w", err)
	}

	log.Info("已订阅 Binance 交易对: %v", m.config.Symbols)
	return nil
}

// receiveLoop 接收循环
func (m *PriceMonitor) receiveLoop(ctx context.Context) {
	for {
		m.runningMu.RLock()
		running := m.running
		m.runningMu.RUnlock()

		if !running {
			return
		}

		select {
		case <-ctx.Done():
			return
		default:
		}

		_, message, err := m.conn.ReadMessage()
		if err != nil {
			log.Warn("读取 WebSocket 消息失败: %v", err)
			m.reconnect(ctx)
			continue
		}

		m.handleMessage(message)
	}
}

// handleMessage 处理消息
func (m *PriceMonitor) handleMessage(message []byte) {
	// Binance bookTicker 消息格式
	var ticker struct {
		Symbol   string `json:"s"`  // 交易对
		BidPrice string `json:"b"`  // 买一价
		BidQty   string `json:"B"`  // 买一量
		AskPrice string `json:"a"`  // 卖一价
		AskQty   string `json:"A"`  // 卖一量
	}

	if err := json.Unmarshal(message, &ticker); err != nil {
		// 可能是订阅响应或其他消息，忽略
		return
	}

	if ticker.Symbol == "" {
		return
	}

	// 解析价格
	bidPrice := parseFloat(ticker.BidPrice)
	bidQty := parseFloat(ticker.BidQty)
	askPrice := parseFloat(ticker.AskPrice)
	askQty := parseFloat(ticker.AskQty)

	price := &CEXPrice{
		Symbol:    ticker.Symbol,
		BidPrice:  bidPrice,
		BidQty:    bidQty,
		AskPrice:  askPrice,
		AskQty:    askQty,
		LastPrice: (bidPrice + askPrice) / 2,
		Exchange:  "Binance",
		Timestamp: time.Now(),
	}

	// 更新缓存
	m.pricesMu.Lock()
	m.prices[ticker.Symbol] = price
	m.pricesMu.Unlock()

	// 发送到通道
	select {
	case m.priceCh <- price:
	default:
		// 通道满，丢弃
	}
}

// pingLoop Ping 循环
func (m *PriceMonitor) pingLoop(ctx context.Context) {
	ticker := time.NewTicker(m.config.PingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if m.conn != nil {
				if err := m.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					log.Warn("发送 Ping 失败: %v", err)
				}
			}
		}
	}
}

// reconnect 重连
func (m *PriceMonitor) reconnect(ctx context.Context) {
	m.runningMu.RLock()
	running := m.running
	m.runningMu.RUnlock()

	if !running {
		return
	}

	log.Info("WebSocket 断开，%v 后重连", m.config.ReconnectDelay)
	time.Sleep(m.config.ReconnectDelay)

	if m.conn != nil {
		m.conn.Close()
	}

	if err := m.connect(); err != nil {
		log.Warn("重连失败: %v", err)
		return
	}

	if err := m.subscribe(); err != nil {
		log.Warn("重新订阅失败: %v", err)
	}
}

// GetPrice 获取指定交易对的最新价格
func (m *PriceMonitor) GetPrice(symbol string) *CEXPrice {
	m.pricesMu.RLock()
	defer m.pricesMu.RUnlock()
	return m.prices[symbol]
}

// GetAllPrices 获取所有价格
func (m *PriceMonitor) GetAllPrices() map[string]*CEXPrice {
	m.pricesMu.RLock()
	defer m.pricesMu.RUnlock()

	result := make(map[string]*CEXPrice)
	for k, v := range m.prices {
		result[k] = v
	}
	return result
}

// UpdatePrice 手动更新价格（用于 REST API 模式）
func (m *PriceMonitor) UpdatePrice(price *CEXPrice) {
	if price == nil || price.Symbol == "" {
		return
	}

	// 更新缓存
	m.pricesMu.Lock()
	m.prices[price.Symbol] = price
	m.pricesMu.Unlock()

	// 发送到通道
	select {
	case m.priceCh <- price:
	default:
		// 通道满，丢弃
	}
}

// GetPriceChan 获取价格更新通道
func (m *PriceMonitor) GetPriceChan() <-chan *CEXPrice {
	return m.priceCh
}

// IsRunning 是否正在运行
func (m *PriceMonitor) IsRunning() bool {
	m.runningMu.RLock()
	defer m.runningMu.RUnlock()
	return m.running
}

// parseFloat 解析字符串为浮点数
func parseFloat(s string) float64 {
	f, _ := new(big.Float).SetString(s)
	if f == nil {
		return 0
	}
	result, _ := f.Float64()
	return result
}

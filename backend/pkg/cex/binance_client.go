package cex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// BinanceClient 币安客户端
type BinanceClient struct {
	httpClient  *http.Client
	apiEndpoint string // https://api.binance.com
	wsEndpoint  string // wss://stream.binance.com:9443
	apiKey      string // API Key（可选，读取公开数据不需要）
	apiSecret   string // API Secret（可选）
	rateLimit   *RateLimiter
}

// NewBinanceClient 创建币安客户端
func NewBinanceClient(config *BinanceConfig) *BinanceClient {
	// 默认配置
	if config.APIEndpoint == "" {
		config.APIEndpoint = "https://api.binance.com"
	}
	if config.WSEndpoint == "" {
		config.WSEndpoint = "wss://stream.binance.com:9443"
	}
	if config.RateLimit == 0 {
		config.RateLimit = 1200 // 默认每分钟1200次
	}

	return &BinanceClient{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		apiEndpoint: config.APIEndpoint,
		wsEndpoint:  config.WSEndpoint,
		apiKey:      config.APIKey,
		apiSecret:   config.APISecret,
		rateLimit:   NewRateLimiter(config.RateLimit, time.Minute),
	}
}

// GetTicker 获取24小时行情
// API: GET /api/v3/ticker/24hr
func (c *BinanceClient) GetTicker(symbol string) (*Ticker, error) {
	url := fmt.Sprintf("%s/api/v3/ticker/24hr?symbol=%s", c.apiEndpoint, symbol)

	// 等待速率限制
	c.rateLimit.Wait()

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API错误 (%d): %s", resp.StatusCode, string(body))
	}

	var ticker Ticker
	if err := json.NewDecoder(resp.Body).Decode(&ticker); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	return &ticker, nil
}

// GetOrderBook 获取订单簿
// API: GET /api/v3/depth
// limit: 5, 10, 20, 50, 100, 500, 1000, 5000
func (c *BinanceClient) GetOrderBook(symbol string, limit int) (*OrderBookResponse, error) {
	// 限制 limit 参数
	validLimits := []int{5, 10, 20, 50, 100, 500, 1000, 5000}
	if !contains(validLimits, limit) {
		limit = 20 // 默认20档
	}

	url := fmt.Sprintf("%s/api/v3/depth?symbol=%s&limit=%d",
		c.apiEndpoint, symbol, limit)

	c.rateLimit.Wait()

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API错误 (%d): %s", resp.StatusCode, string(body))
	}

	var orderBook OrderBookResponse
	if err := json.NewDecoder(resp.Body).Decode(&orderBook); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	return &orderBook, nil
}

// GetPrice 获取当前价格（快速方法）
// API: GET /api/v3/ticker/price
func (c *BinanceClient) GetPrice(symbol string) (string, error) {
	url := fmt.Sprintf("%s/api/v3/ticker/price?symbol=%s", c.apiEndpoint, symbol)

	c.rateLimit.Wait()

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API错误 (%d): %s", resp.StatusCode, string(body))
	}

	var priceResp PriceData
	if err := json.NewDecoder(resp.Body).Decode(&priceResp); err != nil {
		return "", fmt.Errorf("解析响应失败: %w", err)
	}

	return priceResp.Price, nil
}

// GetExchangeInfo 获取交易所信息（用于验证交易对）
// API: GET /api/v3/exchangeInfo
func (c *BinanceClient) GetExchangeInfo() (*ExchangeInfo, error) {
	url := fmt.Sprintf("%s/api/v3/exchangeInfo", c.apiEndpoint)

	c.rateLimit.Wait()

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API错误 (%d): %s", resp.StatusCode, string(body))
	}

	var info ExchangeInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	return &info, nil
}

// SubscribeTicker 订阅实时行情（WebSocket）
// 订阅多个交易对的实时更新
func (c *BinanceClient) SubscribeTicker(
	ctx context.Context,
	symbols []string,
	callback func(*TickerUpdate),
) error {

	// 构建 WebSocket URL
	// wss://stream.binance.com:9443/stream?streams=ethusdt@ticker/btcusdt@ticker
	streams := make([]string, len(symbols))
	for i, symbol := range symbols {
		streams[i] = fmt.Sprintf("%s@ticker", strings.ToLower(symbol))
	}

	wsURL := fmt.Sprintf("%s/stream?streams=%s",
		c.wsEndpoint, strings.Join(streams, "/"))

	// 连接 WebSocket
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("WebSocket连接失败: %w", err)
	}
	defer conn.Close()

	// 设置ping/pong处理（保持连接）
	conn.SetPingHandler(func(appData string) error {
		return conn.WriteControl(websocket.PongMessage, []byte{}, time.Now().Add(time.Second))
	})

	// 接收消息循环
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			// 设置读取超时
			conn.SetReadDeadline(time.Now().Add(60 * time.Second))

			var msg struct {
				Stream string       `json:"stream"`
				Data   TickerUpdate `json:"data"`
			}

			if err := conn.ReadJSON(&msg); err != nil {
				// 检查是否是正常关闭
				if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
					return nil
				}
				return fmt.Errorf("读取消息失败: %w", err)
			}

			// 回调处理
			callback(&msg.Data)
		}
	}
}

// Ping 测试连接
func (c *BinanceClient) Ping() error {
	url := fmt.Sprintf("%s/api/v3/ping", c.apiEndpoint)

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return fmt.Errorf("ping失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ping失败: status %d", resp.StatusCode)
	}

	return nil
}

// GetServerTime 获取服务器时间
func (c *BinanceClient) GetServerTime() (int64, error) {
	url := fmt.Sprintf("%s/api/v3/time", c.apiEndpoint)

	c.rateLimit.Wait()

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return 0, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	var timeResp struct {
		ServerTime int64 `json:"serverTime"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&timeResp); err != nil {
		return 0, fmt.Errorf("解析响应失败: %w", err)
	}

	return timeResp.ServerTime, nil
}

// 辅助函数

// contains 检查数组是否包含元素
func contains(arr []int, val int) bool {
	for _, v := range arr {
		if v == val {
			return true
		}
	}
	return false
}

// ParseSymbol 解析币安交易对符号
// 例如: "ETHUSDT" → base="ETH", quote="USDT"
func ParseSymbol(symbol string) (string, string) {
	// 常见的报价资产
	quotes := []string{"USDT", "USDC", "BUSD", "BTC", "ETH", "BNB", "DAI"}

	for _, quote := range quotes {
		if strings.HasSuffix(symbol, quote) {
			base := strings.TrimSuffix(symbol, quote)
			return base, quote
		}
	}

	// 默认：最后4个字符是报价资产
	if len(symbol) > 4 {
		return symbol[:len(symbol)-4], symbol[len(symbol)-4:]
	}

	return symbol, ""
}

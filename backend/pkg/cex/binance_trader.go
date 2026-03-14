// pkg/cex/binance_trader.go
// Binance Spot Trading API — 市价买卖、余额查询、订单状态
package cex

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// BinanceTrader 币安交易客户端（需要 Spot Trading 权限的 API Key）
type BinanceTrader struct {
	httpClient  *http.Client
	apiEndpoint string
	apiKey      string
	apiSecret   string
	rateLimit   *RateLimiter
}

// NewBinanceTrader 创建交易客户端
func NewBinanceTrader(config *BinanceConfig) *BinanceTrader {
	if config.APIEndpoint == "" {
		config.APIEndpoint = "https://api.binance.com"
	}
	if config.RateLimit == 0 {
		config.RateLimit = 1200
	}

	return &BinanceTrader{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		apiEndpoint: config.APIEndpoint,
		apiKey:      config.APIKey,
		apiSecret:   config.APISecret,
		rateLimit:   NewRateLimiter(config.RateLimit, time.Minute),
	}
}

// OrderResult 下单结果
type OrderResult struct {
	Symbol              string `json:"symbol"`
	OrderID             int64  `json:"orderId"`
	ClientOrderID       string `json:"clientOrderId"`
	TransactTime        int64  `json:"transactTime"`
	Price               string `json:"price"`
	OrigQty             string `json:"origQty"`
	ExecutedQty         string `json:"executedQty"`
	CumulativeQuoteQty  string `json:"cummulativeQuoteQty"`
	Status              string `json:"status"`   // NEW, FILLED, PARTIALLY_FILLED, CANCELED
	Type                string `json:"type"`     // MARKET, LIMIT
	Side                string `json:"side"`     // BUY, SELL
	Fills               []Fill `json:"fills"`
}

// Fill 成交明细
type Fill struct {
	Price           string `json:"price"`
	Qty             string `json:"qty"`
	Commission      string `json:"commission"`
	CommissionAsset string `json:"commissionAsset"`
}

// BalanceInfo 余额信息
type BalanceInfo struct {
	Asset  string `json:"asset"`
	Free   string `json:"free"`   // 可用余额
	Locked string `json:"locked"` // 冻结余额
}

// AccountInfo 账户信息
type AccountInfo struct {
	MakerCommission  int           `json:"makerCommission"`
	TakerCommission  int           `json:"takerCommission"`
	CanTrade         bool          `json:"canTrade"`
	CanWithdraw      bool          `json:"canWithdraw"`
	CanDeposit       bool          `json:"canDeposit"`
	Balances         []BalanceInfo `json:"balances"`
}

// MarketBuy 市价买入
func (t *BinanceTrader) MarketBuy(symbol string, quoteQty string) (*OrderResult, error) {
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("side", "BUY")
	params.Set("type", "MARKET")
	params.Set("quoteOrderQty", quoteQty) // 按报价币金额买入（如花 100 USDT 买 ETH）

	return t.placeOrder(params)
}

// MarketBuyBase 市价买入（按基础币数量）
func (t *BinanceTrader) MarketBuyBase(symbol string, qty string) (*OrderResult, error) {
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("side", "BUY")
	params.Set("type", "MARKET")
	params.Set("quantity", qty) // 按基础币数量买入（如买 0.1 ETH）

	return t.placeOrder(params)
}

// MarketSell 市价卖出
func (t *BinanceTrader) MarketSell(symbol string, qty string) (*OrderResult, error) {
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("side", "SELL")
	params.Set("type", "MARKET")
	params.Set("quantity", qty)

	return t.placeOrder(params)
}

// GetBalance 获取指定资产余额
func (t *BinanceTrader) GetBalance(asset string) (free float64, locked float64, err error) {
	account, err := t.GetAccountInfo()
	if err != nil {
		return 0, 0, err
	}

	for _, b := range account.Balances {
		if b.Asset == asset {
			free, _ = strconv.ParseFloat(b.Free, 64)
			locked, _ = strconv.ParseFloat(b.Locked, 64)
			return free, locked, nil
		}
	}

	return 0, 0, nil // 未找到该资产
}

// GetAccountInfo 获取账户信息
func (t *BinanceTrader) GetAccountInfo() (*AccountInfo, error) {
	params := url.Values{}

	body, err := t.signedRequest("GET", "/api/v3/account", params)
	if err != nil {
		return nil, err
	}

	var account AccountInfo
	if err := json.Unmarshal(body, &account); err != nil {
		return nil, fmt.Errorf("parse account info: %w", err)
	}

	return &account, nil
}

// GetOrderStatus 获取订单状态
func (t *BinanceTrader) GetOrderStatus(symbol string, orderID int64) (*OrderResult, error) {
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("orderId", strconv.FormatInt(orderID, 10))

	body, err := t.signedRequest("GET", "/api/v3/order", params)
	if err != nil {
		return nil, err
	}

	var order OrderResult
	if err := json.Unmarshal(body, &order); err != nil {
		return nil, fmt.Errorf("parse order: %w", err)
	}

	return &order, nil
}

// TestConnectivity 测试 API 连通性和交易权限
func (t *BinanceTrader) TestConnectivity() error {
	account, err := t.GetAccountInfo()
	if err != nil {
		return fmt.Errorf("API connectivity failed: %w", err)
	}

	if !account.CanTrade {
		return fmt.Errorf("account cannot trade (canTrade=false), check API key permissions")
	}

	return nil
}

// placeOrder 下单（内部方法）
func (t *BinanceTrader) placeOrder(params url.Values) (*OrderResult, error) {
	body, err := t.signedRequest("POST", "/api/v3/order", params)
	if err != nil {
		return nil, err
	}

	var order OrderResult
	if err := json.Unmarshal(body, &order); err != nil {
		return nil, fmt.Errorf("parse order result: %w", err)
	}

	return &order, nil
}

// signedRequest 发送签名请求（HMAC SHA256）
func (t *BinanceTrader) signedRequest(method, path string, params url.Values) ([]byte, error) {
	if t.apiKey == "" || t.apiSecret == "" {
		return nil, fmt.Errorf("API key and secret required for signed requests")
	}

	t.rateLimit.Wait()

	// 添加时间戳
	params.Set("timestamp", strconv.FormatInt(time.Now().UnixMilli(), 10))

	// HMAC SHA256 签名
	queryString := params.Encode()
	mac := hmac.New(sha256.New, []byte(t.apiSecret))
	mac.Write([]byte(queryString))
	signature := hex.EncodeToString(mac.Sum(nil))
	params.Set("signature", signature)

	// 构建请求
	fullURL := fmt.Sprintf("%s%s", t.apiEndpoint, path)
	var req *http.Request
	var err error

	if method == "GET" {
		fullURL = fmt.Sprintf("%s?%s", fullURL, params.Encode())
		req, err = http.NewRequest("GET", fullURL, nil)
	} else {
		req, err = http.NewRequest("POST", fullURL+"?"+params.Encode(), nil)
	}
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("X-MBX-APIKEY", t.apiKey)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// DepositAddress 存款地址信息
type DepositAddress struct {
	Address string `json:"address"`
	Tag     string `json:"tag"`
	Coin    string `json:"coin"`
	URL     string `json:"url"`
}

// WithdrawResult 提现结果
type WithdrawResult struct {
	ID string `json:"id"`
}

// GetDepositAddress 获取存款地址
func (t *BinanceTrader) GetDepositAddress(coin, network string) (*DepositAddress, error) {
	params := url.Values{}
	params.Set("coin", coin)
	if network != "" {
		params.Set("network", network)
	}

	body, err := t.signedRequest("GET", "/sapi/v1/capital/deposit/address", params)
	if err != nil {
		return nil, err
	}

	var addr DepositAddress
	if err := json.Unmarshal(body, &addr); err != nil {
		return nil, fmt.Errorf("parse deposit address: %w", err)
	}
	return &addr, nil
}

// Withdraw 提现
func (t *BinanceTrader) Withdraw(coin, network, address string, amount float64) (*WithdrawResult, error) {
	params := url.Values{}
	params.Set("coin", coin)
	params.Set("network", network)
	params.Set("address", address)
	params.Set("amount", strconv.FormatFloat(amount, 'f', 6, 64))

	body, err := t.signedRequest("POST", "/sapi/v1/capital/withdraw/apply", params)
	if err != nil {
		return nil, err
	}

	var result WithdrawResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse withdraw result: %w", err)
	}
	return &result, nil
}

// NetworkInfo 网络信息
type NetworkInfo struct {
	Network         string `json:"network"`
	WithdrawFee     string `json:"withdrawFee"`
	WithdrawMin     string `json:"withdrawMin"`
	WithdrawEnable  bool   `json:"withdrawEnable"`
	DepositEnable   bool   `json:"depositEnable"`
	Name            string `json:"name"`
}

// CoinInfo 币种信息
type CoinInfo struct {
	Coin       string        `json:"coin"`
	NetworkList []NetworkInfo `json:"networkList"`
}

// GetCoinNetwork 获取币种的网络信息（提现费、最小提现额等）
func (t *BinanceTrader) GetCoinNetwork(coin string) ([]NetworkInfo, error) {
	params := url.Values{}
	params.Set("coin", coin)

	body, err := t.signedRequest("GET", "/sapi/v1/capital/config/getall", params)
	if err != nil {
		return nil, err
	}

	var coins []CoinInfo
	if err := json.Unmarshal(body, &coins); err != nil {
		return nil, fmt.Errorf("parse coin info: %w", err)
	}

	for _, c := range coins {
		if c.Coin == coin {
			return c.NetworkList, nil
		}
	}
	return nil, fmt.Errorf("coin %s not found", coin)
}

// GetAvgFillPrice 从订单结果计算平均成交价
func (o *OrderResult) GetAvgFillPrice() float64 {
	if o.ExecutedQty == "" || o.CumulativeQuoteQty == "" {
		return 0
	}
	execQty, _ := strconv.ParseFloat(o.ExecutedQty, 64)
	quoteQty, _ := strconv.ParseFloat(o.CumulativeQuoteQty, 64)
	if execQty == 0 {
		return 0
	}
	return quoteQty / execQty
}

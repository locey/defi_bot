// pkg/aggregator/zerox.go
// 0x Swap API v2 — 跨 DEX 路由报价 + 可执行 calldata
// 需要 API key（免费注册 https://dashboard.0x.org）
// 率限：~5 RPS (free tier)
//
// AllowanceHolder (所有 Cancun 链): 0x0000000000001fF3684f28c67538d4D072C22734
package aggregator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/defi-bot/backend/pkg/log"
	"github.com/ethereum/go-ethereum/common"
)

// ZeroXClient 0x API 客户端
type ZeroXClient struct {
	httpClient *http.Client
	chainID    int64
	apiKey     string
	baseURL    string
	rateMu     sync.Mutex
	lastCall   time.Time
	minDelay   time.Duration
}

// ZeroXConfig 配置
type ZeroXConfig struct {
	ChainID  int64
	APIKey   string        // 必须
	MinDelay time.Duration // 默认 200ms
}

// ZeroXQuoteResult 报价结果
type ZeroXQuoteResult struct {
	BuyAmount          *big.Int
	SellAmount         *big.Int
	AllowanceTarget    common.Address
	LiquidityAvailable bool
	Gas                int64
	// 交易数据（仅 GetSwap 返回）
	TxTo    common.Address
	TxData  []byte
	TxValue *big.Int
}

// NewZeroXClient 创建 0x 客户端
func NewZeroXClient(config *ZeroXConfig) *ZeroXClient {
	if config.MinDelay == 0 {
		config.MinDelay = 200 * time.Millisecond
	}

	return &ZeroXClient{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		chainID:    config.ChainID,
		apiKey:     config.APIKey,
		baseURL:    "https://api.0x.org",
		minDelay:   config.MinDelay,
	}
}

// GetQuote 获取报价（不含 calldata）
func (c *ZeroXClient) GetQuote(ctx context.Context, params *QuoteParams) (*QuoteResult, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("0x: API key required")
	}

	c.rateLimit()

	q := url.Values{}
	q.Set("chainId", strconv.FormatInt(c.chainID, 10))
	q.Set("sellToken", params.Src.Hex())
	q.Set("buyToken", params.Dst.Hex())
	q.Set("sellAmount", params.Amount.String())

	fullURL := fmt.Sprintf("%s/swap/allowance-holder/price?%s", c.baseURL, q.Encode())

	req, err := http.NewRequestWithContext(ctx, "GET", fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("0x create request: %w", err)
	}
	req.Header.Set("0x-api-key", c.apiKey)
	req.Header.Set("0x-version", "v2")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("0x request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("0x read body: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("0x HTTP %d: %s", resp.StatusCode, string(body))
	}

	var priceResp struct {
		BuyAmount          string `json:"buyAmount"`
		LiquidityAvailable bool   `json:"liquidityAvailable"`
		AllowanceTarget    string `json:"allowanceTarget"`
	}
	if err := json.Unmarshal(body, &priceResp); err != nil {
		return nil, fmt.Errorf("0x parse: %w", err)
	}

	if !priceResp.LiquidityAvailable {
		return nil, fmt.Errorf("0x: no liquidity available")
	}

	buyAmount, ok := new(big.Int).SetString(priceResp.BuyAmount, 10)
	if !ok {
		return nil, fmt.Errorf("0x: invalid buyAmount: %s", priceResp.BuyAmount)
	}

	log.Strategy().Debug().
		Str("src", params.Src.Hex()[:10]).
		Str("dst", params.Dst.Hex()[:10]).
		Str("buyAmount", buyAmount.String()).
		Msg("0x 报价")

	return &QuoteResult{
		DstAmount:    buyAmount,
		EstimatedGas: 0, // price endpoint 不返回 gas
	}, nil
}

// GetSwap 获取可执行 swap（含 calldata）
func (c *ZeroXClient) GetSwap(ctx context.Context, params *SwapParams) (*ZeroXQuoteResult, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("0x: API key required")
	}

	c.rateLimit()

	q := url.Values{}
	q.Set("chainId", strconv.FormatInt(c.chainID, 10))
	q.Set("sellToken", params.Src.Hex())
	q.Set("buyToken", params.Dst.Hex())
	q.Set("sellAmount", params.Amount.String())
	q.Set("taker", params.From.Hex())
	if params.Slippage > 0 {
		q.Set("slippageBps", strconv.Itoa(int(params.Slippage*100))) // 1.0% → 100 bps
	}

	fullURL := fmt.Sprintf("%s/swap/allowance-holder/quote?%s", c.baseURL, q.Encode())

	req, err := http.NewRequestWithContext(ctx, "GET", fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("0x create request: %w", err)
	}
	req.Header.Set("0x-api-key", c.apiKey)
	req.Header.Set("0x-version", "v2")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("0x request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("0x read body: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("0x HTTP %d: %s", resp.StatusCode, string(body))
	}

	var swapResp struct {
		BuyAmount          string `json:"buyAmount"`
		SellAmount         string `json:"sellAmount"`
		AllowanceTarget    string `json:"allowanceTarget"`
		LiquidityAvailable bool   `json:"liquidityAvailable"`
		Transaction        struct {
			To       string `json:"to"`
			Data     string `json:"data"`
			Value    string `json:"value"`
			Gas      string `json:"gas"`
		} `json:"transaction"`
	}
	if err := json.Unmarshal(body, &swapResp); err != nil {
		return nil, fmt.Errorf("0x parse: %w", err)
	}

	if !swapResp.LiquidityAvailable {
		return nil, fmt.Errorf("0x: no liquidity available")
	}

	buyAmount, ok := new(big.Int).SetString(swapResp.BuyAmount, 10)
	if !ok {
		return nil, fmt.Errorf("0x: invalid buyAmount: %s", swapResp.BuyAmount)
	}
	sellAmount, ok := new(big.Int).SetString(swapResp.SellAmount, 10)
	if !ok {
		return nil, fmt.Errorf("0x: invalid sellAmount: %s", swapResp.SellAmount)
	}
	gas, _ := strconv.ParseInt(swapResp.Transaction.Gas, 10, 64)

	txValue := big.NewInt(0)
	if swapResp.Transaction.Value != "" && swapResp.Transaction.Value != "0" {
		txValue, _ = new(big.Int).SetString(swapResp.Transaction.Value, 10)
		if txValue == nil {
			txValue = big.NewInt(0)
		}
	}

	return &ZeroXQuoteResult{
		BuyAmount:          buyAmount,
		SellAmount:         sellAmount,
		AllowanceTarget:    common.HexToAddress(swapResp.AllowanceTarget),
		LiquidityAvailable: true,
		Gas:                gas,
		TxTo:               common.HexToAddress(swapResp.Transaction.To),
		TxData:             common.FromHex(swapResp.Transaction.Data),
		TxValue:            txValue,
	}, nil
}

// rateLimit 速率限制
func (c *ZeroXClient) rateLimit() {
	c.rateMu.Lock()
	defer c.rateMu.Unlock()

	elapsed := time.Since(c.lastCall)
	if elapsed < c.minDelay {
		time.Sleep(c.minDelay - elapsed)
	}
	c.lastCall = time.Now()
}

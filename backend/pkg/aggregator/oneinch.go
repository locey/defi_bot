// pkg/aggregator/oneinch.go
// 1inch Swap API v6.0 — 跨 DEX 路由报价 + 可执行 calldata
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

// OneInchClient 1inch API 客户端
type OneInchClient struct {
	httpClient *http.Client
	chainID    int64
	apiKey     string // 可选，免费额度 5 req/s
	baseURL    string
	rateMu     sync.Mutex
	lastCall   time.Time
	minDelay   time.Duration // 200ms = 5 req/s
}

// OneInchConfig 配置
type OneInchConfig struct {
	ChainID  int64
	APIKey   string
	MinDelay time.Duration
}

// QuoteParams 报价参数
type QuoteParams struct {
	Src         common.Address // 输入 token
	Dst         common.Address // 输出 token
	Amount      *big.Int       // 输入金额 (raw, 含 decimals)
	SrcDecimals int            // 输入 token 精度（0 = 默认 18）
	DstDecimals int            // 输出 token 精度（0 = 默认 18）
}

// QuoteResult 报价结果
type QuoteResult struct {
	DstAmount    *big.Int // 输出金额
	Gas          int64    // 估算 gas
	SrcToken     TokenInfo
	DstToken     TokenInfo
	Protocols    [][]ProtocolStep // 路由路径
	EstimatedGas int64
}

// SwapParams swap 参数
type SwapParams struct {
	Src      common.Address
	Dst      common.Address
	Amount   *big.Int
	From     common.Address // 发送方地址
	Slippage float64        // 如 1.0 = 1%
}

// SwapResult swap 结果
type SwapResult struct {
	DstAmount *big.Int
	TxData    []byte         // 可直接发送的 calldata
	TxTo      common.Address // 1inch Router 地址
	TxValue   *big.Int       // msg.value (ETH swap 时非零)
	Gas       int64
}

// TokenInfo 1inch token 信息
type TokenInfo struct {
	Symbol   string `json:"symbol"`
	Name     string `json:"name"`
	Address  string `json:"address"`
	Decimals int    `json:"decimals"`
}

// ProtocolStep 路由步骤
type ProtocolStep struct {
	Name string `json:"name"`
	Part int    `json:"part"` // 比例 (1-100)
}

// NewOneInchClient 创建 1inch 客户端
func NewOneInchClient(config *OneInchConfig) *OneInchClient {
	if config.MinDelay == 0 {
		config.MinDelay = 200 * time.Millisecond
	}

	return &OneInchClient{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		chainID:    config.ChainID,
		apiKey:     config.APIKey,
		baseURL:    fmt.Sprintf("https://api.1inch.dev/swap/v6.0/%d", config.ChainID),
		minDelay:   config.MinDelay,
	}
}

// GetQuote 获取报价（不生成 calldata）
func (c *OneInchClient) GetQuote(ctx context.Context, params *QuoteParams) (*QuoteResult, error) {
	c.rateLimit()

	q := url.Values{}
	q.Set("src", params.Src.Hex())
	q.Set("dst", params.Dst.Hex())
	q.Set("amount", params.Amount.String())

	body, err := c.doRequest(ctx, "/quote", q)
	if err != nil {
		return nil, fmt.Errorf("1inch quote: %w", err)
	}

	var resp struct {
		DstAmount string      `json:"dstAmount"`
		Gas       int64       `json:"gas"`
		SrcToken  TokenInfo   `json:"srcToken"`
		DstToken  TokenInfo   `json:"dstToken"`
		Protocols [][][]struct {
			Name string `json:"name"`
			Part int    `json:"part"`
		} `json:"protocols"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("1inch parse quote: %w", err)
	}

	dstAmount, ok := new(big.Int).SetString(resp.DstAmount, 10)
	if !ok {
		return nil, fmt.Errorf("1inch: invalid dstAmount: %s", resp.DstAmount)
	}

	result := &QuoteResult{
		DstAmount:    dstAmount,
		Gas:          resp.Gas,
		SrcToken:     resp.SrcToken,
		DstToken:     resp.DstToken,
		EstimatedGas: resp.Gas,
	}

	// Flatten protocols
	for _, route := range resp.Protocols {
		for _, steps := range route {
			var flatSteps []ProtocolStep
			for _, s := range steps {
				flatSteps = append(flatSteps, ProtocolStep{Name: s.Name, Part: s.Part})
			}
			result.Protocols = append(result.Protocols, flatSteps)
		}
	}

	return result, nil
}

// GetSwap 获取 swap calldata（可直接执行）
func (c *OneInchClient) GetSwap(ctx context.Context, params *SwapParams) (*SwapResult, error) {
	c.rateLimit()

	q := url.Values{}
	q.Set("src", params.Src.Hex())
	q.Set("dst", params.Dst.Hex())
	q.Set("amount", params.Amount.String())
	q.Set("from", params.From.Hex())
	q.Set("slippage", strconv.FormatFloat(params.Slippage, 'f', 2, 64))
	q.Set("disableEstimate", "true") // 跳过链上验证，加快响应

	body, err := c.doRequest(ctx, "/swap", q)
	if err != nil {
		return nil, fmt.Errorf("1inch swap: %w", err)
	}

	var resp struct {
		DstAmount string `json:"dstAmount"`
		Tx        struct {
			From     string `json:"from"`
			To       string `json:"to"`
			Data     string `json:"data"`
			Value    string `json:"value"`
			Gas      int64  `json:"gas"`
			GasPrice string `json:"gasPrice"`
		} `json:"tx"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("1inch parse swap: %w", err)
	}

	dstAmount, ok := new(big.Int).SetString(resp.DstAmount, 10)
	if !ok {
		return nil, fmt.Errorf("1inch: invalid dstAmount: %s", resp.DstAmount)
	}
	txValue, ok := new(big.Int).SetString(resp.Tx.Value, 10)
	if !ok {
		txValue = big.NewInt(0)
	}

	txData := common.FromHex(resp.Tx.Data)

	return &SwapResult{
		DstAmount: dstAmount,
		TxData:    txData,
		TxTo:      common.HexToAddress(resp.Tx.To),
		TxValue:   txValue,
		Gas:       resp.Tx.Gas,
	}, nil
}

// CompareWithLocal 对比 1inch 报价与本地计算
// 返回 1inch 比本地多出的百分比（正数 = 1inch 更优）
func (c *OneInchClient) CompareWithLocal(ctx context.Context, src, dst common.Address, amount, localOutput *big.Int) (float64, *QuoteResult, error) {
	quote, err := c.GetQuote(ctx, &QuoteParams{Src: src, Dst: dst, Amount: amount})
	if err != nil {
		return 0, nil, err
	}

	if localOutput == nil || localOutput.Sign() == 0 {
		return 0, quote, nil
	}

	// improvement = (1inch - local) / local * 100%
	diff := new(big.Int).Sub(quote.DstAmount, localOutput)
	diffFloat := new(big.Float).SetInt(diff)
	localFloat := new(big.Float).SetInt(localOutput)
	improvement, _ := new(big.Float).Quo(diffFloat, localFloat).Float64()

	log.Strategy().Info().
		Str("src", src.Hex()[:10]).
		Str("dst", dst.Hex()[:10]).
		Str("1inch", quote.DstAmount.String()).
		Str("local", localOutput.String()).
		Float64("improvement_pct", improvement*100).
		Msg("1inch vs 本地报价对比")

	return improvement, quote, nil
}

// rateLimit 限速
func (c *OneInchClient) rateLimit() {
	c.rateMu.Lock()
	defer c.rateMu.Unlock()

	elapsed := time.Since(c.lastCall)
	if elapsed < c.minDelay {
		time.Sleep(c.minDelay - elapsed)
	}
	c.lastCall = time.Now()
}

// doRequest 发送 HTTP 请求
func (c *OneInchClient) doRequest(ctx context.Context, path string, params url.Values) ([]byte, error) {
	reqURL := fmt.Sprintf("%s%s?%s", c.baseURL, path, params.Encode())

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}

	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("1inch API error (%d): %s", resp.StatusCode, string(body))
	}

	return body, nil
}

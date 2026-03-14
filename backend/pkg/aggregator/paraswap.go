// pkg/aggregator/paraswap.go
// ParaSwap API — 跨 DEX 路由报价 + 可执行 calldata
// 无需 API key，率限约 1 req/s
//
// Augustus Swapper (所有链): 0xDEF171Fe48CF0115B1d80b88dc8eAB59176FEe57
// TokenTransferProxy:        0x216B4B4Ba9F3e719726886d34a177484278Bfcae
package aggregator

import (
	"bytes"
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

// ParaSwapClient ParaSwap API 客户端
type ParaSwapClient struct {
	httpClient *http.Client
	chainID    int64
	baseURL    string
	rateMu     sync.Mutex
	lastCall   time.Time
	minDelay   time.Duration
}

// ParaSwapConfig 配置
type ParaSwapConfig struct {
	ChainID  int64
	MinDelay time.Duration // 默认 1s
}

// ParaSwapQuoteResult 报价结果
type ParaSwapQuoteResult struct {
	DestAmount   *big.Int // 输出金额
	GasCost      int64    // 估算 gas
	GasCostUSD   string   // Gas 费 USD
	SrcUSD       string   // 输入 USD 价值
	DestUSD      string   // 输出 USD 价值
	BestRoute    string   // 最佳路由描述
	ContractAddr common.Address
	// 内部：用于构建交易
	priceRoute json.RawMessage
}

// ParaSwapTxResult 交易构建结果
type ParaSwapTxResult struct {
	To       common.Address
	Data     []byte
	Value    *big.Int
	GasPrice *big.Int
	Gas      int64
}

// NewParaSwapClient 创建 ParaSwap 客户端
func NewParaSwapClient(config *ParaSwapConfig) *ParaSwapClient {
	if config.MinDelay == 0 {
		config.MinDelay = 1 * time.Second
	}

	return &ParaSwapClient{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		chainID:    config.ChainID,
		baseURL:    "https://api.paraswap.io",
		minDelay:   config.MinDelay,
	}
}

// GetQuote 获取 ParaSwap 报价
func (c *ParaSwapClient) GetQuote(ctx context.Context, params *QuoteParams) (*QuoteResult, error) {
	c.rateLimit()

	q := url.Values{}
	q.Set("srcToken", params.Src.Hex())
	q.Set("destToken", params.Dst.Hex())
	q.Set("amount", params.Amount.String())
	srcDec := params.SrcDecimals
	if srcDec == 0 {
		srcDec = 18
	}
	dstDec := params.DstDecimals
	if dstDec == 0 {
		dstDec = 18
	}
	q.Set("srcDecimals", strconv.Itoa(srcDec))
	q.Set("destDecimals", strconv.Itoa(dstDec))
	q.Set("side", "SELL")
	q.Set("network", strconv.FormatInt(c.chainID, 10))

	fullURL := fmt.Sprintf("%s/prices?%s", c.baseURL, q.Encode())

	req, err := http.NewRequestWithContext(ctx, "GET", fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("paraswap create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("paraswap request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("paraswap read body: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("paraswap HTTP %d: %s", resp.StatusCode, string(body))
	}

	var priceResp struct {
		PriceRoute struct {
			DestAmount      string `json:"destAmount"`
			GasCost         string `json:"gasCost"`
			GasCostUSD      string `json:"gasCostUSD"`
			SrcUSD          string `json:"srcUSD"`
			DestUSD         string `json:"destUSD"`
			ContractAddress string `json:"contractAddress"`
			BestRoute       []struct {
				Percent int `json:"percent"`
				Swaps   []struct {
					SwapExchanges []struct {
						Exchange string `json:"exchange"`
						Percent  int    `json:"percent"`
					} `json:"swapExchanges"`
				} `json:"swaps"`
			} `json:"bestRoute"`
		} `json:"priceRoute"`
	}
	if err := json.Unmarshal(body, &priceResp); err != nil {
		return nil, fmt.Errorf("paraswap parse: %w", err)
	}

	destAmount, ok := new(big.Int).SetString(priceResp.PriceRoute.DestAmount, 10)
	if !ok {
		return nil, fmt.Errorf("paraswap: invalid destAmount: %s", priceResp.PriceRoute.DestAmount)
	}

	gasCost, _ := strconv.ParseInt(priceResp.PriceRoute.GasCost, 10, 64)

	// 提取路由信息
	var routeDesc string
	for _, r := range priceResp.PriceRoute.BestRoute {
		for _, s := range r.Swaps {
			for _, ex := range s.SwapExchanges {
				if routeDesc != "" {
					routeDesc += " → "
				}
				routeDesc += fmt.Sprintf("%s(%d%%)", ex.Exchange, ex.Percent)
			}
		}
	}

	// 将报价结果映射到通用 QuoteResult
	result := &QuoteResult{
		DstAmount:    destAmount,
		Gas:          gasCost,
		EstimatedGas: gasCost,
	}

	log.Strategy().Debug().
		Str("src", params.Src.Hex()[:10]).
		Str("dst", params.Dst.Hex()[:10]).
		Str("destAmount", destAmount.String()).
		Str("route", routeDesc).
		Str("gasCostUSD", priceResp.PriceRoute.GasCostUSD).
		Msg("ParaSwap 报价")

	return result, nil
}

// GetQuoteDetailed 获取详细报价（含 priceRoute，用于后续 BuildTx）
func (c *ParaSwapClient) GetQuoteDetailed(
	ctx context.Context,
	srcToken common.Address, srcDecimals int,
	destToken common.Address, destDecimals int,
	amount *big.Int,
) (*ParaSwapQuoteResult, error) {
	c.rateLimit()

	q := url.Values{}
	q.Set("srcToken", srcToken.Hex())
	q.Set("destToken", destToken.Hex())
	q.Set("amount", amount.String())
	q.Set("srcDecimals", strconv.Itoa(srcDecimals))
	q.Set("destDecimals", strconv.Itoa(destDecimals))
	q.Set("side", "SELL")
	q.Set("network", strconv.FormatInt(c.chainID, 10))

	fullURL := fmt.Sprintf("%s/prices?%s", c.baseURL, q.Encode())

	req, err := http.NewRequestWithContext(ctx, "GET", fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("paraswap create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("paraswap request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("paraswap read body: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("paraswap HTTP %d: %s", resp.StatusCode, string(body))
	}

	// 解析整个 priceRoute 保留原始 JSON
	var rawResp struct {
		PriceRoute json.RawMessage `json:"priceRoute"`
	}
	if err := json.Unmarshal(body, &rawResp); err != nil {
		return nil, fmt.Errorf("paraswap parse raw: %w", err)
	}

	// 解析关键字段
	var priceRoute struct {
		DestAmount      string `json:"destAmount"`
		GasCost         string `json:"gasCost"`
		GasCostUSD      string `json:"gasCostUSD"`
		SrcUSD          string `json:"srcUSD"`
		DestUSD         string `json:"destUSD"`
		ContractAddress string `json:"contractAddress"`
	}
	if err := json.Unmarshal(rawResp.PriceRoute, &priceRoute); err != nil {
		return nil, fmt.Errorf("paraswap parse priceRoute: %w", err)
	}

	destAmount, ok := new(big.Int).SetString(priceRoute.DestAmount, 10)
	if !ok {
		return nil, fmt.Errorf("paraswap: invalid destAmount")
	}

	gasCost, _ := strconv.ParseInt(priceRoute.GasCost, 10, 64)

	return &ParaSwapQuoteResult{
		DestAmount:   destAmount,
		GasCost:      gasCost,
		GasCostUSD:   priceRoute.GasCostUSD,
		SrcUSD:       priceRoute.SrcUSD,
		DestUSD:      priceRoute.DestUSD,
		ContractAddr: common.HexToAddress(priceRoute.ContractAddress),
		priceRoute:   rawResp.PriceRoute,
	}, nil
}

// BuildTx 构建可执行交易（需先调用 GetQuoteDetailed 获取 priceRoute）
func (c *ParaSwapClient) BuildTx(
	ctx context.Context,
	srcToken common.Address, srcDecimals int,
	destToken common.Address, destDecimals int,
	amount *big.Int,
	slippageBps int, // 100 = 1%
	userAddress common.Address,
	quote *ParaSwapQuoteResult,
) (*ParaSwapTxResult, error) {
	c.rateLimit()

	reqBody := map[string]interface{}{
		"srcToken":    srcToken.Hex(),
		"srcDecimals": srcDecimals,
		"destToken":   destToken.Hex(),
		"destDecimals": destDecimals,
		"srcAmount":   amount.String(),
		"slippage":    slippageBps,
		"userAddress": userAddress.Hex(),
		"priceRoute":  quote.priceRoute,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("paraswap marshal body: %w", err)
	}

	fullURL := fmt.Sprintf("%s/transactions/%d?ignoreChecks=true", c.baseURL, c.chainID)

	req, err := http.NewRequestWithContext(ctx, "POST", fullURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("paraswap create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("paraswap build tx: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("paraswap read body: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("paraswap build tx HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var txResp struct {
		To       string `json:"to"`
		Data     string `json:"data"`
		Value    string `json:"value"`
		GasPrice string `json:"gasPrice"`
		Gas      string `json:"gas"`
	}
	if err := json.Unmarshal(respBody, &txResp); err != nil {
		return nil, fmt.Errorf("paraswap parse tx: %w", err)
	}

	value := big.NewInt(0)
	if txResp.Value != "" && txResp.Value != "0" {
		value, _ = new(big.Int).SetString(txResp.Value, 10)
		if value == nil {
			value = big.NewInt(0)
		}
	}

	gasPrice := big.NewInt(0)
	if txResp.GasPrice != "" {
		gasPrice, _ = new(big.Int).SetString(txResp.GasPrice, 10)
		if gasPrice == nil {
			gasPrice = big.NewInt(0)
		}
	}

	gas, _ := strconv.ParseInt(txResp.Gas, 10, 64)

	return &ParaSwapTxResult{
		To:       common.HexToAddress(txResp.To),
		Data:     common.FromHex(txResp.Data),
		Value:    value,
		GasPrice: gasPrice,
		Gas:      gas,
	}, nil
}

// CompareQuotes 对比 ParaSwap 和 1inch 的报价
func CompareQuotes(
	ctx context.Context,
	paraswap *ParaSwapClient,
	oneInch *OneInchClient,
	src, dst common.Address,
	amount *big.Int,
) (paraResult, oneInchResult *QuoteResult, err error) {
	// 并发获取两个报价
	type quoteResult struct {
		result *QuoteResult
		err    error
		source string
	}

	ch := make(chan quoteResult, 2)

	params := &QuoteParams{Src: src, Dst: dst, Amount: amount}

	go func() {
		r, e := paraswap.GetQuote(ctx, params)
		ch <- quoteResult{result: r, err: e, source: "ParaSwap"}
	}()

	go func() {
		r, e := oneInch.GetQuote(ctx, params)
		ch <- quoteResult{result: r, err: e, source: "1inch"}
	}()

	for i := 0; i < 2; i++ {
		qr := <-ch
		if qr.err != nil {
			log.Strategy().Warn().Err(qr.err).Str("source", qr.source).Msg("聚合器报价失败")
			continue
		}
		switch qr.source {
		case "ParaSwap":
			paraResult = qr.result
		case "1inch":
			oneInchResult = qr.result
		}
	}

	if paraResult != nil && oneInchResult != nil {
		paraBF := new(big.Float).SetInt(paraResult.DstAmount)
		oneBF := new(big.Float).SetInt(oneInchResult.DstAmount)
		diff := new(big.Float).Sub(paraBF, oneBF)
		pct, _ := new(big.Float).Quo(new(big.Float).Mul(diff, big.NewFloat(100)), oneBF).Float64()

		log.Strategy().Info().
			Str("paraswap", paraResult.DstAmount.String()).
			Str("1inch", oneInchResult.DstAmount.String()).
			Float64("para_vs_1inch_pct", pct).
			Msg("聚合器报价对比")
	}

	return paraResult, oneInchResult, nil
}

// rateLimit 速率限制
func (c *ParaSwapClient) rateLimit() {
	c.rateMu.Lock()
	defer c.rateMu.Unlock()

	elapsed := time.Since(c.lastCall)
	if elapsed < c.minDelay {
		time.Sleep(c.minDelay - elapsed)
	}
	c.lastCall = time.Now()
}

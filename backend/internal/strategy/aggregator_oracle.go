// internal/strategy/aggregator_oracle.go
// 聚合器价格预言机 — 通过 Paraswap/1inch API 获取最优跨 DEX 报价
// 用于交叉验证本地利润计算，找到真实套利机会
package strategy

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/defi-bot/backend/pkg/log"
	"github.com/ethereum/go-ethereum/common"
)

// AggregatorOracle 聚合器价格预言机
type AggregatorOracle struct {
	client    *http.Client
	chainID   int
	cache     sync.Map // token pair → cached quote
	cacheTTL  time.Duration
	rateLimit chan struct{} // 限流
}

// AggregatorQuote 聚合器报价
type AggregatorQuote struct {
	SrcToken    common.Address
	DestToken   common.Address
	SrcAmount   *big.Int
	DestAmount  *big.Int
	BestRoute   string // 最优路由描述
	GasCost     *big.Int
	Timestamp   time.Time
	PriceImpact float64
}

type cacheEntry struct {
	quote     *AggregatorQuote
	timestamp time.Time
}

// NewAggregatorOracle 创建聚合器预言机
func NewAggregatorOracle(chainID int) *AggregatorOracle {
	return &AggregatorOracle{
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
		chainID:   chainID,
		cacheTTL:  3 * time.Second, // 3s 缓存（价格变化快）
		rateLimit: make(chan struct{}, 2), // 最多 2 个并发请求
	}
}

// paraswapPriceResponse Paraswap API 响应
type paraswapPriceResponse struct {
	PriceRoute struct {
		SrcToken    string `json:"srcToken"`
		DestToken   string `json:"destToken"`
		SrcAmount   string `json:"srcAmount"`
		DestAmount  string `json:"destAmount"`
		GasCost     string `json:"gasCost"`
		BestRoute   []struct {
			Percent int `json:"percent"`
			Swaps   []struct {
				SrcToken  string `json:"srcToken"`
				DestToken string `json:"destToken"`
				SwapExchanges []struct {
					Exchange string `json:"exchange"`
					Percent  int    `json:"percent"`
				} `json:"swapExchanges"`
			} `json:"swaps"`
		} `json:"bestRoute"`
	} `json:"priceRoute"`
	Error string `json:"error"`
}

// GetQuote 获取聚合器报价
func (o *AggregatorOracle) GetQuote(
	ctx context.Context,
	srcToken, destToken common.Address,
	amount *big.Int,
) (*AggregatorQuote, error) {
	// 检查缓存
	cacheKey := fmt.Sprintf("%s-%s-%s", srcToken.Hex(), destToken.Hex(), amount.String())
	if cached, ok := o.cache.Load(cacheKey); ok {
		entry := cached.(*cacheEntry)
		if time.Since(entry.timestamp) < o.cacheTTL {
			return entry.quote, nil
		}
	}

	// 限流
	select {
	case o.rateLimit <- struct{}{}:
		defer func() { <-o.rateLimit }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	quote, err := o.queryParaswap(ctx, srcToken, destToken, amount)
	if err != nil {
		return nil, err
	}

	// 缓存结果
	o.cache.Store(cacheKey, &cacheEntry{
		quote:     quote,
		timestamp: time.Now(),
	})

	return quote, nil
}

// GetRoundTripProfit 计算往返套利利润（A→B→A）
// 通过聚合器查询 srcToken→midToken 和 midToken→srcToken 的最优报价
func (o *AggregatorOracle) GetRoundTripProfit(
	ctx context.Context,
	srcToken, midToken common.Address,
	amount *big.Int,
) (*big.Int, string, error) {
	// Step 1: srcToken → midToken
	quote1, err := o.GetQuote(ctx, srcToken, midToken, amount)
	if err != nil {
		return nil, "", fmt.Errorf("leg1 quote: %w", err)
	}

	if quote1.DestAmount == nil || quote1.DestAmount.Sign() <= 0 {
		return big.NewInt(0), "no liquidity for leg1", nil
	}

	// Step 2: midToken → srcToken
	quote2, err := o.GetQuote(ctx, midToken, srcToken, quote1.DestAmount)
	if err != nil {
		return nil, "", fmt.Errorf("leg2 quote: %w", err)
	}

	if quote2.DestAmount == nil || quote2.DestAmount.Sign() <= 0 {
		return big.NewInt(0), "no liquidity for leg2", nil
	}

	// 计算利润
	profit := new(big.Int).Sub(quote2.DestAmount, amount)
	route := fmt.Sprintf("leg1: %s, leg2: %s", quote1.BestRoute, quote2.BestRoute)

	return profit, route, nil
}

// queryParaswap 查询 Paraswap API
func (o *AggregatorOracle) queryParaswap(
	ctx context.Context,
	srcToken, destToken common.Address,
	amount *big.Int,
) (*AggregatorQuote, error) {
	url := fmt.Sprintf(
		"https://apiv5.paraswap.io/prices?srcToken=%s&destToken=%s&amount=%s&srcDecimals=18&destDecimals=18&network=%d&side=SELL",
		srcToken.Hex(), destToken.Hex(), amount.String(), o.chainID,
	)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("paraswap request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("paraswap status %d", resp.StatusCode)
	}

	var result paraswapPriceResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	if result.Error != "" {
		return nil, fmt.Errorf("paraswap error: %s", result.Error)
	}

	destAmount := new(big.Int)
	destAmount.SetString(result.PriceRoute.DestAmount, 10)

	gasCost := new(big.Int)
	gasCost.SetString(result.PriceRoute.GasCost, 10)

	// 提取最优路由描述
	var routeDesc string
	if len(result.PriceRoute.BestRoute) > 0 {
		for _, r := range result.PriceRoute.BestRoute {
			for _, s := range r.Swaps {
				for _, ex := range s.SwapExchanges {
					routeDesc += fmt.Sprintf("%s(%d%%) ", ex.Exchange, ex.Percent)
				}
			}
		}
	}

	quote := &AggregatorQuote{
		SrcToken:   srcToken,
		DestToken:  destToken,
		SrcAmount:  amount,
		DestAmount: destAmount,
		BestRoute:  routeDesc,
		GasCost:    gasCost,
		Timestamp:  time.Now(),
	}

	log.Strategy().Debug().
		Str("src", srcToken.Hex()[:10]).
		Str("dest", destToken.Hex()[:10]).
		Str("amount_in", amount.String()).
		Str("amount_out", destAmount.String()).
		Str("route", routeDesc).
		Msg("Aggregator quote")

	return quote, nil
}

// CrossValidateOpportunity 交叉验证套利机会
// 用聚合器报价验证本地利润计算是否准确
func (o *AggregatorOracle) CrossValidateOpportunity(
	ctx context.Context,
	opp *ArbitrageOpportunity,
) (bool, string) {
	if len(opp.SwapPath) < 3 {
		return false, "path too short"
	}

	srcToken := opp.SwapPath[0]
	midToken := opp.SwapPath[1]

	profit, route, err := o.GetRoundTripProfit(ctx, srcToken, midToken, opp.AmountIn)
	if err != nil {
		return false, fmt.Sprintf("aggregator error: %v", err)
	}

	// 如果聚合器也认为有利可图，机会更可信
	if profit.Sign() > 0 {
		log.Strategy().Info().
			Str("opportunity", opp.ID).
			Str("local_profit", opp.ExpectProfit.String()).
			Str("aggregator_profit", profit.String()).
			Str("aggregator_route", route).
			Msg("✅ Aggregator confirms opportunity")
		return true, route
	}

	log.Strategy().Debug().
		Str("opportunity", opp.ID).
		Str("local_profit", opp.ExpectProfit.String()).
		Str("aggregator_profit", profit.String()).
		Msg("❌ Aggregator does not confirm opportunity")
	return false, "aggregator shows no profit"
}

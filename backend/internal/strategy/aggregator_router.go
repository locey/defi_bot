// internal/strategy/aggregator_router.go
// 聚合器路由比较器 — 1inch vs 本地 PathFinder 对比，选最优路径
package strategy

import (
	"context"
	"math/big"
	"sync"
	"time"

	"github.com/defi-bot/backend/pkg/aggregator"
	"github.com/defi-bot/backend/pkg/log"
	"github.com/ethereum/go-ethereum/common"
)

// AggregatorRouter 聚合器路由优化器
// 用 1inch 覆盖 50+ DEX 报价，与本地 PathFinder 对比，选最优路径
type AggregatorRouter struct {
	oneInch *aggregator.OneInchClient
	cache   sync.Map // cacheKey → *routeCache
	cacheTTL time.Duration
}

type routeCache struct {
	quote     *aggregator.QuoteResult
	timestamp time.Time
}

// NewAggregatorRouter 创建路由器
func NewAggregatorRouter(oneInch *aggregator.OneInchClient) *AggregatorRouter {
	return &AggregatorRouter{
		oneInch:  oneInch,
		cacheTTL: 3 * time.Second,
	}
}

// ValidateOpportunity 用 1inch 验证机会的利润率
// 返回: shouldProceed（是否继续执行）, adjustedProfitRate（1inch 确认的利润率）
func (r *AggregatorRouter) ValidateOpportunity(ctx context.Context, opp *ArbitrageOpportunity) (bool, float64) {
	if r.oneInch == nil || opp == nil || len(opp.SwapPath) < 2 {
		return true, opp.ProfitRate // 无 1inch 客户端，放行
	}

	srcToken := opp.SwapPath[0]
	dstToken := opp.SwapPath[len(opp.SwapPath)-1]

	// 套利是环路（src == dst），验证中间腿的报价
	// 用 1inch 查询 src→midToken 和 midToken→src 两段
	if srcToken == dstToken && len(opp.SwapPath) >= 3 {
		return r.validateRoundTrip(ctx, opp)
	}

	// 非环路直接验证
	return r.validateDirect(ctx, srcToken, dstToken, opp.AmountIn, opp.ProfitRate)
}

// validateRoundTrip 验证环路套利（src → mid → src）
func (r *AggregatorRouter) validateRoundTrip(ctx context.Context, opp *ArbitrageOpportunity) (bool, float64) {
	srcToken := opp.SwapPath[0]
	midToken := opp.SwapPath[1]

	if opp.AmountIn == nil || opp.AmountIn.Sign() <= 0 {
		return true, opp.ProfitRate
	}

	// 检查缓存
	cacheKey := srcToken.Hex() + "-" + midToken.Hex() + "-" + opp.AmountIn.String()
	if cached, ok := r.cache.Load(cacheKey); ok {
		entry := cached.(*routeCache)
		if time.Since(entry.timestamp) < r.cacheTTL {
			// 用缓存计算利润率
			if entry.quote != nil && entry.quote.DstAmount != nil {
				return r.calcProfit(entry.quote.DstAmount, opp.AmountIn, opp.ProfitRate)
			}
		}
	}

	// Leg 1: src → mid
	leg1, err := r.oneInch.GetQuote(ctx, &aggregator.QuoteParams{
		Src:    srcToken,
		Dst:    midToken,
		Amount: opp.AmountIn,
	})
	if err != nil {
		log.Main().Debug().Err(err).Msg("1inch leg1 quote 失败，放行")
		return true, opp.ProfitRate
	}

	// Leg 2: mid → src
	leg2, err := r.oneInch.GetQuote(ctx, &aggregator.QuoteParams{
		Src:    midToken,
		Dst:    srcToken,
		Amount: leg1.DstAmount,
	})
	if err != nil {
		log.Main().Debug().Err(err).Msg("1inch leg2 quote 失败，放行")
		return true, opp.ProfitRate
	}

	// 缓存
	r.cache.Store(cacheKey, &routeCache{quote: leg2, timestamp: time.Now()})

	return r.calcProfit(leg2.DstAmount, opp.AmountIn, opp.ProfitRate)
}

// validateDirect 直接验证（非环路）
func (r *AggregatorRouter) validateDirect(ctx context.Context, src, dst common.Address, amountIn *big.Int, localRate float64) (bool, float64) {
	if amountIn == nil || amountIn.Sign() <= 0 {
		return true, localRate
	}

	quote, err := r.oneInch.GetQuote(ctx, &aggregator.QuoteParams{
		Src:    src,
		Dst:    dst,
		Amount: amountIn,
	})
	if err != nil {
		return true, localRate
	}

	return r.calcProfit(quote.DstAmount, amountIn, localRate)
}

// calcProfit 从 1inch 结果计算利润率
func (r *AggregatorRouter) calcProfit(output, input *big.Int, localRate float64) (bool, float64) {
	if output == nil || input == nil || input.Sign() == 0 {
		return true, localRate
	}

	// 利润 = output - input
	profit := new(big.Int).Sub(output, input)
	profitFloat := new(big.Float).SetInt(profit)
	inputFloat := new(big.Float).SetInt(input)
	rate, _ := new(big.Float).Quo(profitFloat, inputFloat).Float64()

	if profit.Sign() <= 0 {
		log.Info("1inch: 无利润 (rate=%.4f%%), 本地预估=%.4f%% → 过滤", rate*100, localRate*100)
		return false, rate
	}

	log.Info("1inch: 确认利润 rate=%.4f%%, 本地预估=%.4f%%", rate*100, localRate*100)
	return true, rate
}

// FindBetterRoute 查询 1inch 是否有更优路径（返回可执行 calldata）
func (r *AggregatorRouter) FindBetterRoute(
	ctx context.Context,
	src, dst common.Address,
	amountIn *big.Int,
	from common.Address,
	localOutput *big.Int,
) (*aggregator.SwapResult, float64, error) {
	if r.oneInch == nil {
		return nil, 0, nil
	}

	improvement, _, err := r.oneInch.CompareWithLocal(ctx, src, dst, amountIn, localOutput)
	if err != nil {
		return nil, 0, err
	}

	// 1inch 比本地好 0.5% 以上，获取可执行 calldata
	if improvement > 0.005 {
		swapResult, err := r.oneInch.GetSwap(ctx, &aggregator.SwapParams{
			Src:      src,
			Dst:      dst,
			Amount:   amountIn,
			From:     from,
			Slippage: 0.5,
		})
		if err != nil {
			return nil, improvement, err
		}
		return swapResult, improvement, nil
	}

	return nil, improvement, nil
}

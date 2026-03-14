// pkg/aggregator/multi.go
// 多聚合器竞价 — 同时询价 1inch + ParaSwap + 0x，选最优报价
package aggregator

import (
	"context"
	"math/big"
	"sync"

	"github.com/defi-bot/backend/pkg/log"
	"github.com/ethereum/go-ethereum/common"
)

// MultiAggregator 多聚合器竞价
type MultiAggregator struct {
	oneInch  *OneInchClient
	paraSwap *ParaSwapClient
	zeroX    *ZeroXClient
}

// AggQuoteResult 聚合报价结果
type AggQuoteResult struct {
	Source    string   // "1inch", "ParaSwap", "0x"
	DstAmount *big.Int
	Gas       int64
}

// NewMultiAggregator 创建多聚合器
func NewMultiAggregator(oneInch *OneInchClient, paraSwap *ParaSwapClient, zeroX *ZeroXClient) *MultiAggregator {
	return &MultiAggregator{
		oneInch:  oneInch,
		paraSwap: paraSwap,
		zeroX:    zeroX,
	}
}

// BestQuote 获取最优报价（并发询价所有可用聚合器）
func (m *MultiAggregator) BestQuote(ctx context.Context, src, dst common.Address, amount *big.Int) (*AggQuoteResult, error) {
	params := &QuoteParams{Src: src, Dst: dst, Amount: amount}

	type result struct {
		source string
		quote  *QuoteResult
		err    error
	}

	var wg sync.WaitGroup
	ch := make(chan result, 3)

	// 1inch
	if m.oneInch != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q, err := m.oneInch.GetQuote(ctx, params)
			ch <- result{source: "1inch", quote: q, err: err}
		}()
	}

	// ParaSwap
	if m.paraSwap != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q, err := m.paraSwap.GetQuote(ctx, params)
			ch <- result{source: "ParaSwap", quote: q, err: err}
		}()
	}

	// 0x
	if m.zeroX != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q, err := m.zeroX.GetQuote(ctx, params)
			ch <- result{source: "0x", quote: q, err: err}
		}()
	}

	// 等待全部完成
	go func() {
		wg.Wait()
		close(ch)
	}()

	var best *AggQuoteResult
	var results []result

	for r := range ch {
		results = append(results, r)
		if r.err != nil {
			log.Strategy().Debug().Err(r.err).Str("source", r.source).Msg("聚合器报价失败")
			continue
		}
		if r.quote == nil || r.quote.DstAmount == nil || r.quote.DstAmount.Sign() <= 0 {
			continue
		}

		candidate := &AggQuoteResult{
			Source:    r.source,
			DstAmount: r.quote.DstAmount,
			Gas:       r.quote.EstimatedGas,
		}

		if best == nil || candidate.DstAmount.Cmp(best.DstAmount) > 0 {
			best = candidate
		}
	}

	if best == nil {
		return nil, &NoQuoteError{Sources: len(results)}
	}

	// 记录竞价结果
	log.Strategy().Info().
		Str("winner", best.Source).
		Str("amount", best.DstAmount.String()).
		Int("sources_tried", len(results)).
		Msg("多聚合器竞价完成")

	return best, nil
}

// NoQuoteError 所有聚合器均无报价
type NoQuoteError struct {
	Sources int
}

func (e *NoQuoteError) Error() string {
	return "all aggregators failed to quote"
}
